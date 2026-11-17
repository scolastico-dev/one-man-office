package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/mcpauth"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// wireBasicPassword is the basic-auth password of the tests below; the
// server log must never hold it.
const wireBasicPassword = "basic-pw-7f3c91"

// wireAuthModes are omo's three auth modes.
var wireAuthModes = []string{"token", "basic", "none"}

// wireServer is a real server on a temporary home, served over TCP on
// loopback, with its log kept.
type wireServer struct {
	t    *testing.T
	s    *Server
	url  string
	port string
	log  *bytes.Buffer
	mode string
}

func newWireServer(t *testing.T, mode string, mutate ...func(*serverconfig.Config)) *wireServer {
	t.Helper()
	return newWireServerAt(t, testHome(t), mode, mutate...)
}

// newWireServerAt is newWireServer on the home l, to start a server again.
func newWireServerAt(t *testing.T, l globalhome.Layout, mode string, mutate ...func(*serverconfig.Config)) *wireServer {
	t.Helper()
	var logBuf bytes.Buffer
	cfg := testConfig(append([]func(*serverconfig.Config){func(c *serverconfig.Config) {
		c.Auth = mode
		if mode == "basic" {
			c.BasicAuth = "owner:" + wireBasicPassword
		}
	}}, mutate...)...)
	s, err := newServer(Options{Layout: l, Config: cfg, Version: "test"}, &syncWriter{w: &logBuf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	srv := httptest.NewServer(s.BrowserHandler())
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return &wireServer{t: t, s: s, url: srv.URL, port: port, log: &logBuf, mode: mode}
}

// host is the default Host header: omo.localhost on the bound port.
func (w *wireServer) host() string { return hostOmoLocalhost + ":" + w.port }

// user are the headers that authenticate the user in w's auth mode.
func (w *wireServer) user() map[string]string {
	switch w.mode {
	case "token":
		return map[string]string{"Authorization": "Bearer " + w.s.token}
	case "basic":
		return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("owner:"+wireBasicPassword))}
	}
	return map[string]string{}
}

