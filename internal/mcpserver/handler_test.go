package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// audiences are every kind of agent session.
var audiences = []aicmd.Audience{
	{Role: api.RoleCEO, Purpose: api.PurposeWork},
	{Role: api.RolePM, Purpose: api.PurposeWork},
	{Role: api.RoleDeveloper, Purpose: api.PurposeWork},
	{Role: api.RoleReviewer, Purpose: api.PurposeWork},
	{Role: api.RoleDeveloper, Purpose: api.PurposeExport},
}

func agent(a aicmd.Audience) Identity {
	return Identity{Class: ClassAgent, Actor: board.Actor{Role: a.Role, Purpose: a.Purpose, Session: "s1"},
		Session: "s1", Instance: "s1", Local: true, Via: "socket"}
}

var noSession = Identity{Class: ClassUser, Instance: "user", Via: "pat"}

// fakeRelay offers one tool per server; a server lists itself for the
// identities its filter lets through.
type fakeRelay struct {
	mu      sync.Mutex
	servers map[string]func(Identity) bool
	calls   []string
}

func (f *fakeRelay) Tools(_ context.Context, id Identity) ([]RelayedTool, error) {
	var out []RelayedTool
	for name, ok := range f.servers {
		if ok(id) {
			out = append(out, RelayedTool{Name: name + "__echo", Server: name, Tool: &mcp.Tool{
				Name: "echo", Description: "echoes", InputSchema: map[string]any{"type": "object"}}})
		}
	}
	return out, nil
}

func (f *fakeRelay) Call(_ context.Context, id Identity, name string, args json.RawMessage, progress func(done, total float64, msg string)) (*mcp.CallToolResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name+" "+id.Via)
	f.mu.Unlock()
	if progress != nil {
		progress(1, 2, "half")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo " + string(args)}}}, nil
}

func (f *fakeRelay) Generation() uint64 { return 1 }

// fakeAPI answers the routes the tests call and records the requests.
type fakeAPI struct {
	mu   sync.Mutex
	seen []string
	body []byte
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.seen = append(f.seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("X-Omo-Session"))
	f.body = body
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/me":
		ticket := 7
		json.NewEncoder(w).Encode(api.Me{Actor: api.Actor{Role: api.RoleDeveloper, Name: "dev"}, Ticket: &ticket})
	case r.URL.Path == "/api/projects":
		json.NewEncoder(w).Encode([]api.Project{{ID: "p1", Name: "one", Path: "/x"}})
	case strings.HasSuffix(r.URL.Path, "/images"):
		json.NewEncoder(w).Encode(api.Image{ID: "img1", Name: "a.png"})
	default:
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "forbidden", "message": "not yours"}})
	}
}

type fixture struct {
	api   *fakeAPI
	relay *fakeRelay
	log   *bytes.Buffer
	opts  Options
}

func newFixture() *fixture {
	f := &fixture{api: &fakeAPI{}, log: &bytes.Buffer{}, relay: &fakeRelay{servers: map[string]func(Identity) bool{
		"agents": func(id Identity) bool { return id.Class == ClassAgent },
		"global": func(Identity) bool { return true },
	}}}
	f.opts = Options{
		Relay: f.relay,
		Dispatch: func(id Identity) *apiclient.Client {
			return apiclient.NewWithTransport(apiclient.HandlerTransport(f.api), id.Session)
		},
		Log:     log.New(f.log, "", 0),
		Version: "test",
	}
	return f
}

// connect serves the handler of f as id and connects an MCP client to it.
func (f *fixture) connect(t *testing.T, id Identity) *mcp.ClientSession {
	t.Helper()
	h := NewHandler(f.opts)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
	}))
	t.Cleanup(srv.Close)
	return connectTo(t, srv.URL, nil)
}

