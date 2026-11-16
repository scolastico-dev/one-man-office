package engine

import (
	"context"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/branchname"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// The branch of a ticket is named by a template the user types (see
// BranchName), so its name says nothing about who made it. What makes a
// branch omo's to push or delete is the record: omo creates a ticket's
// branch only when no branch of that name exists (gitops
// AddWorktreeNewBranch) and records it on that ticket alone. checkOwnBranch
// is the one check before omo pushes a ticket's branch or deletes it; the
// merge moves only a ticket's merge target, never its own branch.

// checkOwnBranch reports why omo must not push or delete branch as the
// branch of ticket t in project p of the repository repo, or nil: branch
// must be the branch recorded for t in p, recorded nowhere else in the
// repository (no other ticket, no other project of t there), and
// neither t's base branch nor a protected branch of the repository (main,
// master, a remote's default branch; gitops.ProtectedBranches).
func (e *Engine) checkOwnBranch(ctx context.Context, t board.EngineTicket, p board.EngineProject, repo, branch string) error {
	// exact: the branch must be the recorded name itself.
	if branch == "" || branch != p.Branch {
		return fmt.Errorf("%s is not the branch recorded for #%d in project %s", code(branch), t.ID, code(p.ID))
	}
	if branchname.Same(branch, p.Base) {
		return fmt.Errorf("%s is the base branch of #%d in project %s; omo never pushes or deletes it", code(branch), t.ID, code(p.ID))
	}
	recorded, err := e.recordedBranches(ctx, repo)
	if err != nil {
		return fmt.Errorf("cannot read the branches recorded for project %s: %w", code(p.ID), err)
	}
	var owners []board.RecordedBranch
	for _, r := range recorded {
		if branchname.Same(r.Branch, branch) {
			owners = append(owners, r)
		}
	}
	if len(owners) != 1 || owners[0].Ticket != t.ID || owners[0].Project != p.ID {
		return fmt.Errorf("%s is recorded for %s in the repository of project %s, not for #%d alone; omo leaves it alone", code(branch), recordList(owners), code(p.ID), t.ID)
	}
	protected, err := e.git.ProtectedBranches(repo)
	if err != nil {
		return fmt.Errorf("cannot read the default branches of project %s: %w", code(p.ID), err)
	}
	if branchname.In(protected, branch) {
		return fmt.Errorf("%s is a default branch of project %s; omo never pushes or deletes it", code(branch), code(p.ID))
	}
	return nil
}

// recordedBranches returns the branches recorded on any ticket in a project
// that lies in the repository repo, since branch names belong to the
// repository and several projects can share one (folders of a monorepo). A
// project whose folder omo cannot resolve counts as sharing it.
func (e *Engine) recordedBranches(ctx context.Context, repo string) ([]board.RecordedBranch, error) {
	all, err := e.b.RecordedBranches(ctx)
	if err != nil {
		return nil, err
	}
	shares := map[string]bool{}
	var out []board.RecordedBranch
	for _, r := range all {
		in, ok := shares[r.Project]
		if !ok {
			in = true
			if proj, found := e.st.Project(r.Project); found {
				if same, known := gitops.SameRepository(proj.Path, repo); known {
					in = same
				}
			}
			shares[r.Project] = in
		}
		if in {
			out = append(out, r)
		}
	}
	return out, nil
}

// recordList is "#1 (project a), #2 (project b)" or "no ticket".
func recordList(rs []board.RecordedBranch) string {
	if len(rs) == 0 {
		return "no ticket"
	}
	out := ""
	for i, r := range rs {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("#%d (project %s)", r.Ticket, code(r.Project))
	}
	return out
}

// projectOf is the project id of t, or a zero project with only the id.
func projectOf(t board.EngineTicket, id string) board.EngineProject {
	for _, p := range t.Projects {
		if p.ID == id {
			return p
		}
	}
	return board.EngineProject{ID: id}
}
