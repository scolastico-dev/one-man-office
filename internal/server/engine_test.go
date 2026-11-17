package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/webterm"
)

// ---- an agent without a process ----

// pipeProcess is a terminal process made of pipes: the test writes its
// output with say, and reads what was typed into it.
type pipeProcess struct {
	outR *io.PipeReader
	outW *io.PipeWriter

	mu    sync.Mutex
	input bytes.Buffer

	done chan struct{}
	once sync.Once
}

func newPipeProcess() *pipeProcess {
	r, w := io.Pipe()
	return &pipeProcess{outR: r, outW: w, done: make(chan struct{})}
}

func (p *pipeProcess) Read(b []byte) (int, error) { return p.outR.Read(b) }
func (p *pipeProcess) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input.Write(b)
}
func (p *pipeProcess) Resize(rows, cols uint16) error { return nil }
func (p *pipeProcess) Close() error {
	p.outR.Close()
	return p.outW.Close()
}
func (p *pipeProcess) Kill() error {
	p.once.Do(func() { close(p.done) })
	return nil
}
func (p *pipeProcess) Wait() error {
	<-p.done
	return nil
}

func (p *pipeProcess) typed() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input.String()
}

// testAgent is an agent without a process; its web terminal is a
// pipeProcess.
type testAgent struct {
	id     string
	termID string
	proc   *pipeProcess
	term   *webterm.Terminal
	exited chan struct{}
	once   sync.Once

	mu    sync.Mutex
	lines []string
}

func newTestAgent(sessionID string) *testAgent {
	raw := make([]byte, 8)
	rand.Read(raw)
	a := &testAgent{id: sessionID, termID: "term-" + hex.EncodeToString(raw), proc: newPipeProcess(), exited: make(chan struct{})}
	a.term = webterm.Own(a.termID, a.proc, nil)
	return a
}

// say writes a line of agent output to its terminal.
func (a *testAgent) say(text string) {
	go a.proc.outW.Write([]byte(text + "\r\n"))
}

func (a *testAgent) SessionID() string           { return a.id }
func (a *testAgent) TerminalID() string          { return a.termID }
func (a *testAgent) Exited() <-chan struct{}     { return a.exited }
func (a *testAgent) Done() <-chan struct{}       { return a.exited }
func (a *testAgent) ExitErr() error              { return nil }
func (a *testAgent) SendPrompt(string) error     { return nil }
func (a *testAgent) LastOutput() time.Time       { return time.Now() }
func (a *testAgent) Tail(int) []string           { return nil }
func (a *testAgent) Terminal() *webterm.Terminal { return a.term }
func (a *testAgent) Warnings() []string          { return nil }
func (a *testAgent) Notify(line string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lines = append(a.lines, line)
}
func (a *testAgent) Kill() error {
	a.once.Do(func() {
		a.proc.Kill()
		close(a.exited)
	})
	return nil
}

func (a *testAgent) isOver() bool {
	select {
	case <-a.exited:
		return true
	default:
		return false
	}
}

// testRunner starts testAgents.
type testRunner struct {
	mu     sync.Mutex
	agents []*testAgent
}

func (r *testRunner) Start(spec agents.Spec) (agents.Agent, error) {
	a := newTestAgent(spec.SessionID)
	r.mu.Lock()
	r.agents = append(r.agents, a)
	r.mu.Unlock()
	return a, nil
}

// agent returns the agent of a session.
func (r *testRunner) agent(t *testing.T, sessionID string) *testAgent {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.agents {
		if a.id == sessionID {
			return a
		}
	}
	t.Fatal("no agent for the session")
	return nil
}

func (r *testRunner) all() []*testAgent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*testAgent(nil), r.agents...)
}

// ---- an engine server ----

// isolateAgents keeps a test away from the developer's home, git and agent
// configuration.
func isolateAgents(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.invalid")
	}
	t.Setenv("OMO_AGENT_ID", "")
	os.Unsetenv("OMO_AGENT_ID")
}

// engineServer opens a server with runner on a fresh home that has a
// profile "fake" assigned to every role and one project (returned) with a
// commit on main. start runs the engine.
func engineServer(t *testing.T, runner agents.Runner, prof settings.Profile, start bool) (*Server, string) {
	t.Helper()
	return engineServerWith(t, runner, prof, start, nil)
}

