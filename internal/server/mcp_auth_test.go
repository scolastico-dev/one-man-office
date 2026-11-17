package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"golang.org/x/oauth2"
)

// oauthRoutes are the consent routes of the web app's /oauth/authorize page.
var oauthRoutes = []extensionRoute{
	{"GET /api/oauth/consent", nil},
	{"POST /api/oauth/consent", api.OAuthDecision{Query: "client_id=x"}},
}

// TestOAuthRoutesRefuseAgents: an agent session can neither read an
// authorization request nor approve one.
func TestOAuthRoutesRefuseAgents(t *testing.T) {
	s := newTestServer(t)
	sess := liveSession(t, s, api.RoleDeveloper)
	for _, r := range oauthRoutes {
		if !s.registeredPattern(r.pattern) {
			t.Fatalf("%s is not registered", r.pattern)
		}
		method, path, _ := strings.Cut(r.pattern, " ")
		if rec := socketDo(t, s, method, path, r.body, sess.ID); rec.Code != http.StatusForbidden {
			t.Fatalf("an agent got %d from %s, want 403", rec.Code, r.pattern)
		}
	}
	for _, row := range s.routeTable {
		if _, path, _ := strings.Cut(row.pattern, " "); strings.HasPrefix(path, "/api/oauth") && row.access != userOnly {
			t.Errorf("%s is %s, want user only", row.pattern, row.access)
		}
	}
}

