package engine

import (
	"context"
	"fmt"
	"slices"

	"github.com/scolastico-dev/one-man-office/internal/board"
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
// must be the branch recorded for t in p, recorded for no other ticket, and
// neither t's base branch nor a protected branch of the repository (main,
// master, a remote's default branch; gitops.ProtectedBranches).
func (e *Engine) checkOwnBranch(ctx context.Context, t board.EngineTicket, p board.EngineProject, repo, branch string) error {
	if branch == "" || branch != p.Branch {
		return fmt.Errorf("%s is not the branch recorded for #%d in project %s", code(branch), t.ID, code(p.ID))
	}
	if branch == p.Base {
		return fmt.Errorf("%s is the base branch of #%d in project %s; omo never pushes or deletes it", code(branch), t.ID, code(p.ID))
	}
	recorded, err := e.b.RecordedBranches(ctx, p.ID)
	if err != nil {
		return fmt.Errorf("cannot read the branches recorded for project %s: %w", code(p.ID), err)
	}
	if owners := recorded[branch]; !slices.Equal(owners, []int64{t.ID}) {
		return fmt.Errorf("%s is recorded for %s in project %s, not for #%d alone; omo leaves it alone", code(branch), ticketList(owners), code(p.ID), t.ID)
	}
	protected, err := e.git.ProtectedBranches(repo)
	if err != nil {
		return fmt.Errorf("cannot read the default branches of project %s: %w", code(p.ID), err)
	}
	if slices.Contains(protected, branch) {
		return fmt.Errorf("%s is a default branch of project %s; omo never pushes or deletes it", code(branch), code(p.ID))
	}
	return nil
}

// ticketList is "#1, #2" or "no ticket".
func ticketList(ns []int64) string {
	if len(ns) == 0 {
		return "no ticket"
	}
	out := ""
	for i, n := range ns {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("#%d", n)
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
