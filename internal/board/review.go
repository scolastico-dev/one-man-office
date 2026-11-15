package board

import (
	"context"
	"database/sql"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// Review verdicts of DoneRequest.Verdict.
const (
	VerdictApprove = "approve"
	VerdictReject  = "reject"
)

// workingRole is the agent role that works on t: the pm of a story, the
// developer of a task.
func workingRole(t ticketRow) api.Role {
	if t.isStory() {
		return api.RolePM
	}
	return api.RoleDeveloper
}

// approvedStage is the review stage after an approval: exporting when the
// ticket is exported (by its setting or on request), else merging.
func approvedStage(p Policy, t ticketRow, export bool) api.ReviewStage {
	if export || effectiveWhenApproved(p, t) == api.WhenApprovedExport {
		return api.ReviewStageExporting
	}
	return api.ReviewStageMerging
}

// Done is `omo ai done`. For the developer of a task or the pm of a story it
// hands the ticket in for review; for a reviewer it records the verdict for
// the reviewer's project. The ticket is the one the agent's session is bound
// to.
func (b *Board) Done(ctx context.Context, a Actor, req api.DoneRequest) (api.Ticket, error) {
	p := b.env.Policy()
	switch a.Role {
	case api.RoleDeveloper, api.RolePM, api.RoleReviewer:
	default:
		return api.Ticket{}, api.Errorf(api.CodeForbidden,
			"done is for the developer, product manager or reviewer working on a ticket, not for a %s", a.Role)
	}
	if a.Ticket == 0 {
		return api.Ticket{}, api.Errorf(api.CodeForbidden, "this %s session is not bound to a ticket", a.Role)
	}
	var out api.Ticket
	err := b.write(ctx, func(tx *sql.Tx) error {
		if err := checkRead(tx, a, a.Ticket); err != nil {
			return err
		}
		t, err := loadTicket(tx, a.Ticket)
		if err != nil {
			return err
		}
		if a.Role == api.RoleReviewer {
			err = b.verdict(tx, p, a, t, req)
		} else {
			err = b.handIn(tx, p, a, t, req)
		}
		if err != nil {
			return err
		}
		out, err = b.ticket(tx, t.ID)
		return err
	})
	return out, err
}

// handIn moves t from In Progress to Review and starts a review round. A
// story needs every task that is not cancelled done and at least one
// completed.
func (b *Board) handIn(tx *sql.Tx, p Policy, a Actor, t ticketRow, req api.DoneRequest) error {
	if req.Verdict != "" {
		return api.Errorf(api.CodeValidation, "only a reviewer gives a verdict; a %s hands in without one", a.Role)
	}
	if workingRole(t) != a.Role {
		return api.Errorf(api.CodeForbidden, "a %s does not hand in %s #%d", a.Role, t.Type, t.ID)
	}
	if t.Col != api.ColumnInProgress {
		return api.Errorf(api.CodeInvalidTransition, "#%d is in %s; only a ticket in in_progress can be handed in", t.ID, t.Col)
	}
	if t.isStory() {
		if err := checkStoryComplete(tx, t.ID); err != nil {
			return err
		}
	}
	stage := api.ReviewStageAgent
	switch effectiveReview(p, t) {
	case api.ReviewSkipped:
		stage = approvedStage(p, t, false)
	case api.ReviewHuman:
		stage = api.ReviewStageHuman
	}
	rank, err := endRankExcept(tx, api.ColumnReview, t.ID)
	if err != nil {
		return err
	}
	if err := b.setColumn(tx, t.ID, api.ColumnReview, rank, ""); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tickets SET review_stage = ?, review_round = review_round + 1 WHERE id = ?`,
		stage, t.ID); err != nil {
		return err
	}
	if body := strings.TrimSpace(req.Comment); body != "" {
		if _, err := b.addComment(tx, a, t.ID, body, ""); err != nil {
			return err
		}
	}
	return b.finish(tx, a, []move{{id: t.ID, from: t.Col, to: api.ColumnReview, action: ActionHandIn}})
}

// checkStoryComplete requires every task of the story that is not cancelled
// to be done, and at least one of them completed or exported.
func checkStoryComplete(q db.Queryer, story int64) error {
	var open sql.NullInt64
	var finished int
	if err := q.QueryRow(`SELECT MIN(CASE WHEN col <> 'done' THEN id END),
		COALESCE(SUM(resolution IN ('completed', 'exported')), 0) FROM tickets WHERE story_id = ?`,
		story).Scan(&open, &finished); err != nil {
		return err
	}
	if open.Valid {
		return api.Errorf(api.CodeInvalidTransition,
			"story #%d cannot be handed in: task #%d is not done; every task must be done or cancelled", story, open.Int64)
	}
	if finished == 0 {
		return api.Errorf(api.CodeInvalidTransition,
			"story #%d cannot be handed in: none of its tasks was completed; add and finish a task, or cancel the story", story)
	}
	return nil
}

// verdict records a reviewer's verdict for its project in the current round.
// A rejection sends the ticket back to In Progress with the reviewer's
// comment; once every project of the round is approved the ticket moves on
// to the user's review or to merging or exporting.
func (b *Board) verdict(tx *sql.Tx, p Policy, a Actor, t ticketRow, req api.DoneRequest) error {
	if req.Verdict != VerdictApprove && req.Verdict != VerdictReject {
		return api.Errorf(api.CodeValidation, "a reviewer finishes with the verdict approve or reject, not %q", req.Verdict)
	}
	if t.Col != api.ColumnReview || t.ReviewStage != api.ReviewStageAgent {
		return api.Errorf(api.CodeInvalidTransition, "#%d is not waiting for an agent review (column %s, stage %q)",
			t.ID, t.Col, t.ReviewStage)
	}
	ps, err := loadProjects(tx, t.ID)
	if err != nil {
		return err
	}
	project := a.Project
	if project == "" && len(ps) == 1 {
		project = ps[0].id
	}
	if !containsProject(ps, project) {
		return api.Errorf(api.CodeValidation, "this reviewer session has no project of #%d (%q); a verdict is given per project", t.ID, project)
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM review_verdicts WHERE ticket_id = ? AND round = ? AND project_id = ?`,
		t.ID, t.ReviewRound, project).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return api.Errorf(api.CodeInvalidTransition, "project %q of #%d already has a verdict in review round %d", project, t.ID, t.ReviewRound)
	}
	body := strings.TrimSpace(req.Comment)
	if req.Verdict == VerdictReject && body == "" {
		return api.Errorf(api.CodeValidation, "a rejection needs a comment that says what to fix")
	}

	var commentID any
	if body != "" {
		var mention api.Role
		if req.Verdict == VerdictReject {
			mention = workingRole(t)
		}
		id, err := b.addComment(tx, a, t.ID, body, mention)
		if err != nil {
			return err
		}
		commentID = id
	}
	if _, err := tx.Exec(`INSERT INTO review_verdicts (ticket_id, round, project_id, session_id, verdict, comment_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, t.ID, t.ReviewRound, project, a.sessionArg(), req.Verdict, commentID, b.stamp()); err != nil {
		return err
	}
	if err := emit(tx, api.KindReviewVerdict, t.ID, a, api.ReviewVerdictData{
		Ticket: int(t.ID), Round: t.ReviewRound, Project: project, Verdict: req.Verdict,
	}); err != nil {
		return err
	}

	if req.Verdict == VerdictReject {
		rank, err := endRankExcept(tx, api.ColumnInProgress, t.ID)
		if err != nil {
			return err
		}
		if err := b.setColumn(tx, t.ID, api.ColumnInProgress, rank, ""); err != nil {
			return err
		}
		return b.finish(tx, a, []move{{id: t.ID, from: api.ColumnReview, to: api.ColumnInProgress, action: ActionVerdict}})
	}

	var approved int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM review_verdicts v JOIN ticket_projects tp
		ON tp.ticket_id = v.ticket_id AND tp.project_id = v.project_id
		WHERE v.ticket_id = ? AND v.round = ? AND v.verdict = 'approve'`, t.ID, t.ReviewRound).Scan(&approved); err != nil {
		return err
	}
	if approved < len(ps) {
		return nil
	}
	stage := approvedStage(p, t, false)
	if effectiveReview(p, t) == api.ReviewAgentThenHuman {
		stage = api.ReviewStageHuman
	}
	if err := b.setStage(tx, t.ID, stage, ""); err != nil {
		return err
	}
	return b.finish(tx, a, []move{{id: t.ID, from: api.ColumnReview, to: api.ColumnReview, action: ActionVerdict}})
}