// engineServerWith is engineServer with the server's configuration changed
// by cfg.
func engineServerWith(t *testing.T, runner agents.Runner, prof settings.Profile, start bool, cfg func(*serverconfig.Config)) (*Server, string) {
	t.Helper()
	isolateAgents(t)
	logs := &syncBuffer{}
	var mutate []func(*serverconfig.Config)
	if cfg != nil {
		mutate = append(mutate, cfg)
	}
	s, err := newServer(Options{Layout: testHome(t), Config: testConfig(mutate...), Version: "test", Runner: runner}, logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})
	g := s.settings.General()
	g.AgentEnv = map[string]string{"GIT_AUTHOR_NAME": "Agent", "GIT_AUTHOR_EMAIL": "agent@example.invalid"}
	if err := s.settings.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.PutProfile(prof); err != nil {
		t.Fatal(err)
	}
	for _, r := range settings.RoleNames {
		if err := s.settings.PutRole(settings.Role{Name: r, Models: []string{"fake/a"}, Assignment: api.AssignmentRoundRobin}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.settings.AddProject(committedRepo(t, "main"), "app")
	if err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())
	if start {
		s.StartEngine()
	}
	return s, p.ID
}

// fakeProfile is a profile whose agents the testRunner plays.
func fakeProfile() settings.Profile {
	return settings.Profile{Name: "fake", CLI: api.CLICustom, Command: "fake-agent", Models: map[string]settings.Model{"a": {}}}
}

// waitFor polls cond until it holds or a minute passed.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// liveOf returns the live session of role on ticket n (0: none).
func liveOf(t *testing.T, s *Server, role api.Role, n int) (api.AgentSession, bool) {
	t.Helper()
	all, err := s.board.AgentSessions(context.Background(), board.User, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, sess := range all {
		if sess.Role != role {
			continue
		}
		if n == 0 || (sess.Ticket != nil && *sess.Ticket == n) {
			return sess, true
		}
	}
	return api.AgentSession{}, false
}

// session returns the current record of a session.
func sessionByID(t *testing.T, s *Server, id string) api.AgentSession {
	t.Helper()
	all, err := s.board.AgentSessions(context.Background(), board.User, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, sess := range all {
		if sess.ID == id {
			return sess
		}
	}
	t.Fatal("no such session")
	return api.AgentSession{}
}

// startDeveloper creates a standalone task, approves it and waits until the
// engine runs its developer.
func startDeveloper(t *testing.T, s *Server, project string) (int, api.AgentSession) {
	t.Helper()
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{project}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Move(ctx, board.User, int64(tk.Number), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		t.Fatal(err)
	}
	var dev api.AgentSession
	waitFor(t, "the developer", func() bool {
		var ok bool
		dev, ok = liveOf(t, s, api.RoleDeveloper, tk.Number)
		return ok && hasTerminal(s, dev.ID)
	})
	return tk.Number, dev
}

func hasTerminal(s *Server, id string) bool {
	_, ok := s.engine.Terminal(id)
	return ok
}

// socketCtx sends a request through the socket handler under ctx.
func socketCtx(t *testing.T, ctx context.Context, s *Server, method, path string, body any, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(t, method, "http://omo"+path, body).WithContext(ctx)
	if session != "" {
		req.Header.Set(SessionHeader, session)
	}
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	return rec
}

// wantError checks a recorded error answer.
func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body.Bytes())
	}
	if got := errorCode(t, bytes.NewReader(rec.Body.Bytes())); got != code {
		t.Fatalf("code %q, want %q", got, code)
	}
}

// browserError checks an error answer of the browser listener.
func browserError(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	if resp.StatusCode != status {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, want %d: %s", resp.StatusCode, status, raw)
	}
	if got := errorCode(t, resp.Body); got != code {
		t.Fatalf("code %q, want %q", got, code)
	}
}

var empty = struct{}{}

// ---- tests ----

func TestAgentRoutesWithoutEngineAre503(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	dev := liveSession(t, s, api.RoleDeveloper)
	for _, c := range []struct {
		method, path string
		body         any
		session      string
	}{
		{"POST", "/api/ai/ready", empty, dev.ID},
		{"POST", "/api/ai/wait", api.WaitRequest{Timeout: api.Duration(time.Minute)}, dev.ID},
		{"POST", "/api/ai/export-text", empty, dev.ID},
		{"GET", "/api/scheduler", nil, ""},
		{"POST", "/api/agents/stop-all", empty, ""},
		{"POST", "/api/ceo", empty, ""},
		{"POST", "/api/ceo/new-session", empty, ""},
		{"POST", "/api/ceo/stop", empty, ""},
		{"GET", "/api/ceo", nil, ""},
		{"GET", "/api/agents/x/terminal", nil, ""},
	} {
		wantError(t, socketDo(t, s, c.method, c.path, c.body, c.session), http.StatusServiceUnavailable, api.CodeUnavailable)
		if c.session == "" {
			browserError(t, asUser(b, s, c.method, c.path, c.body), http.StatusServiceUnavailable, api.CodeUnavailable)
		}
	}
	// Without an engine there is no hand-in guard, and retry is the
	// board's: neither depends on the engine.
	rec := socketDo(t, s, "POST", tp(int(*dev.Ticket))+"/retry", empty, "")
	wantError(t, rec, http.StatusConflict, api.CodeInvalidTransition)
}

