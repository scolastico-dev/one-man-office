package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/branchname"
)

// worktreeEntry is one record of `git worktree list --porcelain`.
type worktreeEntry struct {
	path     string
	branch   string // full ref, "" when detached or bare
	detached bool
	bare     bool
	locked   bool
	// lockReason is the reason given to `git worktree lock`, if any.
	lockReason string
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
		case field == "locked":
			cur.locked = true
		case strings.HasPrefix(field, "locked "):
			cur.locked = true
			cur.lockReason = strings.TrimPrefix(field, "locked ")
		}
	}
	return list, nil
}

// WorktreeOf returns the worktree of repo (its main folder or a linked
// worktree) that has branch checked out, or that is rebasing or bisecting
// it with a detached HEAD. Names are compared folded (branchname.Fold): on a
// case-insensitive file system a checkout of Fix holds fix too.
func (g *Git) WorktreeOf(repo, branch string) (string, bool, error) {
	wt, found, err := g.worktreeOf(repo, branch)
	return wt.path, found, err
}

func (g *Git) worktreeOf(repo, branch string) (worktreeEntry, bool, error) {
	list, err := g.listWorktrees(repo)
	if err != nil {
		return worktreeEntry{}, false, err
	}
	for _, wt := range list {
		if isBranchRef(wt.branch, branch) {
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
		busy, err := g.operatesOn(repo, wt.path, branch)
		if err != nil {
			return worktreeEntry{}, false, err
		}
		if busy {
			return wt, true, nil
		}
	}
	return worktreeEntry{}, false, nil
}

// operatesOn reports whether a rebase or bisect in the worktree dir of repo
// will return to branch when it finishes. The state files are read from the
// git directory repo records for dir.
func (g *Git) operatesOn(repo, dir, branch string) (bool, error) {
	gitDir, err := g.worktreeRecord(repo, dir)
	if err != nil {
		return false, err
	}
	for _, f := range []string{"rebase-merge/head-name", "rebase-apply/head-name"} {
		if raw, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(f))); err == nil &&
			isBranchRef(strings.TrimSpace(string(raw)), branch) {
			return true, nil
		}
	}
	if raw, err := os.ReadFile(filepath.Join(gitDir, "BISECT_START")); err == nil {
		name := strings.TrimSpace(string(raw))
		if branchname.Same(name, branch) || isBranchRef(name, branch) {
			return true, nil
		}
	}
	return false, nil
}