func containsProject(ps []boundProject, id string) bool {
	for _, p := range ps {
		if p.id == id {
			return true
		}
	}
	return false
}

// approveReview approves a review that waits for the user: the ticket stays
// in Review and the engine merges or exports it.
func (b *Board) approveReview(tx *sql.Tx, p Policy, a Actor, t ticketRow, export bool) error {
	if a.Role != api.RoleUser {
		return api.Errorf(api.CodeForbidden, "only the user approves a review that waits for them")
	}
	if err := checkHumanStage(t); err != nil {
		return err
	}
	if export && t.inStory() {
		return api.Errorf(api.CodeValidation, "#%d is a task inside a story; it is merged into the story and cannot be exported", t.ID)
	}
	if err := b.setStage(tx, t.ID, approvedStage(p, t, export), ""); err != nil {
		return err
	}
	return b.finish(tx, a, []move{{id: t.ID, from: api.ColumnReview, to: api.ColumnReview, action: ActionApprove}})
}

// checkHumanStage requires t to wait for the user's review.
func checkHumanStage(t ticketRow) error {
	if t.Col != api.ColumnReview {
		return api.Errorf(api.CodeInvalidTransition, "#%d is in %s, not in review", t.ID, t.Col)
	}
	switch t.ReviewStage {
	case api.ReviewStageHuman:
		return nil
	case api.ReviewStageAgent:
		return api.Errorf(api.CodeInvalidTransition, "the review of #%d is with the reviewer agent; it waits for the user only after that", t.ID)
	default:
		return api.Errorf(api.CodeInvalidTransition, "#%d is already approved and %s", t.ID, t.ReviewStage)
	}
}

