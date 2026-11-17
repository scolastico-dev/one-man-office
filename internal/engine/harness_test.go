package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/fakeagent"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/gittest"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/webterm"
)

// TestMain lets the test binary run as the scripted fake agent when a test
// starts it as an agent process.
func TestMain(m *testing.M) {
	if fakeagent.Enabled() {
		os.Exit(fakeagent.Main())
	}
	gittest.Isolate()
	os.Exit(m.Run())
}

// ---- fake runner and agent ----

// fakeRunner records the specs it is asked to start and returns fakeAgents.
type fakeRunner struct {
	mu     sync.Mutex
	specs  []agents.Spec
	agents []*fakeAgent
	// err, when set, is returned by Start instead of an agent.
	err error
	// block, when set, makes Start wait until it is closed.
	block chan struct{}
	// entered receives every spec Start is called with (if not nil).
	entered chan agents.Spec
}

func (r *fakeRunner) Start(spec agents.Spec) (agents.Agent, error) {
	r.mu.Lock()
	r.specs = append(r.specs, spec)
	block, entered, err := r.block, r.entered, r.err
	r.mu.Unlock()
	if entered != nil {
		entered <- spec
	}
	if block != nil {
		<-block
	}
	if err != nil {
		return nil, err
	}
	a := newFakeAgent(spec.SessionID)
	r.mu.Lock()
	r.agents = append(r.agents, a)
	r.mu.Unlock()
	return a, nil
}

func (r *fakeRunner) setErr(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

func (r *fakeRunner) all() []*fakeAgent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*fakeAgent(nil), r.agents...)
}

func (r *fakeRunner) specList() []agents.Spec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agents.Spec(nil), r.specs...)
}

// fakeAgent is an agent without a process.
type fakeAgent struct {
	id     string
	exited chan struct{}
	once   sync.Once

	mu      sync.Mutex
	exitErr error
	lines   []string
	prompts []string
	killed  bool
	last    time.Time
}

func newFakeAgent(id string) *fakeAgent {
	return &fakeAgent{id: id, exited: make(chan struct{}), last: time.Now()}
}

// exit ends the fake process with err (nil = exit 0).
func (a *fakeAgent) exit(err error) {
	a.once.Do(func() {
		a.mu.Lock()
		a.exitErr = err
		a.mu.Unlock()
		close(a.exited)
	})
}

func (a *fakeAgent) isKilled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.killed
}

func (a *fakeAgent) isOver() bool {
	select {
	case <-a.exited:
		return true
	default:
		return false
	}
}

func (a *fakeAgent) SessionID() string           { return a.id }
func (a *fakeAgent) TerminalID() string          { return "term-" + fmt.Sprint(len(a.id)) + a.id[:4] }
func (a *fakeAgent) Exited() <-chan struct{}     { return a.exited }
func (a *fakeAgent) Done() <-chan struct{}       { return a.exited }
func (a *fakeAgent) Terminal() *webterm.Terminal { return nil }
func (a *fakeAgent) Warnings() []string          { return nil }
func (a *fakeAgent) Tail(n int) []string         { return []string{"fake output line"} }
func (a *fakeAgent) ExitErr() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exitErr
}
func (a *fakeAgent) SendPrompt(text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prompts = append(a.prompts, text)
	return nil
}
func (a *fakeAgent) Notify(line string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lines = append(a.lines, line)
}
func (a *fakeAgent) Kill() error {
	a.mu.Lock()
	if !a.isOverLocked() {
		a.killed = true
	}
	a.mu.Unlock()
	a.exit(errors.New("signal: killed"))
	return nil
}
func (a *fakeAgent) isOverLocked() bool {
	select {
	case <-a.exited:
		return true
	default:
		return false
	}
}
func (a *fakeAgent) LastOutput() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last
}

// ---- clock ----

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// ---- board environment ----

// testEnv gives the board the settings and git, like the server does.
type testEnv struct {
	st  *settings.Store
	git *gitops.Git
}