func TestOldGitRefusesStart(t *testing.T) {
	old := gitVersion
	t.Cleanup(func() { gitVersion = old })
	gitVersion = func() (int, int, error) { return 2, 37, nil }
	l := testHome(t)
	_, err := newServer(Options{Layout: l, Config: testConfig(), Runner: &testRunner{}}, io.Discard)
	if err == nil || err.Error() != "omo needs git 2.38 or newer (found 2.37)" {
		t.Fatalf("New = %v", err)
	}
	if _, err := os.Stat(l.DB); !os.IsNotExist(err) {
		t.Fatalf("the database was opened: %v", err)
	}
	// Run fails the same way, before any listener exists.
	if err := Run(context.Background(), Options{Layout: l, Config: testConfig(), Runner: &testRunner{}}, io.Discard, nil); err == nil ||
		!strings.Contains(err.Error(), "omo needs git 2.38 or newer") {
		t.Fatalf("Run = %v", err)
	}
	assertReleased(t, l, "")
	// Without agents the version does not matter.
	s, err := newServer(Options{Layout: l, Config: testConfig()}, io.Discard)
	if err != nil {
		t.Fatalf("New without a runner = %v", err)
	}
	s.Close()
}

func TestDefaultPromptsAreListed(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	infos := body[[]api.PromptInfo](t, asUser(b, s, "GET", "/api/prompts", nil))
	names := map[string]bool{}
	for _, p := range infos {
		names[p.Name] = true
	}
	for _, want := range []string{"ceo", "pm", "developer", "reviewer", "export", "developer-instructions", "handoff", "wait-timeout"} {
		if !names[want] {
			t.Errorf("prompt %s is not listed: %+v", want, infos)
		}
	}
	got := body[api.Prompt](t, asUser(b, s, "GET", "/api/prompts/developer", nil))
	if got.Customized || !strings.Contains(got.Text, "omo ai done") {
		t.Fatalf("developer prompt %+v", got)
	}
}

func TestReadyRouteReturnsPrompt(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	b := newBrowser(t, s)
	n, dev := startDeveloper(t, s, p)
	if dev.State != api.SessionStarting {
		t.Fatalf("state %s before ready", dev.State)
	}
	rec := socketDo(t, s, "POST", "/api/ai/ready", empty, dev.ID)
	wantStatus(t, rec.Code, http.StatusOK)
	got := decodeBody[api.ReadyResponse](t, rec.Body)
	if !strings.Contains(got.Prompt, "omo ai done") || !strings.Contains(got.Prompt, fmt.Sprintf("#%d", n)) {
		t.Fatalf("prompt %q", got.Prompt)
	}
	if strings.Contains(got.Prompt, dev.ID) {
		t.Fatal("the prompt shows the session id")
	}
	waitFor(t, "the session to run", func() bool { return sessionByID(t, s, dev.ID).State == api.SessionRunning })
	// Only agent sessions get a prompt; the body is the empty object.
	wantError(t, socketDo(t, s, "POST", "/api/ai/ready", empty, ""), http.StatusForbidden, api.CodeForbidden)
	browserError(t, asUser(b, s, "POST", "/api/ai/ready", empty), http.StatusForbidden, api.CodeForbidden)
	wantError(t, socketDo(t, s, "POST", "/api/ai/ready", map[string]int{"x": 1}, dev.ID), http.StatusBadRequest, api.CodeBadRequest)
}

// Only an export job has a pull request text to check; the engine refuses
// the user and every other agent.
func TestExportTextRouteIsForExportJobs(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	b := newBrowser(t, s)
	_, dev := startDeveloper(t, s, p)
	wantError(t, socketDo(t, s, "POST", "/api/ai/export-text", empty, dev.ID), http.StatusForbidden, api.CodeForbidden)
	wantError(t, socketDo(t, s, "POST", "/api/ai/export-text", empty, ""), http.StatusForbidden, api.CodeForbidden)
	browserError(t, asUser(b, s, "POST", "/api/ai/export-text", empty), http.StatusForbidden, api.CodeForbidden)
	wantError(t, socketDo(t, s, "POST", "/api/ai/export-text", map[string]int{"x": 1}, dev.ID), http.StatusBadRequest, api.CodeBadRequest)
}

