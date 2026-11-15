package board

import (
	"encoding/json"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// fieldDerived marks a ticket.updated event whose stored fields did not
// change, only values computed from other tickets (waiting_for, progress,
// questions, agents).
const fieldDerived = "derived"

// emit appends an event with the JSON-encoded actor and data. A ticket of 0
// stores no ticket.
func emit(tx db.Queryer, kind string, ticket int64, a Actor, data any) error {
	actor, err := json.Marshal(a.API())
	if err != nil {
		return err
	}
	detail, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := db.AppendEvent(tx, kind, string(actor), ticket, string(detail)); err != nil {
		return fmt.Errorf("append %s event: %w", kind, err)
	}
	return nil
}

// emitUpdated emits ticket.updated for n with its current summary and the
// names of the changed fields.
func (b *Board) emitUpdated(tx db.Queryer, a Actor, n int64, fields ...string) error {
	s, err := b.summary(tx, n)
	if err != nil {
		return err
	}
	if fields == nil {
		fields = []string{}
	}
	return emit(tx, api.KindTicketUpdated, n, a, api.TicketUpdatedData{Ticket: s, Fields: fields})
}

// touch emits ticket.updated with fields ["derived"] once for every distinct
// non-zero ticket in ns.
func (b *Board) touch(tx db.Queryer, a Actor, ns ...int64) error {
	seen := make(map[int64]bool, len(ns))
	for _, n := range ns {
		if n == 0 || seen[n] {
			continue
		}
		seen[n] = true
		if err := b.emitUpdated(tx, a, n, fieldDerived); err != nil {
			return err
		}
	}
	return nil
}