// Reject rejects a review that waits for the user: the ticket goes back to
// In Progress with the user's comment, which mentions the developer (task)
// or the product manager (story).
func (b *Board) Reject(ctx context.Context, a Actor, n int64, req api.RejectRequest) (api.Ticket, error) {
	if a.Role != api.RoleUser {
		return api.Ticket{}, api.Errorf(api.CodeForbidden, "only the user rejects a review this way; a reviewer rejects with its verdict")
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
		if err := checkHumanStage(t); err != nil {
			return err
		}
		body := strings.TrimSpace(req.Comment)
		if body == "" {
			return api.Errorf(api.CodeValidation, "a rejection needs a comment that says what to fix")
		}
		rank, err := endRankExcept(tx, api.ColumnInProgress, t.ID)
		if err != nil {
			return err
		}
		if err := b.setColumn(tx, t.ID, api.ColumnInProgress, rank, ""); err != nil {
			return err
		}
		if err := b.finish(tx, a, []move{{id: t.ID, from: api.ColumnReview, to: api.ColumnInProgress, action: ActionReject}}); err != nil {
			return err
		}
		if _, err := b.addComment(tx, a, t.ID, body, workingRole(t)); err != nil {
			return err
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return out, err
}

// MarkMerged records that the pull request of an exported ticket was merged,
// which satisfies dependencies on it.
func (b *Board) MarkMerged(ctx context.Context, a Actor, n int64) (api.Ticket, error) {
	if a.Role != api.RoleUser {
		return api.Ticket{}, api.Errorf(api.CodeForbidden, "only the user marks an export as merged")
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
		if t.Resolution != api.ResolutionExported {
			return api.Errorf(api.CodeInvalidTransition, "#%d was not exported; only an exported ticket can be marked as merged", n)
		}
		if !t.ExportMerged {
			if _, err := tx.Exec(`UPDATE tickets SET export_merged = 1, updated_at = ? WHERE id = ?`, b.stamp(), n); err != nil {
				return err
			}
			if err := b.emitUpdated(tx, a, n, "export_merged"); err != nil {
				return err
			}
			deps, err := dependents(tx, n)
			if err != nil {
				return err
			}
			if err := b.touch(tx, a, deps...); err != nil {
				return err
			}
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return out, err
}

// dependents returns the tickets that depend on n.
func dependents(q db.Queryer, n int64) ([]int64, error) {
	var out []int64
	err := each(q, `SELECT ticket_id FROM dependencies WHERE depends_on_id = ? ORDER BY ticket_id`, n,
		func(rows *sql.Rows) error {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
			return nil
		})
	return out, err
}