func connectTo(t *testing.T, url string, hc *http.Client) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: hc, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tool := range res.Tools {
		out = append(out, tool.Name)
	}
	slices.Sort(out)
	return out
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func text(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// Every `omo ai` command that is not the command line's only is a tool for
// some agent session, and no tool exists without a command: the union of
// the tool lists of every kind of agent session equals the commands.
func TestMCPToolsEqualCommands(t *testing.T) {
	f := newFixture()
	f.opts.Relay = nil
	union := map[string]bool{}
	for _, a := range audiences {
		names := toolNames(t, f.connect(t, agent(a)))
		for _, n := range names {
			union[n] = true
		}
		for _, c := range aicmd.Commands() {
			want := !c.CommandLineOnly && c.Sees(a)
			if slices.Contains(names, c.Tool) != want {
				t.Errorf("%s/%s: tool %s listed %v, want %v", a.Role, a.Purpose, c.Tool, !want, want)
			}
		}
	}
	defined := map[string]bool{}
	for _, c := range aicmd.Commands() {
		if c.CommandLineOnly {
			if union[c.Tool] {
				t.Errorf("%s is command line only, but a tool", c.Tool)
			}
			continue
		}
		defined[c.Tool] = true
		if !union[c.Tool] {
			t.Errorf("command %s is no tool for any agent session", c.Tool)
		}
	}
	for n := range union {
		if !defined[n] {
			t.Errorf("tool %s has no command", n)
		}
	}
	if got := toolNames(t, f.connect(t, noSession)); len(got) != 0 {
		t.Errorf("a connection without a session lists %v", got)
	}
}

// Every input schema is an object of plain values, with no field that
// could name a session or actor: the identity comes only from the
// credential.
func TestNoIdentityFromArguments(t *testing.T) {
	_, tools := commands()
	for _, tool := range tools {
		data, err := json.Marshal(tool.tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(data, &schema); err != nil || schema.Type != "object" {
			t.Fatalf("%s: schema %s", tool.tool.Name, data)
		}
		for name := range schema.Properties {
			if strings.Contains(name, "session") || strings.Contains(name, "actor") ||
				slices.Contains([]string{"as", "agent", "agent_id", "identity", "role", "user"}, name) {
				t.Errorf("%s has the argument %q", tool.tool.Name, name)
			}
		}
	}
}

func TestConnectionWithoutSessionIsSentToTheCommandLine(t *testing.T) {
	f := newFixture()
	cs := f.connect(t, noSession)
	if got := toolNames(t, cs); !slices.Equal(got, []string{"global__echo"}) {
		t.Fatalf("tools %v", got)
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "`omo ai`") {
		t.Errorf("instructions %q", cs.InitializeResult().Instructions)
	}
	for tool, words := range map[string]string{"ticket_show": "omo ai ticket show", "comment": "omo ai comment", "wait": "omo ai wait"} {
		res := call(t, cs, tool, map[string]any{})
		if !res.IsError || !strings.Contains(text(res), "not tied to an omo agent session") || !strings.Contains(text(res), "`"+words+"`") {
			t.Errorf("%s: %+v %q", tool, res.IsError, text(res))
		}
	}
	if len(f.api.seen) != 0 {
		t.Fatalf("routes called: %v", f.api.seen)
	}
}

func TestAgentToolsRunAsTheSession(t *testing.T) {
	f := newFixture()
	cs := f.connect(t, agent(audiences[2]))
	if !strings.Contains(cs.InitializeResult().Instructions, "ticket_show") {
		t.Errorf("instructions %q", cs.InitializeResult().Instructions)
	}
	res := call(t, cs, "whoami", nil)
	if res.IsError || !strings.Contains(text(res), "ticket: #7") {
		t.Fatalf("whoami: %q", text(res))
	}
	if sc, _ := json.Marshal(res.StructuredContent); !strings.Contains(string(sc), `"ticket":7`) {
		t.Errorf("structured %s", sc)
	}
	// An array answer is wrapped in an object.
	res = call(t, cs, "project_list", map[string]any{})
	sc, _ := json.Marshal(res.StructuredContent)
	var wrapped struct {
		Items []api.Project `json:"items"`
	}
	if err := json.Unmarshal(sc, &wrapped); err != nil || len(wrapped.Items) != 1 || wrapped.Items[0].ID != "p1" {
		t.Errorf("project_list structured %s", sc)
	}
	// A route's refusal is a tool error with its message and code.
	res = call(t, cs, "ticket_show", map[string]any{"ticket": 3})
	if !res.IsError || text(res) != "not yours (forbidden)" {
		t.Errorf("refusal %v %q", res.IsError, text(res))
	}
	// Bad arguments are a tool error too, before any route.
	before := len(f.api.seen)
	for _, args := range []map[string]any{{"ticket": "x"}, {"ticket": 1, "session": "s2"}} {
		if res := call(t, cs, "comment", args); !res.IsError {
			t.Errorf("comment %v: %q", args, text(res))
		}
	}
	if len(f.api.seen) != before {
		t.Errorf("bad arguments reached a route: %v", f.api.seen[before:])
	}
	for _, s := range f.api.seen {
		if !strings.HasSuffix(s, " s1") {
			t.Errorf("request %q not as the session", s)
		}
	}
	// Command line only and hidden tools.
	if res := call(t, cs, "wait", map[string]any{}); !res.IsError || !strings.Contains(text(res), "`omo ai wait`") {
		t.Errorf("wait: %q", text(res))
	}
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "export_text"}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("export_text as a developer: %v", err)
	}
}

