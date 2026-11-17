package engine

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// crash replaces the engine by a fresh one on the same board without
// stopping the old one, as a new omo process after a crash would find it:
// sessions still live in the database, claims held, no process of the new
// run. The old engine's fake agents are ended when the test ends.
func (h *harness) crash() *Engine {
	h.t.Helper()
	e := New(Options{
		Board: h.b, Settings: h.st, Git: h.git, Layout: h.layout, Runner: h.runner,
		Socket: func() string { return "" }, Log: log.New(h.logs, "", 0), Now: h.clock.Now,
	})
	h.mu.Lock()
	h.e = e
	h.mu.Unlock()
	h.t.Cleanup(func() { e.halt(context.Background(), "test over") })
	return e
}

// boot runs what Run does before its first pass, then n passes, a health
// tick (which lets actions that changed nothing be tried again) and n more.
func (h *harness) boot(n int) {
	h.t.Helper()
	h.e.startup(h.ctx)
	h.e.recoverAtStart(h.ctx)
	h.steps(n)
	h.e.healthTick(h.ctx, h.clock.Now())
	h.e.settle()
	h.steps(n)
}

// endReasons returns the end reasons of a ticket's ended sessions.
func (h *harness) endReasons(n int64) []string {
	var out []string
	for _, s := range h.sessions(n) {
		if s.State == api.SessionEnded {
			out = append(out, s.EndReason)
		}
	}
	return out
}

func TestRecoveryEndsStaleSessionsAndResumes(t *testing.T) {
	h := newHarness(t, withLimit(2))
	t1, old1 := h.readyDeveloper("one")
	t2, old2 := h.readyDeveloper("two")
	h.crash()
	h.setLimit(1)
	runEngine(t, h)
	var fresh []board.EngineSession
	h.waitFor("fresh developers on both tasks", func() bool {
		fresh = nil
		for _, n := range []int64{t1, t2} {
			s, ok := h.liveOK(api.RoleDeveloper, n)
			if !ok || s.ID == old1.ID || s.ID == old2.ID {
				return false
			}
			fresh = append(fresh, s)
		}
		return true
	})
	for _, n := range []int64{t1, t2} {
		if got := h.endReasons(n); len(got) != 1 || got[0] != "omo restarted" {
			t.Fatalf("#%d ended sessions %q", n, got)
		}
	}
	for _, s := range fresh {
		p, err := h.e.Ready(h.ctx, h.actor(s.ID))
		if err != nil || !strings.Contains(p, "This is a fresh session on work that was already started") {
			t.Fatalf("prompt (%v) lacks the restart warning:\n%s", err, p)
		}
	}
}

func TestRecoveryResumesReviewButNotHumanReview(t *testing.T) {
	h := newHarness(t)
	agentReview := h.create(api.CreateTicket{Type: api.TypeTask, Title: "agent review", Review: api.ReviewAgent})
	humanReview := h.create(api.CreateTicket{Type: api.TypeTask, Title: "human review", Review: api.ReviewHuman})
	for _, n := range []int64{agentReview, humanReview} {
		h.approve(n)
		h.step()
		h.handIn(n)
	}
	h.step()
	h.live(api.RoleReviewer, agentReview)
	h.crash()
	h.boot(2)
	rev := h.live(api.RoleReviewer, agentReview)
	// A ticket waiting for the user's review needs no agent; a handed-in
	// developer is started again only when its ticket goes back to work.
	for _, s := range h.snap().Sessions {
		if s.Ticket == humanReview || (s.Ticket == agentReview && s.Role != api.RoleReviewer) {
			t.Fatalf("a %s started on #%d", s.Role, s.Ticket)
		}
	}
	p, err := h.e.Ready(h.ctx, h.actor(rev.ID))
	if err != nil || !strings.Contains(p, "This is a fresh session") {
		t.Fatalf("reviewer prompt (%v):\n%s", err, p)
	}
}

// Slots are read from the board, so a restart restores nothing: the tickets
// in work hold their slots in the new engine's first pass, before any agent
// of theirs runs again, and new work waits behind them.
func TestRestartKeepsTicketsInWorkCounted(t *testing.T) {
	h := newHarness(t, withLimit(2))
	h.readyDeveloper("one")
	two := h.create(api.CreateTicket{Type: api.TypeTask, Title: "two in review", Review: api.ReviewHuman})
	h.approve(two)
	h.step()
	h.handIn(two)
	h.step()
	waiting := h.standalone("three")
	h.approve(waiting)
	h.crash()
	h.e.startup(h.ctx)
	h.e.pass(h.ctx)
	if st := h.e.Scheduler(); st.Tasks != (api.SlotLoad{InWork: 2, Limit: 2}) || st.Stories != (api.SlotLoad{Limit: 2}) {
		t.Fatalf("scheduler after the restart = %+v", st)
	}
	h.e.settle()
	h.boot(2)
	if tk := h.ticket(waiting); tk.Column != api.ColumnReady || tk.StartBlocked != api.StartBlockedSlot {
		t.Fatalf("#%d: column %s, start_blocked %q; want ready, slot", waiting, tk.Column, tk.StartBlocked)
	}
}

