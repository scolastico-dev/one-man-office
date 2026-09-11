// Package gitops wraps the git operations omo performs: one worktree+branch
// per developer job, and merges serialized per repo. A failed merge is
// always aborted — the repo is never left mid-merge.
package gitops

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	return g.AddWorktreeFromBase(repo, dir, branch, "")
}

// AddWorktreeFromBase creates branch in dir from base. An empty base retains
// git's normal behavior of using the repository checkout's current HEAD.
// The repository checkout is never changed.
func (g *Git) AddWorktreeFromBase(repo, dir, branch, base string) error {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	args := []string{"worktree", "add", "-b", branch, dir}
	if base != "" {
		args = append(args, base)
	}
	_, err := g.run(repo, args...)
	return err
}

// EnsureWorktree reconnects an existing managed worktree to Git after a
// restart. It repairs the worktree metadata when possible and otherwise
// registers the already-existing branch at the target path. It never creates
// a new branch or chooses the repository checkout as a base.
func (g *Git) EnsureWorktree(repo, dir, branch string) error {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	if _, err := g.run(repo, "worktree", "repair", dir); err == nil {
		return nil
	}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return readErr
		}
		if len(entries) > 0 {
			return g.reregisterExistingWorktree(repo, dir, branch)
		}
	}
	if _, err := g.run(repo, "worktree", "add", "--force", dir, branch); err != nil {
		return err
	}
	return nil
}

// reregisterExistingWorktree temporarily moves a non-empty worktree aside so
// git can register the existing branch at the target path. The generated
// checkout is discarded and only its fresh .git pointer is retained; all
// original files, including uncommitted changes, are restored unchanged.
func (g *Git) reregisterExistingWorktree(repo, dir, branch string) error {
	parent := filepath.Dir(dir)
	tmp, err := os.MkdirTemp(parent, ".omo-worktree-recovery-")
	if err != nil {
		return err
	}
	original := filepath.Join(tmp, "original")
	if err := os.Rename(dir, original); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	restore := func() error {
		_ = os.RemoveAll(dir)
		return os.Rename(original, dir)
	}
	if _, err := g.run(repo, "worktree", "add", "--force", dir, branch); err != nil {
		_ = restore()
		return err
	}
	gitPointer, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		_ = restore()
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		_ = restore()
		return err
	}
	if err := os.Rename(original, dir); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, ".git")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), gitPointer, 0o644); err != nil {
		return err
	}
	return os.RemoveAll(tmp)
}

// CurrentBranch returns the branch checked out in repo.
func (g *Git) CurrentBranch(repo string) (string, error) {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	out, err := g.run(repo, "branch", "--show-current")
	return strings.TrimSpace(out), err
}

func (g *Git) RemoveWorktree(repo, dir, branch string) error {
	return g.removeWorktree(repo, dir, branch, true)
}

// RemoveWorktreeKeepBranch removes an isolated checkout while retaining its
// branch for a pull request or other external integration.
func (g *Git) RemoveWorktreeKeepBranch(repo, dir, branch string) error {
	return g.removeWorktree(repo, dir, branch, false)
}

func (g *Git) removeWorktree(repo, dir, branch string, deleteBranch bool) error {
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
	if !deleteBranch {
		return nil
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
	return g.MergeBranchInto(repo, repo, branch)
}

// MergeBranchInto merges branch into the target worktree with --no-ff. Both
// the target and source belong to repo, whose mutex serializes the operation.
// On any failure the target merge is aborted and ErrMergeConflict is returned.
func (g *Git) MergeBranchInto(repo, target, branch string) error {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	if out, err := g.run(target, "merge", "--no-ff", "--no-edit", branch); err != nil {
		g.run(target, "merge", "--abort") // best effort; target must not stay mid-merge
		return fmt.Errorf("%w: %s: %s", ErrMergeConflict, branch, out)
	}
	return nil
}

// Diff returns the changes branch introduces relative to the merge base
// with the current HEAD (git diff HEAD...branch).
func (g *Git) Diff(repo, branch string) (string, error) {
	return g.DiffAgainst(repo, repo, branch)
}

// DiffAgainst returns the changes branch introduces relative to the target
// worktree's HEAD (git diff HEAD...branch).
func (g *Git) DiffAgainst(repo, target, branch string) (string, error) {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	return g.run(target, "diff", "HEAD..."+branch)
}