func (e testEnv) Policy() board.Policy {
	g := e.st.General()
	return board.Policy{
		CEOEnabled:                 g.CEO.Enabled,
		CEOMayApproveStories:       g.CEO.MayApproveStories,
		CEOTicketsNeedUserApproval: g.CEO.TicketsNeedUserApproval,
		TasksRequireUserRelease:    g.Tasks.RequireUserRelease,
		ReviewStory:                g.ReviewDefaults.Story,
		ReviewTask:                 g.ReviewDefaults.Task,
		ReviewStoryTask:            g.ReviewDefaults.StoryTask,
		WhenApprovedStory:          g.WhenApprovedDefaults.Story,
		WhenApprovedTask:           g.WhenApprovedDefaults.Task,
	}
}

func (e testEnv) Project(id string) (board.ProjectInfo, bool) {
	p, ok := e.st.Project(id)
	if !ok {
		return board.ProjectInfo{}, false
	}
	return board.ProjectInfo{ID: p.ID, Path: p.Path, Enabled: p.Enabled, Missing: p.Missing}, true
}

func (e testEnv) Model(id string) (board.ModelInfo, bool) {
	m, ok := e.st.Model(id)
	if !ok {
		return board.ModelInfo{}, false
	}
	return board.ModelInfo{ID: m.ID, Selectable: m.Selectable}, true
}

func (e testEnv) CurrentBranch(path string) (string, error) { return e.git.CurrentBranch(path) }

// ---- harness ----

type harnessConfig struct {
	limit int
	// runner, when set, replaces the fake runner.
	runner agents.Runner
	// profile, when set, replaces the fake profile.
	profile *settings.Profile
	// projects is the number of git repositories registered as projects.
	projects int
	// mcp is the engine's MCP settings; nil is the engine's default.
	mcp func() api.MCPSettings
}

func withLimit(n int) func(*harnessConfig)            { return func(c *harnessConfig) { c.limit = n } }
func withRunner(r agents.Runner) func(*harnessConfig) { return func(c *harnessConfig) { c.runner = r } }
func withProfile(p settings.Profile) func(*harnessConfig) {
	return func(c *harnessConfig) { c.profile = &p }
}
func withProjects(n int) func(*harnessConfig) {
	return func(c *harnessConfig) { c.projects = n }
}
func withMCP(f func() api.MCPSettings) func(*harnessConfig) {
	return func(c *harnessConfig) { c.mcp = f }
}

type harness struct {
	t      *testing.T
	ctx    context.Context
	layout globalhome.Layout
	st     *settings.Store
	b      *board.Board
	git    *gitops.Git
	e      *Engine
	runner *fakeRunner
	clock  *testClock
	logs   *syncBuffer
	// project is the first project's id, repo its folder.
	project string
	repo    string
	// repos maps every project id to its folder.
	repos map[string]string
	ids   []string
	emits []string
	mu    sync.Mutex
}

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

// isolate keeps every test away from the developer's home, git and agent
// configuration.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMO_HOME", filepath.Join(home, "omo"))
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	return home
}

