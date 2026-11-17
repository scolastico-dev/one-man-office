package gitops

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// doneTicket builds, in a fresh repository, a ticket branch with two
// commits off main, main moved on meanwhile, and the branch merged into
// main with style. It returns the repository and the two commits a done
// ticket records: the merge base and the reviewed tip.
func doneTicket(t *testing.T, g *Git, style MergeStyle) (repo, start, end string) {
	t.Helper()
	repo = newTestRepo(t)
	start = rev(t, repo, "main")
	commitOn(t, repo, "omo/1-x", "first", map[string]string{"f.txt": "f2\n", "new.txt": "new\n"})
	end = commitOn(t, repo, "omo/1-x", "second", map[string]string{"f.txt": "f3\n"})
	commitOn(t, repo, "main", "meanwhile", map[string]string{"other.txt": "o2\n"})
	from := rev(t, repo, "main")
	if mb, err := g.MergeBase(repo, "refs/heads/main", end); err != nil || mb != start {
		t.Fatalf("MergeBase = %q, %v; want %s", mb, err, start)
	}
	res, err := g.PrepareMerge(repo, from, end, style, "Merge: x")
	if err != nil || len(res.Conflicts) > 0 {
		t.Fatalf("PrepareMerge: %+v, %v", res, err)
	}
	if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
		t.Fatal(err)
	}
	return repo, start, end
}

// forget removes the ticket's branch and every reflog entry, then lets git
// drop every object no ref reaches.
func forget(t *testing.T, repo string) {
	t.Helper()
	tg(t, repo, "branch", "-D", "omo/1-x")
	tg(t, repo, "reflog", "expire", "--expire=now", "--all")
	tg(t, repo, "gc", "-q", "--prune=now")
}

func diffPaths(t *testing.T, res DiffResult) []string {
	t.Helper()
	var out []string
	for _, f := range res.Files {
		out = append(out, f.Status+" "+f.Path)
	}
	return out
}

// TestKeptCommitsSurviveBranchDeletionAndGC: the diff of a merged or
// squashed ticket is served from its recorded commits after its branch is
// gone and git collected garbage; without omo's refs, git drops the
// squashed work.
func TestKeptCommitsSurviveBranchDeletionAndGC(t *testing.T) {
	ctx := context.Background()
	for _, style := range []MergeStyle{StyleMerge, StyleSquash} {
		t.Run(string(style), func(t *testing.T) {
			g := testGit(t)
			repo, start, end := doneTicket(t, g, style)
			before, err := g.RangeDiff(ctx, repo, start, end, DefaultDiffLimits)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"modified f.txt", "added new.txt"}
			if got := diffPaths(t, before); !slices.Equal(got, want) {
				t.Fatalf("diff = %v, want %v", got, want)
			}
			if err := g.KeepCommits(repo, 1, []string{start, end}); err != nil {
				t.Fatal(err)
			}
			forget(t, repo)
			after, err := g.RangeDiff(ctx, repo, start, end, DefaultDiffLimits)
			if err != nil {
				t.Fatal(err)
			}
			if got := diffPaths(t, after); !slices.Equal(got, want) || after.MergeBase != start || after.Head != end {
				t.Fatalf("after gc: %v %s..%s", got, after.MergeBase, after.Head)
			}
			fc, err := g.RangeFileDiff(ctx, repo, start, end, "f.txt")
			if err != nil || !strings.Contains(string(fc.Patch), "+f3") {
				t.Fatalf("RangeFileDiff = %q, %v", fc.Patch, err)
			}
			fc, err = g.RangeFileContext(ctx, repo, start, end, "f.txt", MaxDiffContext)
			if err != nil || !strings.Contains(string(fc.Patch), "-f1") {
				t.Fatalf("RangeFileContext = %q, %v", fc.Patch, err)
			}
			if _, err := g.RangeFileDiff(ctx, repo, start, end, "other.txt"); !errors.Is(err, ErrNotInDiff) {
				t.Fatalf("a file of main's own change: %v", err)
			}
		})
	}

	t.Run("without the refs", func(t *testing.T) {
		g := testGit(t)
		repo, start, end := doneTicket(t, g, StyleSquash)
		forget(t, repo)
		if _, err := g.RangeDiff(ctx, repo, start, end, DefaultDiffLimits); !errors.Is(err, ErrNoCommit) {
			t.Fatalf("RangeDiff after gc = %v, want ErrNoCommit", err)
		}
	})
}

