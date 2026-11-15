package e2e

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/fakeagent"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/server"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// fakeModel is the only model of the fake profile; every role uses it.
const fakeModel = "fake/default"

// config sets up a world. The zero value is omo's defaults with the fake
// agent's built-in scripts.
type config struct {
	// maxRunning is max_running_agents (0: omo's default).
	maxRunning int
	// handoff is stop.handoff_timeout (0: 30 s).
	handoff time.Duration
	// scripts replaces the fake agent's script of a role ("developer", "pm",
	// "reviewer", "export", "ceo").
	scripts map[string]string
	// delivery is the fake profile's prompt delivery; nil gives the start
	// prompt only through omo ai ready (inject off).
	delivery *api.PromptDelivery
}

// world is one omo home with one project (a repository with a commit on
// main, checked out) and a fake profile assigned to every role, and the
// server currently running on it.
type world struct {
	t       *testing.T
	layout  globalhome.Layout
	repo    string
	project string
	runner  *recordingRunner

	// The running server; nil between stop and start.
	srv     *server.Server
	user    *apiclient.Client
	servers []*http.Server
}

// homeMu serializes opening a home: the layout comes from OMO_HOME, which
// is process-wide.
var homeMu sync.Mutex

// newWorld creates a fresh home and starts omo on it.
func newWorld(t *testing.T, c config) *world {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the end-to-end tests start agents in Unix terminals; Windows is only build-checked")
	}
	t.Parallel()
	homeMu.Lock()
	os.Setenv("OMO_HOME", t.TempDir())
	l, err := globalhome.Open()
	os.Unsetenv("OMO_HOME")
	homeMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, layout: l, repo: newRepo(t), runner: &recordingRunner{pty: agents.NewPTYRunner()}}
	st := w.openSettings()
	g := st.General()
	if c.maxRunning > 0 {
		g.MaxRunningAgents = c.maxRunning
	}
	g.Stop.HandoffTimeout = api.Duration(30 * time.Second)
	if c.handoff > 0 {
		g.Stop.HandoffTimeout = api.Duration(c.handoff)
	}
	g.AgentEnv = map[string]string{
		"GIT_AUTHOR_NAME": "Agent", "GIT_AUTHOR_EMAIL": "agent@example.invalid",
		"GIT_COMMITTER_NAME": "omo", "GIT_COMMITTER_EMAIL": "omo@example.invalid",
	}
	if err := st.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	w.setScripts(st, c)
	for _, r := range settings.RoleNames {
		if err := st.PutRole(settings.Role{Name: r, Models: []string{fakeModel}, Assignment: api.AssignmentRoundRobin}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := st.AddProject(w.repo, "app")
	if err != nil {
		t.Fatal(err)
	}
	w.project = p.ID
	t.Cleanup(w.cleanup)
	w.start()
	return w
}

// openSettings opens the home's settings files; use it only while no
// server runs on the home.
func (w *world) openSettings() *settings.Store {
	w.t.Helper()
	st, err := settings.Open(w.layout.Root, settings.Options{})
	if err != nil {
		w.t.Fatal(err)
	}
	return st
}

// setScripts writes the fake profile with the scripts of c.
func (w *world) setScripts(st *settings.Store, c config) {
	w.t.Helper()
	exe, err := fakeagent.Command()
	if err != nil {
		w.t.Fatal(err)
	}
	env := map[string]string{fakeagent.EnvEnable: "1", runMarker: runID}
	for role, script := range c.scripts {
		env[fakeagent.EnvScriptRolePrefix+strings.ToUpper(role)] = script
	}
	off := false
	delivery := api.PromptDelivery{Inject: &off}
	if c.delivery != nil {
		delivery = *c.delivery
	}
	if err := st.PutProfile(settings.Profile{Name: "fake", CLI: api.CLICustom, Command: exe, Env: env,
		PromptDelivery: delivery, Models: map[string]settings.Model{"default": {}}}); err != nil {
		w.t.Fatal(err)
	}
}

// start runs omo on the home the way the command does, from its parts: the
// server with an agent runner, the agent socket and the browser listener
// served, then the engine.
func (w *world) start() {
	w.t.Helper()
	cfg := serverconfig.Defaults()
	cfg.Port = 0
	srv, err := server.New(server.Options{Layout: w.layout, Config: cfg, Version: "e2e", Runner: w.runner})
	if err != nil {
		w.t.Fatal(err)
	}
	sock, err := srv.ListenSocket()
	if err != nil {
		srv.Close()
		w.t.Fatal(err)
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		sock.Close()
		srv.Close()
		w.t.Fatal(err)
	}
	local := &http.Server{Handler: srv.SocketHandler(), ReadHeaderTimeout: 5 * time.Second}
	browser := &http.Server{Handler: srv.BrowserHandler(), ReadHeaderTimeout: 5 * time.Second}
	go local.Serve(sock)
	go browser.Serve(tcp)
	w.srv, w.servers = srv, []*http.Server{local, browser}
	w.user = apiclient.New(srv.SocketEndpoint, "")
	srv.StartEngine()
}

// stop stops omo gracefully: the agents are asked for a handoff and given
// at most handoff for it while both listeners still serve, then everything
// is closed. It returns once every agent process of this server ended.
func (w *world) stop(handoff time.Duration) {
	w.t.Helper()
	if w.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), handoff)
	w.srv.Shutdown(ctx)
	cancel()
	for _, h := range w.servers {
		h.Close()
	}
	w.srv.Close()
	w.srv, w.servers, w.user = nil, nil, nil
	w.agentsEnded()
}

