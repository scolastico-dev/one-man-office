package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// rejectReview lets the live reviewer of n reject its review.
func (h *harness) rejectReview(n int64) {
	h.t.Helper()
	rev := h.roundReviewer(n, h.ticket(n).ReviewRound)
	if _, err := h.b.Done(h.ctx, h.actor(rev.ID), api.DoneRequest{Verdict: board.VerdictReject, Comment: "not yet"}); err != nil {
		h.t.Fatal(err)
	}
}

func TestReviewPingPongStopsAfterTheLimit(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	for i := 1; i <= maxBounces+1; i++ {
		h.handIn(task)
		h.step()
		if _, ok := h.liveOK(api.RoleReviewer, task); !ok {
			t.Fatalf("no reviewer for round %d after %d rejections", i, i-1)
		}
		h.rejectReview(task)
		h.step()
	}
	// Rejected maxBounces+1 times: the next review does not start.
	h.handIn(task)
	h.step()
	if _, ok := h.liveOK(api.RoleReviewer, task); ok {
		t.Fatal("a reviewer started after too many rejections")
	}
	tk := h.ticket(task)
	want := fmt.Sprintf("its review was rejected %d times", maxBounces+1)
	if tk.FailedStep != api.FailedStart || !strings.Contains(h.lastComment(task).Body, want) {
		t.Fatalf("failed %q, comment:\n%s", tk.FailedStep, h.lastComment(task).Body)
	}
	// A person retries: the count starts again.
	if _, err := h.b.Retry(h.ctx, board.User, task); err != nil {
		t.Fatal(err)
	}
	h.step()
	h.live(api.RoleReviewer, task)
}

func TestConflictPingPongStopsAfterTheLimit(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Edit a")
	h.finishTask(n, h.project, "a.txt", "task's a\n")
	commit(t, h.repo, "a.txt", "user's a\n")
	for i := 1; i <= maxBounces+1; i++ {
		h.step()
		if c := h.ticket(n).Column; c != api.ColumnInProgress {
			t.Fatalf("merge %d: ticket is %s, want back in progress after the conflict", i, c)
		}
		h.handIn(n)
	}
	h.step()
	tk := h.ticket(n)
	want := fmt.Sprintf("merging it conflicted %d times", maxBounces+1)
	if tk.Column != api.ColumnReview || tk.FailedStep != api.FailedMerge || !strings.Contains(h.lastComment(n).Body, want) {
		t.Fatalf("%s failed %q, comment:\n%s", tk.Column, tk.FailedStep, h.lastComment(n).Body)
	}
	h.checkClaimEnded(n)
}
