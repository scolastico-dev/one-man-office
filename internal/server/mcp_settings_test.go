package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// mcpCanary is a credential stored in an MCP server's env and headers. The
// server answers show it as stored; no refusal, event or log line may hold
// it.
const mcpCanary = "sk-mcp-canary-93be"

// fakeRelay stands in for the relay in the route tests. Its errors, when
// set, quote the canary, as a careless relay error could.
type fakeRelay struct {
	mu        sync.Mutex
	changed   []string
	restarted []string
	tested    []api.MCPServer
	connected []string
	dropped   []string
	callbacks int
	fail      bool
}

func (f *fakeRelay) err() error {
	if f.fail {
		return errors.New("upstream refused header " + mcpCanary)
	}
	return nil
}

func (f *fakeRelay) Status() []api.MCPServerState {
	return []api.MCPServerState{{Name: "docs", State: api.MCPStateRunning, Tools: []string{"docs__search"}}}
}

func (f *fakeRelay) Restart(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarted = append(f.restarted, name)
	return f.err()
}

func (f *fakeRelay) Test(_ context.Context, m api.MCPServer) (api.MCPTestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tested = append(f.tested, m)
	return api.MCPTestResult{Tools: []string{"search"}, Millis: 3}, f.err()
}

func (f *fakeRelay) Connect(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = append(f.connected, name)
	return "https://auth.example.invalid/authorize?state=s1", f.err()
}

func (f *fakeRelay) Callback(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.callbacks++
	f.mu.Unlock()
	io.WriteString(w, "callback "+r.URL.Query().Get("state"))
}

func (f *fakeRelay) Disconnect(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropped = append(f.dropped, name)
	return f.err()
}

func (f *fakeRelay) Changed(names []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changed = append(f.changed, names...)
}

func (f *fakeRelay) LogLines(name string, n int) []api.MCPLogLine {
	return []api.MCPLogLine{{At: time.Unix(1, 0).UTC(), Message: "started " + name}}
}

// fakeTokens stands in for the token store.
type fakeTokens struct {
	mu     sync.Mutex
	tokens []api.MCPToken
}

func (f *fakeTokens) CreatePAT(_ context.Context, name string, expires *time.Time) (api.MCPToken, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := api.MCPToken{ID: "0123456789abcdef", Kind: api.MCPTokenPAT, Name: name, Scope: "mcp", CreatedAt: time.Now().UTC(), ExpiresAt: expires}
	f.tokens = append(f.tokens, tok)
	return tok, "omo_pat_secret", nil
}

func (f *fakeTokens) List(context.Context) ([]api.MCPToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.tokens), nil
}

func (f *fakeTokens) Revoke(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.tokens, func(t api.MCPToken) bool { return t.ID == id })
	if i < 0 {
		return api.Errorf(api.CodeNotFound, "no token %s", id)
	}
	f.tokens = slices.Delete(f.tokens, i, i+1)
	return nil
}

func docsServer() api.MCPServer {
	return api.MCPServer{
		Enabled: true, Transport: api.MCPTransportHTTP, URL: "https://mcp.example.invalid/mcp",
		Headers: map[string]string{"Authorization": "Bearer " + mcpCanary},
		Env:     map[string]string{"TOKEN": mcpCanary},
	}
}