// gitRun runs git in dir and fails the test on error.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo creates a git repository with one commit on main.
func newRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "a.txt")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func newHarness(t *testing.T, opts ...func(*harnessConfig)) *harness {
	t.Helper()
	cfg := harnessConfig{limit: 4, projects: 1}
	for _, o := range opts {
		o(&cfg)
	}
	home := isolate(t)
	layout, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ctx: context.Background(), layout: layout, clock: &testClock{t: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)},
		logs: &syncBuffer{}, repos: map[string]string{}, git: gitops.New()}
	h.st, err = settings.Open(layout.Root, settings.Options{PromptDefaults: prompts.Defaults()})
	if err != nil {
		t.Fatal(err)
	}
	g := h.st.General()
	g.Limits = api.LimitSettings{Tasks: cfg.limit, Stories: cfg.limit}
	g.AgentEnv = map[string]string{"GIT_AUTHOR_NAME": "Agent", "GIT_AUTHOR_EMAIL": "agent@example.invalid"}
	if err := h.st.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	prof := settings.Profile{Name: "fake", CLI: api.CLICustom, Command: "fake-agent",
		Models: map[string]settings.Model{"a": {}, "b": {}}}
	if cfg.profile != nil {
		prof = *cfg.profile
	}
	if err := h.st.PutProfile(prof); err != nil {
		t.Fatal(err)
	}
	h.setRoleModels("fake/a", "fake/b")
	for i := 0; i < cfg.projects; i++ {
		name := "app"
		if i > 0 {
			name = fmt.Sprintf("app%d", i+1)
		}
		repo := newRepo(t, filepath.Join(home, "src", name))
		p, err := h.st.AddProject(repo, name)
		if err != nil {
			t.Fatal(err)
		}
		h.repos[p.ID] = repo
		h.ids = append(h.ids, p.ID)
		if i == 0 {
			h.project, h.repo = p.ID, repo
		}
	}
	d, err := db.Open(layout.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	h.b = board.New(d, testEnv{st: h.st, git: h.git}, board.Options{Notify: func() {
		if e := h.engine(); e != nil {
			e.Wake()
		}
	}})
	runner := cfg.runner
	if runner == nil {
		h.runner = &fakeRunner{}
		runner = h.runner
	}
	e := New(Options{
		Board: h.b, Settings: h.st, Git: h.git, Layout: layout, Runner: runner,
		Socket: func() string { return "" },
		Emit: func(kind string, data any) error {
			h.mu.Lock()
			h.emits = append(h.emits, kind)
			h.mu.Unlock()
			return nil
		},
		Log: log.New(h.logs, "", 0), Now: h.clock.Now,
		MCP: cfg.mcp,
	})
	// Tests let waits time out after milliseconds; TestWaitMinimum checks
	// the real minimum.
	e.minWait = 0
	h.mu.Lock()
	h.e = e
	h.mu.Unlock()
	t.Cleanup(func() {
		e.halt(context.Background(), "test over")
		if h.runner != nil {
			for _, a := range h.runner.all() {
				if !a.isOver() {
					t.Errorf("an agent is still running after the test")
				}
			}
		}
		if t.Failed() {
			t.Logf("engine log:\n%s", h.logs.String())
		}
	})
	return h
}

func (h *harness) engine() *Engine {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.e
}

// setRoleModels assigns models to every agent role, round robin.
func (h *harness) setRoleModels(models ...string) {
	h.t.Helper()
	for _, r := range settings.RoleNames {
		if err := h.st.PutRole(settings.Role{Name: r, Models: models, Assignment: api.AssignmentRoundRobin}); err != nil {
			h.t.Fatal(err)
		}
	}
}

// setLimit sets both limits, limits.tasks and limits.stories, to n.
func (h *harness) setLimit(n int) {
	h.t.Helper()
	g := h.st.General()
	g.Limits = api.LimitSettings{Tasks: n, Stories: n}
	if err := h.st.SetGeneral(g); err != nil {
		h.t.Fatal(err)
	}
}

// step runs one pass and waits for its workers.
func (h *harness) step() {
	h.e.pass(h.ctx)
	h.e.settle()
}

// story creates a story in Planning.
func (h *harness) story(title string, projects ...string) int64 {
	h.t.Helper()
	if len(projects) == 0 {
		projects = []string{h.project}
	}
	tk, err := h.b.Create(h.ctx, board.User, api.CreateTicket{Type: api.TypeStory, Title: title, Projects: projects})
	if err != nil {
		h.t.Fatal(err)
	}
	return int64(tk.Number)
}

// standalone creates a standalone task in Planning.
func (h *harness) standalone(title string) int64 {
	h.t.Helper()
	tk, err := h.b.Create(h.ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: title, Projects: []string{h.project}})
	if err != nil {
		h.t.Fatal(err)
	}
	return int64(tk.Number)
}

// task creates a task in story (as the user) in Planning.
func (h *harness) task(story int64, title string, deps ...int64) int64 {
	h.t.Helper()
	return h.taskAs(board.User, story, title, deps...)
}

