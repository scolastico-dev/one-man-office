package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// maxBounces is how often a ticket may go back to work, by rejected
// reviews or by merge conflicts each, without a person acting on it before
// omo starts and merges nothing more for it.
const maxBounces = 5

// bounces counts how often ticket n went back to work since the user last
// acted on it: reviews rejected (by a reviewer or the user) and merges that
// conflicted.
func (e *Engine) bounces(ctx context.Context, n int64) (rejected, conflicts int, err error) {
	evs, err := e.b.TicketEvents(ctx, board.System, n)
	if err != nil {
		return 0, 0, err
	}
	for _, ev := range evs {
		if ev.Actor.Role == api.RoleUser {
			rejected, conflicts = 0, 0
			continue
		}
		if ev.Kind != api.KindTicketMoved {
			continue
		}
		var d api.TicketMovedData
		if json.Unmarshal(ev.Data, &d) != nil || int64(d.Ticket.Number) != n || d.To != api.ColumnInProgress {
			continue
		}
		switch d.Action {
		case board.ActionVerdict, board.ActionReject:
			rejected++
		case board.ActionConflict:
			conflicts++
		}
	}
	return rejected, conflicts, nil
}

// tooManyBounces returns the reason a person must decide about ticket n,
// which went back and forth more than maxBounces times since a person last
// acted on it, or "".
func (e *Engine) tooManyBounces(ctx context.Context, n int64) (string, error) {
	rejected, conflicts, err := e.bounces(ctx, n)
	if err != nil {
		return "", err
	}
	var what string
	switch {
	case rejected > maxBounces:
		what = fmt.Sprintf("its review was rejected %d times", rejected)
	case conflicts > maxBounces:
		what = fmt.Sprintf("merging it conflicted %d times", conflicts)
	default:
		return "", nil
	}
	return fmt.Sprintf("#%d has gone back and forth: %s since a person last acted on it. omo starts and merges nothing more for it; a person should decide how to go on (clarify the ticket, change it, or cancel it), then retry.",
		n, what), nil
}
