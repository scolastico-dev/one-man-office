package board

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// Actions recorded in ticket.moved events.
const (
	ActionApprove      = "approve"
	ActionWithdraw     = "withdraw"
	ActionBeginWork    = "begin_work"
	ActionStop         = "stop"
	ActionHandIn       = "hand_in"
	ActionVerdict      = "verdict"
	ActionReject       = "reject"
	ActionMerged       = "merged"
	ActionConflict     = "conflict"
	ActionExported     = "exported"
	ActionExportFailed = "export_failed"
	ActionCancel       = "cancel"
)

// move is one ticket that changed column or review stage in a transition.
type move struct {
	id           int64
	from, to     api.Column
	action       string
	deleteBranch *bool
	// discard is set on cancel moves, next to deleteBranch.
	discard *bool
}

// Move drags a ticket to a column, or within its column when the column is
// unchanged. Each drag maps onto one transition: Planning → Ready approves,
// Ready → Planning withdraws, In Progress → Planning or Ready stops, Review →
// Done approves a review that waits for the user, a move within a column
// reorders. Before names the ticket the moved one is placed directly above;
// nil places it at the end of the column.
func (b *Board) Move(ctx context.Context, a Actor, n int64, req api.MoveRequest) (api.Ticket, error) {
	p := b.env.Policy()
	if err := checkActor(p, a); err != nil {
		return api.Ticket{}, err
	}
	if !slices.Contains([]api.Column{api.ColumnPlanning, api.ColumnReady, api.ColumnInProgress, api.ColumnReview, api.ColumnDone}, req.Column) {
		return api.Ticket{}, api.Errorf(api.CodeValidation,
			"unknown column %q; use planning, ready, in_progress, review or done", req.Column)
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
		from, to := t.Col, req.Column
		switch {
		case from == api.ColumnDone:
			return api.Errorf(api.CodeInvalidTransition, "#%d is done; done tickets stay where they are", n)
		case from == to:
			err = b.reorder(tx, a, t, req.Before)
		case from == api.ColumnPlanning && to == api.ColumnReady:
			err = b.approve(tx, p, a, t, req.Before)
		case from == api.ColumnReady && to == api.ColumnPlanning:
			err = b.withdraw(tx, p, a, t, req.Before)
		case from == api.ColumnInProgress && (to == api.ColumnPlanning || to == api.ColumnReady):
			err = b.stop(tx, a, t, to, req.Before)
		case from == api.ColumnReview && to == api.ColumnDone && t.ReviewStage == api.ReviewStageAwaitingMerge:
			if a.Role != api.RoleUser {
				return api.Errorf(api.CodeForbidden, "only the user closes an export by hand")
			}
			err = b.markMerged(tx, a, t)
		case from == api.ColumnReview && to == api.ColumnDone:
			err = b.approveReview(tx, p, a, t, false, false)
		case from == api.ColumnReview && to == api.ColumnInProgress:
			if a.Role != api.RoleUser {
				return api.Errorf(api.CodeForbidden, "only the user rejects a review by hand; a reviewer rejects with its verdict")
			}
			return api.Errorf(api.CodeValidation, "rejecting needs a comment: use Reject on the ticket and say what must change")
		default:
			return invalidMove(t, to)
		}
		if err != nil {
			return err
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return forActor(a, out, err)
}

// invalidMove explains why a drag from t's column to col is not possible.
func invalidMove(t ticketRow, to api.Column) error {
	switch {
	case to == api.ColumnInProgress:
		return api.Errorf(api.CodeInvalidTransition,
			"#%d cannot be moved to In Progress by hand: a ticket enters In Progress when its agent starts or when its review is rejected", t.ID)
	case to == api.ColumnReview:
		return api.Errorf(api.CodeInvalidTransition,
			"#%d cannot be moved to Review by hand: a ticket enters Review when its agent hands it in", t.ID)
	case to == api.ColumnDone:
		return api.Errorf(api.CodeInvalidTransition,
			"#%d is in %s: only a review that waits for the user can be dragged to Done; to close the ticket, cancel it", t.ID, columnWords(t.Col))
	case t.Col == api.ColumnReview:
		return api.Errorf(api.CodeInvalidTransition,
			"#%d is in Review: approve it, or reject it back to In Progress", t.ID)
	}
	return api.Errorf(api.CodeInvalidTransition,
		"#%d cannot move from %s to %s: Planning and Ready For Dev swap by approving and withdrawing, In Progress goes back by stopping", t.ID, columnWords(t.Col), columnWords(to))
}

// Approve gives the green light to a ticket in Planning, or approves a
// review that waits for the user. Export asks for an export of the approved
// review instead of a local merge; it is not offered for tasks inside a
// story. Merge asks for a local merge even when the ticket would be
// exported, for example after its export failed.
func (b *Board) Approve(ctx context.Context, a Actor, n int64, req api.ApproveRequest) (api.Ticket, error) {
	p := b.env.Policy()
	if err := checkActor(p, a); err != nil {
		return api.Ticket{}, err
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
		switch t.Col {
		case api.ColumnPlanning:
			if req.Export || req.Merge {
				return api.Errorf(api.CodeValidation, "export and merge apply when approving a review; #%d is in Planning", n)
			}
			err = b.approve(tx, p, a, t, nil)
		case api.ColumnReview:
			err = b.approveReview(tx, p, a, t, req.Export, req.Merge)
		default:
			return api.Errorf(api.CodeInvalidTransition,
				"#%d is in %s: approve gives the green light in Planning or approves a review in Review", n, columnWords(t.Col))
		}
		if err != nil {
			return err
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return forActor(a, out, err)
}

// Cancel closes a ticket that is not done as cancelled. Cancelling a story
// also cancels every task of it that is not done. DeleteBranch and
// DiscardUncommitted are recorded in the events for the engine; only the
// user discards uncommitted work, and only together with the branch.
func (b *Board) Cancel(ctx context.Context, a Actor, n int64, req api.CancelRequest) (api.Ticket, error) {
	p := b.env.Policy()
	if err := checkActor(p, a); err != nil {
		return api.Ticket{}, err
	}
	if req.DiscardUncommitted {
		if a.Role != api.RoleUser {
			return api.Ticket{}, api.Errorf(api.CodeForbidden, "only the user discards uncommitted work when cancelling")
		}
		if !req.DeleteBranch {
			return api.Ticket{}, api.Errorf(api.CodeValidation,
				"discard_uncommitted applies when the branch is deleted; set delete_branch too")
		}
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
		if t.Col == api.ColumnDone {
			return api.Errorf(api.CodeInvalidTransition, "#%d is already done (%s)", n, t.Resolution)
		}
		if !mayCancel(a, t) {
			return api.Errorf(api.CodeForbidden,
				"a %s cannot cancel #%d: the user cancels every ticket, the CEO tickets it created, a product manager tasks it created in its story", a.Role, n)
		}
		if err := checkNotBusy(tx, t); err != nil {
			return err
		}
		del, discard := req.DeleteBranch, req.DiscardUncommitted
		var moves []move
		if t.isStory() {
			tasks, err := storyTasks(tx, t.ID, `col <> 'done'`)
			if err != nil {
				return err
			}
			for _, task := range tasks {
				if err := b.setColumn(tx, task.ID, api.ColumnDone, task.Rank, api.ResolutionCancelled); err != nil {
					return err
				}
				moves = append(moves, move{id: task.ID, from: task.Col, to: api.ColumnDone, action: ActionCancel, deleteBranch: &del, discard: &discard})
			}
		}
		if err := b.setColumn(tx, t.ID, api.ColumnDone, t.Rank, api.ResolutionCancelled); err != nil {
			return err
		}
		moves = append(moves, move{id: t.ID, from: t.Col, to: api.ColumnDone, action: ActionCancel, deleteBranch: &del, discard: &discard})
		if err := b.finish(tx, a, moves); err != nil {
			return err
		}
		if err := b.tellPM(tx, a, t, fmt.Sprintf("Task #%d cancelled by the user.", t.ID)); err != nil {
			return err
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return forActor(a, out, err)
}

// mayCancel: the user always, the CEO tickets it created, a pm the tasks it
// created in its story.
func mayCancel(a Actor, t ticketRow) bool {
	switch a.Role {
	case api.RoleUser:
		return true
	case api.RoleCEO:
		return t.CreatedByRole == api.RoleCEO
	case api.RolePM:
		return t.CreatedByRole == api.RolePM && t.inStory() && t.StoryID == a.Ticket
	}
	return false
}

// checkRelease applies the rule for who gives the green light to t and who
// may withdraw it: the user always; the CEO never a ticket it created while
// "The CEO's tickets wait for your approval" is on; otherwise the CEO a
// story only while "The CEO may approve stories" is on; the CEO a standalone
// task it created and a pm a task it created in its story, both only while
// the effective task release is agents.
func checkRelease(q db.Queryer, p Policy, a Actor, t ticketRow) error {
	switch a.Role {
	case api.RoleUser:
		return nil
	case api.RoleCEO:
		if p.CEOTicketsNeedUserApproval && t.CreatedByRole == api.RoleCEO {
			return api.Errorf(api.CodeForbidden,
				"the user approves the tickets the CEO creates (setting \"The CEO's tickets wait for your approval\" is on): "+
					"only the user approves #%d or withdraws its approval; tell the user that #%d waits in Planning for their approval", t.ID, t.ID)
		}
		if t.isStory() {
			if !p.CEOMayApproveStories {
				return api.Errorf(api.CodeForbidden,
					"the CEO may not approve stories: the setting \"The CEO may approve stories\" is off; the user approves #%d", t.ID)
			}
			return nil
		}
		if !t.standalone() || t.CreatedByRole != api.RoleCEO {
			return api.Errorf(api.CodeForbidden, "the CEO releases only standalone tasks it created; #%d is not one", t.ID)
		}
		if effectiveTaskRelease(p, ticketRow{}) == api.TaskReleaseUser {
			return api.Errorf(api.CodeForbidden, "tasks are released by the user (Settings, Review: Tasks are released by is You); the user releases #%d", t.ID)
		}
		return nil
	case api.RolePM:
		if !t.inStory() || t.StoryID != a.Ticket || t.CreatedByRole != api.RolePM {
			return api.Errorf(api.CodeForbidden, "a product manager releases only tasks it created in its story #%d; #%d is not one", a.Ticket, t.ID)
		}
		story, err := loadTicket(q, t.StoryID)
		if err != nil {
			return err
		}
		if effectiveTaskRelease(p, story) == api.TaskReleaseUser {
			return api.Errorf(api.CodeForbidden, "the tasks of story #%d are released by the user (Tasks are released by is You for this story or in Settings, Review); the user releases #%d", story.ID, t.ID)
		}
		return nil
	}
	return api.Errorf(api.CodeForbidden, "a %s cannot approve or withdraw tickets", a.Role)
}

// approve moves t from Planning to Ready.
func (b *Board) approve(tx *sql.Tx, p Policy, a Actor, t ticketRow, before *int) error {
	if err := checkRelease(tx, p, a, t); err != nil {
		return err
	}
	ps, err := loadProjects(tx, t.ID)
	if err != nil {
		return err
	}
	if len(ps) == 0 && t.isStory() {
		return api.Errorf(api.CodeValidation, "story #%d has no project; add at least one before approving it", t.ID)
	}
	for _, pr := range ps {
		if _, err := b.availableProject(pr.id); err != nil {
			return err
		}
		if !t.inStory() && pr.base == "" {
			return api.Errorf(api.CodeValidation,
				"#%d has no base branch for project %q (its folder was on a detached HEAD); the user sets one under Base branch in the ticket's details before approving", t.ID, pr.id)
		}
	}
	if err := b.relocate(tx, a, t, api.ColumnReady, before, ActionApprove); err != nil {
		return err
	}
	return b.tellPM(tx, a, t, fmt.Sprintf("Task #%d released by the user.", t.ID))
}

// withdraw moves t from Ready back to Planning while no agent works on it.
func (b *Board) withdraw(tx *sql.Tx, p Policy, a Actor, t ticketRow, before *int) error {
	if err := checkRelease(tx, p, a, t); err != nil {
		return err
	}
	if err := checkNotBusy(tx, t); err != nil {
		return err
	}
	var live int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM agent_sessions s JOIN tickets k ON k.id = s.ticket_id
		WHERE s.state <> 'ended' AND (k.id = ?1 OR k.story_id = ?1)`, t.ID).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return api.Errorf(api.CodeInvalidTransition,
			"#%d cannot go back to planning: an agent has started on it; wait for it to end", t.ID)
	}
	if err := b.relocate(tx, a, t, api.ColumnPlanning, before, ActionWithdraw); err != nil {
		return err
	}
	return b.tellPM(tx, a, t, fmt.Sprintf("Task #%d withdrawn to planning by the user.", t.ID))
}

// stop moves t from In Progress back to Planning or Ready; its branch and
// worktree stay. Stopping a story also returns its tasks in In Progress or
// Review to Ready. The engine stops the agents when it sees the events.
func (b *Board) stop(tx *sql.Tx, a Actor, t ticketRow, to api.Column, before *int) error {
	if a.Role != api.RoleUser {
		return api.Errorf(api.CodeForbidden, "only the user stops work on a ticket")
	}
	if err := checkNotBusy(tx, t); err != nil {
		return err
	}
	rank, ok, err := b.placeRank(tx, a, to, before, t.ID)
	if err != nil {
		return err
	}
	if !ok {
		rank = t.Rank
	}
	if err := b.setColumn(tx, t.ID, to, rank, ""); err != nil {
		return err
	}
	moves := []move{{id: t.ID, from: t.Col, to: to, action: ActionStop}}
	if t.isStory() {
		tasks, err := storyTasks(tx, t.ID, `col IN ('in_progress', 'review')`)
		if err != nil {
			return err
		}
		end, err := endRank(tx, api.ColumnReady)
		if err != nil {
			return err
		}
		for i, task := range tasks {
			if err := b.setColumn(tx, task.ID, api.ColumnReady, end+float64(i*rankStep), ""); err != nil {
				return err
			}
			moves = append(moves, move{id: task.ID, from: task.Col, to: api.ColumnReady, action: ActionStop})
		}
	}
	if err := b.finish(tx, a, moves); err != nil {
		return err
	}
	return b.tellPM(tx, a, t, fmt.Sprintf("Task #%d stopped by the user and moved back to %s.", t.ID, columnWords(to)))
}

// reorder moves t within its column (user only).
func (b *Board) reorder(tx *sql.Tx, a Actor, t ticketRow, before *int) error {
	if a.Role != api.RoleUser {
		return api.Errorf(api.CodeForbidden, "only the user changes the order of cards")
	}
	rank, ok, err := b.placeRank(tx, a, t.Col, before, t.ID)
	if err != nil || !ok || rank == t.Rank {
		return err
	}
	if _, err := tx.Exec(`UPDATE tickets SET rank = ?, updated_at = ? WHERE id = ?`, rank, b.stamp(), t.ID); err != nil {
		return err
	}
	return emit(tx, api.KindTicketReordered, t.ID, a, api.TicketReorderedData{Ticket: int(t.ID), Column: t.Col, Rank: rank})
}

// relocate moves one ticket to another column at the place before names (nil
// = end) and records the move.
func (b *Board) relocate(tx *sql.Tx, a Actor, t ticketRow, to api.Column, before *int, action string) error {
	rank, ok, err := b.placeRank(tx, a, to, before, t.ID)
	if err != nil {
		return err
	}
	if !ok {
		rank = t.Rank
	}
	if err := b.setColumn(tx, t.ID, to, rank, ""); err != nil {
		return err
	}
	return b.finish(tx, a, []move{{id: t.ID, from: t.Col, to: to, action: action}})
}

// setColumn puts ticket n into col with rank. Leaving Review clears the
// review stage; every column change clears the attention flag, the failed
// step and the reason a start is blocked, which all belonged to the old
// state, and ends omo's claim: the engine moves a claimed ticket itself, and
// every other move is refused while the claim lasts. Back in In Progress the
// pull requests and watches of an earlier export attempt are forgotten: the work changes,
// so the next export pushes every project again. What a project's export
// left out or failed with is forgotten with any move except the one that
// closes the ticket as exported. Done sets the resolution
// and closing time, closes the ticket's open questions and removes its
// proxy forwards (with removal events) in this transaction; it is refused
// while the commits of a project's diff are not recorded (checkRecorded).
// setColumn is the only writer of a ticket's column (guard test).
func (b *Board) setColumn(tx *sql.Tx, n int64, col api.Column, rank float64, res api.Resolution) error {
	now := b.stamp()
	var closed any
	if col == api.ColumnDone {
		closed = now
		if err := checkRecorded(tx, n, res); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE tickets SET col = ?, rank = ?, resolution = ?, closed_at = ?,
		review_stage = CASE WHEN ? = 'review' THEN review_stage ELSE '' END,
		attention = '', attention_owner = '', failed_step = '', start_blocked = '', busy = 0,
		updated_at = ? WHERE id = ?`, col, rank, res, closed, col, now, n); err != nil {
		return err
	}
	if col == api.ColumnInProgress {
		if _, err := tx.Exec(`UPDATE tickets SET export_url = '', export_merged = 0 WHERE id = ?`, n); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE ticket_projects SET export_url = '', export_remote = '', export_branch = '', export_base = '', export_commit = '', export_target_commit = '' WHERE ticket_id = ?`, n); err != nil {
			return err
		}
	}
	if col != api.ColumnDone || res != api.ResolutionExported {
		// What the export left out or failed with belongs to the export.
		if _, err := tx.Exec(`UPDATE ticket_projects SET export_state = '', export_error = ''
			WHERE ticket_id = ? AND export_state <> ''`, n); err != nil {
			return err
		}
	}
	if col == api.ColumnDone {
		if err := closeProxyForwards(tx, n); err != nil {
			return err
		}
		return b.closeQuestions(tx, n, res)
	}
	return nil
}

// closeQuestions closes the open questions of ticket n, which has just
// closed with res: one system comment says so and becomes their answer, and
// question.answered is emitted for each, so the askers learn that no answer
// will come. A question asked later stays open.
func (b *Board) closeQuestions(tx *sql.Tx, n int64, res api.Resolution) error {
	var ids []int64
	if err := each(tx, `SELECT id FROM comments WHERE ticket_id = ? AND question_to <> '' AND answered_by IS NULL
		ORDER BY id`, n, func(rows *sql.Rows) error {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	}); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	answer, err := b.addComment(tx, System, n,
		fmt.Sprintf("#%d is done (%s): its open questions are closed without an answer.", n, res))
	if err != nil {
		return err
	}
	raw, err := jsonIDs(ids)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE comments SET answered_by = ? WHERE id`+idsIn, answer, raw); err != nil {
		return err
	}
	for _, id := range ids {
		if err := emit(tx, api.KindQuestionAnswered, n, System, api.QuestionAnsweredData{
			Ticket: int(n), Question: int(id), Answer: int(answer),
		}); err != nil {
			return err
		}
	}
	return nil
}

// setStage changes the review stage of a ticket in Review and sets its
// attention flag. While the ticket has a failed step its attention flag is
// that step's reason and stays: the two are cleared together, by Retry,
// ClearFailed or a column change.
func (b *Board) setStage(tx *sql.Tx, n int64, stage api.ReviewStage, attention string) error {
	if stage == api.ReviewStageExporting {
		// A new export attempt tries the failed projects afresh.
		if _, err := tx.Exec(`UPDATE ticket_projects SET export_state = '', export_error = ''
			WHERE ticket_id = ? AND export_state = 'failed'`, n); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`UPDATE tickets SET review_stage = ?,
		attention = CASE WHEN failed_step = '' THEN ? ELSE attention END,
		attention_owner = CASE WHEN failed_step = '' THEN '' ELSE attention_owner END, updated_at = ? WHERE id = ?`,
		stage, attention, b.stamp(), n)
	return err
}

// storyTasks returns the tasks of a story matching cond (a condition on the
// tickets table), in card order.
func storyTasks(q db.Queryer, story int64, cond string) ([]ticketRow, error) {
	rows, err := q.Query(ticketSelect+` WHERE t.story_id = ? AND t.`+cond+` ORDER BY t.rank, t.id`, story)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ticketRow
	for rows.Next() {
		r, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// finish emits ticket.moved for every move, then ticket.updated (derived)
// for the stories of the moved tickets and for every ticket that depends on
// one of them, unless that ticket moved itself. Summaries are built in two
// batches, however many tickets are involved.
func (b *Board) finish(tx *sql.Tx, a Actor, moves []move) error {
	ids := make([]int64, len(moves))
	for i, m := range moves {
		ids[i] = m.id
	}
	byID, err := b.summariesOf(tx, ids)
	if err != nil {
		return err
	}
	for _, m := range moves {
		s, ok := byID[m.id]
		if !ok {
			return fmt.Errorf("moved ticket #%d vanished", m.id)
		}
		data := api.TicketMovedData{Ticket: s, From: m.from, To: m.to, Action: m.action,
			DeleteBranch: m.deleteBranch, DiscardUncommitted: m.discard}
		if err := emit(tx, api.KindTicketMoved, m.id, a, data); err != nil {
			return err
		}
	}
	raw, err := jsonIDs(ids)
	if err != nil {
		return err
	}
	var related []int64
	if err := each(tx, `SELECT id FROM (
			SELECT story_id AS id FROM tickets WHERE story_id IS NOT NULL AND id IN (SELECT value FROM json_each(?1))
			UNION SELECT ticket_id FROM dependencies WHERE depends_on_id IN (SELECT value FROM json_each(?1)))
		ORDER BY id`, raw, func(rows *sql.Rows) error {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if _, moved := byID[id]; !moved {
			related = append(related, id)
		}
		return nil
	}); err != nil {
		return err
	}
	return b.touch(tx, a, related...)
}
