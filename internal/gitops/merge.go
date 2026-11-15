package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MergeStyle selects how PrepareMerge combines two branches.
type MergeStyle string

const (
	// StyleMerge builds a merge commit with the target and the source as
	// parents.
	StyleMerge MergeStyle = "merge"
	// StyleSquash builds one commit on top of the target with the merged
	// tree; the source is not a parent.
	StyleSquash MergeStyle = "squash"
)

// MergeResult is the outcome of PrepareMerge. Exactly one of Commit,
// Conflicts and AlreadyMerged is set.
type MergeResult struct {
	// Commit is the new commit to advance the target to.
	Commit string
	// Conflicts lists the paths that could not be merged.
	Conflicts []string
	// AlreadyMerged reports that the target already contains the source.
	AlreadyMerged bool
}

// Minimum git version: merge-tree --write-tree arrived in 2.38.
const (
	MinMajor = 2
	MinMinor = 38
)

var (
	// ErrCheckoutBlocked marks an advance that git refused in a checkout
	// because of the state of its files: uncommitted changes the merge
	// would overwrite, or an operation (merge, rebase, ...) in progress.
	// Nothing was changed.
	ErrCheckoutBlocked = errors.New("checkout blocks the merge")
	// ErrBranchMoved marks an advance whose branch no longer points at the
	// expected commit. Nothing was changed.
	ErrBranchMoved = errors.New("branch moved")
)

// BlockedError describes an advance refused in the worktree that has the
// branch checked out. It wraps ErrCheckoutBlocked.
type BlockedError struct {
	// Worktree is the folder that has the branch checked out.
	Worktree string
	// Files are the paths git named as standing in the way; empty when the
	// reason is an operation in progress.
	Files []string
	// Output is git's message, or omo's reason when git was not asked.
	Output string
}

func (e *BlockedError) Error() string {
	if len(e.Files) > 0 {
		return fmt.Sprintf("%s: uncommitted changes in %s would be overwritten: %s",
			ErrCheckoutBlocked, e.Worktree, strings.Join(e.Files, ", "))
	}
	return fmt.Sprintf("%s: %s: %s", ErrCheckoutBlocked, e.Worktree, strings.TrimSpace(e.Output))
}

func (e *BlockedError) Unwrap() error { return ErrCheckoutBlocked }

// noHooks keeps the repository's hooks (post-merge, reference-transaction,
// ...) from running for the commands omo runs on its own behalf. It is a
// command-line setting; no configuration file is touched.
var noHooks = []string{"-c", "core.hooksPath=" + os.DevNull}

// cmdOpts configures one git invocation of exec.
type cmdOpts struct {
	timeout time.Duration
	dir     string
	stdin   string
	// cLocale makes git's messages parseable.
	cLocale bool
}

// exec runs git like run, but keeps stdout and stderr apart so that
// machine-readable output is never mixed with warnings. The returned error
// wraps *exec.ExitError when git exited non-zero and ErrTimeout when it was
// killed at its deadline.
func (g *Git) exec(o cmdOpts, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = o.dir
	g.mu.Lock()
	env := append([]string(nil), g.environment...)
	g.mu.Unlock()
	if env == nil {
		env = os.Environ()
	}
	if o.cLocale {
		env = append(env, "LC_ALL=C", "LANGUAGE=")
	}
	cmd.Env = env
	if o.stdin != "" {
		cmd.Stdin = strings.NewReader(o.stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	killWithChildren(cmd)
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out.String(), errOut.String(), fmt.Errorf("git %v: %w after %s", args, ErrTimeout, o.timeout)
		}
		return out.String(), errOut.String(), fmt.Errorf("git %v: %w\n%s%s", args, err, errOut.String(), out.String())
	}
	return out.String(), errOut.String(), nil
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// Version returns the major and minor version of the git on PATH.
func (g *Git) Version() (major, minor int, err error) {
	out, _, err := g.exec(cmdOpts{timeout: g.query}, "version")
	if err != nil {
		return 0, 0, err
	}
	return parseVersion(out)
}

func parseVersion(out string) (major, minor int, err error) {
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, fmt.Errorf("cannot read the git version from %q", strings.TrimSpace(out))
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, nil
}

// CheckVersion reports whether major.minor is new enough for omo.
func CheckVersion(major, minor int) error {
	if major > MinMajor || (major == MinMajor && minor >= MinMinor) {
		return nil
	}
	return fmt.Errorf("omo needs git %d.%d or newer (found %d.%d)", MinMajor, MinMinor, major, minor)
}

// commitOf resolves rev to the full object id of a commit.
func (g *Git) commitOf(repo, rev string) (string, error) {
	out, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo},
		"rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%s is not a commit in %s: %w", rev, repo, err)
	}
	return strings.TrimSpace(out), nil
}

