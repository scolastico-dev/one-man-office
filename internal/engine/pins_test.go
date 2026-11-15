package engine

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/settings"
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

func TestSquashRetryDoesNotReapplyWhatTheUserReverted(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	a, b := h.ids[0], h.ids[1]
	squash := api.StoryMergeSquash
	for _, id := range h.ids {
		if _, err := h.st.UpdateProject(id, settings.ProjectUpdate{StoryMerge: &squash}); err != nil {
			t.Fatal(err)
		}
	}
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{a, b}, Review: api.ReviewSkipped})
	h.startStory(s)
	ta := h.storyTask(s, a, "In a")
	h.finishTask(ta, a, "new.txt", "a\n")
	h.steps(2)
	tb := h.storyTask(s, b, "In b")
	h.finishTask(tb, b, "a.txt", "b's a\n")
	h.steps(2)
	write(t, h.repos[b], "a.txt", "user's uncommitted a\n")
	h.handIn(s)
	h.step()
	if f := h.ticket(s).FailedStep; f != api.FailedMerge {
		t.Fatalf("failed step %q, want merge (project b is blocked)", f)
	}
	gitRun(t, h.repos[a], "cat-file", "-e", "main:new.txt")
	// The user reverts the story's change in project a.
	gitRun(t, h.repos[a], "rm", "-q", "new.txt")
	gitRun(t, h.repos[a], "commit", "-q", "-m", "revert new.txt")
	mainA := gitRun(t, h.repos[a], "rev-parse", "main")
	gitRun(t, h.repos[b], "checkout", "--", "a.txt")
	if _, err := h.b.Retry(h.ctx, board.User, s); err != nil {
		t.Fatal(err)
	}
	h.steps(2)

	if got := gitRun(t, h.repos[a], "rev-parse", "main"); got != mainA {
		t.Fatal("the retry merged the story into project a again")
	}
	tk := h.ticket(s)
	body := h.lastComment(s).Body
	if tk.FailedStep != api.FailedMerge || !strings.Contains(body, "already received story") || !strings.Contains(body, "changed since") {
		t.Fatalf("failed %q, comment:\n%s", tk.FailedStep, body)
	}
}

func TestCleanupKnowsTheSquashCommit(t *testing.T) {
	h := gitHarness(t)
	squash := api.StoryMergeSquash
	if _, err := h.st.UpdateProject(h.project, settings.ProjectUpdate{StoryMerge: &squash}); err != nil {
		t.Fatal(err)
	}
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Squashed", Review: api.ReviewSkipped})
	h.startStory(s)
	task := h.storyTask(s, h.project, "One")
	h.finishTask(task, h.project, "one.txt", "1\n")
	h.steps(2)
	h.handIn(s)
	var objects string
	h.e.pause = func(point string) {
		if point == "before-remove" && objects == "" {
			// Right after the squash, the user reverts part of it; from
			// then on signing would fail.
			gitRun(t, h.repo, "rm", "-q", "one.txt")
			gitRun(t, h.repo, "commit", "-q", "-m", "revert one.txt")
			gitRun(t, h.repo, "config", "commit.gpgsign", "true")
			gitRun(t, h.repo, "config", "gpg.program", "false")
			objects = gitRun(t, h.repo, "count-objects", "-v")
		}
	}
	h.steps(2)
	h.e.pause = nil
	if c := h.ticket(s).Column; c != api.ColumnDone {
		t.Fatalf("story is %s", c)
	}
	if len(h.branches(h.repo)) != 0 {
		t.Fatalf("the squashed story's branch was kept: %v", h.branches(h.repo))
	}
	if got := gitRun(t, h.repo, "count-objects", "-v"); got != objects {
		t.Fatalf("the cleanup wrote objects:\n%s\n%s", objects, got)
	}
}