func TestWaitRouteValidatesTimeout(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	b := newBrowser(t, s)
	_, dev := startDeveloper(t, s, p)
	wantStatus(t, socketDo(t, s, "POST", "/api/ai/ready", empty, dev.ID).Code, http.StatusOK)

	wantError(t, socketDo(t, s, "POST", "/api/ai/wait", empty, dev.ID), http.StatusBadRequest, api.CodeBadRequest)
	wantError(t, socketDo(t, s, "POST", "/api/ai/wait", map[string]string{"timeout": "0s"}, dev.ID), http.StatusBadRequest, api.CodeBadRequest)
	wantError(t, socketDo(t, s, "POST", "/api/ai/wait", map[string]string{"timeout": "soon"}, dev.ID), http.StatusBadRequest, api.CodeBadRequest)
	wantError(t, socketDo(t, s, "POST", "/api/ai/wait", map[string]string{"timeout": "25h"}, dev.ID), http.StatusUnprocessableEntity, api.CodeValidation)
	minute := map[string]string{"timeout": "1m"}
	wantError(t, socketDo(t, s, "POST", "/api/ai/wait", minute, ""), http.StatusForbidden, api.CodeForbidden)
	browserError(t, asUser(b, s, "POST", "/api/ai/wait", minute), http.StatusForbidden, api.CodeForbidden)
	browserError(t, asUser(b, s, "POST", "/api/ai/wait", empty), http.StatusForbidden, api.CodeForbidden)
	if st := sessionByID(t, s, dev.ID).State; st != api.SessionRunning {
		t.Fatalf("state %s after refused waits", st)
	}
}

func TestWaitRouteReturnsOnMention(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	n, dev := startDeveloper(t, s, p)
	wantStatus(t, socketDo(t, s, "POST", "/api/ai/ready", empty, dev.ID).Code, http.StatusOK)

	answer := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		answer <- socketCtx(t, context.Background(), s, "POST", "/api/ai/wait", map[string]string{"timeout": "1h"}, dev.ID)
	}()
	waitFor(t, "the developer to park", func() bool { return sessionByID(t, s, dev.ID).State == api.SessionWaiting })
	if _, err := s.board.Comment(context.Background(), board.User, int64(n), api.CommentRequest{Body: "@developer please rename the file"}); err != nil {
		t.Fatal(err)
	}
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-answer:
	case <-time.After(time.Minute):
		t.Fatal("the wait did not return after the mention")
	}
	wantStatus(t, rec.Code, http.StatusOK)
	got := decodeBody[api.WaitResponse](t, rec.Body)
	if !strings.Contains(got.Reason, fmt.Sprintf("#%d", n)) || strings.Contains(got.Reason, dev.ID) {
		t.Fatalf("reason %q", got.Reason)
	}
	waitFor(t, "the developer to run again", func() bool { return sessionByID(t, s, dev.ID).State == api.SessionRunning })
}

func TestWaitEndsWhenClientLeaves(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	_, dev := startDeveloper(t, s, p)
	wantStatus(t, socketDo(t, s, "POST", "/api/ai/ready", empty, dev.ID).Code, http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())
	answer := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		answer <- socketCtx(t, ctx, s, "POST", "/api/ai/wait", map[string]string{"timeout": "24h"}, dev.ID)
	}()
	waitFor(t, "the developer to park", func() bool { return sessionByID(t, s, dev.ID).State == api.SessionWaiting })
	cancel()
	select {
	case rec := <-answer:
		if rec.Body.Len() != 0 {
			t.Fatalf("answered a client that left: %s", rec.Body.Bytes())
		}
	case <-time.After(time.Minute):
		t.Fatal("the wait outlived its client")
	}
	waitFor(t, "the developer to run again", func() bool { return sessionByID(t, s, dev.ID).State == api.SessionRunning })
}

func TestDoneRouteRefusedWithDirtyWorktree(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	n, dev := startDeveloper(t, s, p)
	stray := filepath.Join(dev.Workdir, "stray.txt")
	if err := os.WriteFile(stray, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := socketDo(t, s, "POST", "/api/ai/done", api.DoneRequest{Comment: "finished"}, dev.ID)
	wantStatus(t, rec.Code, http.StatusConflict)
	var env api.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != api.CodeConflict || !strings.Contains(env.Error.Message, "stray.txt") {
		t.Fatalf("error %+v", env.Error)
	}
	tk, err := s.board.Get(context.Background(), board.User, int64(n))
	if err != nil {
		t.Fatal(err)
	}
	if tk.Column != api.ColumnInProgress {
		t.Fatalf("the refused hand-in moved the ticket to %s", tk.Column)
	}
	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}
	rec = socketDo(t, s, "POST", "/api/ai/done", api.DoneRequest{Comment: "finished"}, dev.ID)
	wantStatus(t, rec.Code, http.StatusOK)
	if got := decodeBody[api.Ticket](t, rec.Body); got.Column != api.ColumnReview {
		t.Fatalf("column %s after the hand-in", got.Column)
	}
}

