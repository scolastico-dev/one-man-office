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

// Claims keep the engine and the user out of each other's way. Before the
// engine does slow work for a ticket outside any transaction (cutting
// branches for a start, merging), it claims the ticket: a check-and-set of
// tickets.busy in one transaction. While the claim lasts, the user actions
// that would pull the ground from under that work (cancel, stop, withdraw,
// reject, a change of projects, base branches or story, a new dependency)
// are refused with conflict. The claim ends with the transaction
// that records the outcome: a column change (BeginWork, CompleteMerge,
// MergeConflict), SetFailed or ReleaseClaim. A claim left behind by a crash
// is released by ReleaseAllClaims when the engine starts.
//
// A failed step records an engine step that cannot go on without a person.
// The engine starts and merges nothing for such a ticket until Retry or a
// column change clears it.

// checkNotBusy refuses a user action on t while omo holds a claim on t or,
// for a story, on one of its tasks.
func checkNotBusy(q db.Queryer, t ticketRow) error {
	var id int64
	var col api.Column
	err := q.QueryRow(`SELECT id, col FROM tickets WHERE busy = 1 AND (id = ?1 OR story_id = ?1)
		ORDER BY id <> ?1, id LIMIT 1`, t.ID).Scan(&id, &col)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	what := "starting work on"
	if col == api.ColumnReview {
		what = "merging"
	}
	return api.Errorf(api.CodeConflict, "omo is %s #%d; try again in a moment", what, id)
}

// ClaimStart claims a ticket in Ready For Dev before the engine prepares its
// start. The ticket must be startable: not claimed, no failed step, no
// dependency still blocking it and, for a task in a story, the story in
// progress. A claimed ticket is api conflict, every other reason
// invalid_transition. The claim also clears the reason the start was
// blocked.
func (b *Board) ClaimStart(ctx context.Context, n int64) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if t.Col != api.ColumnReady {
			return api.Errorf(api.CodeInvalidTransition, "#%d is in %s; omo starts work only on a ticket in ready", n, t.Col)
		}
		if t.Busy {
			return checkNotBusy(tx, t)
		}
		if t.FailedStep != api.FailedNone {
			return api.Errorf(api.CodeInvalidTransition, "#%d needs attention (failed step %s); retry it first", n, t.FailedStep)
		}
		if t.inStory() {
			story, err := loadTicket(tx, t.StoryID)
			if err != nil {
				return err
			}
			if story.Col != api.ColumnInProgress {
				return api.Errorf(api.CodeInvalidTransition,
					"#%d belongs to story #%d, which is in %s; its tasks start only while the story is in progress", n, story.ID, story.Col)
			}
		}
		s, err := b.summary(tx, n)
		if err != nil {
			return err
		}
		if len(s.WaitingFor) > 0 {
			w := s.WaitingFor[0]
			return api.Errorf(api.CodeInvalidTransition, "#%d is waiting for #%d (%s)", n, w.Ticket, w.Reason)
		}
		fields := []string{"busy"}
		if t.StartBlocked != api.StartBlockedNone {
			fields = append(fields, "start_blocked")
		}
		if _, err := tx.Exec(`UPDATE tickets SET busy = 1, start_blocked = '' WHERE id = ?`, n); err != nil {
			return err
		}
		return b.claimChanged(tx, t, fields...)
	})
}

// ClaimMerge claims a ticket in Review at stage merging, without a failed
// step, before the engine merges it.
func (b *Board) ClaimMerge(ctx context.Context, n int64) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkStage(t, api.ReviewStageMerging); err != nil {
			return err
		}
		if t.Busy {
			return checkNotBusy(tx, t)
		}
		if t.FailedStep != api.FailedNone {
			return api.Errorf(api.CodeInvalidTransition, "#%d needs attention (failed step %s); retry it first", n, t.FailedStep)
		}
		if _, err := tx.Exec(`UPDATE tickets SET busy = 1 WHERE id = ?`, n); err != nil {
			return err
		}
		return b.claimChanged(tx, t, "busy")
	})
}

