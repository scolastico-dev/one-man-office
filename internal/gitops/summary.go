package gitops

import (
	"context"
	"fmt"
	"strings"
)

// ChangeSummary is what a commit brings compared with a base branch, without
// patches: the commits on its first-parent chain the base lacks and the
// files that differ from the merge base, each with its line counts.
type ChangeSummary struct {
	MergeBase string
	// Commits lists at most the requested number, newest first;
	// TotalCommits counts them all.
	Commits      []Commit
	TotalCommits int
	// Files lists at most the requested number (no patches); MoreFiles says
	// git listed more.
	Files     []FileChange
	MoreFiles bool
}

// Summarize reads what commit (an object id) brings compared with base (the
// full ref of a branch, see BranchRef): at most maxCommits commits and
// maxFiles files. It only reads the repository.
func (g *Git) Summarize(ctx context.Context, repo, base, commit string, maxCommits, maxFiles int) (ChangeSummary, error) {
	baseID, err := g.resolveCommit(ctx, repo, base)
	if err != nil {
		return ChangeSummary{}, err
	}
	head, err := g.commitOf(repo, commit)
	if err != nil {
		return ChangeSummary{}, err
	}
	out, _, err := g.runCapped(ctx, repo, 4096, "merge-base", "--end-of-options", baseID, head)
	if err != nil {
		if exitCode(err) == 1 {
			return ChangeSummary{}, fmt.Errorf("the commit %s and %s have no common history", head, displayPath(strings.TrimPrefix(base, "refs/heads/")))
		}
		return ChangeSummary{}, err
	}
	s := ChangeSummary{MergeBase: strings.TrimSpace(string(out))}
	if s.Commits, s.TotalCommits, err = g.FirstParentLog(repo, head, baseID, maxCommits); err != nil {
		return ChangeSummary{}, err
	}
	files, more, err := g.list(ctx, inRepo(repo), nil, []string{"--end-of-options", s.MergeBase, head})
	if err != nil {
		return ChangeSummary{}, err
	}
	if len(files) > maxFiles {
		files, more = files[:max(maxFiles, 0)], true
	}
	s.MoreFiles = more
	for _, f := range files {
		s.Files = append(s.Files, f.FileChange)
	}
	return s, nil
}
