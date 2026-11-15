//go:build !windows

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/fakeagent"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// recordingRunner starts real agent processes and remembers them, so a test
// can check that none is left.
type recordingRunner struct {
	pty    *agents.PTYRunner
	mu     sync.Mutex
	agents []agents.Agent
}

func (r *recordingRunner) Start(spec agents.Spec) (agents.Agent, error) {
	a, err := r.pty.Start(spec)
	if err == nil {
		r.mu.Lock()
		r.agents = append(r.agents, a)
		r.mu.Unlock()
	}
	return a, err
}

func (r *recordingRunner) all() []agents.Agent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agents.Agent(nil), r.agents...)
}

// processWorld is a server run by Run on a fresh home, whose agents are the
// scripted fake agent running script as real processes.
type processWorld struct {
	t       *testing.T
	layout  globalhome.Layout
	runner  *recordingRunner
	hurry   chan struct{}
	url     string // the printed access URL, with the login token
	token   string
	project string
	cancel  context.CancelFunc
	done    chan error
}

func startProcessWorld(t *testing.T, script string, handoff time.Duration) *processWorld {
	t.Helper()
	isolateAgents(t)
	l := testHome(t)
	exe, err := fakeagent.Command()
	if err != nil {
		t.Fatal(err)
	}
	// The settings are files: write them before the server reads them.
	st, err := settings.Open(l.Root, settings.Options{})
	if err != nil {
		t.Fatal(err)
	}
	g := st.General()
	g.AgentEnv = map[string]string{"GIT_AUTHOR_NAME": "Agent", "GIT_AUTHOR_EMAIL": "agent@example.invalid"}
	g.Stop.HandoffTimeout = api.Duration(handoff)
	if err := st.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	off := false
	if err := st.PutProfile(settings.Profile{Name: "fake", CLI: api.CLICustom, Command: exe, Env: fakeagent.Env(script),
		PromptDelivery: api.PromptDelivery{Inject: &off}, Models: map[string]settings.Model{"a": {}}}); err != nil {
		t.Fatal(err)
	}
	for _, r := range settings.RoleNames {
		if err := st.PutRole(settings.Role{Name: r, Models: []string{"fake/a"}, Assignment: api.AssignmentRoundRobin}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := st.AddProject(committedRepo(t, "main"), "app")
	if err != nil {
		t.Fatal(err)
	}

	w := &processWorld{t: t, layout: l, runner: &recordingRunner{pty: agents.NewPTYRunner()}, hurry: make(chan struct{}),
		project: p.ID, done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	readyc := make(chan string, 1)
	o := Options{Layout: l, Config: testConfig(), Version: "test", Runner: w.runner, Hurry: w.hurry}
	go func() {
		w.done <- Run(ctx, o, io.Discard, func(u string) error { readyc <- u; return nil })
	}()
	select {
	case w.url = <-readyc:
	case err := <-w.done:
		cancel()
		t.Fatalf("Run ended before ready: %v", err)
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("Run not ready")
	}
	_, w.token, _ = strings.Cut(w.url, "#")
	t.Cleanup(func() {
		w.stop(time.Minute)
		for _, a := range w.runner.all() {
			select {
			case <-a.Done():
			case <-time.After(15 * time.Second):
				t.Errorf("an agent process is still running after the test")
				_ = a.Kill()
			}
		}
	})
	return w
}

// stop ends Run and waits at most limit for it to return.
func (w *processWorld) stop(limit time.Duration) error {
	w.t.Helper()
	w.cancel()
	select {
	case err, ok := <-w.done:
		if ok {
			close(w.done)
		}
		return err
	case <-time.After(limit):
		w.t.Fatalf("Run did not return within %s", limit)
		return nil
	}
}

// call sends a request as the user through the socket and decodes the
// answer into out.
func (w *processWorld) call(method, path string, in, out any) {
	w.t.Helper()
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, "http://omo"+path, body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := socketClient(w.layout).Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		w.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			w.t.Fatal(err)
		}
	}
}

// approveTask creates a standalone task and approves it.
func (w *processWorld) approveTask() int {
	w.t.Helper()
	var tk api.Ticket
	w.call("POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{w.project}}, &tk)
	w.call("POST", tp(tk.Number)+"/move", api.MoveRequest{Column: api.ColumnReady}, nil)
	return tk.Number
}

// developer waits until the developer of ticket n is in state.
func (w *processWorld) developer(n int, state api.SessionState) api.AgentSession {
	w.t.Helper()
	var found api.AgentSession
	waitFor(w.t, "the developer to be "+string(state), func() bool {
		var ss []api.AgentSession
		w.call("GET", tp(n)+"/agents", nil, &ss)
		for _, s := range ss {
			if s.Role == api.RoleDeveloper && s.State == state {
				found = s
				return true
			}
		}
		return false
	})
	return found
}

// terminal opens the terminal of a running agent through the TCP listener
// with the printed address, as a browser does.
func (w *processWorld) terminal(termID string) *websocket.Conn {
	w.t.Helper()
	u, err := url.Parse(w.url)
	if err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+u.Port()+"/api/agents/"+termID+"/terminal", &websocket.DialOptions{
		Host: u.Host, Subprotocols: []string{"omo", webSocketTokenPrefix + w.token},
		HTTPHeader: http.Header{"Origin": {"http://" + u.Host}},
	})
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { c.CloseNow() })
	return c
}