func TestRetryRoute(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	ctx := context.Background()
	p := addProject(t, s)
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{p}})
	if err != nil {
		t.Fatal(err)
	}
	n := int64(tk.Number)
	if err := s.board.SetFailed(ctx, n, api.FailedStart, "the branch is gone"); err != nil {
		t.Fatal(err)
	}
	dev := liveSession(t, s, api.RoleDeveloper)
	wantError(t, socketDo(t, s, "POST", tp(tk.Number)+"/retry", empty, dev.ID), http.StatusForbidden, api.CodeForbidden)
	browserError(t, asUser(b, s, "POST", tp(tk.Number)+"/retry", nil), http.StatusBadRequest, api.CodeBadRequest)
	resp := asUser(b, s, "POST", tp(tk.Number)+"/retry", empty)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := body[api.Ticket](t, resp); got.FailedStep != api.FailedNone || got.Number != tk.Number {
		t.Fatalf("after retry: failed step %q", got.FailedStep)
	}
	// Nothing to retry any more.
	rec := socketDo(t, s, "POST", tp(tk.Number)+"/retry", empty, "")
	wantError(t, rec, http.StatusConflict, api.CodeInvalidTransition)
}

func TestStopAllRoute(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	b := newBrowser(t, s)
	_, dev := startDeveloper(t, s, p)
	resp := asUser(b, s, "POST", "/api/ceo", empty)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	ceo := body[api.AgentSession](t, resp)

	// Agents may not stop everybody.
	wantError(t, socketDo(t, s, "POST", "/api/agents/stop-all", empty, dev.ID), http.StatusForbidden, api.CodeForbidden)
	wantError(t, socketDo(t, s, "POST", "/api/agents/stop-all", empty, ceo.ID), http.StatusForbidden, api.CodeForbidden)

	resp = asUser(b, s, "POST", "/api/agents/stop-all", empty)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if info := body[api.ServerInfo](t, resp); !info.Paused {
		t.Fatal("not paused after stop-all")
	}
	if got := sessionByID(t, s, dev.ID); got.State != api.SessionEnded {
		t.Fatalf("developer %s after stop-all", got.State)
	}
	if !r.agent(t, dev.ID).isOver() {
		t.Fatal("the developer's agent still runs")
	}
	if got := sessionByID(t, s, ceo.ID); got.State == api.SessionEnded || r.agent(t, ceo.ID).isOver() {
		t.Fatal("stop-all ended the CEO")
	}
}

func TestCEORoutes(t *testing.T) {
	r := &testRunner{}
	s, _ := engineServer(t, r, fakeProfile(), true)
	b := newBrowser(t, s)
	resp := asUser(b, s, "POST", "/api/ceo", empty)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	first := body[api.AgentSession](t, resp)
	if first.Role != api.RoleCEO || first.ID == "" || first.State == api.SessionEnded {
		t.Fatalf("CEO %+v", first)
	}
	if want := r.agent(t, first.ID).termID; first.TerminalID != want {
		t.Fatalf("terminal id %q, want %q", first.TerminalID, want)
	}
	again := body[api.AgentSession](t, asUser(b, s, "POST", "/api/ceo", empty))
	if again.ID != first.ID {
		t.Fatal("a second open started another CEO")
	}
	// The socket's user may open it too; agents, the CEO included, may not.
	wantStatus(t, socketDo(t, s, "POST", "/api/ceo", empty, "").Code, http.StatusOK)
	for _, path := range []string{"/api/ceo", "/api/ceo/new-session", "/api/ceo/stop"} {
		wantError(t, socketDo(t, s, "POST", path, empty, first.ID), http.StatusForbidden, api.CodeForbidden)
	}
	wantError(t, socketDo(t, s, "GET", "/api/ceo", nil, first.ID), http.StatusForbidden, api.CodeForbidden)
	browserError(t, asUser(b, s, "POST", "/api/ceo", nil), http.StatusBadRequest, api.CodeBadRequest)

	resp = asUser(b, s, "POST", "/api/ceo/new-session", empty)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	fresh := body[api.AgentSession](t, resp)
	if fresh.ID == first.ID || fresh.State == api.SessionEnded {
		t.Fatalf("new session %+v", fresh)
	}
	if old := sessionByID(t, s, first.ID); old.State != api.SessionEnded {
		t.Fatalf("old CEO %s", old.State)
	}
	if !r.agent(t, first.ID).isOver() {
		t.Fatal("the old CEO's agent still runs")
	}

	// Stop ends it and leaves it stopped until the user starts it again.
	if st := body[api.CEOState](t, asUser(b, s, "GET", "/api/ceo", nil)); st.Stopped {
		t.Fatal("stopped before a stop")
	}
	browserError(t, asUser(b, s, "POST", "/api/ceo/stop", nil), http.StatusBadRequest, api.CodeBadRequest)
	resp = asUser(b, s, "POST", "/api/ceo/stop", empty)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if st := body[api.CEOState](t, resp); !st.Stopped {
		t.Fatal("the stop's answer is not stopped")
	}
	if got := sessionByID(t, s, fresh.ID); got.State != api.SessionEnded {
		t.Fatalf("stopped CEO %s", got.State)
	}
	if st := body[api.CEOState](t, asUser(b, s, "GET", "/api/ceo", nil)); !st.Stopped {
		t.Fatal("not stopped after a stop")
	}
	browserError(t, asUser(b, s, "POST", "/api/ceo/stop", empty), http.StatusConflict, api.CodeConflict)
	fresh = body[api.AgentSession](t, asUser(b, s, "POST", "/api/ceo", empty))
	if st := body[api.CEOState](t, asUser(b, s, "GET", "/api/ceo", nil)); st.Stopped {
		t.Fatal("still stopped after the user's start")
	}

	// Switched off, the CEO does not open.
	g := s.settings.General()
	g.CEO.Enabled = false
	if err := s.settings.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the CEO to end", func() bool { return sessionByID(t, s, fresh.ID).State == api.SessionEnded })
	browserError(t, asUser(b, s, "POST", "/api/ceo", empty), http.StatusUnprocessableEntity, api.CodeValidation)
}

