package mcpauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"golang.org/x/oauth2"
)

const (
	testIssuer   = "http://localhost:8090"
	testResource = testIssuer + "/mcp"
	testRedirect = "http://127.0.0.1:33418/callback"
)

var testResources = []string{testResource, "http://127.0.0.1:8090/mcp"}

type asFixture struct {
	as    *AS
	store *Store
	clock *clock
	srv   *httptest.Server
	log   *bytes.Buffer
}

// newASFixture serves the AS's endpoints on an httptest server; fetch reads
// client ID metadata documents (nil: SafeClient).
func newASFixture(t *testing.T, fetch *http.Client) *asFixture {
	t.Helper()
	s, c, _ := newTestStore(t)
	var buf bytes.Buffer
	a := NewAS(s, fetch, log.New(&buf, "", 0))
	mux := http.NewServeMux()
	mux.Handle("POST "+PathToken, a.Token())
	mux.Handle("POST "+PathRegister, a.Register())
	mux.Handle("POST "+PathRevoke, a.Revoke())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &asFixture{as: a, store: s, clock: c, srv: srv, log: &buf}
}

func (f *asFixture) register(t *testing.T, body any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(f.srv.URL+PathRegister, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (f *asFixture) registerClient(t *testing.T, name string, redirects ...string) string {
	t.Helper()
	status, out := f.register(t, map[string]any{"client_name": name, "redirect_uris": redirects, "token_endpoint_auth_method": "none"})
	if status != http.StatusCreated {
		t.Fatalf("register: %d %v", status, out)
	}
	return out["client_id"].(string)
}

func (f *asFixture) config(clientID string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:    clientID,
		RedirectURL: testRedirect,
		Scopes:      []string{Scope},
		Endpoint: oauth2.Endpoint{
			AuthURL:   testIssuer + PathAuthorize,
			TokenURL:  f.srv.URL + PathToken,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

// authorize builds the query of an authorization request as an oauth2
// client does, with PKCE S256 and the resource.
func authorizeQuery(t *testing.T, cfg *oauth2.Config, verifier string) url.Values {
	t.Helper()
	u, err := url.Parse(cfg.AuthCodeURL("st-1", oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", testResource)))
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

// approve runs consent and the approving decision and returns the code.
func (f *asFixture) approve(t *testing.T, q url.Values) string {
	t.Helper()
	ctx := context.Background()
	if _, err := f.as.Consent(ctx, q, testIssuer, testResources); err != nil {
		t.Fatal(err)
	}
	redirect, err := f.as.Decide(ctx, api.OAuthDecision{Query: q.Encode(), Approve: true}, testIssuer, testResources)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	return u.Query().Get("code")
}

func TestMetadataDocuments(t *testing.T) {
	pr := ProtectedResource(testIssuer)
	if pr.Resource != testResource || len(pr.AuthorizationServers) != 1 || pr.AuthorizationServers[0] != testIssuer ||
		len(pr.ScopesSupported) != 1 || pr.ScopesSupported[0] != "mcp" {
		t.Fatalf("resource metadata = %+v", pr)
	}
	data, _ := json.Marshal(AuthorizationServer(testIssuer))
	var m map[string]any
	json.Unmarshal(data, &m)
	want := map[string]any{
		"issuer":                                         testIssuer,
		"authorization_endpoint":                         testIssuer + "/oauth/authorize",
		"token_endpoint":                                 testIssuer + "/oauth/token",
		"registration_endpoint":                          testIssuer + "/oauth/register",
		"revocation_endpoint":                            testIssuer + "/oauth/revoke",
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	for k, v := range map[string]string{"code_challenge_methods_supported": "S256", "scopes_supported": "mcp", "token_endpoint_auth_methods_supported": "none"} {
		l, _ := m[k].([]any)
		if len(l) != 1 || l[0] != v {
			t.Errorf("%s = %v, want [%s]", k, m[k], v)
		}
	}
}

// TestAuthorizationCodeFlow runs registration, consent, decision, the code
// exchange with PKCE, the use of the access token, refresh rotation, reuse
// detection and revocation.
func TestAuthorizationCodeFlow(t *testing.T) {
	f := newASFixture(t, nil)
	ctx := context.Background()
	id := f.registerClient(t, "Claude Code", testRedirect)
	cfg := f.config(id)
	verifier := oauth2.GenerateVerifier()
	q := authorizeQuery(t, cfg, verifier)

	consent, err := f.as.Consent(ctx, q, testIssuer, testResources)
	if err != nil {
		t.Fatal(err)
	}
	want := api.OAuthConsent{ClientName: "Claude Code", ClientID: id, ClientKind: api.OAuthClientRegistered,
		RedirectHost: "127.0.0.1:33418", LocalhostRedirect: true, Resource: testResource}
	if consent != want {
		t.Fatalf("consent = %+v\nwant %+v", consent, want)
	}
	redirect, err := f.as.Decide(ctx, api.OAuthDecision{Query: "?" + q.Encode(), Approve: true}, testIssuer, testResources)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	if got := u.Scheme + "://" + u.Host + u.Path; got != testRedirect {
		t.Fatalf("redirect to %s", got)
	}
	if u.Query().Get("state") != "st-1" || u.Query().Get("iss") != testIssuer || u.Query().Get("code") == "" {
		t.Fatalf("redirect query = %v", u.Query())
	}
	tok, err := cfg.Exchange(ctx, u.Query().Get("code"), oauth2.VerifierOption(verifier), oauth2.SetAuthURLParam("resource", testResource))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.AccessToken, AccessPrefix) || !strings.HasPrefix(tok.RefreshToken, RefreshPrefix) ||
		tok.TokenType != "Bearer" || tok.Extra("scope") != "mcp" || tok.Expiry.IsZero() {
		t.Fatalf("token = %+v", tok)
	}
	g, err := f.store.Verify(ctx, tok.AccessToken, testResource)
	if err != nil || g.Kind != api.MCPTokenOAuth || g.Name != "Claude Code" {
		t.Fatalf("verify: %+v %v", g, err)
	}
	// The code is used up.
	if _, err := cfg.Exchange(ctx, u.Query().Get("code"), oauth2.VerifierOption(verifier)); err == nil {
		t.Fatal("a code was exchanged twice")
	}

	// Refresh rotates both tokens.
	f.clock.add(AccessLifetime + time.Minute)
	if _, err := f.store.Verify(ctx, tok.AccessToken, testResource); err == nil {
		t.Fatal("an expired access token is accepted")
	}
	fresh, err := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: tok.RefreshToken, Expiry: time.Now().Add(-time.Hour)}).Token()
	if err != nil {
		t.Fatal(err)
	}
	if fresh.RefreshToken == tok.RefreshToken || fresh.AccessToken == tok.AccessToken {
		t.Fatal("refresh did not rotate the tokens")
	}
	if _, err := f.store.Verify(ctx, fresh.AccessToken, testResource); err != nil {
		t.Fatal(err)
	}
	// The old refresh token again: the grant is revoked.
	_, err = cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: tok.RefreshToken, Expiry: time.Now().Add(-time.Hour)}).Token()
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) || re.ErrorCode != "invalid_grant" {
		t.Fatalf("reuse: %v", err)
	}
	if _, err := f.store.Verify(ctx, fresh.AccessToken, testResource); err == nil {
		t.Fatal("the grant survived the reuse of a refresh token")
	}
	if !strings.Contains(f.log.String(), "used a second time") {
		t.Fatalf("the reuse is not logged: %q", f.log.String())
	}
	assertNoSecrets(t, f.log.String(), tok.AccessToken, tok.RefreshToken, fresh.AccessToken, fresh.RefreshToken, u.Query().Get("code"), verifier)
}

func TestRevocationEndpoint(t *testing.T) {
	f := newASFixture(t, nil)
	ctx := context.Background()
	id := f.registerClient(t, "c", testRedirect)
	cfg := f.config(id)
	verifier := oauth2.GenerateVerifier()
	tok, err := cfg.Exchange(ctx, f.approve(t, authorizeQuery(t, cfg, verifier)), oauth2.VerifierOption(verifier))
	if err != nil {
		t.Fatal(err)
	}
	post := func(form url.Values) int {
		resp, err := http.PostForm(f.srv.URL+PathRevoke, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := post(url.Values{}); c != http.StatusBadRequest {
		t.Fatalf("revoke without token: %d", c)
	}
	if c := post(url.Values{"token": {"omo_at_unknown"}}); c != http.StatusOK {
		t.Fatalf("revoke of an unknown token: %d", c)
	}
	if c := post(url.Values{"token": {tok.RefreshToken}, "client_id": {"someone-else"}}); c != http.StatusOK {
		t.Fatalf("revoke by another client: %d", c)
	}
	if _, err := f.store.Verify(ctx, tok.AccessToken, testResource); err != nil {
		t.Fatal("another client revoked the grant")
	}
	if c := post(url.Values{"token": {tok.AccessToken}, "client_id": {id}}); c != http.StatusOK {
		t.Fatalf("revoke: %d", c)
	}
	if _, err := f.store.Verify(ctx, tok.AccessToken, testResource); err == nil {
		t.Fatal("a revoked access token is accepted")
	}
	if _, err := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: tok.RefreshToken, Expiry: time.Now().Add(-time.Hour)}).Token(); err == nil {
		t.Fatal("the refresh token of a revoked grant works")
	}
}

func TestDenyRedirectsAccessDenied(t *testing.T) {
	f := newASFixture(t, nil)
	id := f.registerClient(t, "c", testRedirect)
	q := authorizeQuery(t, f.config(id), oauth2.GenerateVerifier())
	redirect, err := f.as.Decide(context.Background(), api.OAuthDecision{Query: q.Encode()}, testIssuer, testResources)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	if u.Query().Get("error") != "access_denied" || u.Query().Get("code") != "" || u.Query().Get("state") != "st-1" || u.Query().Get("iss") != testIssuer {
		t.Fatalf("redirect = %s", redirect)
	}
}

func TestAuthorizationRequestRefusals(t *testing.T) {
	f := newASFixture(t, nil)
	id := f.registerClient(t, "c", testRedirect)
	other := f.registerClient(t, "other", "https://other.example/cb")
	base := authorizeQuery(t, f.config(id), oauth2.GenerateVerifier())
	forged := strings.Replace(other, "other", "evil!", 1)
	tampered := id[:len(id)-2] + "AA"
	for _, tc := range []struct {
		name string
		set  map[string]string
	}{
		{"no client", map[string]string{"client_id": ""}},
		{"unknown client", map[string]string{"client_id": "random-client"}},
		{"tampered client id", map[string]string{"client_id": tampered}},
		{"forged client id", map[string]string{"client_id": forged}},
		{"redirect of another client", map[string]string{"redirect_uri": "https://other.example/cb"}},
		{"redirect with another port", map[string]string{"redirect_uri": "http://127.0.0.1:33419/callback"}},
		{"no redirect", map[string]string{"redirect_uri": ""}},
		{"plain PKCE", map[string]string{"code_challenge_method": "plain"}},
		{"no PKCE", map[string]string{"code_challenge_method": "", "code_challenge": ""}},
		{"short challenge", map[string]string{"code_challenge": "abc"}},
		{"implicit flow", map[string]string{"response_type": "token"}},
		{"other scope", map[string]string{"scope": "mcp board"}},
		{"other resource", map[string]string{"resource": "https://evil.example/mcp"}},
		{"resource of the base without /mcp", map[string]string{"resource": testIssuer}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{}
			for k, v := range base {
				q[k] = v
			}
			for k, v := range tc.set {
				q.Set(k, v)
			}
			if _, err := f.as.Consent(context.Background(), q, testIssuer, testResources); err == nil {
				t.Fatal("consent accepted the request")
			} else {
				wantCode(t, err, api.CodeBadRequest)
			}
			if _, err := f.as.Decide(context.Background(), api.OAuthDecision{Query: q.Encode(), Approve: true}, testIssuer, testResources); err == nil {
				t.Fatal("the decision accepted the request")
			}
		})
	}
}

// TestResourceDefaultsToIssuer accepts a request without resource for the
// endpoint at the issuer's address.
func TestResourceDefaultsToIssuer(t *testing.T) {
	f := newASFixture(t, nil)
	id := f.registerClient(t, "c", testRedirect)
	q := authorizeQuery(t, f.config(id), oauth2.GenerateVerifier())
	q.Del("resource")
	c, err := f.as.Consent(context.Background(), q, testIssuer, testResources)
	if err != nil || c.Resource != testResource {
		t.Fatalf("consent = %+v, %v", c, err)
	}
}

func TestTokenRefusals(t *testing.T) {
	f := newASFixture(t, nil)
	id := f.registerClient(t, "c", testRedirect)
	cfg := f.config(id)
	type exchange struct {
		name    string
		mutate  func(v url.Values)
		advance time.Duration
		want    string
	}
	for _, tc := range []exchange{
		{"wrong verifier", func(v url.Values) { v.Set("code_verifier", oauth2.GenerateVerifier()) }, 0, "invalid_grant"},
		{"no verifier", func(v url.Values) { v.Del("code_verifier") }, 0, "invalid_grant"},
		{"other client", func(v url.Values) { v.Set("client_id", "omo_dcr_x.y") }, 0, "invalid_grant"},
		{"other redirect", func(v url.Values) { v.Set("redirect_uri", "http://127.0.0.1:1/callback") }, 0, "invalid_grant"},
		{"other resource", func(v url.Values) { v.Set("resource", "http://127.0.0.1:8090/mcp") }, 0, "invalid_target"},
		{"expired code", func(url.Values) {}, codeLifetime, "invalid_grant"},
		{"unknown grant type", func(v url.Values) { v.Set("grant_type", "password") }, 0, "unsupported_grant_type"},
		{"client credentials", func(v url.Values) { v.Set("grant_type", "client_credentials") }, 0, "unsupported_grant_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier := oauth2.GenerateVerifier()
			code := f.approve(t, authorizeQuery(t, cfg, verifier))
			f.clock.add(tc.advance)
			form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {id},
				"redirect_uri": {testRedirect}, "code_verifier": {verifier}, "resource": {testResource}}
			tc.mutate(form)
			status, out := postToken(t, f.srv.URL, form)
			if status != http.StatusBadRequest || out["error"] != tc.want {
				t.Fatalf("answer %d %v, want 400 %s", status, out, tc.want)
			}
			if form.Get("grant_type") != "authorization_code" {
				return
			}
			// A refused exchange uses the code up.
			if status, _ := postToken(t, f.srv.URL, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {id},
				"redirect_uri": {testRedirect}, "code_verifier": {verifier}}); status == http.StatusOK {
				t.Fatal("the code still works after a refused exchange")
			}
		})
	}
	t.Run("not a form", func(t *testing.T) {
		resp, err := http.Post(f.srv.URL+PathToken, "application/json", strings.NewReader(`{"grant_type":"authorization_code"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("answer %d", resp.StatusCode)
		}
	})
	t.Run("refresh with another scope", func(t *testing.T) {
		status, out := postToken(t, f.srv.URL, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"omo_rt_x"}, "scope": {"admin"}})
		if status != http.StatusBadRequest || out["error"] != "invalid_scope" {
			t.Fatalf("answer %d %v", status, out)
		}
	})
}

func postToken(t *testing.T, base string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := http.PostForm(base+PathToken, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestRegistrationRules(t *testing.T) {
	f := newASFixture(t, nil)
	for _, tc := range []struct {
		name string
		body any
		want string
	}{
		{"confidential client", map[string]any{"redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "client_secret_basic"}, "invalid_client_metadata"},
		{"http beyond loopback", map[string]any{"redirect_uris": []string{"http://example.com/cb"}}, "invalid_redirect_uri"},
		{"fragment", map[string]any{"redirect_uris": []string{"https://example.com/cb#x"}}, "invalid_redirect_uri"},
		{"custom scheme", map[string]any{"redirect_uris": []string{"myapp://cb"}}, "invalid_redirect_uri"},
		{"credentials", map[string]any{"redirect_uris": []string{"https://u:p@example.com/cb"}}, "invalid_redirect_uri"},
		{"no redirect", map[string]any{"redirect_uris": []string{}}, "invalid_redirect_uri"},
		{"implicit grant", map[string]any{"redirect_uris": []string{testRedirect}, "grant_types": []string{"implicit"}}, "invalid_client_metadata"},
		{"board scope", map[string]any{"redirect_uris": []string{testRedirect}, "scope": "mcp board"}, "invalid_client_metadata"},
		{"not JSON", "x", "invalid_client_metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, out := f.register(t, tc.body)
			if status != http.StatusBadRequest || out["error"] != tc.want {
				t.Fatalf("answer %d %v, want 400 %s", status, out, tc.want)
			}
		})
	}
	t.Run("name is cleaned", func(t *testing.T) {
		status, out := f.register(t, map[string]any{"redirect_uris": []string{"http://localhost/cb", "https://app.example/cb"},
			"client_name": "  Evil‮\x1b[31m " + strings.Repeat("x", 200)})
		if status != http.StatusCreated {
			t.Fatalf("answer %d %v", status, out)
		}
		name := out["client_name"].(string)
		if strings.ContainsAny(name, "‮\x1b") || len([]rune(name)) > maxNameLength {
			t.Fatalf("name = %q", name)
		}
		if out["token_endpoint_auth_method"] != "none" || !strings.HasPrefix(out["client_id"].(string), dcrPrefix) {
			t.Fatalf("answer %v", out)
		}
	})
}

// cimdServer serves client ID metadata documents over TLS; doc gets the
// document's own address.
func cimdServer(t *testing.T, doc func(self string) (int, string)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		status, body := doc(srv.URL + r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func cimdQuery(t *testing.T, clientID, redirect string) url.Values {
	t.Helper()
	cfg := &oauth2.Config{ClientID: clientID, RedirectURL: redirect, Endpoint: oauth2.Endpoint{AuthURL: testIssuer + PathAuthorize}}
	u, _ := url.Parse(cfg.AuthCodeURL("s", oauth2.S256ChallengeOption(oauth2.GenerateVerifier()), oauth2.SetAuthURLParam("resource", testResource)))
	return u.Query()
}

func docJSON(id, name string, redirects ...string) string {
	b, _ := json.Marshal(map[string]any{"client_id": id, "client_name": name, "redirect_uris": redirects, "token_endpoint_auth_method": "none"})
	return string(b)
}

func TestClientIDMetadataDocument(t *testing.T) {
	srv, hits := cimdServer(t, func(self string) (int, string) {
		return http.StatusOK, docJSON(self, "Antigravity", "https://app.example/oauth-callback", testRedirect)
	})
	// The test server listens on loopback, which SafeClient refuses; its
	// own client reaches it.
	f := newASFixture(t, srv.Client())
	ctx := context.Background()
	id := srv.URL + "/oauth/client-metadata.json"
	q := cimdQuery(t, id, "https://app.example/oauth-callback")
	c, err := f.as.Consent(ctx, q, testIssuer, testResources)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientKind != api.OAuthClientCIMD || c.ClientName != "Antigravity" || c.ClientID != id || c.RedirectHost != "app.example" || c.LocalhostRedirect {
		t.Fatalf("consent = %+v", c)
	}
	// Cached: the decision does not fetch again.
	redirect, err := f.as.Decide(ctx, api.OAuthDecision{Query: q.Encode(), Approve: true}, testIssuer, testResources)
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("the document was fetched %d times", hits.Load())
	}
	f.clock.add(documentTTL)
	if _, err := f.as.Consent(ctx, q, testIssuer, testResources); err != nil || hits.Load() != 2 {
		t.Fatalf("after the cache time: %v, %d fetches", err, hits.Load())
	}
	u, _ := url.Parse(redirect)
	if u.Host != "app.example" || u.Query().Get("code") == "" {
		t.Fatalf("redirect = %s", redirect)
	}
}

func TestClientIDMetadataDocumentRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  func(self string) (int, string)
		path string
		why  string
	}{
		{"client_id of another address", func(string) (int, string) {
			return 200, docJSON("https://other.example/doc", "x", testRedirect)
		}, "/doc", "client_id"},
		{"no name", func(self string) (int, string) { return 200, docJSON(self, " ​ ", testRedirect) }, "/doc", "client_name"},
		{"redirect not listed", func(self string) (int, string) { return 200, docJSON(self, "x", "http://127.0.0.1:1/other") }, "/doc", "redirect_uri"},
		{"http redirect beyond loopback", func(self string) (int, string) {
			return 200, docJSON(self, "x", testRedirect, "http://example.com/cb")
		}, "/doc", "redirect URI"},
		{"confidential client", func(self string) (int, string) {
			b, _ := json.Marshal(map[string]any{"client_id": self, "client_name": "x", "redirect_uris": []string{testRedirect}, "token_endpoint_auth_method": "private_key_jwt"})
			return 200, string(b)
		}, "/doc", "public clients"},
		{"not found", func(string) (int, string) { return 404, "{}" }, "/doc", "404"},
		{"not JSON", func(string) (int, string) { return 200, "<html>" }, "/doc", "JSON"},
		{"too large", func(self string) (int, string) {
			return 200, `{"client_id":"` + self + `","client_name":"` + strings.Repeat("x", maxDocument) + `"}`
		}, "/doc", "larger"},
		{"no path", func(self string) (int, string) { return 200, docJSON(self, "x", testRedirect) }, "/", "path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := cimdServer(t, tc.doc)
			f := newASFixture(t, srv.Client())
			_, err := f.as.Consent(context.Background(), cimdQuery(t, srv.URL+tc.path, testRedirect), testIssuer, testResources)
			if err == nil || !strings.Contains(err.Error(), tc.why) {
				t.Fatalf("error = %v, want one about %s", err, tc.why)
			}
		})
	}
	t.Run("http address", func(t *testing.T) {
		f := newASFixture(t, nil)
		_, err := f.as.Consent(context.Background(), cimdQuery(t, "http://app.example/doc", testRedirect), testIssuer, testResources)
		if err == nil {
			t.Fatal("an http client id was accepted")
		}
	})
}

// TestSafeClientRefusesLoopback runs the production fetcher against a
// document on loopback: the connection is refused at dial time.
func TestSafeClientRefusesLoopback(t *testing.T) {
	srv, hits := cimdServer(t, func(self string) (int, string) { return 200, docJSON(self, "x", testRedirect) })
	f := newASFixture(t, nil)
	_, err := f.as.Consent(context.Background(), cimdQuery(t, srv.URL+"/doc", testRedirect), testIssuer, testResources)
	if err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("error = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("the loopback server was reached")
	}
	// A name that resolves to loopback is refused the same way.
	_, err = SafeClient().Get(strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/doc")
	var refused errRefusedAddress
	if !errors.As(err, &refused) {
		t.Fatalf("localhost: %v", err)
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"93.184.216.34": true, "2606:2800:220:1::1": true, "8.8.8.8": true,
		"127.0.0.1": false, "::1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.69.10": false,
		"169.254.169.254": false, "fe80::1": false, "fc00::1": false, "224.0.0.1": false, "ff02::1": false,
		"0.0.0.0": false, "::": false, "0.1.2.3": false, "100.64.0.1": false, "255.255.255.255": false,
		"::ffff:127.0.0.1": false, "::ffff:10.0.0.1": false,
	} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("publicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestSafeClientRedirectRule(t *testing.T) {
	https, _ := http.NewRequest(http.MethodGet, "https://a.example/doc", nil)
	plain, _ := http.NewRequest(http.MethodGet, "http://a.example/doc", nil)
	if checkRedirect(plain, []*http.Request{https}) == nil {
		t.Fatal("a redirect to http was followed")
	}
	if checkRedirect(https, []*http.Request{https}) != nil {
		t.Fatal("a redirect to https was refused")
	}
	if checkRedirect(https, []*http.Request{https, https, https, https}) == nil {
		t.Fatal("a fifth hop was followed")
	}
	if tr := SafeClient().Transport.(*http.Transport); tr.Proxy != nil {
		t.Fatal("the fetcher uses a proxy, which would bypass the address check")
	}
}

func assertNoSecrets(t *testing.T, logText string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(logText, s) {
			t.Fatalf("the log contains a secret: %q", logText)
		}
	}
}

// The localhost warning follows the redirect the request uses, not the
// client's other registered redirects.
func TestConsentWarnsAboutTheLocalhostRedirectInUse(t *testing.T) {
	f := newASFixture(t, nil)
	ctx := context.Background()
	id := f.registerClient(t, "Mixed", testRedirect, "https://app.example/cb")
	for redirect, want := range map[string]bool{testRedirect: true, "https://app.example/cb": false} {
		cfg := f.config(id)
		cfg.RedirectURL = redirect
		consent, err := f.as.Consent(ctx, authorizeQuery(t, cfg, oauth2.GenerateVerifier()), testIssuer, testResources)
		if err != nil {
			t.Fatal(err)
		}
		if consent.LocalhostRedirect != want {
			t.Errorf("redirect %s: localhost warning %v", redirect, consent.LocalhostRedirect)
		}
	}
}
