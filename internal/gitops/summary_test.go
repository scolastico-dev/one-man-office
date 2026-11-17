package gitops

import (
	"context"
	"testing"
)

func TestSummarizeListsCommitsAndFilesSinceTheMergeBase(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	one := commitOn(t, repo, "topic", "add a", map[string]string{"a.txt": "a\nb\n"})
	two := commitOn(t, repo, "topic", "change a, add c", map[string]string{"a.txt": "a\n", "c.txt": "c\n"})
	// main moves on after the branch was cut: its commit is not the topic's.
	commitOn(t, repo, "main", "on main", map[string]string{"m.txt": "m\n"})
	before := snapshot(t, repo)

	s, err := g.Summarize(context.Background(), repo, BranchRef("main"), two, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if s.TotalCommits != 2 || len(s.Commits) != 2 || s.Commits[0] != (Commit{two, "change a, add c"}) || s.Commits[1].ID != one {
		t.Fatalf("commits %d %+v", s.TotalCommits, s.Commits)
	}
	if len(s.Files) != 2 || s.MoreFiles {
		t.Fatalf("files %+v more %v", s.Files, s.MoreFiles)
	}
	byPath := map[string]FileChange{}
	for _, f := range s.Files {
		if f.Patch != nil {
			t.Errorf("%s carries a patch", f.Path)
		}
		byPath[f.Path] = f
	}
	if a := byPath["a.txt"]; a.Additions != 1 || a.Status != "added" {
		t.Errorf("a.txt %+v", a)
	}
	if c := byPath["c.txt"]; c.Additions != 1 || c.Status != "added" {
		t.Errorf("c.txt %+v", c)
	}
	if _, ok := byPath["m.txt"]; ok {
		t.Error("a file of main counts as the topic's")
	}

	capped, err := g.Summarize(context.Background(), repo, BranchRef("main"), two, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if capped.TotalCommits != 2 || len(capped.Commits) != 1 || len(capped.Files) != 1 || !capped.MoreFiles {
		t.Fatalf("capped %+v", capped)
	}
	if _, err := g.Summarize(context.Background(), repo, "main", two, 1, 1); err == nil {
		t.Fatal("a base that is not a full ref was taken")
	}
	sameSnapshot(t, "the repository", before, snapshot(t, repo))
}
