package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// approvedStage is the review stage after an approval: merging when a local
// merge is asked for, exporting when the ticket is exported (by its setting
// or on request), else merging. A task inside a story is always merged.
func approvedStage(p Policy, t ticketRow, export, merge bool) api.ReviewStage {
	if t.inStory() || merge {
		return api.ReviewStageMerging
	}
	if export || effectiveWhenApproved(p, t) == api.WhenApprovedExport {
		return api.ReviewStageExporting
	}
	return api.ReviewStageMerging
}

// Done is `omo ai done`. For the developer of a task or the pm of a story it
// hands the ticket in for review; for a reviewer it records the verdict for
// the reviewer's project; for an export job it records the result of the
// export of its project. Only an export job sends a url or an error. The
// ticket is the one the agent's session is bound to.
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
	if a.Purpose != api.PurposeExport && (req.URL != "" || req.Error != "") {
		return api.Ticket{}, api.Errorf(api.CodeForbidden,
			"only an export job reports a pull request url or an export error; this %s session hands in or gives a verdict", a.Role)
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
		switch {
		case a.Purpose == api.PurposeExport:
			err = b.exportResult(tx, a, t, req)
		case a.Role == api.RoleReviewer:
			err = b.verdict(tx, p, a, t, req)
		default:
			err = b.handIn(tx, p, a, t, req)
		}
		if err != nil {
			return err
		}
		out, err = b.ticket(tx, t.ID)
		return err
	})
	return forActor(a, out, err)
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
		stage = approvedStage(p, t, false, false)
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

// exportResult records what an export job reports for its project: the url
// of the pull request it opened, or the error that stopped it. Once every
// project of the ticket has a pull request the ticket is done as exported;
// an error sends the ticket back to the user like ExportFailed.
//
// The jobs of one export attempt run side by side, so a job may open its
// pull request after another project's job has failed and the ticket waits
// for the user. That address is stored for its project, the ticket stays
// with the user, and a system comment says that the pull request exists: a
// retry then exports only the other projects, and after a local merge or a
// rejection the comment still points at the open pull request.
func (b *Board) exportResult(tx *sql.Tx, a Actor, t ticketRow, req api.DoneRequest) error {
	if req.Verdict != "" {
		return api.Errorf(api.CodeValidation, "an export job finishes with a url or an error, not with a verdict")
	}
	url, failure := strings.TrimSpace(req.URL), strings.TrimSpace(req.Error)
	switch {
	case url == "" && failure == "":
		return api.Errorf(api.CodeValidation,
			"an export job finishes with the url of its pull request or with the error that stopped it, not with a plain hand-in")
	case url != "" && failure != "":
		return api.Errorf(api.CodeValidation, "an export job reports either a url or an error, not both")
	}
	late := false
	if url != "" {
		var err error
		if late, err = lateExportURL(tx, a, t); err != nil {
			return err
		}
	}
	if !late {
		if err := checkExporting(t); err != nil {
			return err
		}
	}
	ps, err := loadProjects(tx, t.ID)
	if err != nil {
		return err
	}
	if !containsProject(ps, a.Project) {
		return api.Errorf(api.CodeValidation, "this export job has no project of #%d (%q)", t.ID, a.Project)
	}
	if body := strings.TrimSpace(req.Comment); body != "" {
		if _, err := b.addComment(tx, a, t.ID, body, ""); err != nil {
			return err
		}
	}
	if failure != "" {
		return b.exportFailed(tx, a, t, fmt.Sprintf("project %s: %s", a.Project, failure))
	}
	if url, err = checkExportURL(url); err != nil {
		return err
	}
	var done string
	if err := tx.QueryRow(`SELECT export_url FROM ticket_projects WHERE ticket_id = ? AND project_id = ?`,
		t.ID, a.Project).Scan(&done); err != nil {
		return err
	}
	if done != "" {
		return api.Errorf(api.CodeInvalidTransition, "project %q of #%d is already exported (%s)", a.Project, t.ID, done)
	}
	if _, err := tx.Exec(`UPDATE ticket_projects SET export_url = ? WHERE ticket_id = ? AND project_id = ?`,
		url, t.ID, a.Project); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tickets SET updated_at = ? WHERE id = ?`, b.stamp(), t.ID); err != nil {
		return err
	}
	var missing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ticket_projects WHERE ticket_id = ? AND export_url = ''`,
		t.ID).Scan(&missing); err != nil {
		return err
	}
	if late {
		if err := b.emitUpdated(tx, a, t.ID, "projects"); err != nil {
			return err
		}
		return b.systemComment(tx, t.ID, fmt.Sprintf(
			"The pull request of project %s exists although the export of another project failed: %s", a.Project, url),
			t.Supervisor)
	}
	if missing > 0 {
		return b.emitUpdated(tx, a, t.ID, "projects")
	}
	return b.completeExport(tx, a, t)
}

