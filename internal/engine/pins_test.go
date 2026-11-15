package engine

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// approveReview lets the live reviewer of n approve.
func (h *harness) approveReview(n int64) {
	h.t.Helper()
	rev := h.live(api.RoleReviewer, n)
	if _, err := h.b.Done(h.ctx, h.actor(rev.ID), api.DoneRequest{Verdict: board.VerdictApprove}); err != nil {
		h.t.Fatal(err)
	}
}

func TestReviewedCommitIsMerged(t *testing.T) {
	h := gitHarness(t)
	n := h.standalone("Add b")
	reviewed := h.startTask(n, h.project, "b.txt", "b\n")
	h.handIn(n)
	h.step()
	h.approveReview(n)
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s (attention %q)", c, h.ticket(n).Attention)
	}
	if parents := strings.Fields(gitRun(t, h.repo, "log", "-1", "--format=%P", "main")); len(parents) != 2 || parents[1] != reviewed {
		t.Fatalf("merge parents %v, want the reviewed commit %s", parents, reviewed)
	}
}

func TestCommitAfterVerdictIsNotMerged(t *testing.T) {
	h := gitHarness(t)
	n := h.standalone("Add b")
	reviewed := h.startTask(n, h.project, "b.txt", "b\n")
	h.handIn(n)
	h.step()
	h.approveReview(n)
	// The parked developer, woken by a mention, commits after the verdict.
	late := commit(t, h.worktree(h.project, n), "late.txt", "not reviewed\n")
	mainBefore := gitRun(t, h.repo, "rev-parse", "main")
	h.steps(2)

	tk := h.ticket(n)
	if tk.Column != api.ColumnReview || tk.FailedStep != api.FailedMerge {
		t.Fatalf("ticket is %s, failed %q", tk.Column, tk.FailedStep)
	}
	h.checkClaimEnded(n)
	if got := gitRun(t, h.repo, "rev-parse", "main"); got != mainBefore {
		t.Fatal("main moved although the branch has unreviewed commits")
	}
	body := h.lastComment(n).Body
	for _, want := range []string{"New commits were added", short(late), short(reviewed), "reject the review", "retry"} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment lacks %q:\n%s", want, body)
		}
	}

	// Back at the reviewed commit, a retry merges it.
	gitRun(t, h.worktree(h.project, n), "reset", "-q", "--hard", reviewed)
	if _, err := h.b.Retry(h.ctx, board.User, n); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s after the retry", c)
	}
	gitRun(t, h.repo, "cat-file", "-e", "main:b.txt")
	if err := exec.Command("git", "-C", h.repo, "cat-file", "-e", "main:late.txt").Run(); err == nil {
		t.Fatal("the unreviewed file reached main")
	}
}

func TestMultiProjectStoryPinsEachProject(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	a, b := h.ids[0], h.ids[1]
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{a, b}, Review: api.ReviewSkipped})
	h.startStory(s)
	ta := h.storyTask(s, a, "In a")
	h.finishTask(ta, a, "new.txt", "a\n")
	h.steps(2)
	tb := h.storyTask(s, b, "In b")
	h.finishTask(tb, b, "b.txt", "b\n")
	h.steps(2)
	headA := gitRun(t, h.repos[a], "rev-parse", h.ticket(s).Projects[0].Branch)
	headB := gitRun(t, h.repos[b], "rev-parse", h.ticket(s).Projects[1].Branch)
	mainA, mainB := gitRun(t, h.repos[a], "rev-parse", "main"), gitRun(t, h.repos[b], "rev-parse", "main")
	h.handIn(s)
	pins, err := h.e.pins.reviewed(s, h.ticket(s).ReviewRound)
	if err != nil || pins[a] != headA || pins[b] != headB {
		t.Fatalf("pins %v (%v), want %s and %s", pins, err, headA, headB)
	}
	var late string
	h.e.pause = func(point string) {
		if point == "merge-claimed" && late == "" {
			late = commit(t, h.worktree(b, s), "late.txt", "late\n")
		}
	}
	h.step()
	h.e.pause = nil
	tk := h.ticket(s)
	if tk.FailedStep != api.FailedMerge || !strings.Contains(h.lastComment(s).Body, short(late)) {
		t.Fatalf("failed %q, comment:\n%s", tk.FailedStep, h.lastComment(s).Body)
	}
	if gitRun(t, h.repos[a], "rev-parse", "main") != mainA || gitRun(t, h.repos[b], "rev-parse", "main") != mainB {
		t.Fatal("a project was merged although another one moved after the hand-in")
	}
}

func TestUnmergedCommitsOfCompletedBranchAreNamed(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	dir := h.worktree(h.project, n)
	var after string
	h.e.pause = func(point string) {
		if point == "before-remove" && after == "" {
			after = commit(t, dir, "after.txt", "after the merge\n")
		}
	}
	h.steps(2)
	h.e.pause = nil
	branch := h.ticket(n).Projects[0].Branch
	if branch == "" || !strings.Contains(strings.Join(h.branches(h.repo), " "), branch) {
		t.Fatalf("branch %q was not kept", branch)
	}
	body := h.lastComment(n).Body
	if !strings.Contains(body, short(after)) || !strings.Contains(body, "does not contain these commits") {
		t.Fatalf("comment:\n%s", body)
	}
	h.steps(2)
	if c := h.commentsWith(n, "does not contain these commits"); c != 1 {
		t.Fatalf("%d comments about the kept branch", c)
	}
}

func TestHandInWithoutCheckIsPinnedByThePass(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	reviewed := h.startTask(n, h.project, "b.txt", "b\n")
	dev := h.live(api.RoleDeveloper, n)
	if _, err := h.b.Done(h.ctx, h.actor(dev.ID), api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s (attention %q)", c, h.ticket(n).Attention)
	}
	if parents := strings.Fields(gitRun(t, h.repo, "log", "-1", "--format=%P", "main")); len(parents) != 2 || parents[1] != reviewed {
		t.Fatalf("merge parents %v, want %s", parents, reviewed)
	}
}
