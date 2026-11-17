package board

import (
	"context"
	"database/sql"
	"errors"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// The commits of a project's diff. When a ticket goes from Review to Done
// by a merge or an export, the engine records, for each project with a
// branch, the two commits the ticket's diff runs between (RecordCommits):
// the merge base its reviewed work has with the branch it goes into, and
// the reviewed commit. The diff stays viewable from them after the branch
// and the worktree are gone. They are recorded for the ticket's current
// review round; setColumn refuses to close a ticket as completed or
// exported while a project with a branch lacks them (checkRecorded), so no
// way to Done can leave them out.

// RecordCommits records, for the engine, the commits the diff of project of
// ticket n runs between: start, the merge base, and end, the reviewed
// commit (full object ids). The ticket must be in Review at stage merging or
// exporting; the commits belong to its current review round. Nothing
// changes when they are recorded already.
func (b *Board) RecordCommits(ctx context.Context, n int64, project, start, end string) error {
	if !commitID(start) || !commitID(end) {
		return api.Errorf(api.CodeValidation, "the commits of a diff are full object ids, got %q and %q", start, end)
	}
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if t.Col != api.ColumnReview || (t.ReviewStage != api.ReviewStageMerging && t.ReviewStage != api.ReviewStageExporting) {
			return api.Errorf(api.CodeInvalidTransition, "#%d is not being merged or exported (in %s, review stage %q)", n, columnWords(t.Col), t.ReviewStage)
		}
		res, err := tx.Exec(`UPDATE ticket_projects SET start_commit = ?, end_commit = ?, range_round = ?
			WHERE ticket_id = ? AND project_id = ? AND branch <> ''`, start, end, t.ReviewRound, n, project)
		if err != nil {
			return err
		}
		if changed, err := res.RowsAffected(); err != nil {
			return err
		} else if changed == 0 {
			return api.Errorf(api.CodeValidation, "project %q of #%d has no branch to record commits for", project, n)
		}
		return nil
	})
}

// commitID reports whether s is a full object id: 40 (SHA-1) or 64
// (SHA-256) lower-case hexadecimal digits.
func commitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// checkRecorded refuses to close ticket n as res (completed or exported)
// while one of its projects with a branch has no commits recorded for the
// current review round. A cancelled ticket records none.
func checkRecorded(tx *sql.Tx, n int64, res api.Resolution) error {
	if res == api.ResolutionCancelled {
		return nil
	}
	var project string
	err := tx.QueryRow(`SELECT p.project_id FROM ticket_projects p JOIN tickets t ON t.id = p.ticket_id
		WHERE p.ticket_id = ? AND p.branch <> '' AND (p.end_commit = '' OR p.range_round <> t.review_round)
		ORDER BY p.position LIMIT 1`, n).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return api.Errorf(api.CodeConflict,
		"omo has not recorded the commits of project %s of #%d for its diff, so #%d is not done yet", project, n, n)
}
