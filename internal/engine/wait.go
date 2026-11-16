package engine

import (
	"context"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

// MaxWait is the longest timeout omo ai wait accepts.
const MaxWait = 24 * time.Hour

// MinWait is the shortest timeout omo ai wait accepts, so an agent cannot
// spin through waits.
const MinWait = time.Minute

// Wait parks the agent session a until a notification arrives or timeout
// passes, and returns why it returned: the notification lines, or the
// timeout message (with the instruction to investigate on every n-th
// consecutive timeout, n being waiting.timeouts_before_investigate).
// Notifications that arrived before the call return at once. While parked
// the session is waiting and holds no slot. A newer wait of the same session
// replaces this one (which then fails with conflict); the end of the
// session or of its agent, and ctx, release it too.
func (e *Engine) Wait(ctx context.Context, a board.Actor, timeout time.Duration) (string, error) {
	switch a.Role {
	case api.RolePM, api.RoleDeveloper, api.RoleReviewer:
	case api.RoleCEO:
		return "", api.Errorf(api.CodeForbidden, "the CEO does not wait: stay at your prompt for the user")
	default:
		return "", api.Errorf(api.CodeForbidden, "only a product manager, developer or reviewer session waits")
	}
	switch {
	case timeout <= 0:
		return "", api.Errorf(api.CodeValidation, "omo ai wait needs a timeout longer than zero, such as --timeout 30m")
	case timeout < e.minWait:
		return "", api.Errorf(api.CodeValidation, "a wait lasts at least %s; use a longer timeout, such as --timeout 30m", e.minWait)
	case timeout > MaxWait:
		return "", api.Errorf(api.CodeValidation, "a wait lasts at most %s; use a shorter timeout", MaxWait)
	}
	en, ag, ok := e.lookupAgent(a.Session)
	if !ok || ag == nil {
		return "", api.Errorf(api.CodeConflict, "omo is not running this agent session")
	}
	if en.awaitingReady() {
		return "", api.Errorf(api.CodeInvalidTransition, "run omo ai ready first")
	}

	nt := e.nt
	nt.mu.Lock()
	mb := nt.box(a.Session)
	if lines := mb.takeQueue(); len(lines) > 0 {
		nt.mu.Unlock()
		return strings.Join(lines, "\n"), nil
	}
	if old := mb.waiter; old != nil {
		old.release(waitReplaced)
	}
	w := &waiter{wake: make(chan struct{})}
	mb.waiter = w
	nt.mu.Unlock()
	e.syncState(ctx, a.Session, mb)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	timedOut := false
	select {
	case <-w.wake:
	case <-timer.C:
		timedOut = true
	case <-ctx.Done():
	case <-ag.Exited():
	case <-en.finished:
	}

	nt.mu.Lock()
	if mb.waiter == w {
		mb.waiter = nil
	}
	if w.why == waitOpen {
		// Nothing was handed to this wait; from now on nothing will be.
		w.why = waitEnded
		close(w.wake)
	} else {
		timedOut = false
	}
	var reason string
	var err error
	timeouts := 0
	requeued := false
	switch {
	case w.why == waitDelivered && ctx.Err() == nil:
		reason = strings.Join(w.lines, "\n")
	case w.why == waitDelivered:
		// The caller is gone: keep the lines for the agent's next wait,
		// or to be typed.
		mb.queue = append(w.lines, mb.queue...)
		if nt.grace > 0 && mb.timer == nil {
			id := a.Session
			mb.timer = time.AfterFunc(nt.grace, func() { e.flush(id) })
		}
		requeued = nt.grace <= 0
		err = ctx.Err()
	case w.why == waitReplaced:
		err = api.Errorf(api.CodeConflict, "a newer omo ai wait of this session replaced this one")
	case timedOut:
		mb.timeouts++
		timeouts = mb.timeouts
	case ctx.Err() != nil:
		err = ctx.Err()
	default:
		err = api.Errorf(api.CodeConflict, "this agent session has ended")
	}
	nt.mu.Unlock()
	if w.why != waitReplaced {
		e.syncState(ctx, a.Session, mb)
	}
	if requeued {
		e.flush(a.Session)
	}
	if err != nil {
		return "", err
	}
	if timeouts > 0 {
		reason = e.timeoutReason(ctx, a, timeout, timeouts)
	}
	return reason, nil
}

// timeoutReason is the answer of a wait that timed out, the n-th in a row.
// Every waiting.timeouts_before_investigate-th one tells the agent to find
// out why nothing happens, except for a kept agent (see keptAgent): it has
// nothing to do but wait for a rejection, a merge conflict, a question or a
// mention, so it only waits again.
func (e *Engine) timeoutReason(ctx context.Context, a board.Actor, timeout time.Duration, n int) string {
	d := prompts.Data{Name: a.Name, Role: string(a.Role), Ticket: int(a.Ticket), WaitTimeout: timeout.String(), Timeouts: n}
	reason := e.renderPrompt("wait-timeout", d)
	every := e.st.General().Waiting.TimeoutsBeforeInvestigate
	if every < 1 {
		every = 1
	}
	if n%every == 0 && !e.kept(ctx, a) {
		reason += "\n" + e.renderPrompt("wait-investigate", d)
	}
	return reason
}

// kept reports whether the agent session a is kept on its ticket without
// work of its own (see keptAgent).
func (e *Engine) kept(ctx context.Context, a board.Actor) bool {
	t, ok := e.ticketNow(ctx, a.Ticket)
	s := board.EngineSession{ID: a.Session, Role: a.Role, Ticket: a.Ticket, Project: a.Project, Purpose: a.Purpose,
		ReviewRound: a.ReviewRound}
	return ok && keptAgent(s, t)
}
