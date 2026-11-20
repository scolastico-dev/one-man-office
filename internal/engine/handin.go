package engine

import (
	"context"
	"fmt"
	"os"
	"strings"

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
//   - the product manager of a story: every integration worktree of the
//     story. It may not commit there, so it is told to find out where the
//     changes come from or to ask its supervisor, and never to discard them.
//
// An export job works outside the worktree on the commit omo pushed, so no
// worktree is checked for it; the address it reports for its pull request
// must fit the project's remote (checkExportAddress) and is announced in a
// comment (announceExport), and a reported failure is never refused but
// recorded for its project (recordExportFailure). Reviewers and every other
// actor are not checked. A
// hand-in that passes records the commit each branch is at: that is what is
// reviewed and later merged.
func (e *Engine) CheckHandIn(ctx context.Context, a board.Actor, req api.DoneRequest) error {
	export := a.Purpose == api.PurposeExport
	switch {
	case a.Ticket == 0:
		return nil
	case a.Role == api.RoleDeveloper && export:
		if req.URL == "" {
			e.recordExportFailure(ctx, a, req)
			return nil
		}
		if err := e.checkExportAddress(a, req.URL); err != nil {
			return err
		}
		e.announceExport(ctx, a, req.URL)
		return nil
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
		if p.Worktree == "" {
			continue
		}
		if _, err := os.Lstat(p.Worktree); os.IsNotExist(err) {
			continue
		}
		proj, ok := e.st.Project(p.ID)
		if !ok || proj.Missing {
			return api.Errorf(api.CodeConflict, "omo could not check %s for uncommitted changes: project %s is missing; try again once it is back", p.Worktree, p.ID)
		}
		files, err := e.git.Status(proj.Path, p.Worktree)
		if err != nil {
			return api.Errorf(api.CodeConflict, "omo could not check %s for uncommitted changes (%v); try again", p.Worktree, err)
		}
		if len(files) == 0 {
			continue
		}
		list := joinCapped(files)
		switch {
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
	if t.Column == api.ColumnInProgress && a.Role == workingRole(t) {
		// What is handed in now is what the review sees and what is
		// merged: record it before the board moves the ticket.
		if heads, err := e.headsOf(t); err != nil {
			e.log.Printf("engine: read the branches #%d hands in: %v", t.ID, err)
		} else if err := e.pins.setReviewed(t.ID, t.ReviewRound+1, heads); err != nil {
			e.log.Printf("engine: record the commits #%d hands in: %v", t.ID, err)
		} else if err := e.b.RecordReviewCommits(ctx, t.ID, t.ReviewRound+1, heads); err != nil {
			return err
		}
	}
	return nil
}

// recordExportFailure records the failure an export job reports for its
// project, while its ticket is exporting and the project has no pull
// request: the export of that project is over and the others go on (see
// settleExport). A failure report is never refused.
func (e *Engine) recordExportFailure(ctx context.Context, a board.Actor, req api.DoneRequest) {
	reason := strings.TrimSpace(req.Error)
	if reason == "" || req.Verdict != "" {
		return
	}
	t, _, ok, err := e.read(ctx, a.Ticket)
	if err != nil || !ok || t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting {
		return
	}
	for _, p := range t.Projects {
		if p.ID == a.Project && p.ExportURL == "" {
			if err := e.keepExportFailure(ctx, t.ID, p.ID, reason); err != nil {
				e.log.Printf("engine: record the failed export of #%d in project %s: %v", t.ID, p.ID, err)
			}
			return
		}
	}
}

// announceExport comments the pull request the export job a reports, while
// its ticket is exporting and the project has none yet: its address, the
// title sent and whose text it is (exportTextComment).
func (e *Engine) announceExport(ctx context.Context, a board.Actor, address string) {
	t, _, ok, err := e.read(ctx, a.Ticket)
	if err != nil || !ok || t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting ||
		!hasProject(t, a.Project) || projectOf(t, a.Project).ExportURL != "" {
		return
	}
	e.comment(ctx, t.ID, e.exportTextComment(t.ID, a.Project, address))
}

// headsOf reads the commit the branch of every project of t is at.
func (e *Engine) headsOf(t board.EngineTicket) (map[string]string, error) {
	heads := map[string]string{}
	for _, p := range t.Projects {
		if p.Branch == "" {
			continue
		}
		proj, ok := e.st.Project(p.ID)
		if !ok || proj.Missing {
			return nil, fmt.Errorf("project %s is missing", p.ID)
		}
		id, err := e.git.RevParse(proj.Path, "refs/heads/"+p.Branch+"^{commit}")
		if err != nil {
			return nil, err
		}
		heads[p.ID] = id
	}
	return heads, nil
}

// pinHandIns records, for every ticket of snap in review whose hand-in
// was not recorded (it was handed in without CheckHandIn), the commits its
// branches are at now, and forgets the records no ticket needs. It only
// reads the repositories.
func (e *Engine) pinHandIns(snap board.EngineSnapshot) {
	// The folders of export jobs go with the pins of the round they
	// exported.
	e.removeExportFolders(snap)
	for _, t := range snap.Tickets {
		if t.Column != api.ColumnReview {
			continue
		}
		pins, err := e.pins.reviewed(t.ID, t.ReviewRound)
		if err != nil {
			e.log.Printf("engine: read the recorded hand-ins: %v", err)
			return
		}
		if pins != nil {
			// Pins and board metadata are separate durable writes. Recover
			// the board side after an interrupted hand-in or an upgrade,
			// using the original pin even if the branch has since moved.
			for _, p := range t.Projects {
				if commit := pins[p.ID]; commit != "" && p.ReviewCommit != commit {
					if err := e.b.RecordReviewCommits(context.Background(), t.ID, t.ReviewRound, pins); err != nil {
						e.log.Printf("engine: recover the reviewed commits of #%d: %v", t.ID, err)
					}
					break
				}
			}
			continue
		}
		heads, err := e.headsOf(t)
		if err != nil {
			e.log.Printf("engine: read the branches of #%d in review: %v", t.ID, err)
			continue
		}
		if err := e.pins.setReviewed(t.ID, t.ReviewRound, heads); err != nil {
			e.log.Printf("engine: record the commits of #%d in review: %v", t.ID, err)
		} else if err := e.b.RecordReviewCommits(context.Background(), t.ID, t.ReviewRound, heads); err != nil {
			e.log.Printf("engine: record the reviewed commits of #%d: %v", t.ID, err)
		}
	}
	if err := e.pins.prune(snap); err != nil {
		e.log.Printf("engine: forget recorded hand-ins: %v", err)
	}
}
