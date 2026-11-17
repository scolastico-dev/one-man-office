package server

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
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// relayCanary is a credential in a relayed server's env; neither the log
// nor an event may hold it.
const relayCanary = "sk-relay-canary-5d1e"

// fakeStdio is a stdio server run by the test binary as the fake MCP
// server.
func fakeStdio(roles []string, global bool) api.MCPServer {
	return api.MCPServer{
		Enabled:   true,
		Transport: api.MCPTransportStdio,
		Command:   os.Args[0],
		Env:       map[string]string{fakemcp.EnvEnable: "1", "FAKE_TOKEN": relayCanary},
		Instances: api.MCPInstancesPerSession,
		Roles:     roles,
		Global:    global,
		Timeouts:  api.MCPTimeouts{Startup: api.Duration(20 * time.Second)},
	}
}

// putServer writes an MCP server through the settings route and waits
// until the change is taken into use.
func (w *wireServer) putServer(name string, m api.MCPServer) {
	w.t.Helper()
	hdr := map[string]string{"If-Match": `"` + api.NoRevision + `"`}
	for k, v := range w.user() {
		hdr[k] = v
	}
	if st, out := w.api(http.MethodPut, "/api/mcp/servers/"+name, m, hdr); st != http.StatusOK && st != http.StatusCreated {
		w.t.Fatalf("put %s: %d %s", name, st, out)
	}
	w.s.waitSettingsEvents(context.Background())
}

// serverState is the relay's state of server name, read through the route.
func (w *wireServer) serverState(name string) api.MCPServerState {
	w.t.Helper()
	st, out := w.api(http.MethodGet, "/api/mcp/servers", nil, w.user())
	if st != http.StatusOK {
		w.t.Fatalf("list: %d %s", st, out)
	}
	var list api.MCPServersResponse
	json.Unmarshal(out, &list)
	for _, s := range list.States {
		if s.Name == name {
			return s
		}
	}
	return api.MCPServerState{}
}

// eventsText is every recorded event, kinds and details.
func (w *wireServer) eventsText() string {
	w.t.Helper()
	evs, err := db.EventsSince(w.s.db, 0, 100000, false)
	if err != nil {
		w.t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&b, "%s %s\n", e.Kind, e.Detail)
	}
	return b.String()
}

// noSecrets fails when the log or an event holds any of the values.
func (w *wireServer) noSecrets(values ...string) {
	w.t.Helper()
	w.noLogged(values...)
	events := w.eventsText()
	for _, v := range values {
		if v != "" && strings.Contains(events, v) {
			w.t.Errorf("an event holds %q", v)
		}
	}
}

// eventually waits until ok holds, for at most 15 s.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args any) string {
	t.Helper()
	res, err := callTool(cs, name, args)
	if err != nil || res.IsError {
		t.Fatalf("%s: %v %+v", name, err, res)
	}
	return toolText(res)
}

// wantRelayedNoticeOnce checks that every relayed tool carries omo's
// notice exactly once.
func wantRelayedNoticeOnce(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if strings.Contains(tool.Name, "__") && strings.Count(tool.Description, "Relayed by omo from server") != 1 {
			t.Errorf("%s: description %q", tool.Name, tool.Description)
		}
	}
}

