package gitops

import (
	"path/filepath"
	"testing"
)

func TestFirstParentLogListsSubjectsNewestFirstAndCapped(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	base := rev(t, repo, "main")
	var ids []string
	for i, subject := range []string{"one", "two", "three"} {
		ids = append(ids, commitOn(t, repo, "topic", subject, map[string]string{"f.txt": subject + "\n", "n": string(rune('a' + i))}))
	}
	// A side branch merged into topic: only first parents are listed.
	commitOn(t, repo, "side", "side work", map[string]string{"side.txt": "s\n"})
	wt := filepath.Join(t.TempDir(), "w")
	tg(t, repo, "worktree", "add", "-q", wt, "topic")
	tg(t, wt, "merge", "-q", "--no-ff", "-m", "merge side", "side")
	tip := rev(t, wt, "HEAD")
	before := snapshot(t, repo)

	all, total, err := g.FirstParentLog(repo, "refs/heads/topic", base, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Commit{{tip, "merge side"}, {ids[2], "three"}, {ids[1], "two"}, {ids[0], "one"}}
	if total != 4 || len(all) != 4 {
		t.Fatalf("total %d, %d listed: %+v", total, len(all), all)
	}
	for i := range want {
		if all[i] != want[i] {
			t.Fatalf("commit %d is %+v, want %+v", i, all[i], want[i])
		}
	}

	capped, total, err := g.FirstParentLog(repo, tip, base, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(capped) != 2 || capped[1] != want[1] {
		t.Fatalf("capped: total %d, %+v", total, capped)
	}

	none, total, err := g.FirstParentLog(repo, base, tip, 10)
	if err != nil || total != 0 || len(none) != 0 {
		t.Fatalf("contained tip: %+v %d %v", none, total, err)
	}
	if _, _, err := g.FirstParentLog(repo, "refs/heads/missing", base, 10); err == nil {
		t.Fatal("a missing tip gave no error")
	}
	sameSnapshot(t, "the repository", before, snapshot(t, repo))
}
