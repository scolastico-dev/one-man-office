package mcprelay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

// fakeAS is an MCP server protected by its own authorization server:
// protected resource and server metadata, dynamic registration, an
// authorize endpoint that approves at once and redirects to the callback,
// tokens with refresh rotation, and revocation.
type fakeAS struct {
	t   *testing.T
	srv *httptest.Server
	mcp http.Handler

	mu         sync.Mutex
	clients    map[string]string // client id → redirect
	codes      map[string]fakeCode
	access     map[string]bool
	refresh    map[string]bool
	registered int
	refreshes  int
	revoked    []string
	expiresIn  int
	issued     []string
}

type fakeCode struct {
	client, redirect, challenge, resource string
}

func newFakeAS(t *testing.T) *fakeAS {
	a := &fakeAS{
		t:         t,
		mcp:       fakemcp.Handler(fakemcp.Config{}),
		clients:   map[string]string{},
		codes:     map[string]fakeCode{},
		access:    map[string]bool{},
		refresh:   map[string]bool{},
		expiresIn: 3600,
	}
	a.srv = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *fakeAS) url() string { return a.srv.URL + "/mcp" }

func random() string {
	return rand.Text()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (a *fakeAS) serve(w http.ResponseWriter, r *http.Request) {
	base := a.srv.URL
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource/mcp":
		writeJSON(w, 200, map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}, "scopes_supported": []string{"tools"}})
	case "/.well-known/oauth-authorization-server":
		writeJSON(w, 200, map[string]any{
			"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token",
			"registration_endpoint": base + "/register", "revocation_endpoint": base + "/revoke",
			"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
			"authorization_response_iss_parameter_supported": true,
		})
	case "/register":
		var meta struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		json.NewDecoder(r.Body).Decode(&meta)
		a.mu.Lock()
		a.registered++
		id := fmt.Sprintf("client-%d", a.registered)
		a.clients[id] = meta.RedirectURIs[0]
		a.mu.Unlock()
		writeJSON(w, 201, map[string]any{"client_id": id, "redirect_uris": meta.RedirectURIs, "token_endpoint_auth_method": "none"})
	case "/authorize":
		q := r.URL.Query()
		a.mu.Lock()
		redirect, ok := a.clients[q.Get("client_id")]
		a.mu.Unlock()
		if !ok || redirect != q.Get("redirect_uri") || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "bad authorization request", 400)
			return
		}
		code := random()
		a.mu.Lock()
		a.codes[code] = fakeCode{q.Get("client_id"), redirect, q.Get("code_challenge"), q.Get("resource")}
		a.mu.Unlock()
		u, _ := url.Parse(redirect)
		u.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}, "iss": {base}}.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	case "/token":
		r.ParseForm()
		if id, _, ok := r.BasicAuth(); ok {
			r.Form.Set("client_id", id)
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			c, ok := a.codes[r.Form.Get("code")]
			delete(a.codes, r.Form.Get("code"))
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || c.client != r.Form.Get("client_id") || c.redirect != r.Form.Get("redirect_uri") ||
				base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge || c.resource != base+"/mcp" {
				writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
		case "refresh_token":
			if !a.refresh[r.Form.Get("refresh_token")] {
				writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
			delete(a.refresh, r.Form.Get("refresh_token"))
			a.refreshes++
		default:
			writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
			return
		}
		at, rt := "at-"+random(), "rt-"+random()
		a.access[at], a.refresh[rt] = true, true
		a.issued = append(a.issued, at, rt)
		writeJSON(w, 200, map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": a.expiresIn, "refresh_token": rt, "scope": "tools"})
	case "/revoke":
		r.ParseForm()
		a.mu.Lock()
		a.revoked = append(a.revoked, r.Form.Get("token"))
		delete(a.refresh, r.Form.Get("token"))
		delete(a.access, r.Form.Get("token"))
		a.mu.Unlock()
		w.WriteHeader(200)
	case "/mcp":
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		a.mu.Lock()
		ok := a.access[tok]
		a.mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/.well-known/oauth-protected-resource/mcp", scope="tools"`)
			http.Error(w, "unauthorized", 401)
			return
		}
		a.mcp.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

// revokeAll ends every token, as a provider does when the user removes
// the grant.
func (a *fakeAS) revokeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	clear(a.access)
	clear(a.refresh)
}

func (a *fakeAS) secrets() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.issued)
}