// Stdio servers defined on the settings route are relayed through the real
// server: by role, one process per agent session, ended with the session,
// only global ones to a connection without a session.
func TestRelayedStdioServersThroughServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	w := newWireServer(t, "token")
	ctx := context.Background()
	before, _ := db.LastEventID(w.s.db)
	w.putServer("local", fakeStdio([]string{string(api.RoleDeveloper)}, false))
	w.putServer("everywhere", fakeStdio(nil, true))
	evs, _ := db.EventsSince(w.s.db, before, 1000, true)
	if !slices.ContainsFunc(evs, func(e db.Event) bool { return e.Kind == api.KindMCPToolsChanged }) {
		t.Fatal("a settings change did not announce mcp.tools_changed")
	}

	dev1, dev2 := liveSession(t, w.s, api.RoleDeveloper), liveSession(t, w.s, api.RoleDeveloper)
	rev := liveSession(t, w.s, api.RoleReviewer)
	cs1, cs2, csRev := viaSocket(t, w.s, dev1.ID), viaSocket(t, w.s, dev2.ID), viaSocket(t, w.s, rev.ID)

	if names := listToolNames(t, cs1); !slices.Contains(names, "local__echo") || !slices.Contains(names, "everywhere__echo") {
		t.Fatalf("developer tools %v", names)
	}
	if names := listToolNames(t, csRev); slices.Contains(names, "local__echo") || !slices.Contains(names, "everywhere__echo") {
		t.Fatalf("reviewer tools %v", names)
	}
	if res, err := callTool(csRev, "local__echo", map[string]string{"text": "hi"}); err == nil && !res.IsError {
		t.Fatal("the reviewer called a server its role may not use")
	}
	wantRelayedNoticeOnce(t, cs1)
	if got := callText(t, cs1, "local__echo", map[string]string{"text": "hi"}); got != "echo: hi" {
		t.Fatalf("echo answered %q", got)
	}
	who1, who2 := callText(t, cs1, "local__whoami", nil), callText(t, cs2, "local__whoami", nil)
	if who1 == who2 {
		t.Fatalf("two sessions share one process: %s", who1)
	}
	if n := w.serverState("local").Instances; n != 2 {
		t.Fatalf("local runs %d instances, want 2", n)
	}

	// A personal token is a connection without a session: only the global
	// server, and no omo tools.
	st, out := w.api(http.MethodPost, "/api/mcp/tokens", api.MCPTokenRequest{Name: "laptop"}, w.user())
	if st != http.StatusCreated {
		t.Fatalf("create token: %d %s", st, out)
	}
	var created api.MCPTokenCreated
	json.Unmarshal(out, &created)
	pat := w.connect("", created.Secret)
	names := listToolNames(t, pat)
	if slices.Contains(names, "local__echo") || !slices.Contains(names, "everywhere__echo") || slices.Contains(names, "ticket_list") {
		t.Fatalf("personal token tools %v", names)
	}
	if got := callText(t, pat, "everywhere__echo", map[string]string{"text": "x"}); got != "echo: x" {
		t.Fatalf("echo answered %q", got)
	}
	if res, err := callTool(pat, "local__echo", map[string]string{"text": "x"}); err == nil && !res.IsError {
		t.Fatal("a connection without a session called a server that is not global")
	}

	// Ending a session stops its process, however it ends.
	if err := w.s.board.EndSession(ctx, dev1.ID, "test"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the ended session's process stop", func() bool { return w.serverState("local").Instances == 1 })
	if got := callText(t, cs2, "local__whoami", nil); got != who2 {
		t.Fatalf("the other session's process changed: %s, was %s", got, who2)
	}

	// Switched off, nothing is relayed; switched on, it is again.
	setMCP(t, w, false)
	eventually(t, "the switched-off relay stop", func() bool { return w.serverState("local").Instances == 0 })
	setMCP(t, w, true)
	if got := callText(t, cs2, "local__echo", map[string]string{"text": "back"}); got != "echo: back" {
		t.Fatalf("echo answered %q", got)
	}
	w.noSecrets(relayCanary, created.Secret)
	// Session ids are in the board's agent events, never in the log.
	w.noLogged(dev1.ID, dev2.ID)
}

func setMCP(t *testing.T, w *wireServer, on bool) {
	t.Helper()
	g := w.s.settings.General()
	g.MCP.Enabled = on
	st, out := w.api(http.MethodPut, "/api/settings", api.General(g), mergeHeaders(w.user(), map[string]string{"If-Match": `"` + w.s.settings.Revision(settings.SectionGeneral, "") + `"`}))
	if st != http.StatusOK {
		t.Fatalf("switch MCP %v: %d %s", on, st, out)
	}
	w.s.waitSettingsEvents(context.Background())
}

