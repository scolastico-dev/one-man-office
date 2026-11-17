package mcpbridge

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/server"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

// helperEnv makes the test binary run as `omo mcp stdio`.
const helperEnv = "OMO_TEST_MCP_BRIDGE"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		os.Exit(runHelper())
	}
	os.Exit(m.Run())
}

// runHelper is the bridge process: what `omo mcp stdio` runs.
func runHelper() int {
	retryWait = 100 * time.Millisecond
	session, dial, err := Environment()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	err = Run(context.Background(), Options{Dial: dial, Session: session, In: os.Stdin, Out: os.Stdout,
		Log: log.New(os.Stderr, "bridge: ", 0), Version: "test"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type stubEnv struct{}

func (stubEnv) Policy() board.Policy                      { return board.Policy{} }
func (stubEnv) Project(string) (board.ProjectInfo, bool)  { return board.ProjectInfo{}, false }
func (stubEnv) Model(string) (board.ModelInfo, bool)      { return board.ModelInfo{}, false }
func (stubEnv) CurrentBranch(string) (string, error)      { return "main", nil }
func (stubEnv) BranchExists(string, string) (bool, error) { return true, nil }

// omo is a real server on a temporary home, reachable on its socket.
type omo struct {
	t      *testing.T
	home   string
	layout globalhome.Layout
	srv    *server.Server
	http   *http.Server
	stop   func()
	socket string
}

func newOmo(t *testing.T) *omo {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	layout, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	o := &omo{t: t, home: home, layout: layout}
	o.start()
	t.Cleanup(o.close)
	return o
}

func (o *omo) start() {
	o.t.Helper()
	srv, err := server.New(server.Options{Layout: o.layout, Config: serverconfig.Defaults(), Version: "test"})
	if err != nil {
		o.t.Fatal(err)
	}
	actual, _, cleanup, err := transport.Endpoint(o.layout.Run)
	if err != nil {
		srv.Close()
		o.t.Fatal(err)
	}
	ln, err := transport.Listen(actual)
	if err != nil {
		cleanup()
		srv.Close()
		o.t.Fatal(err)
	}
	o.srv, o.socket, o.stop = srv, actual, cleanup
	o.http = &http.Server{Handler: srv.SocketHandler(), ReadHeaderTimeout: 5 * time.Second}
	go o.http.Serve(ln)
}

// close stops the server; start runs it again on the same home.
func (o *omo) close() {
	if o.srv == nil {
		return
	}
	o.http.Close()
	o.stop()
	o.srv.Close()
	o.srv = nil
}

// withDB runs f on the home's database next to the running server.
func (o *omo) withDB(f func(*sql.DB)) {
	o.t.Helper()
	d, err := db.Open(o.layout.DB)
	if err != nil {
		o.t.Fatal(err)
	}
	defer d.Close()
	f(d)
}

// ceoSession creates a live CEO session and returns its id and name.
func (o *omo) ceoSession() (id, name string) {
	o.t.Helper()
	o.withDB(func(d *sql.DB) {
		s, err := board.New(d, stubEnv{}, board.Options{}).CreateSession(context.Background(),
			board.NewSession{Role: api.RoleCEO, Name: "Ada", Model: "fake/default", Workdir: o.t.TempDir(), LogPath: o.t.TempDir() + "/ceo.log"})
		if err != nil {
			o.t.Fatal(err)
		}
		id, name = s.ID, s.Name
	})
	return id, name
}

// toolsChanged emits mcp.tools_changed as omo does.
func (o *omo) toolsChanged() {
	o.withDB(func(d *sql.DB) {
		if _, err := db.AppendEvent(d, api.KindMCPToolsChanged, `{"role":"system"}`, 0, "{}"); err != nil {
			o.t.Fatal(err)
		}
	})
}

// syncBuffer is a buffer the bridge's stderr is copied into.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// bridgeProc is a running bridge process and its MCP client.
type bridgeProc struct {
	cs      *mcp.ClientSession
	cmd     *exec.Cmd
	stderr  *syncBuffer
	changed chan struct{}
}

// bridgeCmd is the bridge process with env laid over the test's
// environment (without OMO_AGENT_ID and OMO_SOCKET unless env sets them).
func bridgeCmd(env map[string]string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "OMO_AGENT_ID=") && !strings.HasPrefix(kv, "OMO_SOCKET=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, helperEnv+"=1")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	return cmd
}

// startBridge runs the bridge with env and connects an MCP client speaking
// version ("" is the SDK's newest).
func startBridge(t *testing.T, env map[string]string, version string) *bridgeProc {
	t.Helper()
	b := &bridgeProc{cmd: bridgeCmd(env), stderr: &syncBuffer{}, changed: make(chan struct{}, 16)}
	b.cmd.Stderr = b.stderr
	c := mcp.NewClient(&mcp.Implementation{Name: "test-cli", Version: "1"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			select {
			case b.changed <- struct{}{}:
			default:
			}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cs, err := c.Connect(ctx, &mcp.CommandTransport{Command: b.cmd}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatalf("connect: %v\nbridge: %s", err, b.stderr)
	}
	b.cs = cs
	t.Cleanup(func() { cs.Close() })
	return b
}

func (b *bridgeProc) tools(t *testing.T) []string {
	t.Helper()
	res, err := b.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v\nbridge: %s", err, b.stderr)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func (b *bridgeProc) call(t *testing.T, name string, args any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := b.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("tools/call %s: %v\nbridge: %s", name, err, b.stderr)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String(), res.IsError
}

// versions are the protocol eras a CLI may speak over stdio.
var versions = []string{"2025-06-18", "2025-11-25", ""}

func TestBridgeActsAsItsAgentSession(t *testing.T) {
	o := newOmo(t)
	id, name := o.ceoSession()
	for _, v := range versions {
		t.Run("version "+v, func(t *testing.T) {
			b := startBridge(t, map[string]string{"OMO_AGENT_ID": id, "OMO_SOCKET": o.socket}, v)
			if got := b.cs.InitializeResult().Instructions; !strings.Contains(got, "ticket_show") || strings.Contains(got, "not tied") {
				t.Errorf("instructions %q", got)
			}
			tools := b.tools(t)
			if !slices.Contains(tools, "whoami") || !slices.Contains(tools, "ticket_create") || slices.Contains(tools, "wait") {
				t.Fatalf("tools of a CEO session: %v", tools)
			}
			text, isErr := b.call(t, "whoami", map[string]any{})
			if isErr || !strings.Contains(text, name) || !strings.Contains(text, "ceo") {
				t.Fatalf("whoami = %q (error %v)", text, isErr)
			}
			if strings.Contains(b.stderr.String(), id) {
				t.Fatal("the bridge logged the session id")
			}
		})
	}
}

// A bridge that does not learn the session (a program that does not pass
// OMO_AGENT_ID to it) connects without one: omo offers no board tools and
// tells the caller to use `omo ai` in the terminal, in the instructions and
// in the answer of a call.
func TestBridgeWithoutSessionIsToldToUseTheCommandLine(t *testing.T) {
	o := newOmo(t)
	for _, v := range versions {
		t.Run("version "+v, func(t *testing.T) {
			b := startBridge(t, map[string]string{"OMO_SOCKET": o.socket}, v)
			if got := b.cs.InitializeResult().Instructions; !strings.Contains(got, "not tied to an omo agent session") || !strings.Contains(got, "omo ai") {
				t.Errorf("instructions %q", got)
			}
			if tools := b.tools(t); len(tools) != 0 {
				t.Fatalf("tools without a session: %v", tools)
			}
			text, isErr := b.call(t, "comment", map[string]any{"ticket": 1, "text": "hello"})
			if !isErr || !strings.Contains(text, "not tied to an omo agent session") || !strings.Contains(text, "`omo ai comment`") {
				t.Fatalf("comment without a session = %q (error %v)", text, isErr)
			}
		})
	}
}

func TestBridgeFindsTheHomeSocketWithoutOMOSocket(t *testing.T) {
	o := newOmo(t)
	id, name := o.ceoSession()
	b := startBridge(t, map[string]string{"OMO_AGENT_ID": id, "OMO_HOME": o.home}, "")
	if text, isErr := b.call(t, "whoami", map[string]any{}); isErr || !strings.Contains(text, name) {
		t.Fatalf("whoami = %q (error %v)", text, isErr)
	}
}

func TestBridgeRefusesABlankSession(t *testing.T) {
	o := newOmo(t)
	cmd := bridgeCmd(map[string]string{"OMO_AGENT_ID": " ", "OMO_SOCKET": o.socket})
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "OMO_AGENT_ID is set but empty") {
		t.Fatalf("blank OMO_AGENT_ID: %v\n%s", err, out)
	}
}

func TestBridgePushesToolListChanges(t *testing.T) {
	o := newOmo(t)
	id, _ := o.ceoSession()
	for _, v := range versions {
		t.Run("version "+v, func(t *testing.T) {
			b := startBridge(t, map[string]string{"OMO_AGENT_ID": id, "OMO_SOCKET": o.socket}, v)
			b.tools(t)
			// The bridge opens the event stream on its own; emit until one
			// notification arrives.
			deadline := time.After(20 * time.Second)
			tick := time.NewTicker(500 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-b.changed:
					if slices.Contains(b.tools(t), "omo_bridge_tools_changed") {
						t.Fatal("the change marker was listed")
					}
					return
				case <-tick.C:
					o.toolsChanged()
				case <-deadline:
					t.Fatalf("no tools/list_changed\nbridge: %s", b.stderr)
				}
			}
		})
	}
}

// The bridge outlives omo: while omo is down a call says so, and once omo
// runs again on the same home the bridge reaches it and tells its client
// that the tools may have changed.
func TestBridgeSurvivesOmoRestarting(t *testing.T) {
	o := newOmo(t)
	id, name := o.ceoSession()
	b := startBridge(t, map[string]string{"OMO_AGENT_ID": id, "OMO_HOME": o.home}, "2025-11-25")
	if text, isErr := b.call(t, "whoami", map[string]any{}); isErr || !strings.Contains(text, name) {
		t.Fatalf("whoami = %q (error %v)", text, isErr)
	}
	o.close()
	text, isErr := b.call(t, "whoami", map[string]any{})
	if !isErr || !strings.Contains(text, "did not answer") || !strings.Contains(text, "omo ai") {
		t.Fatalf("whoami while omo is down = %q (error %v)", text, isErr)
	}
	for len(b.changed) > 0 {
		<-b.changed
	}
	o.start()
	if text, isErr := b.call(t, "whoami", map[string]any{}); isErr || !strings.Contains(text, name) {
		t.Fatalf("whoami after the restart = %q (error %v)\nbridge: %s", text, isErr, b.stderr)
	}
	select {
	case <-b.changed:
	case <-time.After(20 * time.Second):
		t.Fatalf("no tools/list_changed after the restart\nbridge: %s", b.stderr)
	}
	if b.cmd.ProcessState != nil {
		t.Fatal("the bridge ended")
	}
}

// An ended session is refused at the next call and never taken for a
// connection without a session.
func TestBridgeOfAnEndedSessionIsRefused(t *testing.T) {
	o := newOmo(t)
	id, _ := o.ceoSession()
	b := startBridge(t, map[string]string{"OMO_AGENT_ID": id, "OMO_SOCKET": o.socket}, "2025-11-25")
	b.tools(t)
	o.withDB(func(d *sql.DB) {
		if err := board.New(d, stubEnv{}, board.Options{}).EndSession(context.Background(), id, "test"); err != nil {
			t.Fatal(err)
		}
	})
	text, isErr := b.call(t, "whoami", map[string]any{})
	if !isErr || !strings.Contains(text, "unknown_session") {
		t.Fatalf("whoami of an ended session = %q (error %v)", text, isErr)
	}
	if strings.Contains(text, id) || strings.Contains(b.stderr.String(), id) {
		t.Fatal("the session id was shown")
	}
}

func TestBridgeEndsWhenItsInputCloses(t *testing.T) {
	o := newOmo(t)
	cmd := bridgeCmd(map[string]string{"OMO_SOCKET": o.socket})
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// A request first: once it is answered the bridge serves.
	fmt.Fprintln(stdin, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.Contains(line, `"id":1`) {
		cmd.Process.Kill()
		t.Fatalf("ping = %q, %v", line, err)
	}
	go io.Copy(io.Discard, stdout)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stdin.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the bridge ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the bridge kept running after its input closed")
	}
}