func (h *harness) taskAs(a board.Actor, story int64, title string, deps ...int64) int64 {
	h.t.Helper()
	s := int(story)
	req := api.CreateTicket{Type: api.TypeTask, Title: title, Projects: []string{h.project}, Story: &s}
	for _, d := range deps {
		req.DependsOn = append(req.DependsOn, int(d))
	}
	tk, err := h.b.Create(h.ctx, a, req)
	if err != nil {
		h.t.Fatal(err)
	}
	return int64(tk.Number)
}

// approve gives a ticket in Planning the green light (as the user).
func (h *harness) approve(n int64) {
	h.t.Helper()
	h.approveAs(board.User, n)
}

func (h *harness) approveAs(a board.Actor, n int64) {
	h.t.Helper()
	if _, err := h.b.Approve(h.ctx, a, n, api.ApproveRequest{}); err != nil {
		h.t.Fatal(err)
	}
}

// actor resolves a session to its board actor.
func (h *harness) actor(sessionID string) board.Actor {
	h.t.Helper()
	a, err := h.b.ResolveSession(h.ctx, sessionID)
	if err != nil {
		h.t.Fatal(err)
	}
	return a
}

// snap reads the engine snapshot.
func (h *harness) snap() board.EngineSnapshot {
	h.t.Helper()
	s, err := h.b.EngineSnapshot(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

// live returns the live session of role on ticket; it fails when there is
// none.
func (h *harness) live(role api.Role, ticket int64) board.EngineSession {
	h.t.Helper()
	s, ok := h.liveOK(role, ticket)
	if !ok {
		h.t.Fatalf("no live %s session on #%d", role, ticket)
	}
	return s
}

func (h *harness) liveOK(role api.Role, ticket int64) (board.EngineSession, bool) {
	for _, s := range h.snap().Sessions {
		if s.Role == role && s.Ticket == ticket {
			return s, true
		}
	}
	return board.EngineSession{}, false
}

// liveCount counts the live sessions.
func (h *harness) liveCount() int { return len(h.snap().Sessions) }

// ticket reads a ticket as the user.
func (h *harness) ticket(n int64) api.Ticket {
	h.t.Helper()
	tk, err := h.b.Get(h.ctx, board.User, n)
	if err != nil {
		h.t.Fatal(err)
	}
	return tk
}

// agent returns the fake agent of a session.
func (h *harness) agent(sessionID string) *fakeAgent {
	h.t.Helper()
	for _, a := range h.runner.all() {
		if a.id == sessionID {
			return a
		}
	}
	h.t.Fatalf("no fake agent for the session")
	return nil
}

// sessions lists every session of a ticket, newest first.
func (h *harness) sessions(n int64) []api.AgentSession {
	h.t.Helper()
	all, err := h.b.AgentSessions(h.ctx, board.User, false)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []api.AgentSession
	for _, s := range all {
		if s.Ticket != nil && int64(*s.Ticket) == n {
			out = append(out, s)
		}
	}
	return out
}

// comments returns the bodies of a ticket's comments.
func (h *harness) comments(n int64) []string {
	h.t.Helper()
	cs, err := h.b.Comments(h.ctx, board.User, n)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		out = append(out, c.Body)
	}
	return out
}

// waitFor polls cond until it holds or the deadline passes.
func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitEnded waits until a session has ended.
func (h *harness) waitEnded(id string) api.AgentSession {
	h.t.Helper()
	var out api.AgentSession
	h.waitFor("the session to end", func() bool {
		all, err := h.b.AgentSessions(h.ctx, board.User, false)
		if err != nil {
			h.t.Fatal(err)
		}
		for _, s := range all {
			if s.ID == id && s.State == api.SessionEnded {
				out = s
				return true
			}
		}
		return false
	})
	return out
}

// branches lists the omo branches of a repository.
func (h *harness) branches(repo string) []string {
	h.t.Helper()
	out := gitRun(h.t, repo, "for-each-ref", "--format=%(refname:short)", "refs/heads/omo/")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runEngine runs the engine's loop until the test ends.
func runEngine(t *testing.T, h *harness) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.e.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}
