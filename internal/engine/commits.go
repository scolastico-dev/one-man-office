package engine

import (
	"context"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// The commits of a done ticket's diff. On every way from Review to Done
// that merges or exports a project, the engine records the two commits
// the project's diff runs between (board.RecordCommits): the merge base of
// the reviewed commit with the branch it goes into, as that branch is when
// the work goes in (the base the Diff tab compared with), and the reviewed
// commit itself. The merge records them while it builds the merge of each
// target (merger.record) and keeps them once the target holds the work
// (merger.run); the export records and keeps them when it pushes a project
// (prepareExport) or leaves it out (settleExport). The board refuses Done
// without them. The commits are then held in the project's repository by
// refs of omo's own (gitops.KeepCommits), so they outlive the ticket's
// branch, worktree and a squash.

// recordCommits records the diff of project of ticket n in the repository
// at repo as running from the merge base of end and base (a full ref or
// commit id) to end, and keeps those commits.
func (e *Engine) recordCommits(ctx context.Context, n int64, project, repo, base, end string) error {
	start, err := e.git.MergeBase(repo, base, end)
	if err != nil {
		return fmt.Errorf("find where the work of #%d in project %s started: %w", n, project, err)
	}
	return e.recordRange(ctx, n, project, repo, start, end)
}

// recordRange records start and end for project of ticket n and keeps
// them in the repository at repo.
func (e *Engine) recordRange(ctx context.Context, n int64, project, repo, start, end string) error {
	if err := e.b.RecordCommits(ctx, n, project, start, end); err != nil {
		return fmt.Errorf("record the commits of #%d in project %s: %w", n, project, err)
	}
	return e.keepCommits(ctx, n, repo)
}

// keepCommits makes ticket n's refs in each repository of repos hold
// exactly the commits recorded for the ticket's projects in that
// repository (projects sharing one repository share its refs). A project
// omo cannot find keeps nothing. It is used while the ticket is open; the
// refs of a done ticket are never changed.
func (e *Engine) keepCommits(ctx context.Context, n int64, repos ...string) error {
	t, _, ok, err := e.read(ctx, n)
	if err != nil || !ok {
		return err
	}
	for _, repo := range repos {
		var commits []string
		for _, p := range t.Projects {
			proj, found := e.st.Project(p.ID)
			if !found || proj.Missing || p.End == "" {
				continue
			}
			if same, _ := gitops.SameRepository(proj.Path, repo); same {
				commits = append(commits, p.Start, p.End)
				if p.ExportTargetCommit != "" {
					commits = append(commits, p.ExportTargetCommit)
				}
			}
		}
		if err := e.git.KeepCommits(repo, n, commits); err != nil {
			return fmt.Errorf("keep the commits of #%d: %w", n, err)
		}
	}
	return nil
}