func TestSchedulerRouteScopes(t *testing.T) {
	// The engine is built but not run, so sessions created here stay live.
	s, _ := engineServer(t, &testRunner{}, fakeProfile(), false)
	b := newBrowser(t, s)
	wantStatus(t, asUser(b, s, "GET", "/api/scheduler", nil).StatusCode, http.StatusOK)
	wantStatus(t, socketDo(t, s, "GET", "/api/scheduler", nil, "").Code, http.StatusOK)
	for role, want := range map[api.Role]int{
		api.RoleCEO: http.StatusOK, api.RolePM: http.StatusOK,
		api.RoleDeveloper: http.StatusForbidden, api.RoleReviewer: http.StatusForbidden,
	} {
		sess := liveSession(t, s, role)
		rec := socketDo(t, s, "GET", "/api/scheduler", nil, sess.ID)
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d: %s", role, rec.Code, want, rec.Body.Bytes())
		}
		if want == http.StatusOK {
			if raw := rec.Body.String(); !strings.Contains(raw, `"tasks":{"in_work":`) || !strings.Contains(raw, `"stories":{"in_work":`) || !strings.Contains(raw, `"paused"`) {
				t.Errorf("%s: body %s", role, raw)
			}
		}
	}
}

func TestCEOWaitsForEngineStart(t *testing.T) {
	// An engine that does not run yet: opening the CEO is refused instead
	// of starting one that the engine's start would mistake for a leftover.
	s, _ := engineServer(t, &testRunner{}, fakeProfile(), false)
	wantError(t, socketDo(t, s, "POST", "/api/ceo", empty, ""), http.StatusServiceUnavailable, api.CodeUnavailable)
	s.StartEngine()
	wantStatus(t, socketDo(t, s, "POST", "/api/ceo", empty, "").Code, http.StatusOK)
}

func TestCancelDiscardUncommittedOverSocketIsForbidden(t *testing.T) {
	s := newTestServer(t)
	dev := liveSession(t, s, api.RoleDeveloper)
	req := api.CancelRequest{DeleteBranch: true, DiscardUncommitted: true}
	wantError(t, socketDo(t, s, "POST", tp(int(*dev.Ticket))+"/cancel", req, dev.ID), http.StatusForbidden, api.CodeForbidden)
	pm := liveSession(t, s, api.RolePM)
	wantError(t, socketDo(t, s, "POST", tp(int(*pm.Ticket))+"/cancel", req, pm.ID), http.StatusForbidden, api.CodeForbidden)
	// The user may, with delete_branch only.
	wantError(t, socketDo(t, s, "POST", tp(int(*dev.Ticket))+"/cancel", api.CancelRequest{DiscardUncommitted: true}, ""),
		http.StatusUnprocessableEntity, api.CodeValidation)
	wantStatus(t, socketDo(t, s, "POST", tp(int(*dev.Ticket))+"/cancel", req, "").Code, http.StatusOK)
}

// dialTerminal opens an agent terminal through the browser listener.
func dialTerminal(t *testing.T, b *browser, s *Server, id string, protocols []string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(b.srv.URL, "http")+"/api/agents/"+id+"/terminal", &websocket.DialOptions{
		Host: hostOmoLocalhost + ":" + b.port, Subprotocols: protocols, HTTPHeader: header,
	})
}

// termOutput collects what a terminal connection received.
type termOutput struct {
	t   *testing.T
	c   *websocket.Conn
	got strings.Builder
}

// until reads terminal output until everything received so far contains
// want.
func (o *termOutput) until(want string) {
	o.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for !strings.Contains(o.got.String(), want) {
		kind, data, err := o.c.Read(ctx)
		if err != nil {
			o.t.Fatalf("read the terminal: %v (got %q)", err, o.got.String())
		}
		if kind == websocket.MessageBinary {
			o.got.Write(data)
		}
	}
}

