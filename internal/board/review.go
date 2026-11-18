package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
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
		return api.Errorf(api.CodeInvalidTransition, "#%d is in %s; only a ticket in In Progress can be handed in", t.ID, columnWords(t.Col))
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
	if _, err := tx.Exec(`UPDATE tickets SET review_round = review_round + 1 WHERE id = ?`, t.ID); err != nil {
		return err
	}
	if body := strings.TrimSpace(req.Comment); body != "" {
		if _, err := b.addComment(tx, a, t.ID, body, ""); err != nil {
			return err
		}
	}
	return b.passReview(tx, a, t, t.Col, stage, ActionHandIn)
}

// exportResult records what an export job reports for its project: the url
// of the pull request it opened, or the error that stopped it. Once every
// project of the ticket has a pull request the ticket waits for the merge.
// An error is settled by the engine (Engine.CheckHandIn records it), which
// sends the ticket back to the user with ExportFailed once the other
// projects' exports are over, or ends it with FinishExport.
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
		// The engine (which recorded the failure before the board saw
		// it) settles the export once no other project's export is
		// going on: one project's failure does not stop the others.
		return b.emitUpdated(tx, a, t.ID, "projects")
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
// after the export attempt it belongs to failed: t waits for the user
// because of a failed export, a was started after t last entered stage
// exporting, and a did not report a failure itself (a job reports nothing
// else that changes the ticket while other projects are still exporting,
// so a ticket.updated event of a's session is its failure report). All of
// it is read from the event log, whose ids order the start of a, its
// reports and the stage changes of t.
func lateExportURL(q db.Queryer, a Actor, t ticketRow) (bool, error) {
	if t.inStory() || t.Col != api.ColumnReview || t.ReviewStage != api.ReviewStageHuman || a.Session == "" {
		return false, nil
	}
	var action string
	err := q.QueryRow(`SELECT COALESCE(json_extract(detail, '$.action'), '')
		FROM events WHERE ticket_id = ? AND kind = ? ORDER BY id DESC LIMIT 1`, t.ID, api.KindTicketMoved).Scan(&action)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if action != ActionExportFailed {
		return false, nil
	}
	var entered, started, failedItself int64
	if err := q.QueryRow(`SELECT
		COALESCE((SELECT MAX(id) FROM events WHERE ticket_id = ?1 AND kind = ?2
			AND json_extract(detail, '$.ticket.review_stage') = ?3), 0),
		COALESCE((SELECT MAX(id) FROM events WHERE ticket_id = ?1 AND kind = ?4
			AND json_extract(detail, '$.agent.id') = ?5), 0),
		(SELECT COUNT(*) FROM events WHERE ticket_id = ?1 AND kind = ?6
			AND json_extract(actor, '$.session') = ?5)`,
		t.ID, api.KindTicketMoved, api.ReviewStageExporting, api.KindAgentCreated, a.Session, api.KindTicketUpdated).Scan(&entered, &started, &failedItself); err != nil {
		return false, err
	}
	return started > entered && failedItself == 0, nil
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
// The round collects the verdict of every project of the ticket before it
// ends: a rejection leaves the ticket with the reviewers that are still
// deciding. Once every project has a verdict, a round with a rejection sends
// the ticket back to In Progress, and one comment hands every rejection to
// the developer (task) or product manager (story) together; a round without
// one moves on to the user's review or to merging or exporting.
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
		return api.Errorf(api.CodeInvalidTransition, "#%d is not waiting for an agent review (in %s, stage %q)",
			t.ID, columnWords(t.Col), t.ReviewStage)
	}
	ps, err := loadProjects(tx, t.ID)
	if err != nil {
		return err
	}
	keys := reviewKeysOf(projectIDsOf(ps))
	project := a.Project
	if project == "" && len(ps) == 1 {
		project = ps[0].id
	}
	if !slices.Contains(keys, project) {
		return api.Errorf(api.CodeValidation, "this reviewer session has no project of #%d (%q); a verdict is given per project", t.ID, project)
	}
	round, err := loadRound(tx, t.ID, t.ReviewRound, keys)
	if err != nil {
		return err
	}
	if _, ok := round.verdicts[project]; ok {
		return api.Errorf(api.CodeInvalidTransition, "%s of #%d already has a verdict in review round %d", keyWords(project), t.ID, t.ReviewRound)
	}
	body := strings.TrimSpace(req.Comment)
	if req.Verdict == VerdictReject && body == "" {
		return api.Errorf(api.CodeValidation, "a rejection needs a comment that says what to fix")
	}
	// This verdict ends the round when it is the last one missing. A
	// rejection that ends the round alone mentions the working agent
	// itself; every other rejection waits for the round's comment.
	last := len(round.verdicts) == len(keys)-1
	alone := last && len(round.rejected) == 0

	var commentID any
	if body != "" {
		var mention api.Role
		if req.Verdict == VerdictReject && alone {
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
	if !last {
		return nil
	}
	round.verdicts[project] = req.Verdict
	if req.Verdict == VerdictReject {
		round.rejected = append(round.rejected, roundRejection{project: project, comment: body})
	}
	return b.endRound(tx, p, a, t, keys, round, !alone)
}

// CarryVerdicts records, for the engine, approvals of review round round of
// ticket n that need no reviewer: projects whose approval of an earlier
// round holds for the commit handed in now, or whose branch has no commits
// of its own. Each gets an approving verdict without a session; note, when
// not empty, is posted as one system comment and linked to those verdicts.
// When that leaves no project without a verdict, the round ends as with the
// last reviewer's verdict.
func (b *Board) CarryVerdicts(ctx context.Context, n int64, round int, projects []string, note string) error {
	p := b.env.Policy()
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkStage(t, api.ReviewStageAgent); err != nil {
			return err
		}
		if t.ReviewRound != round {
			return api.Errorf(api.CodeInvalidTransition, "#%d is in review round %d, not %d", n, t.ReviewRound, round)
		}
		ps, err := loadProjects(tx, n)
		if err != nil {
			return err
		}
		keys := reviewKeysOf(projectIDsOf(ps))
		rs, err := loadRound(tx, n, round, keys)
		if err != nil {
			return err
		}
		for _, project := range projects {
			if !containsProject(ps, project) {
				return api.Errorf(api.CodeValidation, "project %q is not a project of #%d", project, n)
			}
			if _, ok := rs.verdicts[project]; ok {
				return api.Errorf(api.CodeInvalidTransition, "project %q of #%d already has a verdict in review round %d", project, n, round)
			}
		}
		var commentID any
		if note = strings.TrimSpace(note); note != "" {
			id, err := b.addComment(tx, System, n, note)
			if err != nil {
				return err
			}
			commentID = id
		}
		for _, project := range projects {
			if _, err := tx.Exec(`INSERT INTO review_verdicts (ticket_id, round, project_id, session_id, verdict, comment_id, created_at)
				VALUES (?, ?, ?, NULL, ?, ?, ?)`, n, round, project, VerdictApprove, commentID, b.stamp()); err != nil {
				return err
			}
			rs.verdicts[project] = VerdictApprove
		}
		if len(rs.verdicts) < len(keys) {
			return nil
		}
		return b.endRound(tx, p, System, t, keys, rs, true)
	})
}