// send sends a request to Host host (the default when "") with a body of
// the given content type and the headers hdr.
func (w *wireServer) send(method, host, path, contentType string, body []byte, hdr map[string]string) (int, []byte, http.Header) {
	w.t.Helper()
	req, err := http.NewRequest(method, w.url+path, bytes.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	req.Host = w.host()
	if host != "" {
		req.Host = host
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, resp.Header
}

// api sends a JSON request to an API route with the given headers.
func (w *wireServer) api(method, path string, body any, hdr map[string]string) (int, []byte) {
	w.t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	status, out, _ := w.send(method, "", path, "application/json", data, hdr)
	return status, out
}

// form posts an OAuth form at Host host, without any omo credential.
func (w *wireServer) form(host, path string, f url.Values) (int, map[string]any) {
	w.t.Helper()
	status, data, _ := w.send(http.MethodPost, host, path, "application/x-www-form-urlencoded", []byte(f.Encode()), nil)
	var out map[string]any
	json.Unmarshal(data, &out)
	return status, out
}

// getJSON reads a JSON document at Host host without any omo credential.
func (w *wireServer) getJSON(host, path string) map[string]any {
	w.t.Helper()
	status, data, _ := w.send(http.MethodGet, host, path, "", nil, nil)
	if status != http.StatusOK {
		w.t.Fatalf("GET %s at %s: %d %s", path, host, status, data)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		w.t.Fatal(err)
	}
	return out
}

// mcpPost sends one tools/list request to /mcp at Host host with the
// bearer token ("" sends none) and answers its status and headers.
func (w *wireServer) mcpPost(host, token string) (int, http.Header) {
	w.t.Helper()
	hdr := map[string]string{"Accept": "application/json, text/event-stream"}
	if token != "" {
		hdr["Authorization"] = "Bearer " + token
	}
	status, _, h := w.send(http.MethodPost, host, mcpPath, "application/json",
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), hdr)
	return status, h
}

// connect connects an MCP client to /mcp at Host host with the bearer
// token ("" sends none).
func (w *wireServer) connect(host, token string) *mcp.ClientSession {
	w.t.Helper()
	hdr := map[string]string{}
	if token != "" {
		hdr["Authorization"] = "Bearer " + token
	}
	if host == "" {
		host = w.host()
	}
	cs, err := mcpConnect(w.t, w.url+mcpPath, headerTransport{host: host, header: hdr})
	if err != nil {
		w.t.Fatalf("connect at %s: %v", host, err)
	}
	return cs
}

// noLogged fails when the server log holds any of the values.
func (w *wireServer) noLogged(values ...string) {
	w.t.Helper()
	logText := w.log.String()
	for _, v := range append(values, w.s.token, wireBasicPassword) {
		if v != "" && strings.Contains(logText, v) {
			w.t.Errorf("the server log holds %q:\n%s", v, logText)
		}
	}
}

// wantNoSession checks that cs is a connection without an agent session:
// no omo tools, and an omo tool's call is refused with the way to the
// terminal.
func wantNoSession(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	if got := listToolNames(t, cs); len(got) != 0 {
		t.Errorf("tools %v", got)
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "not tied to an omo agent session") {
		t.Errorf("instructions %q", cs.InitializeResult().Instructions)
	}
	res, err := callTool(cs, "ticket_list", map[string]any{})
	if err != nil || !res.IsError || !strings.Contains(toolText(res), "use `omo ai ticket list` in the terminal") {
		t.Errorf("ticket_list: %v %v", err, res)
	}
}

// wantRefused checks a 401 of /mcp at Host host whose WWW-Authenticate
// names the resource metadata at base.
func (w *wireServer) wantRefused(host, token, base string) {
	w.t.Helper()
	status, h := w.mcpPost(host, token)
	if status != http.StatusUnauthorized {
		w.t.Fatalf("/mcp at %s: status %d, want 401", host, status)
	}
	want := `resource_metadata="` + base + `/.well-known/oauth-protected-resource/mcp"`
	if got := h.Get("WWW-Authenticate"); !strings.Contains(got, want) {
		w.t.Fatalf("/mcp at %s: WWW-Authenticate %q, want %s", host, got, want)
	}
}

// oauthGrant runs the authorization-code flow with PKCE at Host host for
// resource, consenting as the user through the consent API. It answers the
// token response and the secrets the flow handled.
func (w *wireServer) oauthGrant(host, resource string) (clientID string, tok map[string]any, secrets []string) {
	w.t.Helper()
	base := "http://" + host
	data, _ := json.Marshal(map[string]any{"client_name": "Test Client", "redirect_uris": []string{"http://localhost:5555/callback"}})
	status, out, _ := w.send(http.MethodPost, host, mcpauth.PathRegister, "application/json", data, nil)
	if status != http.StatusCreated {
		w.t.Fatalf("register: %d %s", status, out)
	}
	var reg map[string]any
	json.Unmarshal(out, &reg)
	clientID = reg["client_id"].(string)

	verifier := oauth2.GenerateVerifier()
	cfg := &oauth2.Config{ClientID: clientID, RedirectURL: "http://localhost:5555/callback", Scopes: []string{mcpauth.Scope},
		Endpoint: oauth2.Endpoint{AuthURL: base + mcpauth.PathAuthorize, TokenURL: base + mcpauth.PathToken}}
	authURL, _ := url.Parse(cfg.AuthCodeURL("st", oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("resource", resource)))

	consent := func(method string, body any, hdr map[string]string) (int, []byte) {
		var b []byte
		path := "/api/oauth/consent"
		if body != nil {
			b, _ = json.Marshal(body)
		} else {
			path += "?" + authURL.RawQuery
		}
		st, o, _ := w.send(method, host, path, "application/json", b, hdr)
		return st, o
	}
	if w.mode != "none" {
		// The consent API keeps omo's own protection.
		if st, _ := consent(http.MethodGet, nil, nil); st != http.StatusUnauthorized {
			w.t.Fatalf("consent without credentials: %d", st)
		}
		if st, _ := consent(http.MethodPost, api.OAuthDecision{Query: authURL.RawQuery, Approve: true}, nil); st != http.StatusUnauthorized {
			w.t.Fatalf("decision without credentials: %d", st)
		}
	}
	if st, o := consent(http.MethodGet, nil, w.user()); st != http.StatusOK {
		w.t.Fatalf("consent: %d %s", st, o)
	}
	st, o := consent(http.MethodPost, api.OAuthDecision{Query: authURL.RawQuery, Approve: true}, w.user())
	if st != http.StatusOK {
		w.t.Fatalf("decision: %d %s", st, o)
	}
	var redirect api.OAuthRedirect
	json.Unmarshal(o, &redirect)
	back, _ := url.Parse(redirect.Redirect)
	code := back.Query().Get("code")
	if code == "" {
		w.t.Fatalf("redirect %s", redirect.Redirect)
	}
	status, tok = w.form(host, mcpauth.PathToken, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {cfg.RedirectURL}, "code_verifier": {verifier}, "resource": {resource}})
	if status != http.StatusOK {
		w.t.Fatalf("token: %d %v", status, tok)
	}
	return clientID, tok, []string{code, verifier, tok["access_token"].(string), tok["refresh_token"].(string)}
}

