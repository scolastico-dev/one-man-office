package engine

import (
	"os"
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

// A folder that is gone is still the same folder under each of its names:
// git names the worktree of a removed folder by its real path, while omo
// may know it through a link (or, on Windows, under a short name).
func TestSamePathOfAMissingFolder(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	symlink(t, real, link)
	for _, rel := range []string{"gone", filepath.Join("gone", "deeper")} {
		a, b := filepath.Join(real, rel), filepath.Join(link, rel)
		if !samePath(a, b) || !samePath(b, a) {
			t.Errorf("samePath(%q, %q) is false", a, b)
		}
		if holdKey(a) != holdKey(b) {
			t.Errorf("holdKey differs: %q and %q", holdKey(a), holdKey(b))
		}
		if samePath(a, filepath.Join(real, rel+"-other")) {
			t.Errorf("%q counts as %q", a, rel+"-other")
		}
	}
	if err := os.Mkdir(filepath.Join(real, "there"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !samePath(filepath.Join(real, "there"), filepath.Join(link, "there")) {
		t.Error("an existing folder is not the same through a link")
	}
}
