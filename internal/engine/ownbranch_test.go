package engine

import (
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// engineTicket is n as the engine sees it.
func (h *harness) engineTicket(n int64) board.EngineTicket {
	h.t.Helper()
	for _, t := range h.snap().Tickets {
		if t.ID == n {
			return t
		}
	}
	h.t.Fatalf("#%d is not in the snapshot", n)
	return board.EngineTicket{}
}

// The check before a push or delete holds for any name: the branch must be
// the one recorded for the ticket alone, and never its base or a default
// branch of the repository.
func TestCheckOwnBranch(t *testing.T) {
	h := gitHarness(t)
	gitRun(t, h.repo, "update-ref", "refs/remotes/origin/develop", "HEAD")
	gitRun(t, h.repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	n := h.skippedTask("One")
	other := h.skippedTask("Two")
	check := func(branch string) error {
		if err := h.b.SetBranch(h.ctx, n, h.project, branch, ""); err != nil {
			t.Fatal(err)
		}
		tk := h.engineTicket(n)
		return h.e.checkOwnBranch(h.ctx, tk, tk.Projects[0], h.repo, branch)
	}
	for _, branch := range []string{"fix-login", "jb/task/fix-login", "omo/1-old", "omo/old"} {
		if err := check(branch); err != nil {
			t.Errorf("%s: %v", branch, err)
		}
	}
	for branch, why := range map[string]string{
		"main":    "base branch",
		"master":  "default branch",
		"develop": "default branch",
	} {
		if err := check(branch); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%s: %v, want %q", branch, err, why)
		}
	}
	// Recorded on another ticket as well.
	if err := h.b.SetBranch(h.ctx, other, h.project, "shared", ""); err != nil {
		t.Fatal(err)
	}
	if err := check("shared"); err == nil || !strings.Contains(err.Error(), "not for #") {
		t.Errorf("shared: %v", err)
	}
	// A name the ticket does not record.
	if err := check("mine"); err != nil {
		t.Fatal(err)
	}
	tk := h.engineTicket(n)
	if err := h.e.checkOwnBranch(h.ctx, tk, tk.Projects[0], h.repo, "other"); err == nil {
		t.Error("a branch not recorded for the ticket passed")
	}
}

// recordForeign makes ticket n's record name branch, a branch omo did not
// create at the commit n's own branch is at, keeping n's worktree.
func (h *harness) recordForeign(n int64, branch string) {
	h.t.Helper()
	tk := h.ticket(n)
	p := tk.Projects[0]
	gitRun(h.t, h.repo, "branch", "-f", branch, p.Branch)
	if err := h.b.SetBranch(h.ctx, n, h.project, branch, p.Worktree); err != nil {
		h.t.Fatal(err)
	}
}

// Cleanup of a cancelled ticket asked to delete its branch never deletes a
// default branch, whatever the record says.
func TestCancelDeleteBranchKeepsAForeignBranch(t *testing.T) {
	h := gitHarness(t)
	n, dir, _ := h.startedTask("Abandoned")
	h.recordForeign(n, "master")
	tip := gitRun(t, h.repo, "rev-parse", "master")
	h.cancel(n, api.CancelRequest{DeleteBranch: true})
	if pathExists(dir) {
		t.Fatal("the clean worktree stayed")
	}
	if got := gitRun(t, h.repo, "rev-parse", "master"); got != tip {
		t.Fatalf("master is at %s, want %s", got, tip)
	}
	if c := h.commentsWith(n, "omo kept the branch `master`"); c != 1 {
		t.Fatalf("%d comments about the kept branch", c)
	}
}

// Cleanup of a completed ticket deletes its merged branch only when it is
// the ticket's own: a branch recorded on two tickets stays.
func TestCleanupKeepsABranchRecordedTwice(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	branch := h.ticket(n).Projects[0].Branch
	other := h.skippedTask("Other")
	if err := h.b.SetBranch(h.ctx, other, h.project, branch, ""); err != nil {
		t.Fatal(err)
	}
	h.steps(4)
	tk := h.ticket(n)
	if tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
		t.Fatalf("ticket is %s/%s", tk.Column, tk.Resolution)
	}
	if !slices.Contains(h.allBranches(h.repo), branch) {
		t.Fatalf("the branch recorded twice was deleted")
	}
	if c := h.commentsWith(n, "omo kept the branch"); c != 1 {
		t.Fatalf("%d comments about the kept branch", c)
	}
}

// The branch of an older ticket, named before branch templates existed
// (omo/<n>-<title>), is still cleaned up with any template in effect.
func TestOldBranchNamesAreStillCleanedUp(t *testing.T) {
	h := gitHarness(t)
	h.setBranchTemplate("omo/{number}-{slug}")
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	branch := h.ticket(n).Projects[0].Branch
	h.setBranchTemplate("{user}/{slug}")
	h.steps(4)
	if slices.Contains(h.allBranches(h.repo), branch) {
		t.Fatalf("the merged branch %s stayed", branch)
	}
}

// The export pushes only to a branch the remote does not have yet, or has at
// the commit omo pushed before: a remote branch of the same name that omo
// did not push (one the local repository has not fetched, so the name
// picking could not see it) fails the export of that project and stays
// untouched.
func TestExportNeverPushesOntoAForeignRemoteBranch(t *testing.T) {
	h := gitHarness(t)
	h.setBranchTemplate("{slug}")
	bare := h.origin(h.project)
	gitRun(t, h.repo, "push", "-q", bare, "main:refs/heads/main")
	gitRun(t, bare, "update-ref", "refs/heads/export-me", "refs/heads/main")
	foreign := remoteHead(t, bare, "export-me")
	n := h.create(api.CreateTicket{Type: api.TypeTask, Title: "Export me", Review: api.ReviewSkipped, WhenApproved: api.WhenApprovedExport})
	h.finishTask(n, h.project, "b.txt", "b\n")
	h.steps(2)
	tk := h.ticket(n)
	if tk.Projects[0].Branch != "export-me" {
		t.Fatalf("branch %q", tk.Projects[0].Branch)
	}
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageHuman || !strings.Contains(tk.Attention, "the remote has a branch of that name") {
		t.Fatalf("ticket is %s/%s attention %q", tk.Column, tk.ReviewStage, tk.Attention)
	}
	if got := remoteHead(t, bare, "export-me"); got != foreign {
		t.Fatalf("the remote branch moved to %s", got)
	}
	if _, ok := h.exportSession(n); ok {
		t.Fatal("an export job started")
	}
}

// The export of a ticket whose record names its base branch is refused
// before anything is pushed.
func TestExportRefusesTheBaseBranch(t *testing.T) {
	h := gitHarness(t)
	bare := h.origin(h.project)
	n := h.create(api.CreateTicket{Type: api.TypeTask, Title: "Export me", Review: api.ReviewSkipped, WhenApproved: api.WhenApprovedExport})
	h.finishTask(n, h.project, "b.txt", "b\n")
	tk := h.engineTicket(n)
	if err := h.b.SetBranch(h.ctx, n, h.project, "main", tk.Projects[0].Worktree); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.prepareExport(h.ctx, h.engineTicket(n), h.project); err == nil {
		t.Fatal("the export of the base branch was prepared")
	}
	if got := remoteHead(t, bare, "main"); got != "" {
		t.Fatalf("main was pushed: %s", got)
	}
	if c := h.commentsWith(n, "base branch"); c == 0 {
		at := h.ticket(n)
		t.Fatalf("no word of the base branch; attention %q", at.Attention)
	}
}