// cleanup stops the server at once and checks that no agent process is
// left.
func (w *world) cleanup() {
	w.stop(0)
}

// agentsEnded fails the test for every agent process that is still running
// some seconds after its server stopped, and kills it.
func (w *world) agentsEnded() {
	w.t.Helper()
	for _, a := range w.runner.all() {
		select {
		case <-a.Done():
		case <-time.After(15 * time.Second):
			w.t.Errorf("an agent process outlived its server")
			_ = a.Kill()
		}
	}
}

// recordingRunner starts real agent processes in terminals and remembers
// them.
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

// transcript returns the last lines of every agent started so far.
func (r *recordingRunner) transcript() string {
	var b strings.Builder
	for _, a := range r.all() {
		b.WriteString(strings.Join(a.Tail(25), "\n"))
		b.WriteString("\n----\n")
	}
	return b.String()
}

// ---- waiting for the board ----

// waitFor polls cond until it holds; after a deadline of two minutes the
// test fails with what and the agents' transcripts. It is a condition wait:
// nothing depends on how long anything takes.
func (w *world) waitFor(what string, cond func() bool) {
	w.t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			w.t.Fatalf("timed out waiting for %s\nagents:\n%s", what, w.runner.transcript())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// do calls the API as the user and fails the test on an error.
func (w *world) do(method, path string, in, out any) {
	w.t.Helper()
	if err := w.user.Do(context.Background(), method, path, in, out); err != nil {
		w.t.Fatalf("%s %s: %v", method, path, err)
	}
}

func (w *world) ticket(n int) api.Ticket {
	w.t.Helper()
	tk, err := w.user.Ticket(context.Background(), n)
	if err != nil {
		w.t.Fatal(err)
	}
	return tk
}

// create creates a ticket in the project.
func (w *world) create(req api.CreateTicket) api.Ticket {
	w.t.Helper()
	if len(req.Projects) == 0 {
		req.Projects = []string{w.project}
	}
	tk, err := w.user.CreateTicket(context.Background(), req)
	if err != nil {
		w.t.Fatal(err)
	}
	return tk
}

// approve moves a ticket from Planning to Ready.
func (w *world) approve(n int) {
	w.t.Helper()
	if _, err := w.user.Move(context.Background(), n, api.MoveRequest{Column: api.ColumnReady}); err != nil {
		w.t.Fatal(err)
	}
}

// task creates a standalone task and approves it.
func (w *world) task(title string, review api.Review, when api.WhenApproved) int {
	w.t.Helper()
	tk := w.create(api.CreateTicket{Type: api.TypeTask, Title: title, Review: review, WhenApproved: when})
	w.approve(tk.Number)
	return tk.Number
}

// waitColumn waits until ticket n is in column (and, for Review, at stage
// when stage is set).
func (w *world) waitColumn(n int, col api.Column, stage api.ReviewStage) api.Ticket {
	w.t.Helper()
	var tk api.Ticket
	w.waitFor("#"+itoa(n)+" in "+string(col)+" "+string(stage), func() bool {
		tk = w.ticket(n)
		return tk.Column == col && (stage == "" || tk.ReviewStage == stage)
	})
	return tk
}

// waitFailed waits until ticket n has the failed step.
func (w *world) waitFailed(n int, step api.FailedStep) api.Ticket {
	w.t.Helper()
	var tk api.Ticket
	w.waitFor("#"+itoa(n)+" to fail at "+string(step), func() bool {
		tk = w.ticket(n)
		return tk.FailedStep == step
	})
	return tk
}

// sessions returns every session of ticket n, in no particular order.
func (w *world) sessions(n int) []api.AgentSession {
	w.t.Helper()
	var ss []api.AgentSession
	w.do("GET", "/api/tickets/"+itoa(n)+"/agents", nil, &ss)
	return ss
}

// allSessions returns every session, live or ended.
func (w *world) allSessions() []api.AgentSession {
	w.t.Helper()
	var ss []api.AgentSession
	w.do("GET", "/api/agents", nil, &ss)
	return ss
}

// waitSession waits for a session of role on ticket n in state and returns
// it.
func (w *world) waitSession(n int, role api.Role, state api.SessionState) api.AgentSession {
	w.t.Helper()
	var found api.AgentSession
	w.waitFor("a "+string(role)+" of #"+itoa(n)+" "+string(state), func() bool {
		for _, s := range w.sessions(n) {
			if s.Role == role && s.State == state {
				found = s
				return true
			}
		}
		return false
	})
	return found
}

// comments returns the bodies of ticket n's comments, oldest first.
func (w *world) comments(n int) []string {
	w.t.Helper()
	cs, err := w.user.Comments(context.Background(), n)
	if err != nil {
		w.t.Fatal(err)
	}
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Body
	}
	return out
}