// A personal token created on the settings route reaches /mcp as a
// connection without a session until it is revoked, in every auth mode;
// the basic credentials and the login token are not needed on /mcp.
func TestMCPPersonalTokenThroughServer(t *testing.T) {
	for _, mode := range wireAuthModes {
		t.Run(mode, func(t *testing.T) {
			w := newWireServer(t, mode)
			base := "http://" + w.host()
			if mode != "none" {
				if st, _ := w.api(http.MethodPost, "/api/mcp/tokens", api.MCPTokenRequest{Name: "laptop"}, nil); st != http.StatusUnauthorized {
					t.Fatalf("token route without credentials: %d", st)
				}
				// Without a bearer, /mcp is refused.
				w.wantRefused("", "", base)
			} else {
				// With auth none, a request without credentials is a
				// connection without a session, nothing more.
				wantNoSession(t, w.connect("", ""))
			}
			st, out := w.api(http.MethodPost, "/api/mcp/tokens", api.MCPTokenRequest{Name: "laptop"}, w.user())
			if st != http.StatusCreated {
				t.Fatalf("create: %d %s", st, out)
			}
			var created api.MCPTokenCreated
			json.Unmarshal(out, &created)
			if !strings.HasPrefix(created.Secret, mcpauth.PATPrefix) || created.Token.Kind != api.MCPTokenPAT {
				t.Fatalf("created %+v", created)
			}
			st, out = w.api(http.MethodGet, "/api/mcp/tokens", nil, w.user())
			var list []api.MCPToken
			json.Unmarshal(out, &list)
			if st != http.StatusOK || len(list) != 1 || list[0].ID != created.Token.ID {
				t.Fatalf("list: %d %s", st, out)
			}

			cs := w.connect("", created.Secret)
			wantNoSession(t, cs)

			if st, out := w.api(http.MethodDelete, "/api/mcp/tokens/"+created.Token.ID, nil, w.user()); st != http.StatusNoContent {
				t.Fatalf("revoke: %d %s", st, out)
			}
			w.wantRefused("", created.Secret, base)
			if _, err := callTool(cs, "whoami", nil); err == nil {
				t.Fatal("the open connection still works after the revocation")
			}
			w.noLogged(created.Secret)
		})
	}
}

// The full OAuth authorization-code flow with PKCE through the real
// server, in every auth mode: the OAuth endpoints need no omo credential,
// the consent API does; the access token reaches /mcp as a connection
// without a session; refresh and revocation take effect on /mcp.
func TestMCPOAuthGrantThroughServer(t *testing.T) {
	for _, mode := range wireAuthModes {
		t.Run(mode, func(t *testing.T) {
			w := newWireServer(t, mode)
			base := "http://" + w.host()
			resource := base + mcpPath
			if m := w.getJSON("", "/.well-known/oauth-protected-resource/mcp"); m["resource"] != resource {
				t.Fatalf("resource metadata %v", m)
			}
			if m := w.getJSON("", "/.well-known/oauth-authorization-server"); m["issuer"] != base {
				t.Fatalf("server metadata %v", m)
			}
			clientID, tok, secrets := w.oauthGrant(w.host(), resource)
			access := tok["access_token"].(string)
			wantNoSession(t, w.connect("", access))

			status, out := w.form("", mcpauth.PathToken, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {clientID}})
			if status != http.StatusOK {
				t.Fatalf("refresh: %d %v", status, out)
			}
			access2, refresh2 := out["access_token"].(string), out["refresh_token"].(string)
			secrets = append(secrets, access2, refresh2)
			wantNoSession(t, w.connect("", access2))

			if status, _ := w.form("", mcpauth.PathRevoke, url.Values{"token": {refresh2}}); status != http.StatusOK {
				t.Fatalf("revoke: %d", status)
			}
			w.wantRefused("", access2, base)
			// A failing exchange leaks nothing either.
			w.form("", mcpauth.PathToken, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh2}, "client_id": {clientID}})
			w.noLogged(secrets...)
		})
	}
}