// roundState is what the verdicts of one review round say so far.
type roundState struct {
	// verdicts maps a project of the ticket to its verdict.
	verdicts map[string]string
	// rejected lists the rejections in the order they were given.
	rejected []roundRejection
}

// roundRejection is one rejection of a round and its comment.
type roundRejection struct {
	project, comment string
}

// loadRound reads the verdicts of round of ticket n for its review keys
// (see EngineTicket.ReviewKeys).
func loadRound(q db.Queryer, n int64, round int, keys []string) (roundState, error) {
	out := roundState{verdicts: map[string]string{}}
	rows, err := q.Query(`SELECT v.project_id, v.verdict, COALESCE(c.body, '') FROM review_verdicts v
		LEFT JOIN comments c ON c.id = v.comment_id
		WHERE v.ticket_id = ? AND v.round = ? ORDER BY v.id`, n, round)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var project, verdict, body string
		if err := rows.Scan(&project, &verdict, &body); err != nil {
			return out, err
		}
		if !slices.Contains(keys, project) {
			continue
		}
		out.verdicts[project] = verdict
		if verdict == VerdictReject {
			out.rejected = append(out.rejected, roundRejection{project: project, comment: body})
		}
	}
	return out, rows.Err()
}

// endRound ends the review round of t, in which every review key now has
// a verdict: back to In Progress when one was rejected (with one comment
// that hands the rejections to the working agent when tell is set), else on
// to the user's review, merging or exporting (for a task without a project:
// to Done, see passReview).
func (b *Board) endRound(tx *sql.Tx, p Policy, a Actor, t ticketRow, keys []string, round roundState, tell bool) error {
	if len(round.rejected) > 0 {
		rank, err := endRankExcept(tx, api.ColumnInProgress, t.ID)
		if err != nil {
			return err
		}
		if err := b.setColumn(tx, t.ID, api.ColumnInProgress, rank, ""); err != nil {
			return err
		}
		if err := b.finish(tx, a, []move{{id: t.ID, from: api.ColumnReview, to: api.ColumnInProgress, action: ActionVerdict}}); err != nil {
			return err
		}
		if !tell {
			return nil
		}
		return b.systemComment(tx, t.ID, roundText(t, keys, round), workingRole(t))
	}
	stage := approvedStage(p, t, false, false)
	if effectiveReview(p, t) == api.ReviewAgentThenHuman {
		stage = api.ReviewStageHuman
	}
	return b.passReview(tx, a, t, api.ColumnReview, stage, ActionVerdict)
}

