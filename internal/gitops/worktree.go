package gitops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// worktreeEntry is one record of `git worktree list --porcelain`.
type worktreeEntry struct {
	path     string
	branch   string // full ref, "" when detached or bare
	detached bool
	bare     bool
}

func fromGitPath(p string) string { return filepath.FromSlash(p) }

func (g *Git) listWorktrees(repo string) ([]worktreeEntry, error) {
	out, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var list []worktreeEntry
	var cur *worktreeEntry
	for _, field := range strings.Split(out, "\x00") {
		switch {
		case field == "":
			cur = nil
		case strings.HasPrefix(field, "worktree "):
			list = append(list, worktreeEntry{path: fromGitPath(strings.TrimPrefix(field, "worktree "))})
			cur = &list[len(list)-1]
		case cur == nil:
		case strings.HasPrefix(field, "branch "):
			cur.branch = strings.TrimPrefix(field, "branch ")
		case field == "detached":
			cur.detached = true
		case field == "bare":
			cur.bare = true
		}
	}
	return list, nil
}

// WorktreeOf returns the worktree of repo (its main folder or a linked
// worktree) that has branch checked out, or that is rebasing or bisecting
// it with a detached HEAD.
func (g *Git) WorktreeOf(repo, branch string) (string, bool, error) {
	wt, found, err := g.worktreeOf(repo, branch)
	return wt.path, found, err
}

func (g *Git) worktreeOf(repo, branch string) (worktreeEntry, bool, error) {
	list, err := g.listWorktrees(repo)
	if err != nil {
		return worktreeEntry{}, false, err
	}
	ref := "refs/heads/" + branch
	for _, wt := range list {
		if wt.branch == ref {
			return wt, true, nil
		}
	}
	for _, wt := range list {
		if !wt.detached {
			continue
		}
		if _, err := os.Stat(wt.path); err != nil {
			continue
		}
		busy, err := g.operatesOn(wt.path, branch)
		if err != nil {
			return worktreeEntry{}, false, err
		}
		if busy {
			return wt, true, nil
		}
	}
	return worktreeEntry{}, false, nil
}

// operatesOn reports whether a rebase or bisect in the worktree dir will
// return to branch when it finishes.
func (g *Git) operatesOn(dir, branch string) (bool, error) {
	gitDir, err := g.gitDir(dir)
	if err != nil {
		return false, err
	}
	for _, f := range []string{"rebase-merge/head-name", "rebase-apply/head-name"} {
		if raw, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(f))); err == nil &&
			strings.TrimSpace(string(raw)) == "refs/heads/"+branch {
			return true, nil
		}
	}
	if raw, err := os.ReadFile(filepath.Join(gitDir, "BISECT_START")); err == nil {
		name := strings.TrimSpace(string(raw))
		if name == branch || name == "refs/heads/"+branch {
			return true, nil
		}
	}
	return false, nil
}

// BranchExists reports whether refs/heads/<branch> exists in repo.
func (g *Git) BranchExists(repo, branch string) (bool, error) {
	if err := g.checkBranchName(repo, branch); err != nil {
		return false, err
	}
	_, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// RevParse resolves rev to a full object id.
func (g *Git) RevParse(repo, rev string) (string, error) {
	out, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "rev-parse", "--verify", "--quiet", "--end-of-options", rev)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s in %s: %w", rev, repo, err)
	}
	return strings.TrimSpace(out), nil
}

// IsAncestor reports whether commit a is an ancestor of (or equal to)
// commit b.
func (g *Git) IsAncestor(repo, a, b string) (bool, error) {
	ai, err := g.commitOf(repo, a)
	if err != nil {
		return false, err
	}
	bi, err := g.commitOf(repo, b)
	if err != nil {
		return false, err
	}
	return g.isAncestor(repo, ai, bi)
}

