package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// inject runs SQL on the board's database through a second connection,
// for faults the board cannot be asked to produce: a trigger that refuses
// a write makes that board call fail.
func (h *harness) inject(stmt string) {
	h.t.Helper()
	d, err := db.Open(h.layout.DB)
	if err != nil {
		h.t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(stmt); err != nil {
		h.t.Fatal(err)
	}
}

// fault is a trigger that refuses the writes when matches.
func fault(name, when string) string {
	return "CREATE TRIGGER " + name + " " + when + " BEGIN SELECT RAISE(ABORT, 'injected fault'); END"
}

// retryRounds runs n passes, each after the longest back-off passed and the
// health check let every action be tried again.
func (h *harness) retryRounds(n int) {
	for range n {
		h.clock.Advance(retryAfter[len(retryAfter)-1])
		h.e.forgetTried(h.clock.Now())
		h.step()
	}
}

// tickAfter advances the clock by d, runs the health check's forgetting
// of tries and one pass.
func (h *harness) tickAfter(d time.Duration) {
	h.clock.Advance(d)
	h.e.forgetTried(h.clock.Now())
	h.step()
}

func (h *harness) lastEvent() int64 {
	h.t.Helper()
	id, err := h.b.LastEventID(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return id
}

// checkBounded runs passes and checks that the action failing with logged
// was tried exactly maxAttempts times, then flagged with step, and that
// nothing is tried or written after that.
func (h *harness) checkBounded(n int64, step api.FailedStep, logged string) {
	h.t.Helper()
	h.step()
	if got := strings.Count(h.logs.String(), logged); got != 1 {
		h.t.Fatalf("%d attempts after the first pass, want 1", got)
	}
	// Its own claim and release do not make the action look new.
	h.steps(3)
	if got := strings.Count(h.logs.String(), logged); got != 1 {
		h.t.Fatalf("%d attempts without a change on the board, want 1", got)
	}
	h.retryRounds(10)
	if got := strings.Count(h.logs.String(), logged); got != maxAttempts {
		h.t.Fatalf("%d attempts, want %d:\n%s", got, maxAttempts, h.logs.String())
	}
	tk := h.ticket(n)
	if tk.FailedStep != step || !strings.Contains(h.lastComment(n).Body, "injected fault") || tk.Busy {
		h.t.Fatalf("failed %q busy %v, comment:\n%s", tk.FailedStep, tk.Busy, h.lastComment(n).Body)
	}
	before := h.lastEvent()
	h.retryRounds(5)
	if after := h.lastEvent(); after != before {
		h.t.Fatalf("%d events written after the failed step", after-before)
	}
}

func TestFailingBeginWorkIsBounded(t *testing.T) {
	h := newHarness(t)
	h.inject(fault("t_fault", "BEFORE UPDATE OF col ON tickets WHEN NEW.col = 'in_progress'"))
	task := h.standalone("one")
	h.approve(task)
	h.checkBounded(task, api.FailedStart, "engine: begin work on")
	if c := h.ticket(task).Column; c != api.ColumnReady {
		t.Fatalf("column %s", c)
	}
	if len(h.branches(h.repo)) != 0 {
		t.Fatalf("branches left: %v", h.branches(h.repo))
	}
}

func TestFailingSetBranchIsBounded(t *testing.T) {
	h := newHarness(t)
	h.inject(fault("t_fault", "BEFORE UPDATE OF branch ON ticket_projects WHEN NEW.branch <> ''"))
	task := h.standalone("one")
	h.approve(task)
	h.checkBounded(task, api.FailedStart, "engine: start developer on")
	if len(h.branches(h.repo)) != 0 || pathExists(h.worktree(h.project, task)) {
		t.Fatal("branch or worktree left")
	}
}

func TestFailingSessionCreationIsBounded(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	h.inject(fault("t_fault", "BEFORE INSERT ON agent_sessions"))
	if _, err := h.b.Cancel(h.ctx, h.actor(dev.ID), task, api.CancelRequest{}); err == nil {
		t.Fatal("a developer cancelled its task")
	}
	// The developer is gone; every fresh one fails to get a session.
	h.e.end(h.ctx, dev.ID, "test")
	h.checkBounded(task, api.FailedStart, "create the session of")
}

func TestPanicAfterClaimIsBounded(t *testing.T) {
	h := newHarness(t)
	h.e.pause = func(point string) {
		if point == "claimed" {
			panic("injected fault")
		}
	}
	task := h.standalone("one")
	h.approve(task)
	h.checkBounded(task, api.FailedStart, "panicked: injected fault")
}

func TestFailingMergeRecordIsBounded(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	h.inject(fault("t_fault", "BEFORE UPDATE OF col ON tickets WHEN NEW.col = 'done'"))
	h.checkBounded(n, api.FailedMerge, "engine: record the merge of")
	if mergeCount(t, h.repo, "main") != 1 {
		t.Fatal("want exactly one merge commit")
	}
}

func TestPanicAfterMergeClaimIsBounded(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	h.e.pause = func(point string) {
		if point == "merge-claimed" {
			panic("injected fault")
		}
	}
	h.checkBounded(n, api.FailedMerge, "panicked: injected fault")
}

func TestUserChangeForgetsFailedAttempts(t *testing.T) {
	h := newHarness(t)
	h.inject(fault("t_fault", "BEFORE UPDATE OF col ON tickets WHEN NEW.col = 'in_progress'"))
	task := h.standalone("one")
	h.approve(task)
	h.retryRounds(maxAttempts - 1)
	// The user edits the ticket: the count starts again.
	if _, err := h.b.Update(h.ctx, board.User, task, api.UpdateTicket{Title: api.Some("one, edited")}); err != nil {
		t.Fatal(err)
	}
	h.step()
	if f := h.ticket(task).FailedStep; f != api.FailedNone {
		t.Fatalf("failed step %q after a change by the user", f)
	}
	h.retryRounds(maxAttempts - 1)
	if f := h.ticket(task).FailedStep; f != api.FailedStart {
		t.Fatalf("failed step %q, want start", f)
	}
}

func TestBlockedActionBacksOffBeforeTheBoundTrips(t *testing.T) {
	h := newHarness(t)
	h.inject(fault("t_fault", "BEFORE UPDATE OF col ON tickets WHEN NEW.col = 'in_progress'"))
	task := h.standalone("one")
	h.approve(task)
	const logged = "engine: begin work on"
	tries := func() int { return strings.Count(h.logs.String(), logged) }
	h.step()
	if n := tries(); n != 1 {
		t.Fatalf("%d tries after the first pass", n)
	}
	// Health checks every 30 seconds do not retry it before its back-off.
	for range 3 {
		h.tickAfter(30 * time.Second)
	}
	if n := tries(); n != 1 {
		t.Fatalf("%d tries within 90 seconds of the first failure, want 1", n)
	}
	h.tickAfter(retryAfter[1] - 90*time.Second)
	if n := tries(); n != 2 {
		t.Fatalf("%d tries after the first back-off, want 2", n)
	}
	// The second back-off is longer.
	h.tickAfter(retryAfter[1])
	if n := tries(); n != 2 {
		t.Fatalf("%d tries before the second back-off is over, want 2", n)
	}
	h.tickAfter(retryAfter[2] - retryAfter[1])
	if n, f := tries(), h.ticket(task).FailedStep; n != maxAttempts || f != api.FailedStart {
		t.Fatalf("%d tries, failed step %q; want the bound to trip at the third", n, f)
	}
}

func TestBoardChangeRetriesDuringBackOff(t *testing.T) {
	h := newHarness(t)
	h.inject(fault("t_fault", "BEFORE UPDATE OF col ON tickets WHEN NEW.col = 'in_progress'"))
	task := h.standalone("one")
	h.approve(task)
	h.step()
	h.tickAfter(30 * time.Second)
	if n := strings.Count(h.logs.String(), "engine: begin work on"); n != 1 {
		t.Fatalf("%d tries", n)
	}
	// A comment changes the board: the start is tried at once.
	if _, err := h.b.Comment(h.ctx, board.User, task, api.CommentRequest{Body: "go on"}); err != nil {
		t.Fatal(err)
	}
	h.step()
	if n := strings.Count(h.logs.String(), "engine: begin work on"); n != 2 {
		t.Fatalf("%d tries after a change on the board, want 2", n)
	}
}

func TestSucceedingActionIsTriedAgainAtTheNextHealthCheck(t *testing.T) {
	h := newHarness(t)
	h.e.mu.Lock()
	h.e.tried["x"] = triedAt{event: 1, at: h.clock.Now()}
	h.e.mu.Unlock()
	h.clock.Advance(DefaultHealthInterval)
	h.e.forgetTried(h.clock.Now())
	h.e.mu.Lock()
	_, kept := h.e.tried["x"]
	h.e.mu.Unlock()
	if kept {
		t.Fatal("an action without failures waits longer than one health check")
	}
}