// passReview moves t, which leaves column from with its review settled, on
// to stage in Review. A task without a project has nothing to merge or
// export: where it would be merged or exported it is done (completed)
// instead.
func (b *Board) passReview(tx *sql.Tx, a Actor, t ticketRow, from api.Column, stage api.ReviewStage, action string) error {
	if stage == api.ReviewStageMerging || stage == api.ReviewStageExporting {
		none, err := hasNoProject(tx, t)
		if err != nil {
			return err
		}
		if none {
			if err := b.setColumn(tx, t.ID, api.ColumnDone, t.Rank, api.ResolutionCompleted); err != nil {
				return err
			}
			return b.finish(tx, a, []move{{id: t.ID, from: from, to: api.ColumnDone, action: action}})
		}
	}
	if from != api.ColumnReview {
		rank, err := endRankExcept(tx, api.ColumnReview, t.ID)
		if err != nil {
			return err
		}
		if err := b.setColumn(tx, t.ID, api.ColumnReview, rank, ""); err != nil {
			return err
		}
	}
	if err := b.setStage(tx, t.ID, stage, ""); err != nil {
		return err
	}
	return b.finish(tx, a, []move{{id: t.ID, from: from, to: api.ColumnReview, action: action}})
}

// hasNoProject reports whether t is a task without a project.
func hasNoProject(q db.Queryer, t ticketRow) (bool, error) {
	if t.isStory() {
		return false, nil
	}
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM ticket_projects WHERE ticket_id = ?`, t.ID).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// keyWords names a review key in a message: a project, or the ticket
// itself for a task without a project.
func keyWords(key string) string {
	if key == NoProjectKey {
		return "the ticket"
	}
	return fmt.Sprintf("project %q", key)
}

// roundText is the comment that ends a review round with rejections: every
// rejection with its comment, and the projects that were approved.
func roundText(t ticketRow, keys []string, round roundState) string {
	var names []string
	for _, r := range round.rejected {
		names = append(names, r.project)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Review round %d is over: rejected for %s. Fix every point below, then hand in again.",
		t.ReviewRound, strings.Join(names, ", "))
	var approved []string
	for _, k := range keys {
		if round.verdicts[k] == VerdictApprove {
			approved = append(approved, k)
		}
	}
	if len(approved) > 0 {
		fmt.Fprintf(&sb, " Projects approved: %s.", strings.Join(approved, ", "))
	}
	for _, r := range round.rejected {
		fmt.Fprintf(&sb, "\n\nProject %s:\n%s", r.project, r.comment)
	}
	return sb.String()
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
	if export || merge {
		none, err := hasNoProject(tx, t)
		if err != nil {
			return err
		}
		if none {
			return api.Errorf(api.CodeValidation,
				"#%d has no project, so there is nothing to merge or export; approve it without export or merge and it is done", t.ID)
		}
	}
	return b.passReview(tx, a, t, api.ColumnReview, approvedStage(p, t, export, merge), ActionApprove)
}

// checkHumanStage requires t to wait for the user's review.
func checkHumanStage(t ticketRow) error {
	if t.Col != api.ColumnReview {
		return api.Errorf(api.CodeInvalidTransition, "#%d is in %s, not in Review", t.ID, columnWords(t.Col))
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

// checkRejectable requires t to wait for the user's review or an exported
// merge, or to be approved but stuck with a failed merge step (for example
// because its branch moved after the review): the user may then send it
// back to work instead of retrying the merge.
func checkRejectable(t ticketRow) error {
	if t.Col == api.ColumnReview && (t.ReviewStage == api.ReviewStageAwaitingMerge || (t.ReviewStage == api.ReviewStageMerging && t.FailedStep == api.FailedMerge)) {
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

// markMerged closes an awaiting export and tells tickets that depend on it.
// For legacy Done exports it only sets export_merged. Marking again changes
// nothing; the actor distinguishes user confirmation from engine detection.
func (b *Board) markMerged(tx *sql.Tx, a Actor, t ticketRow) error {
	if t.Col == api.ColumnReview && t.ReviewStage == api.ReviewStageAwaitingMerge {
		if err := b.setColumn(tx, t.ID, api.ColumnDone, t.Rank, api.ResolutionExported); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tickets SET export_merged = 1 WHERE id = ?`, t.ID); err != nil {
			return err
		}
		return b.finish(tx, a, []move{{id: t.ID, from: api.ColumnReview, to: api.ColumnDone, action: ActionMerged}})
	}
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