func TestMCPServerRoutes(t *testing.T) {
	s := newTestServer(t)
	relay := &fakeRelay{}
	s.mcpConf.relay = relay
	b := newBrowser(t, s)
	start := lastEventID(t, s)

	// Create only with If-Match "0"; the answer is the stored server, with
	// its values as stored and global off by default.
	resp := withIfMatch(b, s, "PUT", "/api/mcp/servers/docs", docsServer(), `"`+api.NoRevision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got := body[api.MCPServer](t, resp)
	if got.Name != "docs" || got.Revision == "" || got.Global || got.Auth != api.MCPAuthHeaders || got.Headers["Authorization"] != "Bearer "+mcpCanary {
		t.Fatalf("server = %+v", got)
	}
	if !slices.Equal(relay.changed, []string{"docs"}) {
		t.Fatalf("relay changes = %v", relay.changed)
	}
	wantEvents(t, s, api.KindMCPToolsChanged, start, 1)
	wantStatus(t, withIfMatch(b, s, "PUT", "/api/mcp/servers/docs", docsServer(), `"`+api.NoRevision+`"`).StatusCode, http.StatusConflict)

	// List and get.
	list := body[api.MCPServersResponse](t, asUser(b, s, "GET", "/api/mcp/servers", nil))
	if len(list.Servers) != 1 || list.Servers[0].Revision != got.Revision || len(list.States) != 1 || list.Errors == nil {
		t.Fatalf("list = %+v", list)
	}
	one := body[api.MCPServer](t, asUser(b, s, "GET", "/api/mcp/servers/docs", nil))
	if one.Env["TOKEN"] != mcpCanary {
		t.Fatalf("get = %+v", one)
	}
	wantStatus(t, asUser(b, s, "GET", "/api/mcp/servers/nope", nil).StatusCode, http.StatusNotFound)

	// Replace with the revision; a stale one and a wrong name are refused.
	m := docsServer()
	m.Global = true
	resp = withIfMatch(b, s, "PUT", "/api/mcp/servers/docs", m, `"`+got.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got2 := body[api.MCPServer](t, resp); !got2.Global {
		t.Fatalf("global not stored: %+v", got2)
	}
	wantStatus(t, withIfMatch(b, s, "PUT", "/api/mcp/servers/docs", m, `"`+got.Revision+`"`).StatusCode, http.StatusConflict)
	m.Name = "other"
	wantStatus(t, asUser(b, s, "PUT", "/api/mcp/servers/docs", m).StatusCode, http.StatusUnprocessableEntity)
	bad := docsServer()
	bad.Transport = "ws"
	resp = asUser(b, s, "PUT", "/api/mcp/servers/docs", bad)
	if text := respText(t, resp); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(text, `"field":"transport"`) {
		t.Fatalf("%d %s", resp.StatusCode, text)
	}
	long := docsServer()
	resp = asUser(b, s, "PUT", "/api/mcp/servers/"+strings.Repeat("a", 25), long)
	if text := respText(t, resp); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(text, `"field":"name"`) {
		t.Fatalf("%d %s", resp.StatusCode, text)
	}

	// Delete.
	rev := s.settings.Revision("mcpservers", "docs")
	wantStatus(t, withIfMatch(b, s, "DELETE", "/api/mcp/servers/docs", nil, `"`+got.Revision+`"`).StatusCode, http.StatusConflict)
	wantStatus(t, withIfMatch(b, s, "DELETE", "/api/mcp/servers/docs", nil, `"`+rev+`"`).StatusCode, http.StatusNoContent)
	wantStatus(t, asUser(b, s, "DELETE", "/api/mcp/servers/docs", nil).StatusCode, http.StatusNotFound)
	if !slices.Equal(relay.changed, []string{"docs", "docs", "docs"}) {
		t.Fatalf("relay changes = %v", relay.changed)
	}
	wantEvents(t, s, api.KindMCPToolsChanged, start, 3)

	// A hand edit reaches the relay too.
	writeFile(t, filepath.Join(s.opts.Layout.Root, "mcpservers", "hand.yaml"), "transport: stdio\ncommand: srv\n")
	s.settings.Reload()
	s.waitSettingsEvents(context.Background())
	if !slices.Contains(relay.changed, "hand") {
		t.Fatalf("relay changes = %v", relay.changed)
	}
}

func TestMCPServerActions(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	m := docsServer()
	m.Name = "docs"
	if err := s.settings.PutMCPServerIf(api.NoRevision, m); err != nil {
		t.Fatal(err)
	}
	o := docsServer()
	o.Name, o.Auth, o.Headers = "github", api.MCPAuthOAuth, nil
	if err := s.settings.PutMCPServerIf(api.NoRevision, o); err != nil {
		t.Fatal(err)
	}
	empty := map[string]any{}
	draft := api.MCPServer{Transport: api.MCPTransportStdio, Command: "srv"}

	// Without the relay: the settings work, the actions answer 503.
	if list := body[api.MCPServersResponse](t, asUser(b, s, "GET", "/api/mcp/servers", nil)); len(list.Servers) != 2 || list.States == nil || len(list.States) != 0 {
		t.Fatalf("list = %+v", list)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/mcp/servers/test", draft},
		{"POST", "/api/mcp/servers/docs/restart", empty},
		{"GET", "/api/mcp/servers/docs/log", nil},
		{"POST", "/api/mcp/servers/github/connect", empty},
		{"POST", "/api/mcp/servers/github/disconnect", empty},
		{"GET", "/api/mcp/tokens", nil},
		{"POST", "/api/mcp/tokens", api.MCPTokenRequest{Name: "laptop"}},
		{"DELETE", "/api/mcp/tokens/0123456789abcdef", nil},
		{"GET", "/oauth/callback?state=x", nil},
	} {
		if got := asUser(b, s, c.method, c.path, c.body).StatusCode; got != http.StatusServiceUnavailable {
			t.Errorf("%s %s without relay: %d, want 503", c.method, c.path, got)
		}
	}

	relay := &fakeRelay{}
	tokens := &fakeTokens{}
	s.mcpConf.relay, s.mcpConf.tokens = relay, tokens

	// Test: a draft is validated, then handed to the relay; nothing is
	// saved.
	res := body[api.MCPTestResult](t, asUser(b, s, "POST", "/api/mcp/servers/test", draft))
	if !slices.Equal(res.Tools, []string{"search"}) || len(relay.tested) != 1 || relay.tested[0].Instances != api.MCPInstancesPerSession {
		t.Fatalf("test = %+v, tested %+v", res, relay.tested)
	}
	resp := asUser(b, s, "POST", "/api/mcp/servers/test", api.MCPServer{Transport: api.MCPTransportStdio})
	if text := respText(t, resp); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(text, `"field":"command"`) {
		t.Fatalf("%d %s", resp.StatusCode, text)
	}
	if len(relay.tested) != 1 || len(s.settings.MCPServers()) != 2 {
		t.Fatal("an invalid draft reached the relay, or a draft was saved")
	}

	// Restart, log, connect, disconnect.
	wantStatus(t, asUser(b, s, "POST", "/api/mcp/servers/docs/restart", empty).StatusCode, http.StatusNoContent)
	if lines := body[[]api.MCPLogLine](t, asUser(b, s, "GET", "/api/mcp/servers/docs/log", nil)); len(lines) != 1 || lines[0].Message != "started docs" {
		t.Fatalf("log = %+v", lines)
	}
	if c := body[api.MCPConnect](t, asUser(b, s, "POST", "/api/mcp/servers/github/connect", empty)); !strings.HasPrefix(c.AuthorizeURL, "https://auth.example.invalid/") {
		t.Fatalf("connect = %+v", c)
	}
	wantStatus(t, asUser(b, s, "POST", "/api/mcp/servers/docs/connect", empty).StatusCode, http.StatusConflict)
	wantStatus(t, asUser(b, s, "POST", "/api/mcp/servers/github/disconnect", empty).StatusCode, http.StatusNoContent)
	if !slices.Equal(relay.restarted, []string{"docs"}) || !slices.Equal(relay.connected, []string{"github"}) || !slices.Equal(relay.dropped, []string{"github"}) {
		t.Fatalf("relay = %+v", relay)
	}
	for _, p := range []string{"/api/mcp/servers/nope/restart", "/api/mcp/servers/nope/connect", "/api/mcp/servers/nope/disconnect"} {
		wantStatus(t, asUser(b, s, "POST", p, empty).StatusCode, http.StatusNotFound)
	}
	wantStatus(t, asUser(b, s, "GET", "/api/mcp/servers/nope/log", nil).StatusCode, http.StatusNotFound)

	// The OAuth callback is the provider's navigation: no login token, no
	// JSON; the relay answers it.
	resp = b.do("GET", "/oauth/callback?code=c&state=s1", nil, nil)
	if text := respText(t, resp); resp.StatusCode != http.StatusOK || text != "callback s1" {
		t.Fatalf("callback %d %q", resp.StatusCode, text)
	}

	// Personal tokens.
	if list := body[[]api.MCPToken](t, asUser(b, s, "GET", "/api/mcp/tokens", nil)); list == nil || len(list) != 0 {
		t.Fatalf("tokens = %#v", list)
	}
	resp = asUser(b, s, "POST", "/api/mcp/tokens", api.MCPTokenRequest{Name: "laptop"})
	wantStatus(t, resp.StatusCode, http.StatusCreated)
	if created := body[api.MCPTokenCreated](t, resp); created.Secret == "" || created.Token.Name != "laptop" {
		t.Fatalf("created = %+v", created)
	}
	past := time.Now().Add(-time.Hour)
	for _, req := range []api.MCPTokenRequest{{Name: ""}, {Name: "  "}, {Name: strings.Repeat("n", 101)}, {Name: "a‮b"}, {Name: "x", ExpiresAt: &past}} {
		wantStatus(t, asUser(b, s, "POST", "/api/mcp/tokens", req).StatusCode, http.StatusUnprocessableEntity)
	}
	if list := body[[]api.MCPToken](t, asUser(b, s, "GET", "/api/mcp/tokens", nil)); len(list) != 1 {
		t.Fatalf("tokens = %+v", list)
	}
	wantStatus(t, asUser(b, s, "DELETE", "/api/mcp/tokens/0123456789abcdef", nil).StatusCode, http.StatusNoContent)
	wantStatus(t, asUser(b, s, "DELETE", "/api/mcp/tokens/0123456789abcdef", nil).StatusCode, http.StatusNotFound)
	wantStatus(t, asUser(b, s, "DELETE", "/api/mcp/tokens/NOT-AN-ID", nil).StatusCode, http.StatusNotFound)
}

// TestNoSecretInMCPSettingsLogOrEvents stores credentials in an MCP server,
// is refused for others, loads a broken hand edit holding one and lets the
// relay fail with an error that quotes one: no event, refusal, file error
// or log line holds a value.
func TestNoSecretInMCPSettingsLogOrEvents(t *testing.T) {
	var logs syncBuffer
	s, err := newServer(Options{Layout: testHome(t), Config: testConfig(), Version: "test"}, &logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	relay := &fakeRelay{fail: true}
	s.mcpConf.relay = relay
	b := newBrowser(t, s)
	start := lastEventID(t, s)
	noValue := func(where, text string) {
		t.Helper()
		if strings.Contains(text, mcpCanary) {
			t.Errorf("%s holds the value: %s", where, text)
		}
	}

	wantStatus(t, asUser(b, s, "PUT", "/api/mcp/servers/docs", docsServer()).StatusCode, http.StatusOK)
	o := docsServer()
	o.Auth, o.Headers = api.MCPAuthOAuth, nil
	o.OAuth = api.MCPOAuth{ClientID: "id", ClientSecret: mcpCanary}
	wantStatus(t, asUser(b, s, "PUT", "/api/mcp/servers/github", o).StatusCode, http.StatusOK)
	// Refused writes that carry values.
	for _, raw := range []string{
		`{"transport":"http","url":"https://h.invalid/","headers":{"Authorization":"` + mcpCanary + `\r\nX: y"}}`,
		`{"transport":"http","url":"https://u:` + mcpCanary + `@h.invalid/"}`,
		`{"transport":"stdio","command":"x","timeouts":{"call":"` + mcpCanary + `"}}`,
		`{"transport":"stdio","command":"x","env":{"` + mcpCanary + `=":"v"}}`,
	} {
		resp := rawDo(t, b, s, "PUT", "/api/mcp/servers/docs", raw)
		if resp.StatusCode < 400 {
			t.Fatalf("%s accepted", raw)
		}
		noValue("refusal of "+raw, respText(t, resp))
	}
	// Failing relay calls: the relay's error text is neither answered nor
	// logged.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/mcp/servers/docs/restart"},
		{"POST", "/api/mcp/servers/github/connect"},
		{"POST", "/api/mcp/servers/github/disconnect"},
	} {
		resp := asUser(b, s, c.method, c.path, map[string]any{})
		if resp.StatusCode < 400 {
			t.Fatalf("%s %s: %d", c.method, c.path, resp.StatusCode)
		}
		noValue(c.path, respText(t, resp))
	}
	resp := asUser(b, s, "POST", "/api/mcp/servers/test", docsServer())
	if resp.StatusCode < 400 {
		t.Fatalf("failing test: %d", resp.StatusCode)
	}
	noValue("test", respText(t, resp))
	// A broken hand edit holding a value.
	writeFile(t, filepath.Join(s.opts.Layout.Root, "mcpservers", "hand.yaml"), "transport: http\nurl: https://u:"+mcpCanary+"@h.invalid/\nheaders: [ "+mcpCanary+"\n")
	s.settings.Reload()
	s.waitSettingsEvents(context.Background())
	list := body[api.MCPServersResponse](t, asUser(b, s, "GET", "/api/mcp/servers", nil))
	if len(list.Errors) != 1 {
		t.Fatalf("errors = %+v", list.Errors)
	}
	raw, _ := json.Marshal(list.Errors)
	noValue("file errors", string(raw))
	raw, _ = json.Marshal(list.States)
	noValue("states", string(raw))

	for _, kind := range []string{api.KindSettingsChanged, api.KindSettingsInvalid, api.KindMCPChanged, api.KindMCPToolsChanged} {
		for _, d := range settingsEvents(t, s, kind, start) {
			noValue(kind+" event", string(d))
		}
	}
	if len(settingsEvents(t, s, api.KindMCPToolsChanged, start)) == 0 {
		t.Fatal("no mcp.tools_changed event")
	}
	noValue("the server log", logs.String())
	if !strings.Contains(logs.String(), "restart") {
		t.Fatalf("the failing calls were not logged at all:\n%s", logs.String())
	}
}