func mergeHeaders(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// upstreamAS is an upstream MCP server protected by its own authorization
// server: metadata, registration, an authorize endpoint that approves at
// once, tokens and revocation.
type upstreamAS struct {
	srv *httptest.Server
	mcp http.Handler

	mu      sync.Mutex
	clients map[string]string // client id → redirect
	codes   map[string][2]string
	access  map[string]bool
	issued  []string
	revoked int
}

func newUpstreamAS(t *testing.T) *upstreamAS {
	a := &upstreamAS{mcp: fakemcp.Handler(fakemcp.Config{}), clients: map[string]string{}, codes: map[string][2]string{}, access: map[string]bool{}}
	a.srv = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *upstreamAS) serve(w http.ResponseWriter, r *http.Request) {
	base := a.srv.URL
	reply := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource":
		reply(200, map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}})
	case "/.well-known/oauth-authorization-server":
		reply(200, map[string]any{
			"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token",
			"registration_endpoint": base + "/register", "revocation_endpoint": base + "/revoke",
			"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"},
		})
	case "/register":
		var meta struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		json.NewDecoder(r.Body).Decode(&meta)
		id := fmt.Sprintf("client-%d", len(a.clients)+1)
		a.clients[id] = meta.RedirectURIs[0]
		reply(201, map[string]any{"client_id": id, "redirect_uris": meta.RedirectURIs, "token_endpoint_auth_method": "none"})
	case "/authorize":
		q := r.URL.Query()
		if a.clients[q.Get("client_id")] != q.Get("redirect_uri") || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "bad authorization request", 400)
			return
		}
		code := "code-" + rand.Text()
		a.codes[code] = [2]string{q.Get("redirect_uri"), q.Get("code_challenge")}
		a.issued = append(a.issued, code)
		u, _ := url.Parse(q.Get("redirect_uri"))
		u.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}, "iss": {base}}.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	case "/token":
		r.ParseForm()
		if r.Form.Get("grant_type") == "authorization_code" {
			c, ok := a.codes[r.Form.Get("code")]
			delete(a.codes, r.Form.Get("code"))
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || c[0] != r.Form.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != c[1] {
				reply(400, map[string]string{"error": "invalid_grant"})
				return
			}
		}
		at, rt := "at-"+rand.Text(), "rt-"+rand.Text()
		a.access[at] = true
		a.issued = append(a.issued, at, rt)
		reply(200, map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": 3600, "refresh_token": rt})
	case "/revoke":
		a.revoked++
		w.WriteHeader(200)
	case "/mcp":
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !a.access[tok] {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/.well-known/oauth-protected-resource/mcp"`)
			http.Error(w, "unauthorized", 401)
			return
		}
		a.mu.Unlock()
		a.mcp.ServeHTTP(w, r)
		a.mu.Lock()
	default:
		http.NotFound(w, r)
	}
}

func (a *upstreamAS) secrets() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.issued)
}