// isAncestor takes resolved object ids.
func (g *Git) isAncestor(repo, a, b string) (bool, error) {
	_, _, err := g.exec(cmdOpts{timeout: g.op, dir: repo}, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// Status lists the paths in the worktree dir that hold uncommitted work:
// changes to tracked files and untracked files that are not ignored.
// Ignored files never appear. A clean worktree gives an empty slice. Status
// takes no optional locks, so it never competes with someone working there.
func (g *Git) Status(dir string) ([]string, error) {
	out, _, err := g.exec(cmdOpts{timeout: g.op, dir: dir},
		"--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parseStatus(out), nil
}

func parseStatus(out string) []string {
	files := []string{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		files = append(files, f[3:])
		if f[0] == 'R' || f[0] == 'C' {
			i++ // the original path of a rename or copy follows
		}
	}
	return files
}

// AddWorktreeForBranch checks out the existing branch in a new worktree
// at dir.
func (g *Git) AddWorktreeForBranch(repo, dir, branch string) error {
	if err := g.checkBranchName(repo, branch); err != nil {
		return err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	exists, err := g.BranchExists(repo, branch)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("branch %s does not exist in %s", branch, repo)
	}
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	_, _, err = g.exec(cmdOpts{timeout: g.op, dir: repo}, "worktree", "add", abs, branch)
	return err
}

// registered reports whether dir is a worktree of repo.
func (g *Git) registered(repo, dir string) (bool, error) {
	list, err := g.listWorktrees(repo)
	if err != nil {
		return false, err
	}
	want := filepath.Clean(dir)
	for _, wt := range list {
		if filepath.Clean(wt.path) == want {
			return true, nil
		}
	}
	return false, nil
}

var (
	// ErrUnreferencedCommits marks a worktree that was not removed because
	// its HEAD holds commits no branch, tag or remote ref contains.
	ErrUnreferencedCommits = errors.New("worktree holds commits that no branch, tag or remote ref contains")
	// ErrOperationInProgress marks a worktree that was not removed because
	// a merge, rebase, cherry-pick, revert or bisect is in progress there.
	ErrOperationInProgress = errors.New("an operation is in progress in the worktree")
)

// UnreferencedCommitsError names the commits that removing Worktree would
// leave reachable from no ref. It wraps ErrUnreferencedCommits.
type UnreferencedCommitsError struct {
	Worktree string
	// Commits are full object ids, newest first.
	Commits []string
}

func (e *UnreferencedCommitsError) Error() string {
	const show = 10
	names := make([]string, 0, show)
	for i, c := range e.Commits {
		if i == show {
			break
		}
		if len(c) > 12 {
			c = c[:12]
		}
		names = append(names, c)
	}
	more := ""
	if len(e.Commits) > show {
		more = fmt.Sprintf(" and %d more", len(e.Commits)-show)
	}
	return fmt.Sprintf("%s: %d commit(s) in %s would be lost: %s%s; put them on a branch first",
		ErrUnreferencedCommits, len(e.Commits), e.Worktree, strings.Join(names, ", "), more)
}

func (e *UnreferencedCommitsError) Unwrap() error { return ErrUnreferencedCommits }

// RemoveWorktreeIfClean removes the worktree at dir only when nothing in it
// would be lost; otherwise it removes nothing. When Status finds uncommitted
// work it returns those files. It refuses with an error wrapping
// ErrOperationInProgress while a merge, rebase, cherry-pick, revert or bisect
// is in progress there, and with *UnreferencedCommitsError when HEAD is not
// branch (the branch the caller expects to be checked out there) and holds
// commits that no branch, tag or remote ref contains, such as commits made on
// a detached HEAD. Ignored files do not count and are removed with the
// worktree. Stashes are kept in the main repository and survive the removal.
// A folder that is already gone has its registration removed; an unknown
// folder is a no-op.
func (g *Git) RemoveWorktreeIfClean(repo, dir, branch string) (dirty []string, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		return nil, g.forgetMissing(repo, abs)
	}
	files, err := g.Status(abs)
	if err != nil {
		return nil, err
	}
	if len(files) > 0 {
		return files, nil
	}
	if op, err := g.operationInProgress(abs); err != nil {
		return nil, err
	} else if op != "" {
		return nil, fmt.Errorf("%w: %s is in progress in %s", ErrOperationInProgress, op, abs)
	}
	if err := g.headIsSafe(abs, branch); err != nil {
		return nil, err
	}
	// Without --force git checks again and refuses if something appeared
	// since the status above.
	if _, _, err := g.exec(cmdOpts{timeout: g.op, dir: repo}, "worktree", "remove", abs); err != nil {
		if files, serr := g.Status(abs); serr == nil && len(files) > 0 {
			return files, nil
		}
		return nil, err
	}
	return nil, nil
}

// headIsSafe reports an error unless HEAD in the worktree dir is branch or
// every commit it reaches is also reachable from a branch, tag or remote ref.
func (g *Git) headIsSafe(dir, branch string) error {
	if branch != "" {
		head, _, err := g.exec(cmdOpts{timeout: g.query, dir: dir}, "symbolic-ref", "-q", "HEAD")
		if err == nil && strings.TrimSpace(head) == "refs/heads/"+branch {
			return nil
		}
	}
	out, _, err := g.exec(cmdOpts{timeout: g.op, dir: dir},
		"rev-list", "HEAD", "--not", "--branches", "--tags", "--remotes")
	if err != nil {
		return fmt.Errorf("cannot tell whether %s holds commits no ref contains: %w", dir, err)
	}
	commits := strings.Fields(out)
	if len(commits) > 0 {
		return &UnreferencedCommitsError{Worktree: dir, Commits: commits}
	}
	return nil
}

// ForceRemoveWorktree removes the worktree at dir, discarding uncommitted
// changes. A folder that is already gone has its registration removed.
func (g *Git) ForceRemoveWorktree(repo, dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		return g.forgetMissing(repo, abs)
	}
	_, _, err = g.exec(cmdOpts{timeout: g.op, dir: repo}, "worktree", "remove", "--force", abs)
	return err
}

// forgetMissing drops the registration of a worktree whose folder is gone,
// leaving every other registration (also of missing folders) alone.
func (g *Git) forgetMissing(repo, abs string) error {
	ok, err := g.registered(repo, abs)
	if err != nil || !ok {
		return err
	}
	_, _, err = g.exec(cmdOpts{timeout: g.op, dir: repo}, "worktree", "remove", "--force", abs)
	return err
}

// DeleteBranch deletes branch from repo; a missing branch is not an error.
// Without force git refuses a branch that is not merged into the HEAD of
// repo's folder (or its upstream). git never deletes a branch that is
// checked out in a worktree.
func (g *Git) DeleteBranch(repo, branch string, force bool) error {
	exists, err := g.BranchExists(repo, branch)
	if err != nil || !exists {
		return err
	}
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	flag := "-d"
	if force {
		flag = "-D"
	}
	args := append([]string(nil), noHooks...)
	args = append(args, "branch", flag, "--", branch)
	_, _, err = g.exec(cmdOpts{timeout: g.op, dir: repo}, args...)
	return err
}
