//go:build !windows

package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckWorktreeGitFile(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	ctx := context.Background()
	fresh := func(name string) string {
		t.Helper()
		return branchWorktree(t, repo, name)
	}

	// What git wrote passes.
	wt := fresh("ok")
	if err := g.CheckWorktreeGitFile(ctx, repo, wt); err != nil {
		t.Fatalf("the worktree git made: %v", err)
	}

	// A .git directory with its own config (an fsmonitor hook) is refused.
	dir := fresh("dir")
	hostileDotGit(t, dir, filepath.Join(t.TempDir(), "ran"))
	if err := g.CheckWorktreeGitFile(ctx, repo, dir); !errors.Is(err, ErrForeignGitFile) {
		t.Fatalf("replaced by a directory: %v", err)
	}

	// A link to the right gitfile is no gitfile.
	link := fresh("link")
	saved := filepath.Join(t.TempDir(), "gitfile")
	if err := os.Rename(filepath.Join(link, ".git"), saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(saved, filepath.Join(link, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := g.CheckWorktreeGitFile(ctx, repo, link); !errors.Is(err, ErrForeignGitFile) {
		t.Fatalf("replaced by a link: %v", err)
	}

	// A gitfile that points elsewhere: another worktree's records, or a
	// repository of the agent's.
	other := fresh("other")
	elsewhere := fresh("elsewhere")
	otherRecord := strings.TrimSpace(strings.TrimPrefix(read(t, filepath.Join(other, ".git")), "gitdir: "))
	own := t.TempDir()
	tg(t, own, "init", "-q", "-b", "main")
	for _, target := range []string{otherRecord, filepath.Join(own, ".git"), "../wt"} {
		write(t, filepath.Join(elsewhere, ".git"), "gitdir: "+target+"\n")
		if err := g.CheckWorktreeGitFile(ctx, repo, elsewhere); !errors.Is(err, ErrForeignGitFile) {
			t.Fatalf("gitfile pointing to %s: %v", target, err)
		}
	}

	// Extra content after the path is not what git writes.
	extra := fresh("extra")
	raw := read(t, filepath.Join(extra, ".git"))
	write(t, filepath.Join(extra, ".git"), raw+"[core]\n\tfsmonitor = touch x\n")
	if err := g.CheckWorktreeGitFile(ctx, repo, extra); !errors.Is(err, ErrForeignGitFile) {
		t.Fatalf("gitfile with more lines: %v", err)
	}

	// No .git at all, and a folder the repository does not record.
	gone := fresh("gone")
	if err := os.Remove(filepath.Join(gone, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := g.CheckWorktreeGitFile(ctx, repo, gone); !errors.Is(err, ErrForeignGitFile) {
		t.Fatalf("no .git: %v", err)
	}
	if err := g.CheckWorktreeGitFile(ctx, repo, t.TempDir()); !errors.Is(err, ErrNotRecorded) {
		t.Fatalf("a folder that is no worktree: %v", err)
	}
}
