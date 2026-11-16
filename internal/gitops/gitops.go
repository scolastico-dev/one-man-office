// Package gitops wraps the git operations omo performs: worktrees and
// branches for its agents, and merges, serialized per repository.
//
// Merge commits are built without a checkout (PrepareMerge); the target
// branch is then advanced (AdvanceBranch). The only command omo runs in a
// folder the user works in is a fast-forward (git merge --ff-only), and only
// after a read-only check found nothing in its way, so omo never leaves a
// repository in the middle of a merge. When that check finds something in
// the way, nothing is run and nothing is written there; when git itself
// refuses the fast-forward for a reason the check cannot foresee (signature
// verification, a file that appeared meanwhile), git still rewrites
// .git/ORIG_HEAD, and changes no file, index entry or ref.
package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/branchname"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

// Default deadlines for git invocations. Read-only queries on a healthy
// checkout answer at once, so a longer wait means a hung mount; operations
// that write or traverse the work tree may legitimately take minutes.
const (
	queryTimeout = 10 * time.Second
	opTimeout    = 5 * time.Minute
	// killGrace is how long git has to clean up after SIGTERM before it is
	// killed.
	killGrace = 2 * time.Second
)

// ErrTimeout marks a git command that was killed at its deadline.
var ErrTimeout = errors.New("timed out")

type Git struct {
	mu sync.Mutex
	// locks holds one mutex per repository, keyed by its common git
	// directory; lockKeys caches that key per path a caller passed.
	locks       map[string]*sync.Mutex
	lockKeys    map[string]string
	environment []string
	// agentRoots are the folders agents control (SetAgentFolders).
	agentRoots []string
	// query and op bound read-only queries and long operations; tests
	// shorten them.
	query, op time.Duration
	// grace is the time between SIGTERM and SIGKILL on a timeout.
	grace time.Duration
	// pause, when set, is called at named points between a check and the
	// step that relies on it. Only tests set it, to change the repository
	// in the window a concurrent user could.
	pause func(point string)
}

func New() *Git {
	return &Git{locks: map[string]*sync.Mutex{}, lockKeys: map[string]string{}, query: queryTimeout, op: opTimeout, grace: killGrace}
}

// SetEnvironment replaces the environment inherited by Git subprocesses.
// Until it is called they get omo's own environment without omo's control
// credentials, as agent processes do; git runs repository hooks with it.
// A copy is retained so configuration reloads cannot mutate commands already
// being prepared.
func (g *Git) SetEnvironment(environment []string) {
	g.mu.Lock()
	g.environment = append([]string(nil), environment...)
	g.mu.Unlock()
}

// at calls the pause hook, if any, at point.
func (g *Git) at(point string) {
	if g.pause != nil {
		g.pause(point)
	}
}

// repoLock returns the mutex that serializes omo's operations on the
// repository at repo. Every path into one repository (its main folder, a
// linked worktree, a subfolder, a symlink) gets the same mutex, because the
// key is the repository's common git directory.
func (g *Git) repoLock(repo string) *sync.Mutex {
	key := g.lockKey(repo)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.locks[key] == nil {
		g.locks[key] = &sync.Mutex{}
	}
	return g.locks[key]
}

// lockKey resolves repo to its common git directory once and caches it. A
// path that is not (yet) inside a repository falls back to the cleaned path
// and is resolved again next time.
func (g *Git) lockKey(repo string) string {
	g.mu.Lock()
	key, ok := g.lockKeys[repo]
	g.mu.Unlock()
	if ok {
		return key
	}
	key, ok = commonGitDir(repo)
	if !ok {
		if abs, err := filepath.Abs(repo); err == nil {
			return abs
		}
		return repo
	}
	g.mu.Lock()
	g.lockKeys[repo] = key
	g.mu.Unlock()
	return key
}

// commonGitDir finds the common git directory of the repository that
// contains path, without running git: the nearest .git directory or the
// directory a .git file points to, followed through its commondir file
// (linked worktrees), or path itself when it is a bare repository.
func commonGitDir(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		gitDir := worktreeGitDir(dir)
		if gitDir == "" && isGitDir(dir) {
			gitDir = dir
		}
		if gitDir != "" {
			common := gitDir
			if raw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
				common = strings.TrimSpace(string(raw))
				if !filepath.IsAbs(common) {
					common = filepath.Join(gitDir, common)
				}
			}
			if real, err := filepath.EvalSymlinks(common); err == nil {
				common = real
			}
			return filepath.Clean(common), true
		}
		if filepath.Dir(dir) == dir {
			return "", false
		}
	}
}

