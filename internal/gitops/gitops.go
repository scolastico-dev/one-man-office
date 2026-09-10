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
	mu          sync.Mutex
	locks       map[string]*sync.Mutex
	environment []string
}

func New() *Git {
	return &Git{locks: map[string]*sync.Mutex{}}
}

// SetEnvironment replaces the environment inherited by Git subprocesses.
// A copy is retained so configuration reloads cannot mutate commands already
// being prepared.
func (g *Git) SetEnvironment(environment []string) {
	g.mu.Lock()
	g.environment = append([]string(nil), environment...)
	g.mu.Unlock()
}

func (g *Git) repoLock(repo string) *sync.Mutex {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.locks[repo] == nil {
		g.locks[repo] = &sync.Mutex{}
	}
	return g.locks[repo]
}

func (g *Git) run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	g.mu.Lock()
	if g.environment != nil {
		cmd.Env = append([]string(nil), g.environment...)
	}
	g.mu.Unlock()
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
	_, err := g.run(repo, "worktree", "add", "-b", branch, dir)
	return err
}

func (g *Git) RemoveWorktree(repo, dir, branch string) error {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	if _, err := os.Stat(dir); err == nil {
		if _, err := g.run(repo, "worktree", "remove", "--force", dir); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if _, err := g.run(repo, "worktree", "prune"); err != nil {
		return err
	}
	branches, err := g.run(repo, "branch", "--list", branch)
	if err != nil {
		return err
	}
	if strings.TrimSpace(branches) == "" {
		return nil
	}
	_, err = g.run(repo, "branch", "-D", branch)
	return err
}

// MergeBranch merges branch into the repo's checked-out branch with --no-ff.
// On any failure the merge is aborted and ErrMergeConflict is returned.
func (g *Git) MergeBranch(repo, branch string) error {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	if out, err := g.run(repo, "merge", "--no-ff", "--no-edit", branch); err != nil {
		g.run(repo, "merge", "--abort") // best effort; repo must not stay mid-merge
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
	return g.run(repo, "diff", "HEAD..."+branch)
}
