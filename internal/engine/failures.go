package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// maxFailures is the number of consecutive failures of one agent slot after
// which omo starts no further agent for it until a person acts.
const maxFailures = 3

// tailLines is how many transcript lines go into the comment of a slot that
// failed for good.
const tailLines = 20

// failKey is one agent slot for failure accounting: the agent of role with
// purpose on ticket (for reviewers and export jobs, for project).
type failKey struct {
	ticket  int64
	role    api.Role
	project string
	purpose api.Purpose
}

// failRecord counts the consecutive failures of a slot and remembers where
// its ticket stood at the first of them; when the ticket moves on, the
// count starts again.
type failRecord struct {
	count  int
	column api.Column
	stage  api.ReviewStage
	round  int
}

// slotKey returns the failure slot of a session.
func slotKey(s board.EngineSession) failKey {
	k := failKey{ticket: s.Ticket, role: s.Role, purpose: purposeOf(s)}
	if s.Role == api.RoleReviewer || k.purpose == api.PurposeExport {
		k.project = s.Project
	}
	return k
}

// attempts returns the number of consecutive failures of a slot.
func (e *Engine) attempts(k failKey) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r := e.failures[k]; r != nil {
		return r.count
	}
	return 0
}

// countFailure adds one failure to slot k of ticket t and returns the new
// count.
func (e *Engine) countFailure(k failKey, t board.EngineTicket) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.failures[k]
	if r == nil {
		r = &failRecord{column: t.Column, stage: t.ReviewStage, round: t.ReviewRound}
		e.failures[k] = r
	}
	r.count++
	return r.count
}

// resetFailures forgets the failures of every slot whose ticket made
// progress since: it changed column, review stage or review round, is
// gone, or got a failed step (the person who retries it starts with a clean
// count). The caller holds e.mu.
func (e *Engine) resetFailures(snap board.EngineSnapshot) {
	if len(e.failures) == 0 {
		return
	}
	byID := make(map[int64]board.EngineTicket, len(snap.Tickets))
	for _, t := range snap.Tickets {
		byID[t.ID] = t
	}
	for k, r := range e.failures {
		t, ok := byID[k.ticket]
		if !ok || t.Column != r.column || t.ReviewStage != r.stage || t.ReviewRound != r.round ||
			t.FailedStep != api.FailedNone {
			delete(e.failures, k)
		}
	}
}

// resetFailuresOf forgets every failure counted for ticket n (a retry).
func (e *Engine) resetFailuresOf(n int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k := range e.failures {
		if k.ticket == n {
			delete(e.failures, k)
		}
	}
}

// recordFailure counts a failure of en's slot and tells the ticket; what
// says what happened ("exited unexpectedly (exit status 1)"). The CEO is
// never counted: it is not restarted by itself.
func (e *Engine) recordFailure(ctx context.Context, en *entry, what string) {
	if en.sess.Role == api.RoleCEO || en.sess.Ticket == 0 {
		return
	}
	var tail []string
	if en.agent != nil {
		tail = en.agent.Tail(tailLines)
	}
	e.failed(ctx, en.slot, en.sess.Name, what, tail)
}

// failed counts a failure of slot k by the agent called name and posts what
// follows from it on the ticket: below the limit a comment that a fresh
// agent follows; at the limit a failed step carrying the end of the
// transcript (an export job fails its export instead).
func (e *Engine) failed(ctx context.Context, k failKey, name, what string, tail []string) {
	t, ok := e.ticketNow(ctx, k.ticket)
	if !ok || t.Column == api.ColumnDone || t.Column == api.ColumnPlanning || t.Column == api.ColumnReady {
		return // the ticket no longer needs the agent: nothing starts again
	}
	n := e.countFailure(k, t)
	if n < maxFailures {
		msg := fmt.Sprintf("%s %s; omo starts a fresh agent.", name, what)
		if t.Column == api.ColumnReview && k.role == workingRole(t) && k.purpose == api.PurposeWork {
			// The working agent of a ticket in review was parked: nothing
			// replaces it until the ticket goes back to work.
			msg = fmt.Sprintf("%s %s while #%d is in review. omo starts no agent for it now; a fresh one starts if #%d goes back to work (a rejected review or a merge conflict).",
				name, what, t.ID, t.ID)
		}
		if _, err := e.b.Comment(ctx, board.System, k.ticket, api.CommentRequest{Body: msg}); err != nil {
			e.log.Printf("engine: comment on #%d: %v", k.ticket, err)
		}
		return
	}
	msg := fmt.Sprintf("%s %s, failure %d in a row; omo starts no further agent here until someone retries.",
		name, what, n)
	if len(tail) > 0 {
		msg += "\n\nLast output:\n" + strings.Join(tail, "\n")
	}
	if k.purpose == api.PurposeExport {
		e.comment(ctx, k.ticket, msg)
		e.projectExportFailed(ctx, k.ticket, k.project, fmt.Sprintf("its export job %s, failure %d in a row", what, n))
		return
	}
	if err := e.b.SetFailed(ctx, k.ticket, api.FailedAgent, msg); err != nil {
		e.log.Printf("engine: flag #%d: %v", k.ticket, err)
	}
}

// ticketNow reads ticket n from a fresh snapshot; ok is false when it is
// done and holds nothing on disk, or gone.
func (e *Engine) ticketNow(ctx context.Context, n int64) (board.EngineTicket, bool) {
	t, _, ok, err := e.read(ctx, n)
	if err != nil {
		e.log.Printf("engine: read #%d: %v", n, err)
	}
	return t, ok
}

// read returns ticket n and the snapshot it came from.
func (e *Engine) read(ctx context.Context, n int64) (board.EngineTicket, board.EngineSnapshot, bool, error) {
	snap, err := e.b.EngineSnapshot(ctx)
	if err != nil {
		return board.EngineTicket{}, snap, false, err
	}
	for _, t := range snap.Tickets {
		if t.ID == n {
			return t, snap, true, nil
		}
	}
	return board.EngineTicket{}, snap, false, nil
}