func TestResumeAtStartWhilePausedStartsNothing(t *testing.T) {
	h := newHarness(t)
	t1, _ := h.readyDeveloper("one")
	t2, _ := h.readyDeveloper("two")
	if err := h.b.SetPaused(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	before := len(h.runner.specList())
	h.crash()
	h.boot(3)
	if n := len(h.runner.specList()) - before; n != 0 {
		t.Fatalf("%d agents started while paused", n)
	}
	if n := h.liveCount(); n != 0 {
		t.Fatalf("%d live sessions", n)
	}
	for _, n := range []int64{t1, t2} {
		if c := h.ticket(n).Column; c != api.ColumnInProgress {
			t.Fatalf("#%d is %s", n, c)
		}
	}
	if err := h.b.SetPaused(h.ctx, false); err != nil {
		t.Fatal(err)
	}
	h.step()
	h.live(api.RoleDeveloper, t1)
	h.live(api.RoleDeveloper, t2)
}

func TestRecoveryWithDeletedBranchFlagsStart(t *testing.T) {
	h := gitHarness(t)
	n, dir, branch := h.startedTask("one")
	h.crash()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	gitRun(t, h.repo, "worktree", "prune")
	gitRun(t, h.repo, "branch", "-D", branch)
	before := len(h.runner.specList())
	h.boot(3)
	tk := h.ticket(n)
	if tk.FailedStep != api.FailedStart || !strings.Contains(tk.Attention, "branch "+branch+" of #1 no longer exists") {
		t.Fatalf("ticket %s %q", tk.FailedStep, tk.Attention)
	}
	if got := len(h.runner.specList()) - before; got != 0 || h.liveCount() != 0 {
		t.Fatalf("%d agents started, %d live", got, h.liveCount())
	}
	if tk.Column != api.ColumnInProgress {
		t.Fatalf("ticket moved to %s", tk.Column)
	}
}

func TestRecoveryRecreatesMissingWorktreeFolder(t *testing.T) {
	h := gitHarness(t)
	n, dir, branch := h.startedTask("one")
	tip := gitRun(t, dir, "rev-parse", "HEAD")
	h.crash()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	h.boot(2)
	s := h.live(api.RoleDeveloper, n)
	if s.Workdir != dir {
		t.Fatalf("agent works in %s, want %s", s.Workdir, dir)
	}
	if got := gitRun(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Fatalf("worktree on %s", got)
	}
	if got := gitRun(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatal("the recreated worktree lost the branch's commits")
	}
	if tk := h.ticket(n); tk.FailedStep != api.FailedNone {
		t.Fatalf("failed step %s", tk.FailedStep)
	}
}

func TestRecoveryWithMissingRepositoryFlagsAndWaits(t *testing.T) {
	h := gitHarness(t)
	n, _, _ := h.startedTask("one")
	h.crash()
	moved := h.repo + ".away"
	if err := os.Rename(h.repo, moved); err != nil {
		t.Fatal(err)
	}
	before := len(h.runner.specList())
	h.boot(3)
	tk := h.ticket(n)
	if !strings.Contains(tk.Attention, "project "+h.project+" is missing or disabled") || h.liveCount() != 0 {
		t.Fatalf("attention %q, %d live", tk.Attention, h.liveCount())
	}
	if got := len(h.runner.specList()) - before; got != 0 {
		t.Fatalf("%d agents started without the repository", got)
	}
	if err := os.Rename(moved, h.repo); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	h.live(api.RoleDeveloper, n)
	if tk := h.ticket(n); tk.Attention != "" {
		t.Fatalf("attention %q after the repository came back", tk.Attention)
	}
}

func TestRecoveryWithBrokenProfileStartsBoundedAgents(t *testing.T) {
	h := newHarness(t)
	n, _ := h.readyDeveloper("one")
	before := len(h.runner.specList())
	h.runner.setErr(errors.New("exec: \"fake-agent\": executable file not found in $PATH"))
	h.crash()
	h.boot(5)
	for range 5 {
		h.e.healthTick(h.ctx, h.clock.Now())
		h.e.settle()
		h.steps(2)
	}
	if got := len(h.runner.specList()) - before; got != maxFailures {
		t.Fatalf("%d starts after the restart, want %d", got, maxFailures)
	}
	if tk := h.ticket(n); tk.FailedStep != api.FailedAgent {
		t.Fatalf("failed step %q", tk.FailedStep)
	}
	// The failed step is stored: the next restart starts nothing.
	h.crash()
	h.boot(5)
	if got := len(h.runner.specList()) - before; got != maxFailures {
		t.Fatalf("%d starts after the second restart, want %d", got, maxFailures)
	}
}

func TestRecoverySetsGitEnvironmentForMerges(t *testing.T) {
	h := gitHarness(t)
	h.e.recoverAtStart(h.ctx)
	n := h.skippedTask("one")
	h.finishTask(n, h.project, "one.txt", "one\n")
	h.steps(3)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("#%d is %s", n, c)
	}
	if got := gitRun(t, h.repo, "log", "-1", "--format=%an", "main"); got != "Agent" {
		t.Fatalf("merge commit by %q, want the agent environment's author", got)
	}
	// A changed agent environment reaches git with the next health check.
	g := h.st.General()
	g.AgentEnv = map[string]string{"GIT_AUTHOR_NAME": "Other", "GIT_AUTHOR_EMAIL": "other@example.invalid"}
	if err := h.st.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	h.e.checkHealth(h.ctx, h.clock.Now())
	m := h.skippedTask("two")
	h.finishTask(m, h.project, "two.txt", "two\n")
	h.steps(3)
	if got := gitRun(t, h.repo, "log", "-1", "--format=%an", "main"); got != "Other" {
		t.Fatalf("merge commit by %q after the change", got)
	}
}