// PrepareMerge builds the result of merging source into target without
// touching any worktree, index or ref: only new objects are written. The
// caller advances target with AdvanceBranch.
func (g *Git) PrepareMerge(repo, target, source string, style MergeStyle, message string) (MergeResult, error) {
	if style != StyleMerge && style != StyleSquash {
		return MergeResult{}, fmt.Errorf("unknown merge style %q", style)
	}
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	t, err := g.commitOf(repo, target)
	if err != nil {
		return MergeResult{}, err
	}
	s, err := g.commitOf(repo, source)
	if err != nil {
		return MergeResult{}, err
	}
	if style == StyleMerge {
		merged, err := g.isAncestor(repo, s, t)
		if err != nil {
			return MergeResult{}, err
		}
		if merged {
			return MergeResult{AlreadyMerged: true}, nil
		}
	}
	out, _, err := g.exec(cmdOpts{timeout: g.op, dir: repo},
		"merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", t, s)
	code := exitCode(err)
	if err != nil && code != 1 {
		return MergeResult{}, err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	tree := strings.TrimSpace(fields[0])
	if code == 1 {
		conflicts := []string{}
		seen := map[string]bool{}
		for _, p := range fields[1:] {
			if p != "" && !seen[p] {
				seen[p] = true
				conflicts = append(conflicts, p)
			}
		}
		if len(conflicts) == 0 {
			return MergeResult{}, fmt.Errorf("git merge-tree reported a conflict without naming files")
		}
		return MergeResult{Conflicts: conflicts}, nil
	}
	if tree == "" {
		return MergeResult{}, fmt.Errorf("git merge-tree returned no tree")
	}
	if style == StyleSquash {
		targetTree, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "rev-parse", "--verify", t+"^{tree}")
		if err != nil {
			return MergeResult{}, err
		}
		if strings.TrimSpace(targetTree) == tree {
			return MergeResult{AlreadyMerged: true}, nil
		}
	}
	args := append([]string(nil), noHooks...)
	args = append(args, "commit-tree", tree, "-p", t)
	if style == StyleMerge {
		args = append(args, "-p", s)
	}
	sign, err := g.signsCommits(repo)
	if err != nil {
		return MergeResult{}, err
	}
	if sign {
		args = append(args, "-S")
	} else {
		args = append(args, "--no-gpg-sign")
	}
	// The message travels on stdin so no text can be read as an option.
	args = append(args, "-F", "-")
	commit, _, err := g.exec(cmdOpts{timeout: g.op, dir: repo, stdin: message}, args...)
	if err != nil {
		return MergeResult{}, err
	}
	return MergeResult{Commit: strings.TrimSpace(commit)}, nil
}

