package engine

import (
	"context"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// StartAnyway lets the next start of ticket n use the ticket's own model
// once, although that model is near its usage limit ("Start anyway"). It
// answers invalid_transition unless the ticket waits for usage and has a
// model of its own. The permission is kept in memory until that start or
// until the ticket changes column; a restart of omo forgets it. Only the
// user may ask for it: the route that calls StartAnyway is the user's.
func (e *Engine) StartAnyway(ctx context.Context, n int64) error {
	t, ok := e.ticketNow(ctx, n)
	if !ok {
		return api.Errorf(api.CodeNotFound, "ticket #%d does not exist", n)
	}
	if t.StartBlocked != api.StartBlockedUsage || t.Model == "" {
		return api.Errorf(api.CodeInvalidTransition, "#%d does not wait for usage on a model of its own", n)
	}
	e.mu.Lock()
	if e.usage.forced == nil {
		e.usage.forced = map[int64]api.Column{}
	}
	e.usage.forced[n] = t.Column
	e.mu.Unlock()
	e.Wake()
	return nil
}

// isForced reports whether t may start once on its own model although it is
// near its usage limit.
func (e *Engine) isForced(t board.EngineTicket) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	col, ok := e.usage.forced[t.ID]
	return ok && col == t.Column
}

// clearForced forgets "Start anyway" for ticket n once it was used.
func (e *Engine) clearForced(n int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.usage.forced, n)
}
