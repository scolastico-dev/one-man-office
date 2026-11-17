package mcprelay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
	"github.com/scolastico-dev/one-man-office/internal/mcpserver"
)

// envHeadersCommand makes the test binary print a JSON object of headers
// (the value of the variable) and exit: a header command.
const envHeadersCommand = "MCPRELAY_TEST_HEADERS"

func TestMain(m *testing.M) {
	if fakemcp.Enabled() {
		os.Exit(fakemcp.Main())
	}
	if v := os.Getenv(envHeadersCommand); v != "" {
		// "@path" prints the file's content.
		if path, ok := strings.CutPrefix(v, "@"); ok {
			data, _ := os.ReadFile(path)
			v = string(data)
		}
		fmt.Print(v)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// testTunables are short durations and small limits for tests.
func testTunables() tunables {
	t := defaultTunables()
	t.backoff = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond}
	t.janitor = time.Hour
	t.stopWait = time.Second
	t.connectWait = 5 * time.Second
	t.loginWait = 10 * time.Second
	return t
}

// events records what the pool emits.
type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) emit(kind string, data any) {
	raw, _ := json.Marshal(data)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, kind+" "+string(raw))
}

func (e *events) all() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.Join(e.list, "\n")
}

func (e *events) count(kind string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, l := range e.list {
		if strings.HasPrefix(l, kind+" ") {
			n++
		}
	}
	return n
}

// fixture is a pool over a mutable list of servers.
type fixture struct {
	t       *testing.T
	pool    *Pool
	events  *events
	logDir  string
	home    string
	env     []string
	mu      sync.Mutex
	servers []api.MCPServer
	store   *MemoryStore
}

func newFixture(t *testing.T, tune func(*tunables), servers ...api.MCPServer) *fixture {
	t.Helper()
	f := &fixture{t: t, events: &events{}, logDir: t.TempDir(), home: t.TempDir(), servers: servers, store: NewMemoryStore()}
	f.env = append(childenv.Inherited(), "OMO_SECRET_OF_OMO=not-for-children", "OMO_HOME=/nowhere")
	tun := testTunables()
	if tune != nil {
		tune(&tun)
	}
	f.pool = newPool(Options{
		Servers:  f.list,
		Auth:     f.store,
		Redirect: func(context.Context) string { return "http://localhost:18123/oauth/callback" },
		LogDir:   f.logDir,
		Env:      func() []string { return f.env },
		Home:     f.home,
		Emit:     f.events.emit,
	}, tun)
	t.Cleanup(f.pool.Close)
	return f
}

func (f *fixture) list() []api.MCPServer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.MCPServer(nil), f.servers...)
}

func (f *fixture) set(servers ...api.MCPServer) {
	f.mu.Lock()
	f.servers = servers
	f.mu.Unlock()
}

// stdioServer is a fake stdio server run by the test binary.
func stdioServer(name string, env map[string]string) api.MCPServer {
	e := map[string]string{fakemcp.EnvEnable: "1"}
	for k, v := range env {
		e[k] = v
	}
	return api.MCPServer{
		Name:      name,
		Enabled:   true,
		Transport: api.MCPTransportStdio,
		Command:   os.Args[0],
		Env:       e,
		Instances: api.MCPInstancesPerSession,
		Timeouts:  api.MCPTimeouts{Startup: api.Duration(20 * time.Second)},
	}
}

func agent(session, dir string, role api.Role) mcpserver.Identity {
	return mcpserver.Identity{
		Class:    mcpserver.ClassAgent,
		Actor:    board.Actor{Role: role, Session: session},
		Session:  session,
		Instance: session,
		Workdir:  dir,
		Local:    true,
		Via:      "socket",
	}
}

func userClass() mcpserver.Identity {
	return mcpserver.Identity{Class: mcpserver.ClassUser, Instance: "user", Via: "pat"}
}

func toolNames(tools []mcpserver.RelayedTool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

func resultText(r *mcp.CallToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// call calls tool and fails the test on a Go error.
func (f *fixture) call(id mcpserver.Identity, tool string, args any) *mcp.CallToolResult {
	f.t.Helper()
	raw, _ := json.Marshal(args)
	res, err := f.pool.Call(context.Background(), id, tool, raw, nil)
	if err != nil {
		f.t.Fatalf("call %s: %v", tool, err)
	}
	return res
}

func (f *fixture) state(name string) api.MCPServerState {
	f.t.Helper()
	for _, s := range f.pool.Status() {
		if s.Name == name {
			return s
		}
	}
	f.t.Fatalf("no state of %s", name)
	return api.MCPServerState{}
}

// eventually polls cond until it holds or the deadline passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *fixture) logText(name string) string {
	f.t.Helper()
	data, err := os.ReadFile(f.logDir + "/mcp-" + name + ".log")
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	return string(data)
}

// exe is the test binary, which also acts as the fake server and the
// header command.
func exe() string { return os.Args[0] }