// postForm sends an OAuth form request (outside /api, no login token).
func (b *browser) postForm(path string, form url.Values) (int, map[string]any) {
	b.t.Helper()
	req, err := http.NewRequest(http.MethodPost, b.srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		b.t.Fatal(err)
	}
	req.Host = hostOmoLocalhost + ":" + b.port
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.srv.Client().Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMCPOAuthFlowThroughServer runs a client against the server's own
// routes: discovery, registration, consent and decision as the user, code
// exchange with PKCE, refresh rotation, reuse and revocation; the server log
// holds none of the secrets, failing calls included.
func TestMCPOAuthFlowThroughServer(t *testing.T) {
	var logOut syncBuffer
	s, err := newServer(Options{Layout: testHome(t), Config: testConfig(), Version: "test"}, &logOut)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	b := newBrowser(t, s)
	base := "http://" + hostOmoLocalhost + ":" + b.port
	resource := base + "/mcp"
	ctx := context.Background()

	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		resp := b.do(http.MethodGet, p, nil, nil)
		wantStatus(t, resp.StatusCode, http.StatusOK)
		m := decodeJSON(t, resp)
		if m["resource"] != resource || m["authorization_servers"].([]any)[0] != base {
			t.Fatalf("%s = %v", p, m)
		}
	}
	resp := b.do(http.MethodGet, "/.well-known/oauth-authorization-server", nil, nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	meta := decodeJSON(t, resp)
	if meta["issuer"] != base || meta["token_endpoint"] != base+"/oauth/token" {
		t.Fatalf("metadata = %v", meta)
	}

	resp = b.do(http.MethodPost, "/oauth/register", map[string]any{"client_name": "Claude Code", "redirect_uris": []string{"http://localhost:5555/callback"}}, nil)
	wantStatus(t, resp.StatusCode, http.StatusCreated)
	clientID := decodeJSON(t, resp)["client_id"].(string)

	verifier := oauth2.GenerateVerifier()
	cfg := &oauth2.Config{ClientID: clientID, RedirectURL: "http://localhost:5555/callback", Scopes: []string{"mcp"},
		Endpoint: oauth2.Endpoint{AuthURL: base + "/oauth/authorize", TokenURL: base + "/oauth/token", AuthStyle: oauth2.AuthStyleInParams}}
	authURL, _ := url.Parse(cfg.AuthCodeURL("st", oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", resource)))

	// The consent API is behind the login token, like every API route.
	resp = b.do(http.MethodGet, "/api/oauth/consent?"+authURL.RawQuery, nil, nil)
	wantStatus(t, resp.StatusCode, http.StatusUnauthorized)
	resp = b.do(http.MethodGet, "/api/oauth/consent?"+authURL.RawQuery, nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	consent := decodeBody[api.OAuthConsent](t, resp.Body)
	if consent.ClientName != "Claude Code" || consent.RedirectHost != "localhost:5555" || consent.Resource != resource || !consent.LocalhostOnly {
		t.Fatalf("consent = %+v", consent)
	}
	resp = b.do(http.MethodPost, "/api/oauth/consent", api.OAuthDecision{Query: authURL.RawQuery, Approve: true}, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	back, _ := url.Parse(decodeBody[api.OAuthRedirect](t, resp.Body).Redirect)
	code := back.Query().Get("code")
	if back.Host != "localhost:5555" || code == "" || back.Query().Get("iss") != base || back.Query().Get("state") != "st" {
		t.Fatalf("redirect = %s", back)
	}

	// A wrong verifier uses the code up and leaks nothing into the log.
	status, out := b.postForm("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {cfg.RedirectURL}, "code_verifier": {oauth2.GenerateVerifier()}})
	if status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier: %d %v", status, out)
	}
	resp = b.do(http.MethodPost, "/api/oauth/consent", api.OAuthDecision{Query: authURL.RawQuery, Approve: true}, bearer(s))
	back, _ = url.Parse(decodeBody[api.OAuthRedirect](t, resp.Body).Redirect)
	code = back.Query().Get("code")
	status, out = b.postForm("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {cfg.RedirectURL}, "code_verifier": {verifier}, "resource": {resource}})
	if status != http.StatusOK {
		t.Fatalf("exchange: %d %v", status, out)
	}
	access, refresh := out["access_token"].(string), out["refresh_token"].(string)

	g, err := s.mcpAuth.tokens.Verify(ctx, access, resource)
	if err != nil || g.Kind != api.MCPTokenOAuth {
		t.Fatalf("verify: %+v %v", g, err)
	}
	if _, err := s.mcpAuth.tokens.Verify(ctx, access, "http://localhost:"+b.port+"/mcp"); err == nil {
		t.Fatal("the token is accepted at another address of omo")
	}
	// The login token is no MCP bearer.
	if _, err := s.mcpAuth.tokens.Verify(ctx, s.token, resource); err == nil {
		t.Fatal("the login token is accepted as MCP bearer")
	}

	status, out = b.postForm("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	if status != http.StatusOK {
		t.Fatalf("refresh: %d %v", status, out)
	}
	access2, refresh2 := out["access_token"].(string), out["refresh_token"].(string)
	status, _ = b.postForm("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	if status != http.StatusBadRequest {
		t.Fatalf("reuse: %d", status)
	}
	if _, err := s.mcpAuth.tokens.Verify(ctx, access2, resource); err == nil {
		t.Fatal("the grant survived the reuse of its refresh token")
	}
	status, _ = b.postForm("/oauth/revoke", url.Values{"token": {refresh2}})
	wantStatus(t, status, http.StatusOK)
	status, _ = b.postForm("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {"unknown"}})
	wantStatus(t, status, http.StatusBadRequest)

	logText := logOut.String()
	if !strings.Contains(logText, "used a second time") {
		t.Fatalf("the reuse is not logged: %q", logText)
	}
	for _, secret := range []string{access, refresh, access2, refresh2, code, verifier, s.token} {
		if strings.Contains(logText, secret) {
			t.Fatalf("the server log holds a secret: %q", logText)
		}
	}
}

// TestMCPBase states the issuer omo advertises: public_url when the request
// came by its host, else the request's own address, plain http included.
func TestMCPBase(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) {
		c.Host = "0.0.0.0"
		c.Port = 8090
		c.PublicURL = "https://omo.example.com/"
	})
	for host, want := range map[string]string{
		"omo.example.com":      "https://omo.example.com",
		"omo.localhost:8090":   "http://omo.localhost:8090",
		"192.168.1.5:8090":     "http://192.168.1.5:8090",
		"OMO.Example.com:443":  "https://omo.example.com",
		"omo.example.com:8090": "http://omo.example.com:8090",
	} {
		r := httptest.NewRequest(http.MethodGet, "http://x/.well-known/oauth-authorization-server", nil)
		r.Host = host
		if got := s.mcpBase(r); got != want {
			t.Errorf("Host %s: base %s, want %s", host, got, want)
		}
	}
	plain := newTestServer(t, func(c *serverconfig.Config) { c.Host = "0.0.0.0"; c.Port = 8090 })
	r := httptest.NewRequest(http.MethodGet, "http://x/mcp", nil)
	r.Host = "192.168.1.5:8090"
	if got := plain.mcpResource(r); got != "http://192.168.1.5:8090/mcp" {
		t.Fatalf("resource without public_url: %s", got)
	}
	if res := s.mcpResources(r); !slices.Contains(res, "https://omo.example.com/mcp") || !slices.Contains(res, "http://localhost:8090/mcp") || !slices.Contains(res, "http://192.168.1.5:8090/mcp") {
		t.Fatalf("resources = %v", res)
	}
}