// The OAuth issuer, the resource metadata, the 401's pointer and the
// resource a token is checked for agree for every address omo is reached
// by: public_url (also with its default port and in other case), the
// request's own host and the loopback names. A token works only at the
// address it was issued for.
func TestMCPResourceAgreesWithIssuer(t *testing.T) {
	w := newWireServer(t, "token", func(c *serverconfig.Config) { c.PublicURL = "https://omo.example.com/" })
	type address struct{ host, base string }
	addrs := []address{
		{"omo.example.com", "https://omo.example.com"},
		{"OMO.Example.com:443", "https://omo.example.com"},
		{w.host(), "http://" + w.host()},
		{"OMO.LOCALHOST:" + w.port, "http://" + w.host()},
		{"localhost:" + w.port, "http://localhost:" + w.port},
		{"127.0.0.1:" + w.port, "http://127.0.0.1:" + w.port},
		{"[::1]:" + w.port, "http://[::1]:" + w.port},
	}
	tokens := map[string]string{}
	for _, a := range addrs {
		rm := w.getJSON(a.host, "/.well-known/oauth-protected-resource/mcp")
		sm := w.getJSON(a.host, "/.well-known/oauth-authorization-server")
		if rm["resource"] != a.base+mcpPath || rm["authorization_servers"].([]any)[0] != a.base || sm["issuer"] != a.base {
			t.Errorf("Host %s: resource %v, issuer %v, want base %s", a.host, rm, sm["issuer"], a.base)
		}
		w.wantRefused(a.host, "", a.base)
		if _, ok := tokens[a.base]; !ok {
			_, tok, _ := w.oauthGrant(a.host, a.base+mcpPath)
			tokens[a.base] = tok["access_token"].(string)
		}
	}
	for _, a := range addrs {
		for base, token := range tokens {
			status, _ := w.mcpPost(a.host, token)
			if want := map[bool]int{true: http.StatusOK, false: http.StatusUnauthorized}[base == a.base]; status != want {
				t.Errorf("Host %s with the token of %s: %d, want %d", a.host, base, status, want)
			}
		}
	}
	w.noLogged(slices.Collect(func(yield func(string) bool) {
		for _, v := range tokens {
			if !yield(v) {
				return
			}
		}
	})...)
}

// wantOwnAuth is the complete list of the routes that authenticate their
// callers themselves and are therefore served without omo's basic
// credentials or login token.
var wantOwnAuth = []string{
	"GET /.well-known/oauth-protected-resource",
	"GET /.well-known/oauth-protected-resource/mcp",
	"GET /.well-known/oauth-authorization-server",
	"POST /oauth/token",
	"POST /oauth/register",
	"POST /oauth/revoke",
}

