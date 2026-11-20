package board

import (
	"context"
	"database/sql"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// RequestFollowUp returns exported work to its working agent or starts a fresh
// review round. The old push stays historical; it can no longer close the task.
func (b *Board) RequestFollowUp(ctx context.Context, a Actor, n int64, req api.FollowUpRequest) (api.Ticket, error) {
	if a.Role != api.RoleUser {
		return api.Ticket{}, api.Errorf(api.CodeForbidden, "only the user assigns follow-up work on an export")
	}
	body := strings.TrimSpace(req.Comment)
	if body == "" || (req.Role != "developer" && req.Role != "reviewer") {
		return api.Ticket{}, api.Errorf(api.CodeValidation, "follow-up work needs a task and a developer or reviewer recipient")
	}
	var out api.Ticket
	err := b.write(ctx, func(tx *sql.Tx) error {
		if err := checkRead(tx, a, n); err != nil {
			return err
		}
		t, err := loadTicket(tx, n)
		if err != nil {
			return err
		}
		if err := checkNotBusy(tx, t); err != nil {
			return err
		}
		if t.Col != api.ColumnReview || (t.ReviewStage != api.ReviewStageAwaitingMerge && t.ReviewStage != api.ReviewStageHuman) {
			return api.Errorf(api.CodeInvalidTransition, "follow-up work needs an exported ticket waiting for a merge or human review")
		}
		var exported int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ticket_projects WHERE ticket_id=? AND last_export_commit<>''`, n).Scan(&exported); err != nil {
			return err
		}
		if exported == 0 {
			return api.Errorf(api.CodeInvalidTransition, "this ticket has no previous export")
		}
		to, mention := api.ColumnInProgress, workingRole(t)
		if req.Role == "reviewer" {
			to, mention = api.ColumnReview, api.RoleReviewer
		}
		rank, err := endRankExcept(tx, to, n)
		if err != nil {
			return err
		}
		if err := b.setColumn(tx, n, to, rank, ""); err != nil {
			return err
		}
		if err := clearExportWatch(tx, n); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tickets SET reexport_pending=1,followup_review=? WHERE id=?`, req.Role == "reviewer", n); err != nil {
			return err
		}
		if req.Role == "reviewer" {
			if _, err := tx.Exec(`UPDATE tickets SET review_round=review_round+1,review_stage='agent' WHERE id=?`, n); err != nil {
				return err
			}
		}
		if _, err := b.addComment(tx, a, n, body, mention); err != nil {
			return err
		}
		if err := b.finish(tx, a, []move{{id: n, from: t.Col, to: to, action: "follow_up"}}); err != nil {
			return err
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return forActor(a, out, err)
}

// RecordReviewCommits keeps the hand-in beside the last export so human review
// can explain when the local reviewed work has not yet been re-exported.
func (b *Board) RecordReviewCommits(ctx context.Context, n int64, round int, commits map[string]string) error {
	for _, id := range commits {
		if !commitID(id) {
			return api.Errorf(api.CodeValidation, "a hand-in needs full commit ids")
		}
	}
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if !((t.Col == api.ColumnInProgress && round == t.ReviewRound+1) || (t.Col == api.ColumnReview && round == t.ReviewRound)) {
			return api.Errorf(api.CodeConflict, "the hand-in round changed")
		}
		changed := false
		for project, id := range commits {
			result, err := tx.Exec(`UPDATE ticket_projects SET review_commit=? WHERE ticket_id=? AND project_id=? AND review_commit<>?`, id, n, project, id)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			changed = changed || n > 0
		}
		if !changed {
			return nil
		}
		if _, err := tx.Exec(`UPDATE tickets SET updated_at=? WHERE id=?`, b.stamp(), n); err != nil {
			return err
		}
		return b.emitUpdated(tx, System, n, "projects")
	})
}

func clearExportWatch(tx *sql.Tx, n int64) error {
	if _, err := tx.Exec(`UPDATE tickets SET export_url='',export_merged=0 WHERE id=?`, n); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE ticket_projects SET export_url='',export_remote='',export_branch='',export_base='',export_commit='',export_target_commit='',review_commit='' WHERE ticket_id=?`, n)
	return err
}
