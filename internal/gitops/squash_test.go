package gitops

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// objectCount describes the object store of repo, loose objects included.
func objectCount(t *testing.T, repo string) string {
	t.Helper()
	return tg(t, repo, "count-objects", "-v")
}

func TestSquashAppliedOnlyReads(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	commitOn(t, repo, "feature", "feature", map[string]string{"f.txt": "f2\n"})
	// Signing that would fail: the check must never get to it.
	tg(t, repo, "config", "commit.gpgsign", "true")
	tg(t, repo, "config", "gpg.program", "false")
	beforeRefs, beforeObjects := refs(t, repo), objectCount(t, repo)

	applied, err := g.SquashApplied(repo, "main", "feature")
	if err != nil || applied {
		t.Fatalf("not applied yet: %v, %v", applied, err)
	}
	if objectCount(t, repo) != beforeObjects || refs(t, repo) != beforeRefs {
		t.Fatal("the check wrote into the repository")
	}

	// Once main holds the change, a squash would change nothing.
	commitOn(t, repo, "main", "squashed", map[string]string{"f.txt": "f2\n"})
	applied, err = g.SquashApplied(repo, "main", "feature")
	if err != nil || !applied {
		t.Fatalf("applied: %v, %v", applied, err)
	}
	// A conflict is not applied.
	commitOn(t, repo, "main", "other", map[string]string{"f.txt": "f3\n"})
	commitOn(t, repo, "feature", "more", map[string]string{"f.txt": "f4\n"})
	if applied, err := g.SquashApplied(repo, "main", "feature"); err != nil || applied {
		t.Fatalf("conflict: %v, %v", applied, err)
	}
}

func TestBlockedFastForwardLeavesOrigHeadAlone(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	commitOn(t, repo, "feature", "feature", map[string]string{"f.txt": "f2\n"})
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	origHead := filepath.Join(repo, ".git", "ORIG_HEAD")
	write(t, origHead, "0123456789012345678901234567890123456789\n")
	write(t, filepath.Join(repo, "f.txt"), "user's edit\n")
	before := snapshot(t, repo)

	err := g.AdvanceBranch(repo, "main", from, res.Commit)
	var blocked *BlockedError
	if !errors.As(err, &blocked) || len(blocked.Files) != 1 || blocked.Files[0] != "f.txt" {
		t.Fatalf("err = %v, want blocked by f.txt", err)
	}
	if got := read(t, origHead); got != "0123456789012345678901234567890123456789\n" {
		t.Fatalf("ORIG_HEAD rewritten: %q", got)
	}
	sameSnapshot(t, "repo folder", before, snapshot(t, repo))
	if _, err := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); err == nil {
		t.Fatal("a merge was left in progress")
	}
}
