package gitops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// A done ticket's diff is kept as two commits per project (see
// board.RecordCommits): the merge base its reviewed work had with the
// branch it went into, and the reviewed commit. After a squash merge, and
// once the ticket's branch is deleted, nothing reaches the reviewed commit,
// and for a task of a story the merge base can live only on the story's
// branch, which goes too; git's garbage collection would then drop them.
// KeepCommits holds them with refs of omo's own below refs/omo/tickets/,
// one per commit (refs/omo/tickets/<n>/<id>). They are outside refs/heads,
// refs/tags and refs/remotes, so no branch listing, branch-name check,
// ownership check or unreferenced-commit check sees them, and omo pushes
// only the one refspec of an export (PushCommit), never these.

// ticketRefs is the folder of ticket n's refs.
func ticketRefs(n int64) string { return "refs/omo/tickets/" + strconv.FormatInt(n, 10) + "/" }

// ErrNoCommit is wrapped by the error for a recorded commit the repository
// does not have (any more).
var ErrNoCommit = errors.New("no such commit")

// fullID reports whether s is a full object id in lower-case hexadecimal.
func fullID(s string) bool { return commitID.MatchString(s) }

// MergeBase returns the best common ancestor of the commits a and b (full
// refs or object ids) in the repository at repo. It only reads.
func (g *Git) MergeBase(repo, a, b string) (string, error) {
	ai, err := g.commitOf(repo, a)
	if err != nil {
		return "", err
	}
	bi, err := g.commitOf(repo, b)
	if err != nil {
		return "", err
	}
	out, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "merge-base", "--end-of-options", ai, bi)
	if err != nil {
		if exitCode(err) == 1 {
			return "", fmt.Errorf("%s and %s have no common history", displayPath(a), displayPath(b))
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// KeepCommits makes the refs of ticket n in the repository at repo hold
// exactly the commits given (full object ids): a ref for each that lacks
// one is created, a ref of n for any other commit is deleted. Each ref is
// created only where none exists and deleted only at the commit it was read
// at. Nothing is pushed; the refs stay until the commits are no longer
// wanted.
func (g *Git) KeepCommits(repo string, n int64, commits []string) error {
	want := map[string]bool{}
	for _, c := range commits {
		if !fullID(c) {
			return fmt.Errorf("keep commit %q: not a full object id", c)
		}
		want[c] = true
	}
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	have, err := g.ticketCommits(repo, n)
	if err != nil {
		return err
	}
	o := cmdOpts{timeout: g.op, dir: repo}
	for ref, id := range have {
		c := strings.TrimPrefix(ref, ticketRefs(n))
		if want[c] && c == id { // exact: object ids in git's own lower case
			delete(want, c)
			continue
		}
		args := append(append([]string(nil), noHooks...), "update-ref", "-d", ref, id)
		if _, _, err := g.exec(o, args...); err != nil {
			return fmt.Errorf("forget commit %s of #%d: %w", c, n, err)
		}
	}
	missing := make([]string, 0, len(want))
	for c := range want {
		missing = append(missing, c)
	}
	slices.Sort(missing)
	for _, c := range missing {
		if _, err := g.commitOf(repo, c); err != nil {
			return fmt.Errorf("%w: %s in %s", ErrNoCommit, c, displayPath(repo))
		}
		args := append(append([]string(nil), noHooks...), "update-ref", "-m", "omo: keep the diff of a done ticket", ticketRefs(n)+c, c, "")
		if _, _, err := g.exec(o, args...); err != nil {
			return fmt.Errorf("keep commit %s of #%d: %w", c, n, err)
		}
	}
	return nil
}

// ticketCommits maps each ref of ticket n to the object it points at.
func (g *Git) ticketCommits(repo string, n int64) (map[string]string, error) {
	out, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "for-each-ref", "--format=%(refname) %(objectname)", "--end-of-options", ticketRefs(n))
	if err != nil {
		return nil, err
	}
	have := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		ref, id, ok := strings.Cut(line, " ")
		if ok {
			have[ref] = id
		}
	}
	return have, nil
}

// rangeEnds checks that start and end are full object ids of commits the
// repository at repo has; a missing one wraps ErrNoCommit.
func (g *Git) rangeEnds(ctx context.Context, repo, start, end string) error {
	for _, c := range []string{start, end} {
		if !fullID(c) {
			return fmt.Errorf("%w: %q is not a full object id", ErrNoCommit, c)
		}
		_, _, err := g.runCapped(ctx, repo, 4096, "rev-parse", "--verify", "--quiet", "--end-of-options", c+"^{commit}")
		if err != nil {
			if exitCode(err) == 1 {
				return fmt.Errorf("%w: %s", ErrNoCommit, c)
			}
			return err
		}
	}
	return nil
}

// RangeDiff lists what changed from the commit start to the commit end
// (full object ids, as recorded for a done ticket), with bounded patches.
// It only reads.
func (g *Git) RangeDiff(ctx context.Context, repo, start, end string, lim DiffLimits) (DiffResult, error) {
	lim = lim.fill()
	if err := g.rangeEnds(ctx, repo, start, end); err != nil {
		return DiffResult{}, err
	}
	files, more, err := g.collect(ctx, inRepo(repo), nil, []string{"--end-of-options", start, end}, lim, nil)
	if err != nil {
		return DiffResult{}, err
	}
	return DiffResult{Files: files, MoreFiles: more, MergeBase: start, Head: end}, nil
}

// RangeFileDiff is BranchFileDiff for the diff of RangeDiff.
func (g *Git) RangeFileDiff(ctx context.Context, repo, start, end, path string) (FileChange, error) {
	if err := g.rangeEnds(ctx, repo, start, end); err != nil {
		return FileChange{}, err
	}
	return g.fileDiff(ctx, inRepo(repo), nil, []string{"--end-of-options", start, end}, path, nil, DefaultDiffContext, false)
}

// RangeFileContext is BranchFileContext for the diff of RangeDiff.
func (g *Git) RangeFileContext(ctx context.Context, repo, start, end, path string, lines int) (FileChange, error) {
	if err := g.rangeEnds(ctx, repo, start, end); err != nil {
		return FileChange{}, err
	}
	return g.fileDiff(ctx, inRepo(repo), nil, []string{"--end-of-options", start, end}, path, nil, lines, true)
}