// An OAuth upstream through the real server: Connect, the provider's
// redirect to omo's callback at the address the browser used, connected,
// tools; the sign-in is in the database and survives a restart;
// Disconnect needs a new sign-in.
func TestRelayedOAuthUpstreamThroughServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	as := newUpstreamAS(t)
	home := testHome(t)
	w := newWireServerAt(t, home, "token")
	w.putServer("remote", api.MCPServer{Enabled: true, Transport: api.MCPTransportHTTP, URL: as.srv.URL + "/mcp", Auth: api.MCPAuthOAuth, Global: true})
	if st := w.serverState("remote"); st.Auth.State != api.MCPAuthStateNeedsLogin {
		t.Fatalf("before the sign-in: %+v", st)
	}

	st, out := w.api(http.MethodPost, "/api/mcp/servers/remote/connect", map[string]any{}, w.user())
	if st != http.StatusOK {
		t.Fatalf("connect: %d %s", st, out)
	}
	var c api.MCPConnect
	json.Unmarshal(out, &c)
	authorize, _ := url.Parse(c.AuthorizeURL)
	if got := authorize.Query().Get("redirect_uri"); got != "http://"+w.host()+"/oauth/callback" {
		t.Fatalf("redirect_uri %q", got)
	}
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := browser.Get(c.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize: %d %v", resp.StatusCode, err)
	}
	status, page, _ := w.send(http.MethodGet, back.Host, back.Path+"?"+back.RawQuery, "", nil, nil)
	if status != http.StatusOK || !strings.Contains(string(page), "Connected to remote") {
		t.Fatalf("callback: %d %s", status, page)
	}
	if st := w.serverState("remote"); st.Auth.State != api.MCPAuthStateConnected {
		t.Fatalf("after the sign-in: %+v", st)
	}
	dev := viaSocket(t, w.s, liveSession(t, w.s, api.RoleDeveloper).ID)
	if got := callText(t, dev, "remote__echo", map[string]string{"text": "up"}); got != "echo: up" {
		t.Fatalf("echo answered %q", got)
	}
	var rows int
	if err := w.s.db.QueryRow(`SELECT count(*) FROM mcp_upstream_auth WHERE server = 'remote' AND access_token != ''`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("stored sign-ins: %d %v", rows, err)
	}
	w.noSecrets(as.secrets()...)

	// A restart keeps the sign-in.
	w.s.Close()
	w2 := newWireServerAt(t, home, "token")
	if st := w2.serverState("remote"); st.Auth.State != api.MCPAuthStateConnected {
		t.Fatalf("after a restart: %+v", st)
	}
	dev2 := viaSocket(t, w2.s, liveSession(t, w2.s, api.RoleDeveloper).ID)
	if got := callText(t, dev2, "remote__echo", map[string]string{"text": "again"}); got != "echo: again" {
		t.Fatalf("echo after a restart answered %q", got)
	}

	if st, out := w2.api(http.MethodPost, "/api/mcp/servers/remote/disconnect", map[string]any{}, w2.user()); st != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", st, out)
	}
	if st := w2.serverState("remote"); st.Auth.State != api.MCPAuthStateNeedsLogin {
		t.Fatalf("after disconnect: %+v", st)
	}
	if names := listToolNames(t, dev2); slices.Contains(names, "remote__echo") {
		t.Fatalf("tools after disconnect %v", names)
	}
	w2.noSecrets(as.secrets()...)
}

// The callback of an upstream sign-in is at the address the browser
// reached omo by: public_url, the request's own host or loopback, as the
// OAuth issuer of /mcp; without a request, public_url.
func TestMCPCallbackAddress(t *testing.T) {
	s := newTestServer(t, func(c *serverconfig.Config) {
		c.Host = "0.0.0.0"
		c.Port = 8090
		c.PublicURL = "https://omo.example.com/"
	})
	for host, want := range map[string]string{
		"omo.example.com":     "https://omo.example.com/oauth/callback",
		"OMO.Example.com:443": "https://omo.example.com/oauth/callback",
		"omo.localhost:8090":  "http://omo.localhost:8090/oauth/callback",
		"localhost:8090":      "http://localhost:8090/oauth/callback",
		"192.168.1.5:8090":    "http://192.168.1.5:8090/oauth/callback",
	} {
		r := httptest.NewRequest(http.MethodPost, "http://x/api/mcp/servers/a/connect", nil)
		r.Host = host
		if got := s.mcpCallbackAddress(s.withMCPCallbackBase(r)); got != want {
			t.Errorf("Host %s: callback %s, want %s", host, got, want)
		}
		if base := s.mcpBase(r); want != base+"/oauth/callback" {
			t.Errorf("Host %s: the callback %s is not at the issuer %s", host, want, base)
		}
	}
	if got := s.mcpCallbackAddress(context.Background()); got != "https://omo.example.com/oauth/callback" {
		t.Errorf("without a request: %s", got)
	}
}