// isGitDir reports whether dir looks like a git directory (a bare
// repository): it has HEAD, objects and refs.
func isGitDir(dir string) bool {
	for _, name := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

// command builds a git process. It is the only place in the package that
// does: a command in a resolved worktree (at.wt) gets that worktree's
// repository, work tree and common directory set explicitly and the
// settings of worktreeConfig; any other command lets git find its
// repository in at.dir and is refused when that folder lies in a folder
// agents control (ErrAgentFolder). extraEnv is added to the environment.
func (g *Git) command(ctx context.Context, at where, extraEnv []string, args []string) (*exec.Cmd, error) {
	if at.wt == nil {
		if err := g.checkDiscovery(at.dir); err != nil {
			return nil, err
		}
	} else {
		args = append(append([]string(nil), at.wt.config()...), args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = at.folder()
	g.mu.Lock()
	env := append([]string(nil), g.environment...)
	g.mu.Unlock()
	if env == nil {
		env = session.ProcessEnvironment(nil)
	}
	env = append(env, extraEnv...)
	if at.wt != nil {
		env = append(env, at.wt.env()...)
	}
	cmd.Env = env
	return cmd, nil
}

// run executes git in the repository folder dir and kills it, with its
// children, when ctx ends or the timeout d elapses. A timeout is reported as
// an error wrapping ErrTimeout.
func (g *Git) run(ctx context.Context, d time.Duration, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	cmd, err := g.command(ctx, inRepo(dir), nil, args)
	if err != nil {
		return "", err
	}
	afterWait := killWithChildren(cmd, g.grace)
	// Children that outlive git must not keep the output pipe open forever.
	cmd.WaitDelay = g.grace
	out, err := cmd.CombinedOutput()
	afterWait()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return string(out), fmt.Errorf("git %v: %w after %s%s", args, ErrTimeout, d, indexLockNote(dir, ""))
		}
		return string(out), fmt.Errorf("git %v: %w\n%s", args, err, out)
	}
	return string(out), nil
}

// queryRun runs a read-only query with the short deadline.
func (g *Git) queryRun(dir string, args ...string) (string, error) {
	return g.run(context.Background(), g.query, dir, args...)
}

// opRun runs an operation with the long deadline.
func (g *Git) opRun(dir string, args ...string) (string, error) {
	return g.run(context.Background(), g.op, dir, args...)
}

// AddWorktreeFromBase creates branch in dir from base. An empty base retains
// git's normal behavior of using the repository checkout's current HEAD.
// The repository checkout is never changed.
func (g *Git) AddWorktreeFromBase(repo, dir, branch, base string) error {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	if branch == "" || strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	// --end-of-options: neither the folder nor the base can be read as an
	// option.
	args := []string{"worktree", "add", "-b", branch, "--end-of-options", dir}
	if base != "" {
		args = append(args, base)
	}
	_, err := g.opRun(repo, args...)
	return err
}

// AddWorktreeNewBranch picks a branch name and creates it from base in dir,
// both under the repository's lock, so two tickets started at the same time
// never pick the same name. pick is given a function that reports whether a
// name is taken in repo and returns the name to create. A name is taken by a
// local branch or a remote-tracking branch of that name (of any remote), and
// by one below it (name/...), which keeps git from creating it; a branch
// that is a folder of the name (a for a/b) is a *PathConflictError, since no
// suffix to the name helps. It returns the name created.
//
// The name is checked again before git runs: it must be free and a valid
// branch name (branchname.Valid, git check-ref-format). The branch is then
// created on its own, by a ref update that fails when the branch exists
// (git update-ref refs/heads/<name> <base> ""), and only then checked out
// in dir: a branch someone made after the check is never taken over. When
// git fails, the error is a *NewBranchError, which says whether this call
// created the branch (only then is it the caller's to remove, at base). An
// empty base is the checkout's HEAD.
func (g *Git) AddWorktreeNewBranch(repo, dir, base string, pick func(taken func(name string) (bool, error)) (string, error)) (string, error) {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	taken := func(name string) (bool, error) { return g.branchTaken(repo, name) }
	branch, err := pick(taken)
	if err != nil {
		return "", err
	}
	if err := branchname.Valid(branch); err != nil {
		return "", err
	}
	if err := g.checkBranchName(repo, branch); err != nil {
		return "", err
	}
	if t, err := taken(branch); err != nil {
		return "", err
	} else if t {
		return "", fmt.Errorf("the branch %s exists already", branch)
	}
	if base == "" {
		base = "HEAD"
	}
	baseID, err := g.commitOf(repo, base)
	if err != nil {
		return "", err
	}
	g.at("create-branch")
	create := append(append([]string(nil), noHooks...), "update-ref", "-m", "omo: new branch", "refs/heads/"+branch, baseID, "")
	if _, err := g.opRun(repo, create...); err != nil {
		return "", &NewBranchError{Branch: branch, Err: err}
	}
	// git takes the existing branch of this name, not a tag or another ref
	// that shares it.
	if _, err := g.opRun(repo, "worktree", "add", "--end-of-options", dir, branch); err != nil {
		return "", &NewBranchError{Branch: branch, Created: true, Err: err}
	}
	return branch, nil
}

// NewBranchError is a failed AddWorktreeNewBranch after the name was picked.
// Created says this call created the branch (a ref update that only creates
// succeeded) and could not check it out; the branch then still points at
// the base it was created at.
type NewBranchError struct {
	Branch  string
	Created bool
	Err     error
}

func (e *NewBranchError) Error() string { return e.Err.Error() }
func (e *NewBranchError) Unwrap() error { return e.Err }

// PathConflictError says that a branch Name cannot be created because the
// branch Existing (local, or remote-tracking as "<remote>/<branch>") is one
// of its folders: git keeps a ref and refs below it apart.
type PathConflictError struct {
	Name, Existing string
}

func (e *PathConflictError) Error() string {
	return fmt.Sprintf("the branch %s exists, so git cannot create %s below it; rename that branch or change the branch template", e.Existing, e.Name)
}

// branchTaken reports whether name is taken in repo for a new branch (see
// AddWorktreeNewBranch). Names are compared folded (branchname.Fold).
func (g *Git) branchTaken(repo, name string) (bool, error) {
	out, err := g.queryRun(repo, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return false, err
	}
	taken := false
	for _, ref := range strings.Split(out, "\n") {
		ref = strings.TrimSpace(ref)
		var branch, shown string
		if rest, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			branch, shown = rest, rest
		} else if rest, ok := strings.CutPrefix(ref, "refs/remotes/"); ok {
			remote, b, found := strings.Cut(rest, "/")
			if !found || b == "HEAD" {
				continue
			}
			branch, shown = b, remote+"/"+b
		} else {
			continue
		}
		switch {
		case branchname.Same(branch, name), branchname.Below(branch, name):
			taken = true
		case branchname.Below(name, branch):
			return false, &PathConflictError{Name: name, Existing: shown}
		}
	}
	return taken, nil
}