// signsCommits reads commit.gpgSign as git sees it in repo with omo's
// environment (GIT_CONFIG_* variables included). commit-tree ignores that
// setting, so it is applied explicitly.
func (g *Git) signsCommits(repo string) (bool, error) {
	out, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "config", "--type=bool", "--get", "commit.gpgsign")
	if err != nil {
		if exitCode(err) == 1 { // not set
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

// AdvanceBranch moves branch from the commit from forward to the commit to,
// which must descend from from. When a worktree has the branch checked out
// (the user's folder or a linked worktree), the only thing run there is
// `git merge --ff-only`, which changes nothing if uncommitted changes are in
// the way (*BlockedError). Otherwise the ref is updated only if it still
// points at from. Either way a branch that no longer points at from yields
// ErrBranchMoved and is left as it is. A branch already at to is a success.
func (g *Git) AdvanceBranch(repo, branch, from, to string) error {
	if err := g.checkBranchName(repo, branch); err != nil {
		return err
	}
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	fromID, err := g.commitOf(repo, from)
	if err != nil {
		return err
	}
	toID, err := g.commitOf(repo, to)
	if err != nil {
		return err
	}
	ok, err := g.isAncestor(repo, fromID, toID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("cannot advance %s: %s does not descend from %s", branch, toID, fromID)
	}
	ref := "refs/heads/" + branch
	current, err := g.refValue(repo, ref)
	if err != nil {
		return err
	}
	if current == toID {
		return nil
	}
	if current != fromID {
		return fmt.Errorf("%w: %s is at %s, expected %s", ErrBranchMoved, branch, current, fromID)
	}
	wt, found, err := g.worktreeOf(repo, branch)
	if err != nil {
		return err
	}
	if !found {
		args := append([]string(nil), noHooks...)
		args = append(args, "update-ref", "-m", "omo: merge", ref, toID, fromID)
		if _, _, err := g.exec(cmdOpts{timeout: g.op, dir: repo}, args...); err != nil {
			if now, rerr := g.refValue(repo, ref); rerr == nil && now != fromID {
				return fmt.Errorf("%w: %s is at %s, expected %s", ErrBranchMoved, branch, now, fromID)
			}
			return err
		}
		return nil
	}
	return g.fastForwardIn(wt.path, ref, fromID, toID)
}

// fastForwardIn runs the fast-forward in the worktree dir that has ref
// checked out.
func (g *Git) fastForwardIn(dir, ref, fromID, toID string) error {
	if _, err := os.Stat(dir); err != nil {
		return &BlockedError{Worktree: dir, Output: "the worktree that has the branch checked out is missing; restore it or run git worktree prune"}
	}
	if op, err := g.operationInProgress(dir); err != nil {
		return err
	} else if op != "" {
		return &BlockedError{Worktree: dir, Output: op + " is in progress"}
	}
	head, _, err := g.exec(cmdOpts{timeout: g.query, dir: dir}, "symbolic-ref", "-q", "HEAD")
	if err != nil || strings.TrimSpace(head) != ref {
		return fmt.Errorf("%w: %s no longer has %s checked out", ErrBranchMoved, dir, ref)
	}
	headID, err := g.commitOf(dir, "HEAD")
	if err != nil {
		return err
	}
	if headID != fromID {
		return fmt.Errorf("%w: HEAD of %s is at %s, expected %s", ErrBranchMoved, dir, headID, fromID)
	}
	args := append([]string(nil), noHooks...)
	args = append(args, "-c", "core.quotePath=false",
		"merge", "--ff-only", "--no-overwrite-ignore", toID)
	stdout, stderr, err := g.exec(cmdOpts{timeout: g.op, dir: dir, cLocale: true}, args...)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrTimeout) {
		return err
	}
	output := stderr + stdout
	if strings.Contains(output, "Not possible to fast-forward") {
		return fmt.Errorf("%w: %s moved in %s", ErrBranchMoved, ref, dir)
	}
	return &BlockedError{Worktree: dir, Files: blockingFiles(output), Output: output}
}

// blockingFiles collects the paths git lists under its "would be
// overwritten by merge" (and "would be removed by merge") headings.
func blockingFiles(output string) []string {
	var files []string
	seen := map[string]bool{}
	inList := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.Contains(line, "would be overwritten by") || strings.Contains(line, "would be removed by"):
			inList = true
		case inList && strings.HasPrefix(line, "\t"):
			p := strings.TrimPrefix(line, "\t")
			if strings.HasPrefix(p, `"`) {
				if u, err := strconv.Unquote(p); err == nil {
					p = u
				}
			}
			if !seen[p] {
				seen[p] = true
				files = append(files, p)
			}
		default:
			inList = false
		}
	}
	return files
}

// operationInProgress names a merge, rebase, cherry-pick, revert or bisect
// running in the worktree dir, or returns "".
func (g *Git) operationInProgress(dir string) (string, error) {
	gitDir, err := g.gitDir(dir)
	if err != nil {
		return "", err
	}
	for _, c := range []struct{ file, name string }{
		{"MERGE_HEAD", "a merge"},
		{"rebase-merge", "a rebase"},
		{"rebase-apply", "a rebase or am"},
		{"CHERRY_PICK_HEAD", "a cherry-pick"},
		{"REVERT_HEAD", "a revert"},
		{"sequencer", "a cherry-pick or revert"},
		{"BISECT_START", "a bisect"},
	} {
		if _, err := os.Stat(gitDir + string(os.PathSeparator) + c.file); err == nil {
			return c.name, nil
		}
	}
	return "", nil
}

func (g *Git) gitDir(dir string) (string, error) {
	out, _, err := g.exec(cmdOpts{timeout: g.query, dir: dir}, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return fromGitPath(strings.TrimSpace(out)), nil
}

// refValue returns the commit ref points at, or "" when it does not exist.
func (g *Git) refValue(repo, ref string) (string, error) {
	out, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		if exitCode(err) == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// checkBranchName refuses names git would not accept as a branch, and names
// that could be read as an option.
func (g *Git) checkBranchName(repo, branch string) error {
	if branch == "" || strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	if _, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "check-ref-format", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	return nil
}