func TestImagePathIsTheCallersFolder(t *testing.T) {
	f := newFixture()
	dir := t.TempDir()
	png := []byte("\x89PNG\r\n\x1a\n0000")
	if err := os.WriteFile(filepath.Join(dir, "a.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	id := agent(audiences[2])
	id.Workdir = dir
	res := call(t, f.connect(t, id), "image_add", map[string]any{"ticket": 7, "path": "a.png"})
	if res.IsError || !bytes.Equal(f.api.body, png) {
		t.Fatalf("image_add: %q, uploaded %q", text(res), f.api.body)
	}
	id.Workdir = ""
	res = call(t, f.connect(t, id), "image_add", map[string]any{"ticket": 7, "path": "a.png"})
	if !res.IsError || !strings.Contains(text(res), "absolute path") {
		t.Fatalf("image_add without a folder: %q", text(res))
	}
}

func TestRelayedTools(t *testing.T) {
	f := newFixture()
	cs := f.connect(t, agent(audiences[0]))
	names := toolNames(t, cs)
	if !slices.Contains(names, "agents__echo") || !slices.Contains(names, "global__echo") {
		t.Fatalf("tools %v", names)
	}
	list, _ := cs.ListTools(context.Background(), nil)
	for _, tool := range list.Tools {
		if strings.Contains(tool.Name, "__") && !strings.HasPrefix(tool.Description, "Relayed by omo from server ") {
			t.Errorf("%s: description %q", tool.Name, tool.Description)
		}
	}
	progress := make(chan string, 4)
	c := mcp.NewClient(&mcp.Implementation{Name: "p", Version: "1"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			progress <- r.Params.Message
		},
	})
	h := NewHandler(f.opts)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), agent(audiences[0]))))
	}))
	defer srv.Close()
	pcs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pcs.Close()
	params := &mcp.CallToolParams{Name: "agents__echo", Arguments: map[string]any{"x": 1}}
	params.SetProgressToken("tok")
	res, err := pcs.CallTool(context.Background(), params)
	if err != nil || text(res) != `echo {"x":1}` {
		t.Fatalf("relayed call: %v %+v", err, res)
	}
	// The notification is handled apart from the answer.
	select {
	case msg := <-progress:
		if msg != "half" {
			t.Errorf("progress %q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Error("no progress notification")
	}

	// A relayed tool not listed for the identity is unknown and never
	// reaches the relay.
	user := f.connect(t, noSession)
	if _, err := user.CallTool(context.Background(), &mcp.CallToolParams{Name: "agents__echo"}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("agents__echo without a session: %v", err)
	}
	if res := call(t, user, "global__echo", map[string]any{}); res.IsError {
		t.Errorf("global__echo: %q", text(res))
	}
	if !slices.Equal(f.relay.calls, []string{"agents__echo socket", "global__echo pat"}) {
		t.Errorf("relay calls %v", f.relay.calls)
	}
}

func TestSwitchedOff(t *testing.T) {
	f := newFixture()
	f.opts.Settings = func() api.MCPSettings { return api.MCPSettings{} }
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}")).WithContext(WithIdentity(context.Background(), noSession))
	NewHandler(f.opts).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestSeenRecordsAgentSessionsOnly(t *testing.T) {
	f := newFixture()
	var mu sync.Mutex
	var seen []string
	f.opts.Seen = func(s string) { mu.Lock(); seen = append(seen, s); mu.Unlock() }
	f.connect(t, noSession)
	mu.Lock()
	if len(seen) != 0 {
		t.Fatalf("seen %v", seen)
	}
	mu.Unlock()
	f.connect(t, agent(audiences[1]))
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 || seen[0] != "s1" {
		t.Fatalf("seen %v", seen)
	}
}

// The tool inputs are the aicmd input types, decoded strictly.
func TestToolInputIsTheCommandInput(t *testing.T) {
	_, tools := commands()
	for _, tool := range tools {
		in, err := tool.input(nil, "/w")
		if err != nil {
			// A required argument is missing: that is the schema's job.
			continue
		}
		if reflect.TypeOf(in) != reflect.PointerTo(tool.cmd.Input) {
			t.Errorf("%s: input %T", tool.tool.Name, in)
		}
	}
}