func TestKeepCommitsHoldsExactlyTheGivenCommits(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	a := rev(t, repo, "main")
	b := commitOn(t, repo, "omo/1-x", "b", map[string]string{"f.txt": "b\n"})
	c := commitOn(t, repo, "omo/1-x", "c", map[string]string{"f.txt": "c\n"})
	if err := g.KeepCommits(repo, 7, []string{a, b, b}); err != nil {
		t.Fatal(err)
	}
	if err := g.KeepCommits(repo, 8, []string{c}); err != nil {
		t.Fatal(err)
	}
	have := func(n int64) []string {
		t.Helper()
		got, err := g.TicketCommits(repo, n)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got, want := have(7), sortedIDs(a, b); !slices.Equal(got, want) {
		t.Fatalf("#7 keeps %v, want %v", got, want)
	}
	if err := g.KeepCommits(repo, 7, []string{b, c}); err != nil {
		t.Fatal(err)
	}
	if got, want := have(7), sortedIDs(b, c); !slices.Equal(got, want) {
		t.Fatalf("#7 keeps %v, want %v", got, want)
	}
	// Ticket 70's refs are not ticket 7's.
	if err := g.KeepCommits(repo, 70, []string{a}); err != nil {
		t.Fatal(err)
	}
	if err := g.KeepCommits(repo, 7, nil); err != nil {
		t.Fatal(err)
	}
	if got := have(7); len(got) != 0 {
		t.Fatalf("#7 keeps %v after forgetting", got)
	}
	if got := have(70); !slices.Equal(got, []string{a}) || !slices.Equal(have(8), []string{c}) {
		t.Fatalf("other tickets' refs changed: %v %v", got, have(8))
	}
	for _, bad := range []string{"", "HEAD", "main", strings.ToUpper(a), a[:12]} {
		if err := g.KeepCommits(repo, 7, []string{bad}); err == nil {
			t.Errorf("KeepCommits(%q) succeeded", bad)
		}
	}
	missing := strings.Repeat("e", 40)
	if err := g.KeepCommits(repo, 7, []string{missing}); !errors.Is(err, ErrNoCommit) {
		t.Fatalf("KeepCommits of a commit the repository lacks = %v", err)
	}
}

func sortedIDs(ids ...string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

// TestKeptCommitsStayOutOfBranchesAndPushes: omo's refs are no branch, no
// protected branch and are never pushed with an export.
func TestKeptCommitsStayOutOfBranchesAndPushes(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	tg(t, repo, "remote", "add", "origin", remote)
	pinned := commitOn(t, repo, "omo/1-x", "reviewed", map[string]string{"f.txt": "reviewed\n"})
	protectedBefore, err := g.ProtectedBranches(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.KeepCommits(repo, 1, []string{rev(t, repo, "main"), pinned}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(tg(t, repo, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes", "refs/tags")); got != "refs/heads/main\nrefs/heads/omo/1-x" {
		t.Fatalf("branches = %q", got)
	}
	if taken, err := g.branchTaken(repo, "omo"); err != nil || !taken {
		t.Fatalf("branchTaken(omo) = %v, %v", taken, err) // omo/1-x is below it
	}
	if taken, err := g.branchTaken(repo, "tickets"); err != nil || taken {
		t.Fatalf("branchTaken(tickets) = %v, %v", taken, err)
	}
	if protected, err := g.ProtectedBranches(repo); err != nil || !slices.Equal(protected, protectedBefore) {
		t.Fatalf("ProtectedBranches = %v, %v; want %v", protected, err, protectedBefore)
	}
	r, err := g.RemoteFor(context.Background(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.PushCommit(context.Background(), repo, r, pinned, "omo/1-x", ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(refs(t, remote)); strings.Contains(got, "refs/omo/") || strings.Count(got, "\n") != 0 {
		t.Fatalf("the remote has %q", got)
	}
}