// comments reads ticket n's comments from the database after Run returned.
func (w *processWorld) comments(n int) []string {
	w.t.Helper()
	d, err := db.Open(w.layout.DB)
	if err != nil {
		w.t.Fatal(err)
	}
	defer d.Close()
	st, err := settings.Open(w.layout.Root, settings.Options{})
	if err != nil {
		w.t.Fatal(err)
	}
	b := board.New(d, boardEnv{st: st}, board.Options{})
	cs, err := b.Comments(context.Background(), board.User, int64(n))
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		out = append(out, c.Body)
	}
	return out
}

// sessions reads every session after Run returned.
func (w *processWorld) sessions() []api.AgentSession {
	w.t.Helper()
	d, err := db.Open(w.layout.DB)
	if err != nil {
		w.t.Fatal(err)
	}
	defer d.Close()
	st, err := settings.Open(w.layout.Root, settings.Options{})
	if err != nil {
		w.t.Fatal(err)
	}
	ss, err := board.New(d, boardEnv{st: st}, board.Options{}).AgentSessions(context.Background(), board.User, false)
	if err != nil {
		w.t.Fatal(err)
	}
	return ss
}

// TestFakeAgentReadyWaitAndTerminalThroughRun runs the fake agent as a real
// process against a server started by Run: it runs omo ai ready and parks in
// omo ai wait over the real socket, a mention wakes it, and the user watches
// all of it in the agent's terminal over the TCP listener. A terminal held
// open does not keep the server from stopping.
func TestFakeAgentReadyWaitAndTerminalThroughRun(t *testing.T) {
	w := startProcessWorld(t, "ready\nwait|1h\nprint|after the wait\nhang", 5*time.Second)
	n := w.approveTask()
	dev := w.developer(n, api.SessionWaiting)
	if dev.TerminalID == "" {
		t.Fatal("the user's agent list has no terminal id")
	}
	c := w.terminal(dev.TerminalID)
	out := &termOutput{t: t, c: c}
	out.until("fake: ready")
	w.call("POST", tp(n)+"/comments", api.CommentRequest{Body: "@developer wake up"}, nil)
	out.until(fmt.Sprintf("fake: woke up: omo: #%d", n))
	out.until("after the wait")
	w.developer(n, api.SessionRunning)

	// The terminal is still open; the server stops anyway, the agent with
	// it, and the connection ends.
	if err := w.stop(time.Minute); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the terminal stayed open after the server stopped")
			}
			break
		}
	}
	for _, s := range w.sessions() {
		if s.State != api.SessionEnded || s.EndReason != "omo stopped" {
			t.Fatalf("session %s %q after the stop", s.State, s.EndReason)
		}
	}
}

// TestRunHandsOffBeforeClosingSocket stops a server whose agent is at work:
// the agent is asked for a handoff and writes it through the socket, which
// still serves, before Run returns.
func TestRunHandsOffBeforeClosingSocket(t *testing.T) {
	w := startProcessWorld(t, "ready\nawait-line|omo is stopping|10m\ncomment|$TICKET|Handoff: fake state\nwait|10m", 2*time.Minute)
	n := w.approveTask()
	w.developer(n, api.SessionRunning)
	if err := w.stop(time.Minute); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range w.comments(n) {
		if c == "Handoff: fake state" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no handoff comment: %q", w.comments(n))
	}
	for _, s := range w.sessions() {
		if s.State != api.SessionEnded || s.EndReason != "omo stopped" {
			t.Fatalf("session %s %q after the stop", s.State, s.EndReason)
		}
	}
}

// TestSecondStopSkipsHandoffWait stops a server whose agent never answers
// the handoff: the first stop waits for it, a second (Hurry) ends the wait
// at once.
func TestSecondStopSkipsHandoffWait(t *testing.T) {
	w := startProcessWorld(t, "ready\nhang", time.Hour)
	n := w.approveTask()
	w.developer(n, api.SessionRunning)
	agent := w.runner.all()[0]
	w.cancel()
	waitFor(t, "the handoff request", func() bool {
		return strings.Contains(strings.Join(agent.Tail(20), "\n"), "fake: typed: omo: omo is stopping")
	})
	select {
	case <-w.done:
		t.Fatal("Run returned without waiting for the handoff")
	default:
	}
	close(w.hurry)
	if err := w.stop(time.Minute); err != nil {
		t.Fatal(err)
	}
	select {
	case <-agent.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("the agent outlived Run")
	}
}