// hasComment reports whether a comment of ticket n contains text.
func (w *world) hasComment(n int, text string) bool {
	w.t.Helper()
	for _, c := range w.comments(n) {
		if strings.Contains(c, text) {
			return true
		}
	}
	return false
}

// noCrashes fails the test when a comment says an agent exited
// unexpectedly or could not start.
func (w *world) noCrashes(ns ...int) {
	w.t.Helper()
	for _, n := range ns {
		for _, c := range w.comments(n) {
			if strings.Contains(c, "exited unexpectedly") || strings.Contains(c, "could not start") {
				w.t.Errorf("#%d: %s\nagents:\n%s", n, c, w.runner.transcript())
			}
		}
	}
}

// settled checks what every finished ticket leaves behind: no live session,
// no worktree of the project, no worktree registered in the repository but
// the user's checkout, and no omo branch except those in keep.
func (w *world) settled(keep ...string) {
	w.t.Helper()
	w.waitFor("every session to end", func() bool {
		for _, s := range w.allSessions() {
			if s.State != api.SessionEnded {
				return false
			}
		}
		return true
	})
	w.waitFor("the worktrees to be removed", func() bool {
		entries, err := os.ReadDir(filepath.Join(w.layout.Worktrees, w.project))
		return (err != nil && errors.Is(err, os.ErrNotExist)) || (err == nil && len(entries) == 0)
	})
	if list := w.git("worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		w.t.Errorf("worktrees left registered:\n%s", list)
	}
	// Branches are deleted after their worktree is removed.
	var branches []string
	w.waitFor("the omo branches "+strings.Join(keep, " "), func() bool {
		branches = strings.Fields(w.git("for-each-ref", "--format=%(refname:short)", "refs/heads/omo"))
		return strings.Join(branches, " ") == strings.Join(keep, " ")
	})
}

// ---- git ----

// newRepo creates the user's repository: main with one commit, checked
// out.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "README.md")
	gitIn(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// git runs git in the user's repository.
func (w *world) git(args ...string) string {
	w.t.Helper()
	return gitIn(w.t, w.repo, args...)
}

// commitFile commits a file on the branch checked out in dir.
func commitFile(t *testing.T, dir, name, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-q", "-m", message)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// fileOn returns the content of a file on a revision, or "" with ok false.
func (w *world) fileOn(rev, name string) (string, bool) {
	w.t.Helper()
	cmd := exec.Command("git", "show", rev+":"+name)
	cmd.Dir = w.repo
	out, err := cmd.Output()
	return string(out), err == nil
}

// isAncestor reports whether a is an ancestor of b in the user's repository.
func (w *world) isAncestor(a, b string) bool {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", a, b)
	cmd.Dir = w.repo
	return cmd.Run() == nil
}

// subjects lists the subjects of rev's history, newest first.
func (w *world) subjects(rev string) []string {
	w.t.Helper()
	return strings.Split(w.git("log", "--format=%s", rev), "\n")
}

// commitWith returns the newest commit of rev's history whose subject is
// subject.
func (w *world) commitWith(rev, subject string) string {
	w.t.Helper()
	for _, line := range strings.Split(w.git("log", "--format=%H %s", rev), "\n") {
		id, s, _ := strings.Cut(line, " ")
		if s == subject {
			return id
		}
	}
	w.t.Fatalf("no commit %q on %s:\n%s", subject, rev, w.git("log", "--oneline", rev))
	return ""
}

// checkoutClean fails the test when the user's checkout is not on main or
// its files differ from main.
func (w *world) checkoutClean() {
	w.t.Helper()
	if b := w.git("branch", "--show-current"); b != "main" {
		w.t.Errorf("the user's checkout is on %q", b)
	}
	if st := w.git("status", "--porcelain"); st != "" {
		w.t.Errorf("the user's checkout differs from main:\n%s", st)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
