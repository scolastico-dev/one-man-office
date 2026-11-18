package engine

import (
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Exercise cleanup to completion at each hold count. Server integration
// tests cover real shell acquisition/exit; these barriers prove cleanup
// actually ran while both holds, and then only the second hold, remained.
func TestCleanupWaitsForEveryWorktreeHold(t *testing.T) {
	h := gitHarness(t)
	n, dir, branch := h.startedTask("Abandoned")
	first := h.e.HoldWorktree(dir)
	second := h.e.HoldWorktree(dir)
	t.Cleanup(first)
	t.Cleanup(second)
	h.cancel(n, api.CancelRequest{DeleteBranch: true, DiscardUncommitted: true})
	var attempts atomic.Int32
	h.e.pause = func(point string) {
		if point == "cleanup-started" {
			attempts.Add(1)
		}
	}
	t.Cleanup(func() { h.e.pause = nil })
	checkHeld := func() {
		t.Helper()
		before := attempts.Load()
		h.e.retryCleanups()
		h.steps(2)
		if attempts.Load() == before {
			t.Fatal("the held cleanup was never attempted")
		}
		if !pathExists(dir) || !slices.Contains(h.branches(h.repo), branch) {
			t.Fatal("a held worktree or branch was removed")
		}
	}
	checkHeld()
	first()
	checkHeld()
	second()
	h.steps(2)
	if pathExists(dir) || slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("cleanup kept the worktree or branch after the last release")
	}
}

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
