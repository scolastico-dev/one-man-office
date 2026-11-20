package gitops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// A reviewed conflict resolution may rewrite the previously exported patch.
// The replacement must use the reviewed object, even if its branch moves later.
func TestPushReviewedUpdateReplacesConflictResolvedRebase(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	r := Remote{Name: "origin", URL: remote}
	ctx := context.Background()
	before := commitOn(t, repo, "feature", "feature", map[string]string{"f.txt": "feature\n"})
	if err := g.PushCommit(ctx, repo, r, before, "feature", ""); err != nil {
		t.Fatal(err)
	}
	commitOn(t, repo, "main", "upstream", map[string]string{"f.txt": "upstream\n"})
	wt := filepath.Join(t.TempDir(), "rebase")
	tg(t, repo, "worktree", "add", "-q", wt, "feature")
	if out := tgFails(t, wt, "rebase", "main"); !strings.Contains(out, "CONFLICT") {
		t.Fatalf("expected a rebase conflict: %s", out)
	}
	write(t, filepath.Join(wt, "f.txt"), "upstream and reviewed feature\n")
	tg(t, wt, "add", "f.txt")
	tg(t, wt, "-c", "core.editor=true", "rebase", "--continue")
	reviewed := rev(t, wt, "HEAD")
	tg(t, repo, "worktree", "remove", wt)
	tgFails(t, repo, "merge-base", "--is-ancestor", before, reviewed)
	commitOn(t, repo, "feature", "after review", map[string]string{"f.txt": "unreviewed\n"})

	if err := g.PushReviewedUpdate(ctx, repo, r, reviewed, "feature", before); err != nil {
		t.Fatal(err)
	}
	if got := remoteRef(t, remote, "feature"); got != reviewed {
		t.Fatalf("remote has %s, want reviewed rebase %s", got, reviewed)
	}
	if got := tg(t, remote, "show", "refs/heads/feature:f.txt"); got != "upstream and reviewed feature\n" {
		t.Fatalf("remote file = %q", got)
	}
}

// Removing the ownership requirement, protected-name checks, or exact case
// check must never turn a reviewed rewrite into a takeover of another branch.
func TestPushReviewedUpdateRefusesUnownedOrProtectedBranch(t *testing.T) {
	for _, tc := range []struct {
		name, branch, remoteBranch, previous string
		defaultBranch                        bool
		want                                 error
	}{
		{name: "empty previous", branch: "feature", remoteBranch: "feature", want: ErrForeignBranch},
		{name: "foreign tip", branch: "feature", remoteBranch: "feature", previous: "base", want: ErrForeignBranch},
		{name: "missing remote branch", branch: "feature", previous: "old", want: ErrForeignBranch},
		{name: "case collision", branch: "feature", remoteBranch: "Feature", previous: "old", want: ErrForeignBranch},
		{name: "default branch", branch: "trunk", remoteBranch: "trunk", previous: "old", defaultBranch: true, want: ErrBadRef},
		{name: "main", branch: "main", remoteBranch: "main", previous: "old", want: ErrBadRef},
		{name: "master", branch: "master", remoteBranch: "master", previous: "old", want: ErrBadRef},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := testGit(t)
			repo := newTestRepo(t)
			remote := bareRemote(t)
			base := rev(t, repo, "main")
			old := commitOn(t, repo, "old", "old", map[string]string{"f.txt": "old\n"})
			reviewed := commitOn(t, repo, "reviewed", "reviewed", map[string]string{"f.txt": "reviewed\n"})
			if tc.remoteBranch != "" {
				tg(t, repo, "push", "-q", remote, old+":refs/heads/"+tc.remoteBranch)
			}
			if tc.defaultBranch {
				tg(t, remote, "symbolic-ref", "HEAD", "refs/heads/"+tc.remoteBranch)
			}
			prev := map[string]string{"": "", "base": base, "old": old}[tc.previous]
			before := refs(t, remote)
			err := g.PushReviewedUpdate(context.Background(), repo, Remote{Name: "origin", URL: remote}, reviewed, tc.branch, prev)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got := refs(t, remote); got != before {
				t.Fatalf("remote refs changed:\nbefore: %s\nafter: %s", before, got)
			}
		})
	}
}

// The explicit expected tip must still guard the interval after ls-remote.
func TestPushReviewedUpdateRefusesRemoteRace(t *testing.T) {
	for _, change := range []string{"move", "delete"} {
		t.Run(change, func(t *testing.T) {
			g := testGit(t)
			repo := newTestRepo(t)
			remote := bareRemote(t)
			ctx := context.Background()
			r := Remote{Name: "origin", URL: remote}
			base := rev(t, repo, "main")
			old := commitOn(t, repo, "old", "old", map[string]string{"f.txt": "old\n"})
			reviewed := commitOn(t, repo, "reviewed", "reviewed", map[string]string{"f.txt": "reviewed\n"})
			if err := g.PushCommit(ctx, repo, r, old, "feature", ""); err != nil {
				t.Fatal(err)
			}
			want := base
			g.pause = func(point string) {
				if point == "push" {
					if change == "delete" {
						tg(t, remote, "update-ref", "-d", "refs/heads/feature", old)
						want = ""
					} else {
						tg(t, remote, "update-ref", "refs/heads/feature", base, old)
					}
				}
			}
			if err := g.PushReviewedUpdate(ctx, repo, r, reviewed, "feature", old); !errors.Is(err, ErrPushRejected) {
				t.Fatalf("err = %v, want ErrPushRejected", err)
			}
			if got := remoteRef(t, remote, "feature"); got != want {
				t.Fatalf("remote has %s, want raced tip %s", got, want)
			}
		})
	}
}

func TestPushReviewedUpdateAlreadyExportedIsNoOp(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	remote := bareRemote(t)
	r := Remote{Name: "origin", URL: remote}
	ctx := context.Background()
	commit := commitOn(t, repo, "feature", "reviewed", map[string]string{"f.txt": "reviewed\n"})
	if err := g.PushCommit(ctx, repo, r, commit, "feature", ""); err != nil {
		t.Fatal(err)
	}
	g.pause = func(point string) {
		if point == "push" {
			t.Fatal("already exported commit attempted a push")
		}
	}
	for _, prev := range []string{"", rev(t, repo, "main"), commit} {
		if err := g.PushReviewedUpdate(ctx, repo, r, commit, "feature", prev); err != nil {
			t.Fatalf("already exported with previous %q: %v", prev, err)
		}
	}
	if got := remoteRef(t, remote, "feature"); got != commit {
		t.Fatalf("remote moved to %s", got)
	}
}