func oauthServer(name, u string) api.MCPServer {
	s := httpServer(name, u)
	s.Auth = api.MCPAuthOAuth
	return s
}

// signIn runs Connect, follows the authorization address like a browser
// and hands the redirect to the callback; it returns the callback's page.
func (f *fixture) signIn(name string) (int, string) {
	f.t.Helper()
	u, err := f.pool.Connect(context.Background(), name)
	if err != nil {
		f.t.Fatalf("connect: %v", err)
	}
	if u == "" {
		f.t.Fatal("connect gave no authorization address")
	}
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := browser.Get(u)
	if err != nil {
		f.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		f.t.Fatalf("authorize answered %d", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Path != "/oauth/callback" {
		f.t.Fatalf("redirected to %s", loc)
	}
	rec := httptest.NewRecorder()
	f.pool.Callback(rec, httptest.NewRequest(http.MethodGet, "/oauth/callback?"+loc.RawQuery, nil))
	return rec.Code, rec.Body.String()
}

func TestOAuthSignInRefreshAndLoginAgain(t *testing.T) {
	as := newFakeAS(t)
	f := newFixture(t, nil, oauthServer("remote", as.url()))
	id := agent("s", t.TempDir(), api.RoleDeveloper)

	// Without a sign-in the server's tools are left out.
	if tools, _ := f.pool.Tools(context.Background(), id); len(tools) != 0 {
		t.Fatalf("tools before the sign-in: %v", toolNames(tools))
	}
	if st := f.state("remote"); st.State != api.MCPStateNeedsLogin || st.Auth.State != api.MCPAuthStateNeedsLogin {
		t.Fatalf("state %+v", st)
	}
	res := f.call(id, "remote__echo", nil)
	if !strings.Contains(resultText(res), "needs a sign-in") {
		t.Fatalf("answered %q", resultText(res))
	}

	code, page := f.signIn("remote")
	if code != 200 || !strings.Contains(page, "Connected to remote. You can close this tab.") {
		t.Fatalf("callback %d: %s", code, page)
	}
	st := f.state("remote")
	if st.Auth.State != api.MCPAuthStateConnected || !slices.Equal(st.Auth.Scopes, []string{"tools"}) {
		t.Fatalf("state after the sign-in %+v", st)
	}
	stored, ok, _ := f.store.Load(context.Background(), "remote")
	if !ok || stored.Token == nil || stored.ClientID != "client-1" || stored.Issuer != as.srv.URL || stored.RedirectURI != "http://localhost:18123/oauth/callback" {
		t.Fatalf("stored %+v", stored)
	}
	if res := f.call(id, "remote__echo", map[string]string{"text": "x"}); resultText(res) != "echo: x" {
		t.Fatalf("answered %q", resultText(res))
	}

	// Refresh: tokens that expire at once are refreshed and stored.
	as.mu.Lock()
	as.expiresIn = 1
	as.mu.Unlock()
	f.signIn("remote")
	if as.registered != 1 {
		t.Fatalf("registered %d clients; the first registration is reused", as.registered)
	}
	first, _, _ := f.store.Load(context.Background(), "remote")
	for range 2 {
		if res := f.call(id, "remote__echo", nil); res.IsError {
			t.Fatalf("with expiring tokens: %q\n%s", resultText(res), f.logText("remote"))
		}
	}
	later, _, _ := f.store.Load(context.Background(), "remote")
	if as.refreshes == 0 || later.Token.AccessToken == first.Token.AccessToken {
		t.Fatalf("refreshes %d; the refreshed token was not stored", as.refreshes)
	}

	// The provider ends the grant: omo needs a new sign-in.
	as.revokeAll()
	gen := f.pool.Generation()
	res = f.call(id, "remote__echo", nil)
	if !res.IsError {
		t.Fatal("a revoked grant answered")
	}
	eventually(t, "needs_login", func() bool { return f.state("remote").State == api.MCPStateNeedsLogin })
	eventually(t, "the tool lists to change", func() bool { return f.pool.Generation() > gen })
	if tools, _ := f.pool.Tools(context.Background(), id); len(tools) != 0 {
		t.Fatalf("tools without a sign-in: %v", toolNames(tools))
	}
	as.mu.Lock()
	as.expiresIn = 3600
	as.mu.Unlock()
	f.signIn("remote")
	if res := f.call(id, "remote__echo", map[string]string{"text": "again"}); resultText(res) != "echo: again" {
		t.Fatalf("after signing in again: %q", resultText(res))
	}

	// Disconnect revokes and forgets the sign-in.
	current, _, _ := f.store.Load(context.Background(), "remote")
	if err := f.pool.Disconnect(context.Background(), "remote"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := f.store.Load(context.Background(), "remote"); ok {
		t.Fatal("the sign-in is still stored")
	}
	if !slices.Contains(as.revoked, current.Token.RefreshToken) {
		t.Fatalf("revoked %v", as.revoked)
	}
	if st := f.state("remote"); st.State != api.MCPStateNeedsLogin {
		t.Fatalf("state after disconnect %+v", st)
	}

	// No token, client secret or code reaches the log, the events or the
	// states.
	all := f.logText("remote") + f.events.all()
	raw, _ := json.Marshal(f.pool.Status())
	all += string(raw)
	for _, secret := range as.secrets() {
		if strings.Contains(all, secret) {
			t.Fatalf("a token reached the log, the events or the state")
		}
	}
}

func TestOAuthWithAPreRegisteredClient(t *testing.T) {
	as := newFakeAS(t)
	as.clients["my-client"] = "http://localhost:18123/oauth/callback"
	s := oauthServer("remote", as.url())
	s.OAuth = api.MCPOAuth{ClientID: "my-client", ClientSecret: "a-client-secret-value", Scopes: []string{"tools"}}
	f := newFixture(t, nil, s)
	if code, page := f.signIn("remote"); code != 200 {
		t.Fatalf("callback %d: %s", code, page)
	}
	if as.registered != 0 {
		t.Fatal("a pre-registered client was registered again")
	}
	stored, _, _ := f.store.Load(context.Background(), "remote")
	if stored.ClientID != "my-client" {
		t.Fatalf("stored %+v", stored)
	}
	if res := f.call(agent("s", t.TempDir(), api.RoleDeveloper), "remote__echo", map[string]string{"text": "x"}); resultText(res) != "echo: x" {
		t.Fatalf("answered %q", resultText(res))
	}
}

func TestOAuthSignInSurvivesARestartOfOmo(t *testing.T) {
	as := newFakeAS(t)
	f := newFixture(t, nil, oauthServer("remote", as.url()))
	f.signIn("remote")
	// A new pool over the same store: the stored sign-in is used.
	g := newFixture(t, nil, oauthServer("remote", as.url()))
	g.store = f.store
	g.pool.store = f.store
	if res := g.call(agent("s", t.TempDir(), api.RoleDeveloper), "remote__echo", map[string]string{"text": "x"}); resultText(res) != "echo: x" {
		t.Fatalf("answered %q", resultText(res))
	}
}

func TestCallbackOfAnUnknownSignIn(t *testing.T) {
	f := newFixture(t, nil)
	rec := httptest.NewRecorder()
	f.pool.Callback(rec, httptest.NewRequest(http.MethodGet, "/oauth/callback?code=x&state=nothing", nil))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "This sign-in is unknown or expired.") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestTheProviderRefusesTheSignIn(t *testing.T) {
	as := newFakeAS(t)
	f := newFixture(t, nil, oauthServer("remote", as.url()))
	u, err := f.pool.Connect(context.Background(), "remote")
	if err != nil {
		t.Fatal(err)
	}
	state := mustParse(t, u).Query().Get("state")
	rec := httptest.NewRecorder()
	f.pool.Callback(rec, httptest.NewRequest(http.MethodGet, "/oauth/callback?error=access_denied&error_description=%3Cb%3Eno%3C%2Fb%3E&state="+state, nil))
	body := rec.Body.String()
	if rec.Code != http.StatusBadGateway || !strings.Contains(body, "access_denied: &lt;b&gt;no&lt;/b&gt;") {
		t.Fatalf("%d %s", rec.Code, body)
	}
	if st := f.state("remote"); st.Auth.State != api.MCPAuthStateError {
		t.Fatalf("state %+v", st)
	}
}

func TestConnectRefusesServersWithoutOAuth(t *testing.T) {
	srv := httptest.NewServer(fakemcp.Handler(fakemcp.Config{}))
	t.Cleanup(srv.Close)
	open := oauthServer("open", srv.URL)
	f := newFixture(t, nil, httpServer("plain", srv.URL), open)
	if _, err := f.pool.Connect(context.Background(), "plain"); err == nil {
		t.Fatal("a server with headers started a sign-in")
	}
	if _, err := f.pool.Connect(context.Background(), "open"); err == nil || !strings.Contains(err.Error(), "did not ask for a sign-in") {
		t.Fatalf("a server without authorization: %v", err)
	}
	if _, err := f.pool.Connect(context.Background(), "nothing"); err == nil {
		t.Fatal("an unknown server started a sign-in")
	}
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestNoSecretInMCPLogOrEvents: the values of a server's env and headers
// never appear in its log, the events, the states or the errors of failing
// calls, even when the server itself prints them.
func TestNoSecretInMCPLogOrEvents(t *testing.T) {
	const envSecret = "env-secret-value-0123456789"
	const headerSecret = "header-secret-value-abcdef"
	const referenced = "referenced-secret-value-xyz"
	stdio := stdioServer("leaky", map[string]string{"API_TOKEN": envSecret, "FROM_OMO": "${OMO_REFERENCED}"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "refused "+r.Header.Get("Authorization"), http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	remote := httpServer("remote", srv.URL)
	remote.Headers = map[string]string{"Authorization": "Bearer " + headerSecret}
	remote.Timeouts.Startup = api.Duration(2 * time.Second)
	f := newFixture(t, nil, stdio, remote)
	f.env = append(f.env, "OMO_REFERENCED="+referenced)
	id := agent("s", t.TempDir(), api.RoleDeveloper)

	var texts []string
	for _, c := range []struct {
		tool string
		args any
	}{
		{"leaky__stderr", map[string]string{"text": "token is " + envSecret + " and " + referenced}},
		{"leaky__rpcerror", map[string]string{"text": "bad token " + envSecret}},
		{"leaky__echo", map[string]string{"text": "fine"}},
		{"remote__echo", nil},
	} {
		texts = append(texts, resultText(f.call(id, c.tool, c.args)))
	}
	if !strings.Contains(texts[1], "bad token "+redacted) {
		t.Fatalf("the upstream's error is not redacted: %q", texts[1])
	}
	eventually(t, "the stderr line", func() bool { return strings.Contains(f.logText("leaky"), "stderr: token is") })
	raw, _ := json.Marshal(f.pool.Status())
	all := strings.Join(texts, "\n") + f.logText("leaky") + f.logText("remote") + f.events.all() + string(raw)
	for _, secret := range []string{envSecret, headerSecret, referenced} {
		if strings.Contains(all, secret) {
			t.Fatalf("a value reached the log, an event, a state or an error:\n%s", all)
		}
	}
}