func TestAgentTerminalWebsocket(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	b := newBrowser(t, s)
	n, dev := startDeveloper(t, s, p)
	agent := r.agent(t, dev.ID)
	agent.say("hello from the agent")

	// The user's list names the terminal; an agent's never does.
	resp := asUser(b, s, "GET", "/api/agents?live=true", nil)
	var termID string
	for _, sess := range body[[]api.AgentSession](t, resp) {
		if sess.ID == dev.ID {
			termID = sess.TerminalID
		}
	}
	if termID != agent.termID {
		t.Fatalf("listed terminal id %q, want %q", termID, agent.termID)
	}
	resp = asUser(b, s, "GET", tp(n)+"/agents", nil)
	if raw, _ := io.ReadAll(resp.Body); !bytes.Contains(raw, []byte(termID)) {
		t.Fatalf("ticket agents lack the terminal id: %s", raw)
	}
	if rec := socketDo(t, s, "GET", tp(n)+"/agents", nil, dev.ID); bytes.Contains(rec.Body.Bytes(), []byte(termID)) ||
		bytes.Contains(rec.Body.Bytes(), []byte("terminal_id")) {
		t.Fatalf("an agent sees a terminal id: %s", rec.Body.Bytes())
	}

	// An agent may open no terminal, not even its own.
	wantError(t, socketDo(t, s, "GET", "/api/agents/"+termID+"/terminal", nil, dev.ID), http.StatusForbidden, api.CodeForbidden)
	// The browser rules apply: login token, Host and Origin.
	if _, resp, err := dialTerminal(t, b, s, termID, []string{"omo"}, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without the token: %v %v", err, resp)
	}
	evil := http.Header{"Origin": {"http://evil.example"}}
	if _, resp, err := dialTerminal(t, b, s, termID, []string{"omo", webSocketTokenPrefix + s.token}, evil); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("from another origin: %v %v", err, resp)
	}
	// A session id or an unknown id names no terminal.
	for _, id := range []string{dev.ID, "nope"} {
		if _, resp, err := dialTerminal(t, b, s, id, []string{"omo", webSocketTokenPrefix + s.token}, nil); err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
			t.Fatalf("terminal %q: %v %v", id, err, resp)
		}
	}

	origin := http.Header{"Origin": {"http://" + hostOmoLocalhost + ":" + b.port}}
	c, resp, err := dialTerminal(t, b, s, termID, []string{"omo", webSocketTokenPrefix + s.token}, origin)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if c.Subprotocol() != "omo" {
		t.Fatalf("subprotocol %q", c.Subprotocol())
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			if strings.Contains(v, s.token) {
				t.Fatalf("response header %s echoes the login token", k)
			}
		}
	}
	(&termOutput{t: t, c: c}).until("hello from the agent")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, []byte("typed by the user\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the input to reach the agent", func() bool { return strings.Contains(agent.proc.typed(), "typed by the user") })

	// The connection ends when the agent ends.
	if _, err := s.board.Cancel(context.Background(), board.User, int64(n), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	for {
		if _, _, err := c.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the terminal stayed open after the agent ended")
			}
			break
		}
	}
}

// TestEngineRoutesNeverShowSessionIDsToAgents calls every route an agent may
// call that the engine answers and fails if a session id appears.
func TestEngineRoutesNeverShowSessionIDsToAgents(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	n, dev := startDeveloper(t, s, p)
	ceo := body[api.AgentSession](t, asUser(newBrowser(t, s), s, "POST", "/api/ceo", empty))
	ids := []string{dev.ID, ceo.ID}
	var bodies [][]byte
	for _, who := range []string{dev.ID, ceo.ID} {
		bodies = append(bodies,
			socketDo(t, s, "POST", "/api/ai/ready", empty, who).Body.Bytes(),
			socketDo(t, s, "GET", "/api/scheduler", nil, who).Body.Bytes(),
			socketDo(t, s, "POST", "/api/ai/wait", map[string]string{"timeout": "25h"}, who).Body.Bytes(),
			socketDo(t, s, "POST", "/api/ceo", empty, who).Body.Bytes(),
			socketDo(t, s, "POST", "/api/agents/stop-all", empty, who).Body.Bytes(),
			socketDo(t, s, "GET", "/api/agents/x/terminal", nil, who).Body.Bytes(),
			socketDo(t, s, "POST", tp(n)+"/retry", empty, who).Body.Bytes(),
			socketDo(t, s, "GET", tp(n)+"/agents", nil, who).Body.Bytes(),
		)
	}
	// A wait that a mention ends.
	answer := make(chan []byte, 1)
	go func() {
		answer <- socketCtx(t, context.Background(), s, "POST", "/api/ai/wait", map[string]string{"timeout": "1h"}, dev.ID).Body.Bytes()
	}()
	waitFor(t, "the developer to park", func() bool { return sessionByID(t, s, dev.ID).State == api.SessionWaiting })
	if _, err := s.board.Comment(context.Background(), board.User, int64(n), api.CommentRequest{Body: "@developer look"}); err != nil {
		t.Fatal(err)
	}
	bodies = append(bodies, <-answer)
	// The hand-in guard's refusal.
	if err := os.WriteFile(filepath.Join(dev.Workdir, "stray.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	bodies = append(bodies, socketDo(t, s, "POST", "/api/ai/done", empty, dev.ID).Body.Bytes())
	for _, raw := range bodies {
		for _, id := range ids {
			if bytes.Contains(raw, []byte(id)) {
				t.Errorf("an agent sees a session id: %s", raw)
			}
		}
	}
}

// TestCloseEndsAgentsAtOnce closes a server whose agents run: every agent is
// killed without a handoff and every session ended.
func TestCloseEndsAgentsAtOnce(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	_, dev := startDeveloper(t, s, p)
	g := s.settings.General()
	g.Stop.HandoffTimeout = api.Duration(time.Hour)
	if err := s.settings.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Minute):
		t.Fatal("Close did not return")
	}
	for _, a := range r.all() {
		if !a.isOver() {
			t.Fatal("an agent outlived Close")
		}
	}
	a := r.agent(t, dev.ID)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.lines) != 0 {
		t.Fatalf("Close asked for a handoff: %q", a.lines)
	}
}

