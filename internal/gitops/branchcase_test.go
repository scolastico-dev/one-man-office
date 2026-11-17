package gitops

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/branchname"
)

// On macOS and Windows git keeps JIRA-1-fix and jira-1-fix in one file. On
// Linux the two are different branches; omo treats them as one on every
// system, so each decision below is the one a case-insensitive file system
// needs. (Linux cannot show git itself mixing them up.)

func TestBranchTakenIgnoresCase(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	tg(t, repo, "branch", "JIRA-1-Fix")
	tg(t, repo, "branch", "Team")
	tg(t, repo, "update-ref", "refs/remotes/origin/Far/Away", "main")
	for _, name := range []string{"jira-1-fix", "JIRA-1-FIX", "far"} {
		if taken, err := g.branchTaken(repo, name); err != nil || !taken {
			t.Errorf("%s: taken %v, %v", name, taken, err)
		}
	}
	var conflict *PathConflictError
	if _, err := g.branchTaken(repo, "team/x"); !errors.As(err, &conflict) || conflict.Existing != "Team" {
		t.Errorf("team/x: %v", err)
	}
	// So no branch is created over it, and nothing of it is undone.
	before := rev(t, repo, "JIRA-1-Fix")
	_, err := g.AddWorktreeNewBranch(repo, filepath.Join(t.TempDir(), "w"), "main", func(func(string) (bool, error)) (string, error) { return "jira-1-fix", nil })
	var failed *NewBranchError
	if err == nil || errors.As(err, &failed) {
		t.Fatalf("created over a branch of another case: %v", err)
	}
	if rev(t, repo, "JIRA-1-Fix") != before || strings.Contains(refs(t, repo), "refs/heads/jira-1-fix") {
		t.Fatal("the branch of another case changed")
	}
}

func TestCheckedOutIgnoresCase(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	tg(t, repo, "branch", "Feature")
	tg(t, repo, "branch", "feature")
	wt := filepath.Join(t.TempDir(), "wt")
	tg(t, repo, "worktree", "add", wt, "Feature")
	at, found, err := g.WorktreeOf(repo, "feature")
	if err != nil || !found || !samePathT(at, wt) {
		t.Fatalf("checkout of Feature not found for feature: %q %v %v", at, found, err)
	}
	// A branch whose other-case twin is checked out is not deleted.
	if err := g.DeleteBranch(repo, "feature", "refs/heads/feature"); err == nil || !strings.Contains(err.Error(), "checked out") {
		t.Fatalf("delete: %v", err)
	}
	// Nor moved by a fast-forward in that worktree (on Linux it is another
	// branch there).
	next := commitOn(t, repo, "main", "next", map[string]string{"n.txt": "n"})
	from := rev(t, repo, "feature")
	if err := g.AdvanceBranch(repo, "feature", from, next); err == nil {
		t.Fatal("advanced through the checkout of Feature")
	} else if !strings.Contains(err.Error(), "only in case") {
		t.Fatalf("advance: %v", err)
	}
	if rev(t, repo, "Feature") != from || rev(t, repo, "feature") != from {
		t.Fatal("a branch moved")
	}
}

func samePathT(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

func TestProtectedLookupIgnoresCase(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	tg(t, repo, "update-ref", "refs/remotes/origin/Develop", "main")
	tg(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/Develop")
	got, err := g.ProtectedBranches(repo)
	if err != nil || !branchname.In(got, "develop") || !branchname.In(got, "MAIN") {
		t.Fatalf("%v, %v", got, err)
	}
}
