package engine

import (
	"context"
	"os"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// CheckHandIn refuses the hand-in of agent session a (its `omo ai done`
// with req) while a worktree it hands in holds uncommitted work: changes to
// tracked files or untracked files that git does not ignore. Ignored files
// never count. It runs git outside any board transaction and answers
// conflict with a message that lists the files and says what to do:
//
//   - the developer of a task: its task worktree; commit or remove the files.
//   - an export job reporting its pull request: the worktree of its
//     project, since a pull request would miss that work; it reports a
//     failure instead and touches nothing. A reported failure is never
//     refused.
//   - the product manager of a story: every integration worktree of the
//     story. It may not commit there, so it is told to find out where the
//     changes come from or to ask its supervisor, and never to discard them.
//
// Reviewers and every other actor are not checked.
func (e *Engine) CheckHandIn(ctx context.Context, a board.Actor, req api.DoneRequest) error {
	export := a.Purpose == api.PurposeExport
	switch {
	case a.Ticket == 0:
		return nil
	case a.Role == api.RoleDeveloper && export:
		if req.URL == "" {
			return nil
		}
	case a.Role == api.RoleDeveloper, a.Role == api.RolePM:
	default:
		return nil
	}
	t, _, ok, err := e.read(ctx, a.Ticket)
	if err != nil {
		return api.Errorf(api.CodeConflict, "omo could not read #%d to check its worktree for uncommitted changes; try again", a.Ticket)
	}
	if !ok {
		return nil
	}
	for _, p := range t.Projects {
		if p.Worktree == "" || (export && p.ID != a.Project) {
			continue
		}
		if _, err := os.Stat(p.Worktree); os.IsNotExist(err) {
			continue
		}
		files, err := e.git.Status(p.Worktree)
		if err != nil {
			return api.Errorf(api.CodeConflict, "omo could not check %s for uncommitted changes (%v); try again", p.Worktree, err)
		}
		if len(files) == 0 {
			continue
		}
		list := joinCapped(files)
		switch {
		case export:
			return api.Errorf(api.CodeConflict,
				"the worktree %s has uncommitted changes the pull request would miss: %s. Do not commit, change or discard them; report the failure with omo ai done --failed instead",
				p.Worktree, list)
		case a.Role == api.RolePM:
			return api.Errorf(api.CodeConflict,
				"the integration worktree %s (project %s) has uncommitted changes: %s. Do not commit or discard them: find out where they come from, or ask your supervisor; then run omo ai done again",
				p.Worktree, p.ID, list)
		default:
			return api.Errorf(api.CodeConflict,
				"commit or discard these changes first: %s (in %s). Commit what belongs to the work, delete files you created that do not, or ignore build output in .gitignore; then run omo ai done again",
				list, p.Worktree)
		}
	}
	return nil
}
