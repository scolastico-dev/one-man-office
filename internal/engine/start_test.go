package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/session"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

func TestApprovedStoryStartsPMWithIntegrationWorktree(t *testing.T) {
	h := newHarness(t)
	s := h.story("Add OAuth login!")
	h.approve(s)
	h.step()

	tk := h.ticket(s)
	if tk.Column != api.ColumnInProgress {
		t.Fatalf("story column = %s, want in_progress", tk.Column)
	}
	branch := "omo/add-oauth-login"
	if got := gitRun(t, h.repo, "rev-parse", branch); got != gitRun(t, h.repo, "rev-parse", "main") {
		t.Fatalf("%s is at %s, not at main", branch, got)
	}
	dir := WorktreeDir(h.layout.Worktrees, h.project, s)
	if got := gitRun(t, dir, "branch", "--show-current"); got != branch {
		t.Fatalf("worktree %s has %q checked out, want %s", dir, got, branch)
	}
	if p := tk.Projects[0]; p.Branch != branch || p.Worktree != dir {
		t.Fatalf("recorded branch %q worktree %q, want %q %q", p.Branch, p.Worktree, branch, dir)
	}
	specs := h.runner.specList()
	if len(specs) != 1 {
		t.Fatalf("%d agents started, want 1", len(specs))
	}
	pm := h.live(api.RolePM, s)
	if specs[0].Dir != dir || specs[0].SessionID != pm.ID {
		t.Fatalf("agent started in %q for another session; want %q", specs[0].Dir, dir)
	}
	if !strings.Contains(specs[0].StartPrompt, "omo ai ready") || strings.Contains(specs[0].StartPrompt, pm.ID) {
		t.Fatalf("start prompt %q", specs[0].StartPrompt)
	}
	if strings.Contains(specs[0].LogPath, pm.ID) || filepath.Dir(specs[0].LogPath) != h.layout.Logs {
		t.Fatalf("log path %q", specs[0].LogPath)
	}
	if tk.Busy {
		t.Fatal("the claim was not released")
	}
}

