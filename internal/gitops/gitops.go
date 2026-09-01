// Package gitops wraps the git operations omo performs: one worktree+branch
// per developer job, and merges serialized per repo. A failed merge is
// always aborted — the repo is never left mid-merge.
package gitops

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var ErrMergeConflict = errors.New("merge conflict")

type Git struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New() *Git {
	return &Git{locks: map[string]*sync.Mutex{}}
}

func (g *Git) repoLock(repo string) *sync.Mutex {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.locks[repo] == nil {
		g.locks[repo] = &sync.Mutex{}
	}
	return g.locks[repo]
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %v: %w\n%s", args, err, out)
	}
	return string(out), nil
}

func (g *Git) AddWorktree(repo, dir, branch string) error {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	_, err := run(repo, "worktree", "add", "-b", branch, dir)
	return err
}

func (g *Git) RemoveWorktree(repo, dir, branch string) error {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	if _, err := os.Stat(dir); err == nil {
		if _, err := run(repo, "worktree", "remove", "--force", dir); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if _, err := run(repo, "worktree", "prune"); err != nil {
		return err
	}
	branches, err := run(repo, "branch", "--list", branch)
	if err != nil {
		return err
	}
	if strings.TrimSpace(branches) == "" {
		return nil
	}
	_, err = run(repo, "branch", "-D", branch)
	return err
}

// MergeBranch merges branch into the repo's checked-out branch with --no-ff.
// On any failure the merge is aborted and ErrMergeConflict is returned.
func (g *Git) MergeBranch(repo, branch string) error {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	if out, err := run(repo, "merge", "--no-ff", "--no-edit", branch); err != nil {
		run(repo, "merge", "--abort") // best effort; repo must not stay mid-merge
		return fmt.Errorf("%w: %s: %s", ErrMergeConflict, branch, out)
	}
	return nil
}

// Diff returns the changes branch introduces relative to the merge base
// with the current HEAD (git diff HEAD...branch).
func (g *Git) Diff(repo, branch string) (string, error) {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	return run(repo, "diff", "HEAD..."+branch)
}
