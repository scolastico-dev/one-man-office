package engine

import (
	"strings"
	"testing"

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

// retryRounds runs n passes, each after the health check let every action
// be tried again.
func (h *harness) retryRounds(n int) {
	for range n {
		h.e.mu.Lock()
		clear(h.e.tried)
		h.e.mu.Unlock()
		h.step()
	}
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