// isBranchRef reports whether the full ref names the branch, compared
// folded (branchname.Fold).
func isBranchRef(ref, branch string) bool {
	name, ok := strings.CutPrefix(ref, "refs/heads/")
	return ok && branchname.Same(name, branch)
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

// RevParse resolves rev to a full object id. A branch's full ref
// (refs/heads/<name>, optionally peeled with ^{commit}) resolves only to
// that branch (see checkBranchRev).
func (g *Git) RevParse(repo, rev string) (string, error) {
	if err := checkBranchRev(rev); err != nil {
		return "", err
	}
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
	return g.isAncestorIn(inRepo(repo), a, b)
}

func (g *Git) isAncestorIn(at where, a, b string) (bool, error) {
	_, _, err := g.exec(cmdOpts{timeout: g.op, dir: at.dir, wt: at.wt}, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// Status lists the paths in the worktree dir of the repository at repo that
// hold uncommitted work: changes to tracked files and untracked files that
// are not ignored. Ignored files never appear. A clean worktree gives an
// empty slice. The worktree is resolved from repo's records (see
// inWorktree); git reads a private copy of its index (see scratchIndex) and
// takes no optional locks, so it never competes with someone working there.
// Submodules count when they point at another commit; changes inside a
// submodule's own folder are not looked at, because git would find that
// submodule's repository through the agent's folder.
func (g *Git) Status(repo, dir string) ([]string, error) {
	w, err := g.inWorktree(context.Background(), repo, dir)
	if err != nil {
		return nil, err
	}
	return g.status(w)
}

func (g *Git) status(w *worktreeGit) ([]string, error) {
	env, cleanup, err := scratchIndex(context.Background(), w)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	out, _, err := g.exec(cmdOpts{timeout: g.op, wt: w, env: env},
		"--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=dirty")
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
	_, _, err = g.exec(cmdOpts{timeout: g.op, dir: repo}, "worktree", "add", "--end-of-options", abs, branch)
	return err
}

// BranchAt returns the branch the worktree at dir of repo has checked out,
// as git records it; found is false when repo records no worktree there or
// it has no branch checked out (detached or bare).
func (g *Git) BranchAt(repo, dir string) (branch string, found bool, err error) {
	wt, ok, err := g.findWorktree(repo, dir)
	if err != nil || !ok || wt.detached || wt.bare {
		return "", false, err
	}
	name, ok := strings.CutPrefix(wt.branch, "refs/heads/")
	return name, ok && name != "", nil
}

// findWorktree returns the registration of the worktree at dir. git names
// the folder resolved, dir may name it through a link, on Windows by a
// short name or in another case, and the folder may be gone: dir is compared
// as given and in the form of realPath, both as the file system compares
// names.
func (g *Git) findWorktree(repo, dir string) (worktreeEntry, bool, error) {
	list, err := g.listWorktrees(repo)
	if err != nil {
		return worktreeEntry{}, false, err
	}
	want := filepath.Clean(dir)
	wantReal := realPath(dir)
	for _, wt := range list {
		p := filepath.Clean(wt.path)
		if samePathString(p, want) || samePathString(p, wantReal) {
			return wt, true, nil
		}
	}
	return worktreeEntry{}, false, nil
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
	if _, err := os.Lstat(abs); os.IsNotExist(err) {
		return nil, g.forgetMissing(repo, abs)
	}
	w, err := g.inWorktree(context.Background(), repo, abs)
	if err != nil {
		return nil, err
	}
	files, err := g.status(w)
	if err != nil {
		return nil, err
	}
	if len(files) > 0 {
		return files, nil
	}
	if op := operationInProgress(w.gitDir); op != "" {
		return nil, fmt.Errorf("%w: %s is in progress in %s", ErrOperationInProgress, op, abs)
	}
	if err := g.headIsSafe(w, branch); err != nil {
		return nil, err
	}
	// Without --force git checks again and refuses if something appeared
	// since the status above.
	if _, _, err := g.exec(cmdOpts{timeout: g.op, dir: repo}, append(removeConfig(), "worktree", "remove", "--end-of-options", abs)...); err != nil {
		if files, serr := g.status(w); serr == nil && len(files) > 0 {
			return files, nil
		}
		return nil, err
	}
	return nil, nil
}

// headIsSafe reports an error unless HEAD in the worktree w is branch or
// every commit it reaches is also reachable from a branch, tag or remote ref.
func (g *Git) headIsSafe(w *worktreeGit, branch string) error {
	dir := w.dir
	if branch != "" {
		head, _, err := g.exec(cmdOpts{timeout: g.query, wt: w}, "symbolic-ref", "-q", "HEAD")
		if err == nil && isBranchRef(strings.TrimSpace(head), branch) {
			return nil
		}
	}
	out, _, err := g.exec(cmdOpts{timeout: g.op, wt: w},
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

// ErrWorktreeLocked marks a worktree omo will not remove because it was
// locked with `git worktree lock`.
var ErrWorktreeLocked = errors.New("worktree is locked")

// ForceRemoveWorktree removes the worktree at dir and destroys, without
// asking: its uncommitted changes and its untracked and ignored files;
// commits reachable only from a detached HEAD there (they are left in the
// object store, reachable from no ref, until garbage collection drops them);
// and the git directories of submodules kept under the worktree's own git
// directory, with any submodule commits not pushed anywhere. The branch
// checked out there and stashes are kept. A worktree locked with
// `git worktree lock` is refused with an error wrapping ErrWorktreeLocked. A
// folder that is already gone has its registration removed.
func (g *Git) ForceRemoveWorktree(repo, dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	if wt, ok, err := g.findWorktree(repo, abs); err != nil {
		return err
	} else if ok && wt.locked {
		reason := ""
		if wt.lockReason != "" {
			reason = " (" + wt.lockReason + ")"
		}
		return fmt.Errorf("%w: %s%s; unlock it with git worktree unlock first if it may be removed", ErrWorktreeLocked, abs, reason)
	}
	if _, err := os.Lstat(abs); os.IsNotExist(err) {
		return g.forgetMissing(repo, abs)
	}
	// git finds the worktree to remove through links: a link in place of
	// the folder would have it remove the worktree the link points at.
	if _, err := g.inWorktree(context.Background(), repo, abs); err != nil {
		return err
	}
	_, _, err = g.exec(cmdOpts{timeout: g.op, dir: repo}, append(removeConfig(), "worktree", "remove", "--force", "--end-of-options", abs)...)
	return err
}

// removeConfig is put before `git worktree remove`: git checks the worktree
// with a git status of its own there, which gets these settings too.
func removeConfig() []string {
	return []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.untrackedCache=false"}
}

// forgetMissing drops the registration of a worktree whose folder is gone,
// leaving every other registration (also of missing folders) alone. git is
// given the folder as it records it: it cannot resolve a folder that is
// gone, so another spelling of it (a link in a parent, on Windows a short
// name) would name no worktree to it.
func (g *Git) forgetMissing(repo, abs string) error {
	wt, ok, err := g.findWorktree(repo, abs)
	if err != nil || !ok {
		return err
	}
	_, _, err = g.exec(cmdOpts{timeout: g.op, dir: repo}, "worktree", "remove", "--force", "--end-of-options", wt.path)
	return err
}

// DeleteBranch deletes branch from repo if it still points at expected (a
// revision naming a commit, normally the tip the caller checked); a missing
// branch is not an error. The delete is a compare-and-swap: a branch that
// points elsewhere, because commits were added after the caller's check, is
// left alone and an error wrapping ErrBranchMoved is returned. A branch
// checked out (or being rebased or bisected) in any worktree is refused. The
// branch's reflog is deleted with it; its commits stay in the object store
// until garbage collection drops those no other ref reaches.
func (g *Git) DeleteBranch(repo, branch, expected string) error {
	exists, err := g.BranchExists(repo, branch)
	if err != nil || !exists {
		return err
	}
	if expected == "" {
		return fmt.Errorf("delete %s: the expected tip is required", branch)
	}
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	expID, err := g.commitOf(repo, expected)
	if err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	if wt, found, err := g.worktreeOf(repo, branch); err != nil {
		return err
	} else if found {
		return fmt.Errorf("%s is checked out in %s; not deleted", branch, wt.path)
	}
	g.at("delete-branch")
	args := append([]string(nil), noHooks...)
	args = append(args, "update-ref", "-d", ref, expID)
	if _, _, err := g.exec(cmdOpts{timeout: g.op, dir: repo}, args...); err != nil {
		now, rerr := g.refValue(repo, ref)
		if rerr == nil && now == "" {
			return nil
		}
		if rerr == nil && now != expID {
			return fmt.Errorf("%w: %s is at %s, expected %s; not deleted", ErrBranchMoved, branch, now, expID)
		}
		return err
	}
	// A checkout of the branch that slipped in before the delete would now
	// have an unborn HEAD: put the branch back for it.
	if wt, found, err := g.worktreeOf(repo, branch); err == nil && found {
		args := append([]string(nil), noHooks...)
		args = append(args, "update-ref", "-m", "omo: restore", ref, expID, "")
		if _, _, rerr := g.exec(cmdOpts{timeout: g.op, dir: repo}, args...); rerr != nil {
			return fmt.Errorf("%s was checked out in %s while it was deleted and could not be restored at %s: %w", branch, wt.path, expID, rerr)
		}
		return fmt.Errorf("%s was checked out in %s while it was deleted; it was restored at %s", branch, wt.path, expID)
	}
	return nil
}

// WorktreeHead returns the commit HEAD of the worktree dir of the
// repository at repo is at. The worktree is resolved from repo's records
// (see inWorktree).
func (g *Git) WorktreeHead(repo, dir string) (string, error) {
	w, err := g.inWorktree(context.Background(), repo, dir)
	if err != nil {
		return "", err
	}
	return g.commitIn(w.at(), "HEAD")
}

// HasSubmoduleRepos reports whether the linked worktree dir of the
// repository at repo keeps submodule repositories in its own git directory
// (the one repo records for it), where a forced removal would delete them.
func (g *Git) HasSubmoduleRepos(repo, dir string) (bool, error) {
	gitDir, err := g.worktreeRecord(repo, dir)
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(filepath.Join(gitDir, "modules"))
	return err == nil, nil
}