// claimChanged emits ticket.updated with fields for t, whose claim changed,
// and a derived update for its story: a story's summary shows the claims of
// its tasks as busy.
func (b *Board) claimChanged(tx *sql.Tx, t ticketRow, fields ...string) error {
	if err := b.emitUpdated(tx, System, t.ID, fields...); err != nil {
		return err
	}
	return b.touch(tx, System, t.StoryID)
}

// ReleaseClaim ends omo's claim on a ticket without recording an outcome,
// for work the engine gave up before it changed anything. Releasing a
// ticket that is not claimed changes nothing.
func (b *Board) ReleaseClaim(ctx context.Context, n int64) error {
	return b.writeChanges(ctx, func(tx *sql.Tx) (bool, error) {
		t, err := loadTicket(tx, n)
		if err != nil || !t.Busy {
			return false, err
		}
		if _, err := tx.Exec(`UPDATE tickets SET busy = 0 WHERE id = ?`, n); err != nil {
			return false, err
		}
		return true, b.claimChanged(tx, t, "busy")
	})
}

// ReleaseAllClaims ends every claim. The engine calls it when it starts: a
// claim that is still set was left by a run that ended in the middle of its
// work, and the engine's next pass redoes that work from the ticket's state.
//
// It must run before the engine's first pass and never while a worker holds
// a claim: it cannot tell a claim left by a crash from one in use, and
// releasing one in use lets the user cancel, stop or edit a ticket while the
// worker is still cutting its branches or merging it.
func (b *Board) ReleaseAllClaims(ctx context.Context) error {
	return b.writeChanges(ctx, func(tx *sql.Tx) (bool, error) {
		var ids, stories []int64
		if err := each(tx, `SELECT id, COALESCE(story_id, 0) FROM tickets WHERE busy = ? ORDER BY id`, 1, func(rows *sql.Rows) error {
			var id, story int64
			if err := rows.Scan(&id, &story); err != nil {
				return err
			}
			ids = append(ids, id)
			stories = append(stories, story)
			return nil
		}); err != nil || len(ids) == 0 {
			return false, err
		}
		if _, err := tx.Exec(`UPDATE tickets SET busy = 0 WHERE busy = 1`); err != nil {
			return false, err
		}
		byID, err := b.summariesOf(tx, ids)
		if err != nil {
			return false, err
		}
		for _, id := range ids {
			data := api.TicketUpdatedData{Ticket: byID[id], Fields: []string{"busy"}}
			if err := emit(tx, api.KindTicketUpdated, id, System, data); err != nil {
				return false, err
			}
		}
		// The stories of released tasks no longer show them as busy.
		slices.Sort(stories)
		stories = slices.DeleteFunc(stories, func(s int64) bool { _, released := byID[s]; return released })
		return true, b.touch(tx, System, stories...)
	})
}

// SetFailed records that an engine step cannot go on without a person: the
// ticket gets the failed step and the reason, cut to one line (see
// attentionLine), as its attention flag, omo's claim on it ends, and a
// system comment with the full reason tells the supervisor. Done tickets
// cannot be flagged. Recording the same step and reason again changes
// nothing.
func (b *Board) SetFailed(ctx context.Context, n int64, step api.FailedStep, reason string) error {
	if !slices.Contains([]api.FailedStep{api.FailedStart, api.FailedAgent, api.FailedMerge}, step) {
		return api.Errorf(api.CodeValidation, "a failed step is start, agent or merge, not %q", step)
	}
	reason = strings.TrimSpace(reason)
	line := attentionLine(reason)
	if line == "" {
		return api.Errorf(api.CodeValidation, "a failed step needs the reason a person has to look at")
	}
	return b.writeChanges(ctx, func(tx *sql.Tx) (bool, error) {
		t, err := loadTicket(tx, n)
		if err != nil {
			return false, err
		}
		if t.Col == api.ColumnDone {
			return false, api.Errorf(api.CodeInvalidTransition, "#%d is done and needs no attention", n)
		}
		var fields []string
		if t.FailedStep != step {
			fields = append(fields, "failed_step")
		}
		if t.Attention != line {
			fields = append(fields, "attention")
		}
		if t.Busy {
			fields = append(fields, "busy")
		}
		if t.StartBlocked != api.StartBlockedNone {
			fields = append(fields, "start_blocked")
		}
		if len(fields) == 0 {
			return false, nil
		}
		if _, err := tx.Exec(`UPDATE tickets SET failed_step = ?, attention = ?, busy = 0, start_blocked = '',
			updated_at = ? WHERE id = ?`, step, line, b.stamp(), n); err != nil {
			return false, err
		}
		if t.Busy {
			err = b.claimChanged(tx, t, fields...)
		} else {
			err = b.emitUpdated(tx, System, n, fields...)
		}
		if err != nil {
			return false, err
		}
		if t.FailedStep == step && t.Attention == line {
			return true, nil
		}
		return true, b.systemComment(tx, n, fmt.Sprintf("Needs attention: %s", reason), t.Supervisor)
	})
}