// The exemption from omo's own authentication covers exactly the OAuth
// endpoints and /mcp: each is reachable without credentials in basic mode,
// and nothing else becomes reachable by it, a wrong method on an exempt
// path included.
func TestOwnAuthExemptsOnlyOAuthEndpoints(t *testing.T) {
	if !slices.Equal(oauthPublicRoutes, wantOwnAuth) {
		t.Fatalf("exempt routes %v, want %v", oauthPublicRoutes, wantOwnAuth)
	}
	if !ownAuth(mcpPath) {
		t.Fatal("/mcp is not exempt")
	}
	w := newWireServer(t, "basic")
	for _, pattern := range wantOwnAuth {
		method, path, _ := strings.Cut(pattern, " ")
		if !ownAuth(path) {
			t.Errorf("%s is not exempt", path)
		}
		if _, got := w.s.mcpAuth.public.Handler(httptest.NewRequest(method, path, nil)); got != pattern {
			t.Errorf("%s is served by the pattern %q of the OAuth mux", pattern, got)
		}
		if _, got := w.s.mux.Handler(httptest.NewRequest(method, path, nil)); got == pattern {
			t.Errorf("%s is also registered on the main mux", pattern)
		}
		ct, body := "", []byte(nil)
		if method == http.MethodPost {
			ct, body = "application/x-www-form-urlencoded", []byte("x=1")
		}
		status, out, _ := w.send(method, "", path, ct, body, nil)
		if status == http.StatusUnauthorized || status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status >= 500 {
			t.Errorf("%s without credentials: %d %s", pattern, status, out)
		}
	}
	negative := []string{
		"GET /oauth/authorize",
		"POST /oauth/authorize",
		"GET /oauth/callback",
		"GET /",
		"GET /settings",
		"GET /oauth/token/",
		"GET /oauth/tokens",
		"GET /OAUTH/TOKEN",
		"GET /.well-known/oauth-authorization-server/x",
		"GET /.well-known/openid-configuration",
		"GET /.well-known/oauth-protected-resource/api",
		"GET /api/oauth/consent",
		"POST /api/oauth/consent",
		"GET /api/mcp/tokens",
		"POST /api/mcp/tokens",
		"GET /api/server",
	}
	for _, pattern := range negative {
		method, path, _ := strings.Cut(pattern, " ")
		if ownAuth(path) {
			t.Errorf("%s is exempt", path)
		}
		status, _, _ := w.send(method, "", path, "application/json", nil, nil)
		if status != http.StatusUnauthorized {
			t.Errorf("%s without credentials in basic mode: %d, want 401", pattern, status)
		}
	}
	// A wrong method on an exempt path reaches nothing else (no web app).
	for _, pattern := range []string{"GET /oauth/token", "GET /oauth/register", "GET /oauth/revoke", "POST /.well-known/oauth-authorization-server"} {
		method, path, _ := strings.Cut(pattern, " ")
		status, out, _ := w.send(method, "", path, "", nil, nil)
		if status != http.StatusMethodNotAllowed || bytes.Contains(out, []byte("<html")) {
			t.Errorf("%s: %d %.80s, want 405", pattern, status, out)
		}
	}
	tw := newWireServer(t, "token")
	for _, pattern := range negative {
		if method, path, _ := strings.Cut(pattern, " "); isAPI(path) {
			if status, _, _ := tw.send(method, "", path, "application/json", nil, nil); status != http.StatusUnauthorized {
				t.Errorf("%s without the login token: %d, want 401", pattern, status)
			}
		}
	}
}

// /mcp answers 404 while MCP is switched off in the general settings,
// whatever the credential, and serves again once it is switched on.
func TestMCPFollowsGeneralSwitch(t *testing.T) {
	w := newWireServer(t, "token")
	_, secret, err := w.s.mcpAuth.tokens.CreatePAT(context.Background(), "laptop", nil)
	if err != nil {
		t.Fatal(err)
	}
	dev := liveSession(t, w.s, api.RoleDeveloper)
	set := func(on bool) {
		if _, err := w.s.settings.UpdateGeneral("", func(g *settings.General) error {
			g.MCP.Enabled = on
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if status, _ := w.mcpPost("", secret); status != http.StatusOK {
		t.Fatalf("switched on: %d", status)
	}
	set(false)
	for name, token := range map[string]string{"personal token": secret, "no bearer": "", "unknown token": "omo_pat_nothing", "agent session": dev.ID} {
		if status, h := w.mcpPost("", token); status != http.StatusNotFound || h.Get("WWW-Authenticate") != "" {
			t.Errorf("switched off, %s: %d %q", name, status, h.Get("WWW-Authenticate"))
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://omo/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(SessionHeader, dev.ID)
	w.s.SocketHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "switched off") {
		t.Errorf("switched off, socket: %d %s", rec.Code, rec.Body)
	}
	set(true)
	if status, _ := w.mcpPost("", secret); status != http.StatusOK {
		t.Fatalf("switched on again: %d", status)
	}
}
