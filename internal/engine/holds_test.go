package engine

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// A user shell opened after a forced removal (cancel with "discard
// uncommitted changes") checked for holds, but before it removed the
// folder, keeps the folder; the release lets the removal run.
func TestHoldTakenJustBeforeDiscardKeepsWorktree(t *testing.T) {
	h := gitHarness(t)
	n, dir, branch := h.startedTask("Abandoned")
	write(t, dir, "scratch.txt", "x\n")
	var release func()
	h.e.pause = func(point string) {
		if point == "before-remove" && release == nil {
			release = h.e.HoldWorktree(dir)
		}
	}
	h.cancel(n, api.CancelRequest{DeleteBranch: true, DiscardUncommitted: true})
	h.e.pause = nil
	if release == nil {
		t.Fatal("the discard did not reach the removal")
	}
	if !pathExists(filepath.Join(dir, "scratch.txt")) || !slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("a worktree held just before its forced removal was removed")
	}
	release()
	h.steps(2)
	if pathExists(dir) || slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("the worktree or branch stayed after the hold was released")
	}
}