// lateExportURL reports whether an address from the export job a arrives
// after another project's export of the same attempt failed: t waits for the
// user because of that failure, a was started after t last entered stage
// exporting, and a is not the job that reported the failure. Both are read
// from the event log, whose ids order the start of a and the stage changes
// of t.
func lateExportURL(q db.Queryer, a Actor, t ticketRow) (bool, error) {
	if t.inStory() || t.Col != api.ColumnReview || t.ReviewStage != api.ReviewStageHuman || a.Session == "" {
		return false, nil
	}
	var action, failedBy string
	err := q.QueryRow(`SELECT COALESCE(json_extract(detail, '$.action'), ''), COALESCE(json_extract(actor, '$.session'), '')
		FROM events WHERE ticket_id = ? AND kind = ? ORDER BY id DESC LIMIT 1`, t.ID, api.KindTicketMoved).Scan(&action, &failedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if action != ActionExportFailed || failedBy == a.Session {
		return false, nil
	}
	var entered, started int64
	if err := q.QueryRow(`SELECT
		COALESCE((SELECT MAX(id) FROM events WHERE ticket_id = ?1 AND kind = ?2
			AND json_extract(detail, '$.ticket.review_stage') = ?3), 0),
		COALESCE((SELECT MAX(id) FROM events WHERE ticket_id = ?1 AND kind = ?4
			AND json_extract(detail, '$.agent.id') = ?5), 0)`,
		t.ID, api.KindTicketMoved, api.ReviewStageExporting, api.KindAgentCreated, a.Session).Scan(&entered, &started); err != nil {
		return false, err
	}
	return started > entered, nil
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
//
// A reviewer belongs to the review round it was started for: a verdict from
// a reviewer of another round is forbidden, so a reviewer of an earlier
// round can never decide a later one.
func (b *Board) verdict(tx *sql.Tx, p Policy, a Actor, t ticketRow, req api.DoneRequest) error {
	if a.ReviewRound != t.ReviewRound {
		return api.Errorf(api.CodeForbidden, "this reviewer belongs to review round %d; #%d is in round %d",
			a.ReviewRound, t.ID, t.ReviewRound)
	}
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
	stage := approvedStage(p, t, false, false)
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
// in Review and the engine merges or exports it. export asks for an export
// and merge for a local merge, whatever "when approved" says; this is also
// how the user retries a failed export or merges it locally instead.
func (b *Board) approveReview(tx *sql.Tx, p Policy, a Actor, t ticketRow, export, merge bool) error {
	if a.Role != api.RoleUser {
		return api.Errorf(api.CodeForbidden, "only the user approves a review that waits for them")
	}
	if export && merge {
		return api.Errorf(api.CodeValidation, "approve either with export or with merge, not both")
	}
	if err := checkHumanStage(t); err != nil {
		return err
	}
	if export && t.inStory() {
		return api.Errorf(api.CodeValidation, "#%d is a task inside a story; it is merged into the story and cannot be exported", t.ID)
	}
	if err := b.setStage(tx, t.ID, approvedStage(p, t, export, merge), ""); err != nil {
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

// checkRejectable requires t to wait for the user's review, or to be
// approved but stuck at merging with a failed merge step (for example
// because its branch moved after the review): the user may then send it
// back to work instead of retrying the merge.
func checkRejectable(t ticketRow) error {
	if t.Col == api.ColumnReview && t.ReviewStage == api.ReviewStageMerging && t.FailedStep == api.FailedMerge {
		return nil
	}
	return checkHumanStage(t)
}

// Reject rejects a review that waits for the user, or an approved ticket
// whose merge failed: the ticket goes back to In Progress with the user's
// comment, which mentions the developer (task) or the product manager
// (story).
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
		if err := checkNotBusy(tx, t); err != nil {
			return err
		}
		if err := checkRejectable(t); err != nil {
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
	return forActor(a, out, err)
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
		if err := b.markMerged(tx, a, t); err != nil {
			return err
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return forActor(a, out, err)
}

// SetExportMerged is MarkMerged for the engine, which found every exported
// branch of the ticket in its base branch.
func (b *Board) SetExportMerged(ctx context.Context, n int64) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		return b.markMerged(tx, System, t)
	})
}

// markMerged sets export_merged on the exported ticket t and tells the
// tickets that depend on it. Marking it again changes nothing.
func (b *Board) markMerged(tx *sql.Tx, a Actor, t ticketRow) error {
	if t.Resolution != api.ResolutionExported {
		return api.Errorf(api.CodeInvalidTransition, "#%d was not exported; only an exported ticket can be marked as merged", t.ID)
	}
	if t.ExportMerged {
		return nil
	}
	if _, err := tx.Exec(`UPDATE tickets SET export_merged = 1, updated_at = ? WHERE id = ?`, b.stamp(), t.ID); err != nil {
		return err
	}
	if err := b.emitUpdated(tx, a, t.ID, "export_merged"); err != nil {
		return err
	}
	deps, err := dependents(tx, t.ID)
	if err != nil {
		return err
	}
	return b.touch(tx, a, deps...)
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