func TestReleasedTaskStartsDeveloperFromIntegrationBranch(t *testing.T) {
	h := newHarness(t)
	s := h.story("Story")
	h.approve(s)
	h.step()
	pm := h.live(api.RolePM, s)
	integration := WorktreeDir(h.layout.Worktrees, h.project, s)
	if err := os.WriteFile(filepath.Join(integration, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, integration, "add", "b.txt")
	gitRun(t, integration, "commit", "-q", "-m", "on the story")
	head := gitRun(t, integration, "rev-parse", "HEAD")

	pa := h.actor(pm.ID)
	task := h.taskAs(pa, s, "Write the docs")
	h.approveAs(pa, task)
	h.step()

	tk := h.ticket(task)
	if tk.Column != api.ColumnInProgress {
		t.Fatalf("task column = %s", tk.Column)
	}
	branch := "omo/write-the-docs"
	if got := gitRun(t, h.repo, "rev-parse", branch); got != head {
		t.Fatalf("task branch at %s, want the integration head %s", got, head)
	}
	dev := h.live(api.RoleDeveloper, task)
	dir := WorktreeDir(h.layout.Worktrees, h.project, task)
	if dev.Workdir != dir || !pathExists(filepath.Join(dir, "b.txt")) {
		t.Fatalf("developer works in %q; want %q with the story's file", dev.Workdir, dir)
	}
}

func TestReviewStartsWhileSlotsFull(t *testing.T) {
	h := newHarness(t, withLimit(2))
	t1, t2 := h.standalone("one"), h.standalone("two")
	h.approve(t1)
	h.approve(t2)
	h.step()
	h.setLimit(1)
	d2 := h.live(api.RoleDeveloper, t2)
	if _, err := h.b.Done(h.ctx, h.actor(d2.ID), api.DoneRequest{Comment: "finished"}); err != nil {
		t.Fatal(err)
	}
	h.step()
	h.live(api.RoleReviewer, t2)
	st := h.e.Scheduler()
	if st.Tasks != (api.SlotLoad{InWork: 2, Limit: 1}) {
		t.Fatalf("scheduler = %+v, want 2 tasks in work over limit 1", st)
	}
}

// startBlockedEvents counts the ticket.updated events of n that changed
// start_blocked.
func startBlockedEvents(t *testing.T, h *harness, n int64) int {
	t.Helper()
	evs, err := h.b.EventsAfter(h.ctx, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, ev := range evs {
		if ev.Kind != api.KindTicketUpdated || ev.Ticket == nil || int64(*ev.Ticket) != n {
			continue
		}
		var d api.TicketUpdatedData
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(d.Fields, "start_blocked") {
			count++
		}
	}
	return count
}

func TestGatedStartWaitsForSlot(t *testing.T) {
	h := newHarness(t, withLimit(1))
	t1, t2 := h.standalone("one"), h.standalone("two")
	h.approve(t1)
	h.step()
	d1 := h.live(api.RoleDeveloper, t1)
	h.approve(t2)
	h.step()
	h.step()
	tk := h.ticket(t2)
	if tk.Column != api.ColumnReady || tk.StartBlocked != api.StartBlockedSlot {
		t.Fatalf("#%d: column %s, start_blocked %q; want ready, slot", t2, tk.Column, tk.StartBlocked)
	}
	if n := startBlockedEvents(t, h, t2); n != 1 {
		t.Fatalf("%d start_blocked updates, want 1", n)
	}
	// A developer in omo ai wait gives nothing back.
	if err := h.b.SetSessionState(h.ctx, d1.ID, api.SessionWaiting); err != nil {
		t.Fatal(err)
	}
	h.step()
	if tk = h.ticket(t2); tk.Column != api.ColumnReady || tk.StartBlocked != api.StartBlockedSlot {
		t.Fatalf("#%d: column %s, start_blocked %q after a wait; want ready, slot", t2, tk.Column, tk.StartBlocked)
	}
	// Done (here: cancelled) gives the slot back.
	if _, err := h.b.Cancel(h.ctx, board.User, t1, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	h.step()
	tk = h.ticket(t2)
	if tk.Column != api.ColumnInProgress || tk.StartBlocked != api.StartBlockedNone {
		t.Fatalf("#%d: column %s, start_blocked %q; want in_progress, none", t2, tk.Column, tk.StartBlocked)
	}
}

func TestCancelDuringAgentStartEndsTheAgent(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	entered := make(chan agents.Spec, 1)
	h.runner.block, h.runner.entered = release, entered
	task := h.standalone("one")
	h.approve(task)
	h.e.pass(h.ctx)
	<-entered
	if h.ticket(task).Busy {
		t.Fatal("the claim should have ended with BeginWork")
	}
	if _, err := h.b.Cancel(h.ctx, board.User, task, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	close(release)
	h.e.settle()
	h.step()
	a := h.runner.all()
	if len(a) != 1 || !a[0].isKilled() {
		t.Fatalf("the agent started during the cancel was not killed")
	}
	got := h.waitEnded(a[0].id)
	if got.EndReason != "#1 was cancelled" {
		t.Fatalf("end reason %q, want #1 was cancelled", got.EndReason)
	}
	if h.liveCount() != 0 {
		t.Fatal("a session is still live")
	}
}

func TestCancelBeforeSessionAbandonsStart(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.e.pause = func(point string) {
		switch point {
		case "prepared":
			// The claim holds until the ticket is in progress.
			if _, err := h.b.Cancel(h.ctx, board.User, task, api.CancelRequest{}); err == nil {
				t.Error("cancel accepted while the ticket is claimed")
			}
		case "begun":
			if _, err := h.b.Cancel(h.ctx, board.User, task, api.CancelRequest{}); err != nil {
				t.Error(err)
			}
		case "spawn":
			t.Error("a session is created for a cancelled ticket")
		}
	}
	h.step()
	if len(h.sessions(task)) != 0 || len(h.runner.all()) != 0 {
		t.Fatal("a session or agent was created for a cancelled ticket")
	}
	if tk := h.ticket(task); tk.Column != api.ColumnDone || tk.Busy {
		t.Fatalf("column %s busy %v", tk.Column, tk.Busy)
	}
}

func TestStopEndsAgentsOfStoryAndTasks(t *testing.T) {
	h := newHarness(t)
	s := h.story("Story")
	h.approve(s)
	h.step()
	pm := h.live(api.RolePM, s)
	task := h.taskAs(h.actor(pm.ID), s, "Task")
	h.approveAs(h.actor(pm.ID), task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	if _, err := h.b.Move(h.ctx, board.User, s, api.MoveRequest{Column: api.ColumnPlanning}); err != nil {
		t.Fatal(err)
	}
	h.step()
	for _, id := range []string{pm.ID, dev.ID} {
		if !h.agent(id).isKilled() {
			t.Fatal("an agent of the stopped story still runs")
		}
		h.waitEnded(id)
	}
	for _, n := range []int64{s, task} {
		dir := WorktreeDir(h.layout.Worktrees, h.project, n)
		if !pathExists(dir) {
			t.Fatalf("worktree of #%d removed by a stop", n)
		}
	}
	if len(h.branches(h.repo)) != 2 {
		t.Fatalf("branches %v, want both kept", h.branches(h.repo))
	}
	if h.liveCount() != 0 {
		t.Fatal("a session is still live after the stop")
	}
}

func TestCrashingAgentFlaggedAfterThreeAttempts(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	for i := 1; i <= 3; i++ {
		dev := h.live(api.RoleDeveloper, task)
		h.agent(dev.ID).exit(errors.New("exit status 1"))
		ended := h.waitEnded(dev.ID)
		if !strings.Contains(ended.EndReason, "exited unexpectedly") {
			t.Fatalf("end reason %q", ended.EndReason)
		}
		h.step()
	}
	tk := h.ticket(task)
	if tk.FailedStep != api.FailedAgent || tk.Attention == "" {
		t.Fatalf("failed step %q attention %q after three crashes", tk.FailedStep, tk.Attention)
	}
	if n := len(h.runner.all()); n != 3 {
		t.Fatalf("%d agents started, want 3", n)
	}
	h.step()
	if n := len(h.runner.all()); n != 3 {
		t.Fatalf("a 4th agent started")
	}
	fresh := 0
	for _, c := range h.comments(task) {
		if strings.Contains(c, "omo starts a fresh agent") {
			fresh++
		}
	}
	if fresh != 2 {
		t.Fatalf("%d fresh-agent comments, want 2: %q", fresh, h.comments(task))
	}

	// A retry starts again with a clean count.
	if _, err := h.b.Retry(h.ctx, board.User, task); err != nil {
		t.Fatal(err)
	}
	h.step()
	h.live(api.RoleDeveloper, task)
	if got := h.e.attempts(failKey{ticket: task, role: api.RoleDeveloper, purpose: api.PurposeWork}); got != 0 {
		t.Fatalf("attempts after retry = %d", got)
	}
}

func TestHandshakeTimeoutCountsAsFailure(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	h.e.checkStarting(h.ctx, h.clock.Now().Add(time.Minute))
	if h.agent(dev.ID).isKilled() {
		t.Fatal("killed before the ready timeout")
	}
	h.clock.Advance(agents.ResolveDelivery(api.PromptDelivery{}).ReadyTimeout() + time.Second)
	h.e.checkStarting(h.ctx, h.clock.Now())
	if !h.agent(dev.ID).isKilled() {
		t.Fatal("an agent that never ran omo ai ready was not killed")
	}
	if got := h.waitEnded(dev.ID); got.EndReason != "did not run omo ai ready in time" {
		t.Fatalf("end reason %q", got.EndReason)
	}
	h.waitFor("the failure to be counted", func() bool {
		return h.e.attempts(failKey{ticket: task, role: api.RoleDeveloper, purpose: api.PurposeWork}) == 1
	})
	h.step()
	fresh := h.live(api.RoleDeveloper, task)
	if fresh.ID == dev.ID {
		t.Fatal("no fresh agent")
	}
	// An agent that acknowledged its start is left alone.
	h.e.markReady(fresh.ID)
	h.clock.Advance(time.Hour)
	h.e.checkStarting(h.ctx, h.clock.Now())
	if h.agent(fresh.ID).isKilled() {
		t.Fatal("a ready agent was killed")
	}
}

func TestRestartedSlotReusesModel(t *testing.T) {
	h := newHarness(t)
	t1, t2 := h.standalone("one"), h.standalone("two")
	h.approve(t1)
	h.step()
	h.approve(t2)
	h.step()
	d2 := h.live(api.RoleDeveloper, t2)
	if d2.Model != "fake/b" {
		t.Fatalf("second developer on %s, want fake/b (round robin)", d2.Model)
	}
	h.agent(d2.ID).exit(errors.New("exit status 2"))
	h.waitEnded(d2.ID)
	h.step()
	if got := h.live(api.RoleDeveloper, t2); got.Model != "fake/b" {
		t.Fatalf("fresh developer on %s, want the previous fake/b", got.Model)
	}
}

func TestMissingProjectFlagsAttentionAndStartsNothing(t *testing.T) {
	h := newHarness(t)
	t1, t2 := h.standalone("one"), h.standalone("two")
	h.approve(t1)
	h.step()
	d1 := h.live(api.RoleDeveloper, t1)
	h.approve(t2)
	f := false
	if _, err := h.st.UpdateProject(h.project, settings.ProjectUpdate{Enabled: &f}); err != nil {
		t.Fatal(err)
	}
	h.agent(d1.ID).exit(errors.New("exit status 1"))
	h.waitEnded(d1.ID)
	h.step()
	h.step()
	if h.liveCount() != 0 || len(h.runner.all()) != 1 {
		t.Fatal("an agent started on a disabled project")
	}
	for _, n := range []int64{t1, t2} {
		if tk := h.ticket(n); !strings.Contains(tk.Attention, "project "+h.project+" is missing or disabled") {
			t.Fatalf("#%d attention %q", n, tk.Attention)
		}
	}
	tr := true
	if _, err := h.st.UpdateProject(h.project, settings.ProjectUpdate{Enabled: &tr}); err != nil {
		t.Fatal(err)
	}
	h.step()
	h.step()
	if tk := h.ticket(t1); tk.Attention != "" {
		t.Fatalf("attention kept: %q", tk.Attention)
	}
	h.live(api.RoleDeveloper, t1)
}

func TestUnknownFolderAtWorktreePathIsNotTouched(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	dir := WorktreeDir(h.layout.Worktrees, h.project, task)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.approve(task)
	h.step()
	tk := h.ticket(task)
	if tk.FailedStep != api.FailedStart || !strings.Contains(tk.Attention, dir) {
		t.Fatalf("failed step %q attention %q", tk.FailedStep, tk.Attention)
	}
	if tk.Column != api.ColumnReady || tk.Busy {
		t.Fatalf("column %s busy %v", tk.Column, tk.Busy)
	}
	if raw, err := os.ReadFile(keep); err != nil || string(raw) != "mine" {
		t.Fatalf("keep.txt changed: %q %v", raw, err)
	}
	if b := h.branches(h.repo); len(b) != 0 || len(h.runner.all()) != 0 {
		t.Fatalf("branches %v, agents %d", b, len(h.runner.all()))
	}
}

// interruptedCut makes the branch and worktree a start of task in the
// project cuts, without recording them: omo stopped in between. It returns
// the branch.
func (h *harness) interruptedCut(task int64, name string) string {
	h.t.Helper()
	dir := WorktreeDir(h.layout.Worktrees, h.project, task)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		h.t.Fatal(err)
	}
	base := gitRun(h.t, h.repo, "rev-parse", "main")
	branch, err := h.git.AddWorktreeNewBranch(h.repo, dir, base, func(func(string) (bool, error)) (string, error) { return name, nil })
	if err != nil {
		h.t.Fatal(err)
	}
	return branch
}

// A start interrupted after git cut the branch and worktree and before the
// board recorded them is continued by the next start, with that branch.
func TestStartContinuesWithBranchOfInterruptedStart(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	tk := h.ticket(task)
	name, err := BranchName(h.st.General().Branches.Template, branchVars(board.EngineTicket{ID: task, Title: tk.Title, Type: tk.Type}, h.project), nil)
	if err != nil {
		t.Fatal(err)
	}
	branch := h.interruptedCut(task, name)
	h.approve(task)
	h.step()
	tk = h.ticket(task)
	dir := WorktreeDir(h.layout.Worktrees, h.project, task)
	if tk.FailedStep != api.FailedNone || tk.Column != api.ColumnInProgress {
		t.Fatalf("failed %q (%s), column %s", tk.FailedStep, tk.Attention, tk.Column)
	}
	if tk.Projects[0].Branch != branch || tk.Projects[0].Worktree != dir {
		t.Fatalf("branch %q in %q, want %s in %s", tk.Projects[0].Branch, tk.Projects[0].Worktree, branch, dir)
	}
	if b := h.branches(h.repo); len(b) != 1 {
		t.Fatalf("branches %v, want only %s", b, branch)
	}
	h.live(api.RoleDeveloper, task)
}

// What is in the worktree's place is continued only when it is that start's
// own: another ticket's branch name, a branch with commits of its own or
// uncommitted changes fail the start and are left alone.
func TestStartRefusesWorktreeThatIsNotItsOwn(t *testing.T) {
	cases := map[string]func(h *harness, task int64, own string) string{
		"other name": func(h *harness, task int64, own string) string { return h.interruptedCut(task, "someone/else") },
		"own commits": func(h *harness, task int64, own string) string {
			b := h.interruptedCut(task, own)
			commit(h.t, WorktreeDir(h.layout.Worktrees, h.project, task), "mine.txt", "mine\n")
			return b
		},
		"changes": func(h *harness, task int64, own string) string {
			b := h.interruptedCut(task, own)
			write(h.t, WorktreeDir(h.layout.Worktrees, h.project, task), "draft.txt", "draft\n")
			return b
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			task := h.standalone("one")
			tk := h.ticket(task)
			own, err := BranchName(h.st.General().Branches.Template, branchVars(board.EngineTicket{ID: task, Title: tk.Title, Type: tk.Type}, h.project), nil)
			if err != nil {
				t.Fatal(err)
			}
			branch := setup(h, task, own)
			head := gitRun(t, h.repo, "rev-parse", "refs/heads/"+branch)
			h.approve(task)
			h.step()
			tk = h.ticket(task)
			if tk.FailedStep != api.FailedStart || !strings.Contains(tk.Attention, "already exists and is not a worktree omo recorded") {
				t.Fatalf("failed %q attention %q", tk.FailedStep, tk.Attention)
			}
			if tk.Projects[0].Branch != "" || len(h.runner.all()) != 0 {
				t.Fatalf("branch %q recorded, %d agents", tk.Projects[0].Branch, len(h.runner.all()))
			}
			if got := gitRun(t, h.repo, "rev-parse", "refs/heads/"+branch); got != head {
				t.Fatalf("branch %s moved to %s", branch, got)
			}
			if !pathExists(WorktreeDir(h.layout.Worktrees, h.project, task)) {
				t.Fatal("the worktree was removed")
			}
		})
	}
}

func TestNoModelForRoleSetsStartBlocked(t *testing.T) {
	h := newHarness(t)
	if err := h.st.PutRole(settings.Role{Name: "developer", Assignment: api.AssignmentRoundRobin}); err != nil {
		t.Fatal(err)
	}
	task := h.standalone("one")
	h.approve(task)
	h.step()
	if tk := h.ticket(task); tk.StartBlocked != api.StartBlockedNoModel || tk.Column != api.ColumnReady {
		t.Fatalf("start_blocked %q column %s", tk.StartBlocked, tk.Column)
	}
	h.setRoleModels("fake/a")
	h.step()
	if tk := h.ticket(task); tk.Column != api.ColumnInProgress || tk.StartBlocked != api.StartBlockedNone {
		t.Fatalf("start_blocked %q column %s", tk.StartBlocked, tk.Column)
	}
}

func TestNoModelForResumedWorkSetsAttention(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	if err := h.st.PutRole(settings.Role{Name: "developer", Assignment: api.AssignmentRoundRobin}); err != nil {
		t.Fatal(err)
	}
	h.agent(dev.ID).exit(errors.New("exit status 1"))
	h.waitEnded(dev.ID)
	h.step()
	if tk := h.ticket(task); !strings.Contains(tk.Attention, "no usable model for the developer role") {
		t.Fatalf("attention %q", tk.Attention)
	}
	if h.liveCount() != 0 {
		t.Fatal("an agent started without a model")
	}
}

// ---- failure points of a start ----

func TestFailedSecondProjectUndoesTheFirst(t *testing.T) {
	h := newHarness(t, withProjects(2))
	s := h.story("Two projects", h.ids[0], h.ids[1])
	blocker := WorktreeDir(h.layout.Worktrees, h.ids[1], s)
	if err := os.MkdirAll(blocker, 0o755); err != nil {
		t.Fatal(err)
	}
	h.approve(s)
	h.step()
	tk := h.ticket(s)
	if tk.FailedStep != api.FailedStart || tk.Column != api.ColumnReady || tk.Busy {
		t.Fatalf("failed %q column %s busy %v", tk.FailedStep, tk.Column, tk.Busy)
	}
	if b := h.branches(h.repos[h.ids[0]]); len(b) != 0 {
		t.Fatalf("the first project's branch was kept: %v", b)
	}
	if pathExists(WorktreeDir(h.layout.Worktrees, h.ids[0], s)) {
		t.Fatal("the first project's worktree was kept")
	}
	for _, p := range tk.Projects {
		if p.Branch != "" || p.Worktree != "" {
			t.Fatalf("project %s still records %q %q", p.Project, p.Branch, p.Worktree)
		}
	}
	if h.liveCount() != 0 || len(h.runner.all()) != 0 {
		t.Fatal("an agent or session exists")
	}
}

func TestBranchNamedOmoIsNamedInFailedStep(t *testing.T) {
	h := newHarness(t)
	gitRun(t, h.repo, "branch", "omo")
	task := h.standalone("one")
	h.approve(task)
	h.step()
	tk := h.ticket(task)
	if tk.FailedStep != api.FailedStart || !strings.Contains(tk.Attention, "the branch omo exists, so git cannot create omo/one below it") {
		t.Fatalf("failed %q attention %q", tk.FailedStep, tk.Attention)
	}
	if pathExists(WorktreeDir(h.layout.Worktrees, h.project, task)) {
		t.Fatal("a worktree was left")
	}
}

func TestRunnerStartErrorEndsSessionAndCounts(t *testing.T) {
	h := newHarness(t)
	h.runner.setErr(errors.New("no such command"))
	task := h.standalone("one")
	h.approve(task)
	h.step()
	tk := h.ticket(task)
	if tk.Column != api.ColumnInProgress {
		t.Fatalf("column %s", tk.Column)
	}
	ss := h.sessions(task)
	if len(ss) != 1 || ss[0].State != api.SessionEnded || !strings.Contains(ss[0].EndReason, "could not start") {
		t.Fatalf("sessions %+v", ss)
	}
	if h.e.attempts(failKey{ticket: task, role: api.RoleDeveloper, purpose: api.PurposeWork}) != 1 {
		t.Fatal("the failed start was not counted")
	}
	// The branch belongs to the ticket in progress and stays.
	if len(h.branches(h.repo)) != 1 {
		t.Fatal("the branch of the started ticket was removed")
	}
	h.step()
	h.step()
	if tk := h.ticket(task); tk.FailedStep != api.FailedAgent {
		t.Fatalf("failed step %q after three failed starts", tk.FailedStep)
	}
	if n := len(h.sessions(task)); n != 3 {
		t.Fatalf("%d sessions, want 3", n)
	}
	h.runner.setErr(nil)
	h.step()
	if h.liveCount() != 0 {
		t.Fatal("a 4th start")
	}
}

func TestHangingEnvironmentCommandFailsStart(t *testing.T) {
	h := newHarness(t)
	h.runner.setErr(fmt.Errorf("start agent x: %w", &session.CommandTimeoutError{Variable: "TOKEN", Timeout: time.Second}))
	task := h.standalone("one")
	h.approve(task)
	h.step()
	tk := h.ticket(task)
	if tk.FailedStep != api.FailedStart || !strings.Contains(tk.Attention, "TOKEN") {
		t.Fatalf("failed %q attention %q", tk.FailedStep, tk.Attention)
	}
	if h.liveCount() != 0 {
		t.Fatal("session left live")
	}
}

func TestPanicInStartReleasesClaim(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.e.pause = func(point string) {
		if point == "claimed" {
			panic("boom")
		}
	}
	h.step()
	tk := h.ticket(task)
	if tk.Busy || tk.Column != api.ColumnReady {
		t.Fatalf("busy %v column %s after a panic", tk.Busy, tk.Column)
	}
	if !strings.Contains(h.logs.String(), "panicked") {
		t.Fatal("panic not logged")
	}
	h.e.pause = nil
	// A failed start is tried again once its back-off is over.
	h.clock.Advance(retryAfter[1])
	h.e.healthTick(h.ctx, h.clock.Now())
	h.step()
	if h.ticket(task).Column != api.ColumnInProgress {
		t.Fatal("not started after the panic")
	}
}

func TestPanicAfterCutRemovesBranch(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.e.pause = func(point string) {
		if point == "prepared" {
			panic("boom")
		}
	}
	h.step()
	tk := h.ticket(task)
	if tk.Busy || tk.Column != api.ColumnReady || tk.Projects[0].Branch != "" {
		t.Fatalf("busy %v column %s branch %q", tk.Busy, tk.Column, tk.Projects[0].Branch)
	}
	if len(h.branches(h.repo)) != 0 || pathExists(WorktreeDir(h.layout.Worktrees, h.project, task)) {
		t.Fatal("branch or worktree left behind")
	}
}

func TestMissingBranchOnContinuationFlagsStart(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	tk := h.ticket(task)
	h.agent(dev.ID).exit(errors.New("exit status 1"))
	h.waitEnded(dev.ID)
	gitRun(t, h.repo, "worktree", "remove", "--force", tk.Projects[0].Worktree)
	gitRun(t, h.repo, "branch", "-D", tk.Projects[0].Branch)
	h.step()
	tk = h.ticket(task)
	if tk.FailedStep != api.FailedStart || !strings.Contains(tk.Attention, "no longer exists") {
		t.Fatalf("failed %q attention %q", tk.FailedStep, tk.Attention)
	}
}

func TestMissingWorktreeFolderIsRecreated(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	tk := h.ticket(task)
	h.agent(dev.ID).exit(errors.New("exit status 1"))
	h.waitEnded(dev.ID)
	if err := os.RemoveAll(tk.Projects[0].Worktree); err != nil {
		t.Fatal(err)
	}
	h.step()
	fresh := h.live(api.RoleDeveloper, task)
	if got := gitRun(t, fresh.Workdir, "branch", "--show-current"); got != tk.Projects[0].Branch {
		t.Fatalf("recreated worktree on %q", got)
	}
}

// An agent replaced its worktree folder by a link to another ticket's
// worktree: its next session must not start there.
func TestLinkedWorktreeFolderIsNotReused(t *testing.T) {
	h := newHarness(t, withLimit(2))
	one := h.standalone("one")
	two := h.standalone("two")
	h.approve(one)
	h.approve(two)
	h.step()
	dev := h.live(api.RoleDeveloper, one)
	h.live(api.RoleDeveloper, two)
	wtOne := h.ticket(one).Projects[0].Worktree
	wtTwo := h.ticket(two).Projects[0].Worktree
	h.agent(dev.ID).exit(errors.New("exit status 1"))
	h.waitEnded(dev.ID)
	if err := os.Rename(wtOne, wtOne+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wtTwo, wtOne); err != nil {
		t.Fatal(err)
	}
	startsIn := func(dir string) int {
		n := 0
		for _, s := range h.runner.specList() {
			if s.Dir == dir {
				n++
			}
		}
		return n
	}
	before := startsIn(wtOne)
	h.step()
	if startsIn(wtOne) != before {
		t.Fatalf("a session started in the linked folder %s", wtOne)
	}
	if tk := h.ticket(one); tk.FailedStep != api.FailedStart || !strings.Contains(tk.Attention, "leaves it alone") {
		t.Fatalf("failed %q attention %q", tk.FailedStep, tk.Attention)
	}
}

// ---- engine start and stop ----

func TestRunStopsDuringStartInFlight(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	entered := make(chan agents.Spec, 1)
	h.runner.block, h.runner.entered = release, entered
	task := h.standalone("one")
	h.approve(task)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.e.Run(ctx)
		close(done)
	}()
	<-entered
	cancel()
	select {
	case <-done:
		t.Fatal("Run returned while a start was in flight")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-done
	for _, a := range h.runner.all() {
		if !a.isOver() {
			t.Fatal("agent left running")
		}
	}
	if h.liveCount() != 0 {
		t.Fatal("a session is still live after Run returned")
	}
	// Running again resumes the ticket; a second concurrent Run returns at once.
	h.runner.block = nil
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() {
		h.e.Run(ctx2)
		close(done2)
	}()
	h.waitFor("a fresh developer", func() bool { _, ok := h.liveOK(api.RoleDeveloper, task); return ok })
	h.e.Run(ctx2) // returns at once
	cancel2()
	<-done2
	h.e.halt(context.Background(), "again") // idempotent
	if h.liveCount() != 0 {
		t.Fatal("a session is still live")
	}
}

func TestRunEndsSessionsAndClaimsOfEarlierRun(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	old := h.live(api.RoleDeveloper, task)
	other := h.standalone("two")
	h.approve(other)
	if err := h.b.ClaimStart(h.ctx, other); err != nil {
		t.Fatal(err)
	}
	// A fresh engine on the same board, as after a crash of the first.
	e1 := h.e
	e2 := New(Options{Board: h.b, Settings: h.st, Git: h.git, Layout: h.layout, Runner: h.runner, Now: h.clock.Now})
	h.mu.Lock()
	h.e = e2
	h.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e2.Run(ctx); close(done) }()
	h.waitFor("both tickets to have fresh developers", func() bool {
		a, ok1 := h.liveOK(api.RoleDeveloper, task)
		_, ok2 := h.liveOK(api.RoleDeveloper, other)
		return ok1 && ok2 && a.ID != old.ID
	})
	cancel()
	<-done
	if got := h.waitEnded(old.ID); got.EndReason != "omo restarted" {
		t.Fatalf("old session ended with %q", got.EndReason)
	}
	if h.ticket(other).Busy {
		t.Fatal("claim kept")
	}
	e1.halt(context.Background(), "test over")
}

func TestCrashOfParkedDeveloperInReviewSaysNoAgentStarts(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	h.handIn(task)
	h.step()
	h.live(api.RoleReviewer, task)
	h.agent(dev.ID).exit(errors.New("exit status 1"))
	h.waitEnded(dev.ID)
	h.step()
	if _, ok := h.liveOK(api.RoleDeveloper, task); ok {
		t.Fatal("a developer started for a task in review")
	}
	c := h.lastComment(task).Body
	if strings.Contains(c, "omo starts a fresh agent") || !strings.Contains(c, "omo starts no agent for it now") {
		t.Fatalf("comment on the crash:\n%s", c)
	}
}

// TestEndWhileStartInFlight ends an agent whose process start is still in
// flight, at the same time as the start completes. Run with -race: end
// reads the entry's agent while the start writes it.
func TestEndWhileStartInFlight(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	entered := make(chan agents.Spec, 1)
	h.runner.block, h.runner.entered = release, entered
	task := h.standalone("one")
	h.approve(task)
	h.e.pass(h.ctx)
	spec := <-entered
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		h.e.end(h.ctx, spec.SessionID, "ended by the test")
	}()
	close(release)
	<-ended
	h.e.settle()
	a := h.runner.all()
	if len(a) != 1 || !a[0].isKilled() {
		t.Fatal("the agent whose start was in flight was not killed")
	}
	if got := h.waitEnded(spec.SessionID); got.EndReason != "ended by the test" {
		t.Fatalf("end reason %q", got.EndReason)
	}
}

func TestEveryBranchNameTakenFailsStart(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	// Every name BranchName may try is taken.
	for i := 1; i <= maxBranchSuffix; i++ {
		name := "omo/one"
		if i > 1 {
			name += fmt.Sprintf("-%d", i)
		}
		gitRun(t, h.repo, "branch", name)
	}
	h.step()
	tk := h.ticket(task)
	if tk.FailedStep != api.FailedStart || !strings.Contains(h.lastComment(task).Body, "could not pick a branch name") {
		t.Fatalf("failed %q, comment:\n%s", tk.FailedStep, h.lastComment(task).Body)
	}
	if tk.Busy || pathExists(WorktreeDir(h.layout.Worktrees, h.project, task)) {
		t.Fatal("claim or worktree left")
	}
}

func TestCrashRestartFailsOverToNextModel(t *testing.T) {
	h := newHarness(t)
	for _, r := range settings.RoleNames {
		if err := h.st.PutRole(settings.Role{Name: r, Models: []string{"fake/a", "fake/b"}, Assignment: api.AssignmentFailover}); err != nil {
			t.Fatal(err)
		}
	}
	task := h.standalone("one")
	h.approve(task)
	h.step()
	var got []string
	for range 2 {
		dev := h.live(api.RoleDeveloper, task)
		got = append(got, dev.Model)
		h.agent(dev.ID).exit(errors.New("exit status 1"))
		h.waitEnded(dev.ID)
		h.step()
	}
	got = append(got, h.live(api.RoleDeveloper, task).Model)
	if strings.Join(got, ",") != "fake/a,fake/b,fake/a" {
		t.Fatalf("models after crashes: %v", got)
	}
}

// TestWorkerFinishingDuringPassIsNotPlannedAgain lets a start worker finish
// between the moment a pass copies what is in flight and the moment it
// reads the board: the pass must not start the same agent again.
func TestWorkerFinishingDuringPassIsNotPlannedAgain(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	h.agent(dev.ID).exit(errors.New("exit status 1"))
	h.waitEnded(dev.ID)

	atSpawn, release := make(chan struct{}), make(chan struct{})
	var armed, reached atomic.Bool
	h.e.pause = func(point string) {
		switch {
		case point == "spawn" && !reached.Load():
			reached.Store(true)
			close(atSpawn)
			<-release // the worker waits before it creates the session
		case point == "pass-copied" && armed.CompareAndSwap(true, false):
			close(release)
			h.e.settle()
		}
	}
	h.e.pass(h.ctx) // launches the continuation start
	<-atSpawn
	// Something unrelated changes on the board, so the pass does not skip
	// the start as already tried.
	h.comment(board.User, task, api.CommentRequest{Body: "a note"})
	armed.Store(true)
	h.step()
	if armed.Load() {
		t.Fatal("the pass did not reach the window")
	}
	h.step()
	if n := len(h.runner.all()); n != 2 {
		t.Fatalf("%d agents started, want 2", n)
	}
	if n := len(h.sessions(task)); n != 2 {
		t.Fatalf("%d sessions, want 2", n)
	}
	if strings.Contains(h.logs.String(), "start developer") {
		t.Fatalf("a second start was tried:\n%s", h.logs.String())
	}
}

// The engine's git client refuses to let git look for a repository in the
// worktrees folder: commands there run only through resolved worktrees.
func TestEngineGitKnowsTheAgentFolder(t *testing.T) {
	h := newHarness(t)
	if !slices.Contains(h.e.git.AgentFolders(), filepath.Clean(h.layout.Worktrees)) {
		t.Fatalf("agent folders %v, want %s", h.e.git.AgentFolders(), h.layout.Worktrees)
	}
}

func TestStartWaitsWhenUsageRoseSincePlanned(t *testing.T) {
	h := newHarness(t)
	use := h.withUsage()
	n := h.standalone("one")
	h.approve(n)
	// Between the pass and the model pick both models go over their soft
	// limit: the start gives the ticket back without counting a failure.
	h.e.pause = func(point string) {
		if point == "claimed" {
			use.set("fake/a", api.UsageSoft, 95)
			use.set("fake/b", api.UsageHard, 99)
		}
	}
	h.step()
	h.e.pause = nil
	tk := h.ticket(n)
	if tk.Column != api.ColumnReady || tk.Busy || tk.FailedStep != "" || h.liveCount() != 0 {
		t.Fatalf("after the start: %s busy %v failed %q, %d live", tk.Column, tk.Busy, tk.FailedStep, h.liveCount())
	}
	if len(h.branches(h.repo)) != 0 {
		t.Fatalf("branches %v", h.branches(h.repo))
	}
	h.step()
	if got := h.ticket(n).StartBlocked; got != api.StartBlockedUsage {
		t.Fatalf("start_blocked %q", got)
	}
	h.e.mu.Lock()
	fails := len(h.e.fails)
	h.e.mu.Unlock()
	if fails != 0 {
		t.Fatalf("%d failed attempts recorded", fails)
	}
}
