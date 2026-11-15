package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"slices"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// maxAttempts is how many starts or merges of one action in a row may fail
// without a failed step before the engine sets one and leaves the decision
// to a person.
const maxAttempts = 3

// retryAfter is how long after its last try an action that changed nothing
// on the board waits before the health check lets it run again, by the
// number of its attempts in a row that failed: one that did not fail runs
// again at the next health check, a failing one backs off, so that the
// maxAttempts-th failure, which hands the action to a person, comes about
// twelve minutes after the first: long enough for a passing problem (a
// locked file, a busy repository, a slow disk) to go away. A change on the
// board, or anything the user does, lets it run again at once.
var retryAfter = []time.Duration{0, 2 * time.Minute, 10 * time.Minute}

// triedAt is the last try of an action: the newest event id of the board
// it was tried on and when.
type triedAt struct {
	event int64
	at    time.Time
}

// retryDelay returns how long the action with key waits after its last try
// before a health check lets it run again. The caller holds e.mu.
func (e *Engine) retryDelay(key string) time.Duration {
	n := 0
	if f := e.fails[key]; f != nil {
		n = f.count
	}
	return retryAfter[min(n, len(retryAfter)-1)]
}

// forgetTried lets every action whose back-off is over at now be tried
// again on the next pass, even on an unchanged board.
func (e *Engine) forgetTried(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for key, t := range e.tried {
		if now.Sub(t.at) >= e.retryDelay(key) {
			delete(e.tried, key)
		}
	}
}

// attemptFailure is the run of failed attempts of one action.
type attemptFailure struct {
	count int
	last  error
}

// actionKey names an action for firstTry and settleAttempt.
func actionKey(a action) string {
	return fmt.Sprintf("%s|%d|%s|%s|%s", a.kind, a.ticket, a.role, a.project, a.purpose)
}

// firstTry reports whether a has not been tried on a board that has not
// changed since, and records the try (forgetTried lets it run again once its
// back-off is over). The engine's own bookkeeping does not
// count as a change: the events its claims, their release, the reasons
// a start is blocked and the branches a failed start recorded and forgot
// produce. An event of the user forgets the failed
// attempts of a.
func (e *Engine) firstTry(ctx context.Context, a action, eventID int64) bool {
	key := actionKey(a)
	e.mu.Lock()
	last, tried := e.tried[key]
	e.mu.Unlock()
	if tried && last.event != eventID {
		own, byUser := e.changesSince(ctx, last.event, eventID)
		e.mu.Lock()
		if byUser {
			delete(e.fails, key)
		}
		if t, ok := e.tried[key]; own && ok {
			// Still the same try: its back-off keeps running.
			t.event = eventID
			e.tried[key] = t
		}
		e.mu.Unlock()
		if own {
			return false
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if last, ok := e.tried[key]; ok && last.event == eventID {
		return false
	}
	if len(e.tried) > 4096 {
		clear(e.tried)
	}
	e.tried[key] = triedAt{event: eventID, at: e.now()}
	return true
}

// maxEventsLooked is how many events firstTry reads to tell the engine's
// own bookkeeping from a change; more than that count as a change.
const maxEventsLooked = 200

// changesSince reads the events after id after, up to upto, and reports
// whether all of them are the engine's own bookkeeping, and whether any of
// them is the user's.
func (e *Engine) changesSince(ctx context.Context, after, upto int64) (own, byUser bool) {
	evs, err := e.b.EventsAfter(ctx, after, maxEventsLooked)
	if err != nil {
		e.log.Printf("engine: read events: %v", err)
		return false, false
	}
	own = len(evs) < maxEventsLooked || evs[len(evs)-1].ID >= upto
	for _, ev := range evs {
		if ev.ID > upto {
			break
		}
		if ev.Actor.Role == api.RoleUser {
			byUser = true
		}
		if !bookkeeping(ev) {
			own = false
		}
	}
	return own, byUser
}

// bookkeeping reports whether ev only records the engine's claim on a
// ticket, its release, why a start is blocked, or a branch a failed start
// cut and removed again.
func bookkeeping(ev api.Event) bool {
	if ev.Kind != api.KindTicketUpdated || ev.Actor.Role != api.RoleSystem {
		return false
	}
	var d api.TicketUpdatedData
	if json.Unmarshal(ev.Data, &d) != nil || len(d.Fields) == 0 {
		return false
	}
	for _, f := range d.Fields {
		if !slices.Contains([]string{"busy", "start_blocked", "derived", "projects"}, f) {
			return false
		}
	}
	return true
}

// attempt runs one start or merge of a and settles its outcome; a panic
// counts as a failure.
func (e *Engine) attempt(ctx context.Context, a action, run func() error) {
	var err error
	defer func() {
		if r := recover(); r != nil {
			e.log.Printf("engine: worker for #%d panicked: %v\n%s", a.ticket, r, debug.Stack())
			err = fmt.Errorf("omo hit an internal error (%v)", r)
		}
		e.settleAttempt(ctx, a, err)
	}()
	err = run()
}

// settleAttempt records the outcome of one start or merge of a. A success,
// or a failure recorded on the ticket, ends the run of failures; a failure
// without a failed step adds to it, and the maxAttempts-th in a row sets
// the failed step start or merge with the last error, so a person decides
// instead of the engine trying forever.
func (e *Engine) settleAttempt(ctx context.Context, a action, err error) {
	key := actionKey(a)
	e.mu.Lock()
	if err == nil {
		delete(e.fails, key)
		e.mu.Unlock()
		return
	}
	f := e.fails[key]
	if f == nil {
		f = &attemptFailure{}
		e.fails[key] = f
	}
	f.count++
	f.last = err
	n := f.count
	if n >= maxAttempts {
		delete(e.fails, key)
	}
	e.mu.Unlock()
	if n < maxAttempts {
		return
	}
	step, what := api.FailedStart, "start "+string(a.role)+" on"
	if a.kind == actMerge {
		step, what = api.FailedMerge, "merge"
	}
	reason := fmt.Sprintf("omo tried to %s #%d %d times in a row and failed each time: %v. Check the cause (omo's log has the details), then retry.",
		what, a.ticket, n, err)
	if serr := e.b.SetFailed(context.WithoutCancel(ctx), a.ticket, step, reason); serr != nil {
		e.log.Printf("engine: flag #%d: %v", a.ticket, serr)
	}
}