// CurrentBranch returns the branch checked out in repo.
func (g *Git) CurrentBranch(repo string) (string, error) {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	out, err := g.queryRun(repo, "branch", "--show-current")
	return strings.TrimSpace(out), err
}

// indexLockNote tells, for an error message, whether a timed-out command
// left index.lock in gitDir, the git directory of the worktree dir; an empty
// gitDir is read from the .git entry of dir (a repository folder the user
// owns) instead of asking git, which may be what hung.
func indexLockNote(dir, gitDir string) string {
	if gitDir == "" {
		gitDir = worktreeGitDir(dir)
	}
	if gitDir == "" {
		return ""
	}
	lock := filepath.Join(gitDir, "index.lock")
	if _, err := os.Lstat(lock); err == nil {
		return fmt.Sprintf("; %s remains: git was stopped while it may have been updating the files in %s; check them, and remove the lock only once no git command runs there", lock, dir)
	}
	return fmt.Sprintf("; no index.lock was left in %s", gitDir)
}

// worktreeGitDir returns the git directory named by the .git entry of the
// folder dir of a repository the user owns, or "" when dir has none. It
// never follows a link and never blocks on a pipe.
func worktreeGitDir(dir string) string {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return dotGit
	}
	raw, err := readSmallFile(dotGit, 1<<16)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, "gitdir:") {
		return ""
	}
	p := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p)
}

// ProtectedBranches are branch names omo never creates, pushes, moves or
// deletes as a ticket's branch, whatever the branch template gives: main and
// master, and every branch a remote-tracking HEAD of repo points to (each
// remote's default branch as last fetched; read locally, no network). Look
// a name up with branchname.In.
func (g *Git) ProtectedBranches(repo string) ([]string, error) {
	out := []string{"main", "master"}
	refs, err := g.queryRun(repo, "for-each-ref", "--format=%(refname) %(symref)", "refs/remotes")
	if err != nil {
		return out, err
	}
	for _, line := range strings.Split(refs, "\n") {
		ref, target, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !strings.HasSuffix(ref, "/HEAD") || target == "" {
			continue
		}
		rest, ok := strings.CutPrefix(target, "refs/remotes/")
		if !ok {
			continue
		}
		if _, name, found := strings.Cut(rest, "/"); found && name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}