// ClearFailed clears the failed step and the attention flag of a ticket,
// but only while its failed step is step, so the engine never clears a flag
// another step set meanwhile.
func (b *Board) ClearFailed(ctx context.Context, n int64, step api.FailedStep) error {
	return b.writeChanges(ctx, func(tx *sql.Tx) (bool, error) {
		t, err := loadTicket(tx, n)
		if err != nil || step == api.FailedNone || t.FailedStep != step {
			return false, err
		}
		return true, b.clearFailed(tx, System, t)
	})
}

// clearFailed clears t's failed step and attention flag and emits
// ticket.updated.
func (b *Board) clearFailed(tx *sql.Tx, a Actor, t ticketRow) error {
	if _, err := tx.Exec(`UPDATE tickets SET failed_step = '', attention = '', updated_at = ? WHERE id = ?`,
		b.stamp(), t.ID); err != nil {
		return err
	}
	return b.emitUpdated(tx, a, t.ID, "failed_step", "attention")
}

// Retry clears a ticket's failed step so the engine tries that step again.
// The user retries every ticket, the CEO tickets it supervises, a product
// manager the tasks of its story. Agents retry the same failed step at most
// maxAgentRetries times in a row: the retry that reaches the limit tells the
// user in a system comment, and after it only the user retries.
func (b *Board) Retry(ctx context.Context, a Actor, n int64) (api.Ticket, error) {
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
		if !mayRetry(p, a, t) {
			return api.Errorf(api.CodeForbidden,
				"a %s cannot retry #%d: the user retries every ticket, the CEO tickets it supervises, a product manager the tasks of its story", a.Role, n)
		}
		// A claimed ticket never has a failed step; the claim is the answer
		// the user can act on (wait), so it comes first.
		if t.Busy {
			return checkNotBusy(tx, t)
		}
		if t.FailedStep == api.FailedNone {
			return api.Errorf(api.CodeInvalidTransition, "nothing to retry on #%d", n)
		}
		agentRetries := 0
		if a.isAgent() {
			if agentRetries, err = countAgentRetries(tx, n, t.FailedStep); err != nil {
				return err
			}
			if agentRetries >= maxAgentRetries {
				return api.Errorf(api.CodeForbidden,
					"step %s of #%d failed again after %d retries by agents; the user must decide what happens now", t.FailedStep, n, agentRetries)
			}
		}
		if err := b.clearFailed(tx, a, t); err != nil {
			return err
		}
		if err := emit(tx, api.KindTicketRetried, n, a, api.TicketRetriedData{Ticket: int(n), Step: t.FailedStep}); err != nil {
			return err
		}
		if a.isAgent() && agentRetries+1 == maxAgentRetries {
			if err := b.systemComment(tx, n, fmt.Sprintf(
				"Step %s of #%d has been retried %d times by agents. If it fails again, only the user can retry it.",
				t.FailedStep, n, maxAgentRetries), api.RoleUser); err != nil {
				return err
			}
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return forActor(a, out, err)
}

// maxAgentRetries is how often agents may retry the same failed step of a
// ticket before the user has to decide.
const maxAgentRetries = 2

// countAgentRetries counts the retries of step on ticket n by agents (the
// CEO or a product manager) since the step last went through or a person
// acted, read from the event log: a move of the ticket (a column or review
// stage change), any event of the user on the ticket, or the engine clearing
// a failed step (ClearFailed) starts the count again.
func countAgentRetries(q db.Queryer, n int64, step api.FailedStep) (int, error) {
	var count int
	err := q.QueryRow(`SELECT COUNT(*) FROM events WHERE ticket_id = ?1 AND kind = ?2
		AND json_extract(actor, '$.role') IN (?3, ?4) AND json_extract(detail, '$.step') = ?5
		AND id > (SELECT COALESCE(MAX(id), 0) FROM events WHERE ticket_id = ?1 AND (
			kind = ?6 OR json_extract(actor, '$.role') = ?7
			OR (kind = ?8 AND json_extract(actor, '$.role') = ?9 AND json_extract(detail, '$.ticket.failed_step') = ''
				AND EXISTS (SELECT 1 FROM json_each(detail, '$.fields') WHERE value = 'failed_step'))))`,
		n, api.KindTicketRetried, api.RoleCEO, api.RolePM, step,
		api.KindTicketMoved, api.RoleUser, api.KindTicketUpdated, api.RoleSystem).Scan(&count)
	return count, err
}

// mayRetry: the user always, the CEO on tickets it supervises, a pm on the
// tasks of its story.
func mayRetry(p Policy, a Actor, t ticketRow) bool {
	switch a.Role {
	case api.RoleUser:
		return true
	case api.RoleCEO:
		return effectiveSupervisor(p, t) == api.RoleCEO
	case api.RolePM:
		return t.inStory() && t.StoryID == a.Ticket
	}
	return false
}

// startBlockedFits reports whether a ticket in col may take reason v.
func startBlockedFits(col api.Column, v api.StartBlocked) bool {
	switch col {
	case api.ColumnReady:
		return true
	case api.ColumnInProgress, api.ColumnReview:
		return v == api.StartBlockedUsage || v == api.StartBlockedNone
	}
	return false
}

// SetStartBlocked records why startable tickets in Ready For Dev have not
// started, in one transaction. A ticket in progress or in review takes only
// usage (its next agent waits for usage) or the empty reason; every other
// reason is for Ready only. A ticket in another column, or one that is
// claimed or has a failed step and so is not startable, keeps its reason
// (empty after the column change): it changed since the engine looked.
// Any column change clears the reason. ticket.updated with the field
// start_blocked is emitted for every ticket whose reason changed; when none
// changed nothing is emitted or notified.
func (b *Board) SetStartBlocked(ctx context.Context, changes map[int64]api.StartBlocked) error {
	ids := make([]int64, 0, len(changes))
	for id, v := range changes {
		if !slices.Contains([]api.StartBlocked{api.StartBlockedNone, api.StartBlockedSlot, api.StartBlockedPaused,
			api.StartBlockedNoModel, api.StartBlockedUsage}, v) {
			return api.Errorf(api.CodeValidation, "#%d: a start is blocked by slot, paused, no_model or usage, not %q", id, v)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	return b.writeChanges(ctx, func(tx *sql.Tx) (bool, error) {
		raw, err := jsonIDs(ids)
		if err != nil {
			return false, err
		}
		current := map[int64]ticketRow{}
		rows, err := tx.Query(ticketSelect+` WHERE t.id`+idsIn, raw)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			r, err := scanTicket(rows)
			if err != nil {
				rows.Close()
				return false, err
			}
			current[r.ID] = r
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, err
		}
		var changed []int64
		for _, id := range ids {
			t, ok := current[id]
			want := changes[id]
			if !ok || !startBlockedFits(t.Col, want) || t.StartBlocked == want {
				continue
			}
			if want != api.StartBlockedNone && (t.Busy || t.FailedStep != api.FailedNone) {
				continue
			}
			if _, err := tx.Exec(`UPDATE tickets SET start_blocked = ? WHERE id = ?`, want, id); err != nil {
				return false, err
			}
			changed = append(changed, id)
		}
		if len(changed) == 0 {
			return false, nil
		}
		byID, err := b.summariesOf(tx, changed)
		if err != nil {
			return false, err
		}
		for _, id := range changed {
			data := api.TicketUpdatedData{Ticket: byID[id], Fields: []string{"start_blocked"}}
			if err := emit(tx, api.KindTicketUpdated, id, System, data); err != nil {
				return false, err
			}
		}
		return true, nil
	})
}