// TestSettingsChangeWakesEngine raises the task limit: the engine starts
// the waiting ticket at once, without any board change and long before its
// periodic health check.
func TestSettingsChangeWakesEngine(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), false)
	g := s.settings.General()
	g.Limits.Tasks = 1
	if err := s.settings.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	s.StartEngine()
	first, _ := startDeveloper(t, s, p)
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "second", Projects: []string{p}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Move(ctx, board.User, int64(tk.Number), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second ticket to wait for a slot", func() bool {
		got, err := s.board.Get(ctx, board.User, int64(tk.Number))
		return err == nil && got.StartBlocked == api.StartBlockedSlot
	})
	g.Limits.Tasks = 2
	b := newBrowser(t, s)
	wantStatus(t, asUser(b, s, "PUT", "/api/settings", api.General(g)).StatusCode, http.StatusOK)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ok := liveOf(t, s, api.RoleDeveloper, tk.Number); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the raised limit did not start the second ticket")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := liveOf(t, s, api.RoleDeveloper, first); !ok {
		t.Fatal("the first developer ended")
	}
}

// A hand-in whose client leaves once the request is sent (the agent is killed
// at the handoff deadline) is still recorded.
func TestDoneRouteSurvivesClientLeaving(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	n, dev := startDeveloper(t, s, p)
	gone, leave := context.WithCancel(context.Background())
	s.beforeHandIn = leave // the client leaves while the request runs
	socketCtx(t, gone, s, "POST", "/api/ai/done", api.DoneRequest{Comment: "finished"}, dev.ID)
	tk, err := s.board.Get(context.Background(), board.User, int64(n))
	if err != nil {
		t.Fatal(err)
	}
	if tk.Column != api.ColumnReview {
		t.Fatalf("the hand-in of a client that left was lost: ticket is in %s", tk.Column)
	}
}

// Stopping everything is not left half done when the client leaves.
func TestStopAllSurvivesClientLeaving(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	_, dev := startDeveloper(t, s, p)
	gone, leave := context.WithCancel(context.Background())
	leave()
	req := newRequest(t, "POST", "http://omo/api/agents/stop-all", struct{}{}).WithContext(gone)
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	if paused, err := s.board.Paused(context.Background()); err != nil || !paused {
		t.Fatalf("stop-all of a client that left was dropped (paused %v, err %v, answer %d)", paused, err, rec.Code)
	}
	waitFor(t, "the developer to be ended", func() bool { return sessionByID(t, s, dev.ID).State != api.SessionRunning })
}

// Switching origin_check off does not open the terminal to other sites: the
// websocket handshake still refuses an Origin that is not the request's Host.
func TestTerminalForeignOriginRefusedWithoutOriginCheck(t *testing.T) {
	r := &testRunner{}
	s, p := engineServerWith(t, r, fakeProfile(), true, func(c *serverconfig.Config) { c.OriginCheck = false })
	b := newBrowser(t, s)
	_, dev := startDeveloper(t, s, p)
	termID := r.agent(t, dev.ID).termID
	protocols := []string{"omo", webSocketTokenPrefix + s.token}
	evil := http.Header{"Origin": {"http://evil.example"}}
	c, resp, err := dialTerminal(t, b, s, termID, protocols, evil)
	if err == nil {
		c.CloseNow()
		t.Fatal("a foreign Origin opened the terminal with origin_check off")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Origin: %v %v, want 403", err, resp)
	}
	// The same dial with the server's own origin opens.
	own := http.Header{"Origin": {"http://" + hostOmoLocalhost + ":" + b.port}}
	c, _, err = dialTerminal(t, b, s, termID, protocols, own)
	if err != nil {
		t.Fatalf("own origin: %v", err)
	}
	c.CloseNow()
}
