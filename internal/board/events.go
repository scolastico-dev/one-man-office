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
// non-zero ticket in ns, in the given order. The summaries are built in one
// batch, so the cost does not grow with the number of queries per ticket.
func (b *Board) touch(tx db.Queryer, a Actor, ns ...int64) error {
	ids := distinctIDs(ns)
	if len(ids) == 0 {
		return nil
	}
	byID, err := b.summariesOf(tx, ids)
	if err != nil {
		return err
	}
	for _, n := range ids {
		s, ok := byID[n]
		if !ok {
			return api.Errorf(api.CodeNotFound, "ticket #%d does not exist", n)
		}
		data := api.TicketUpdatedData{Ticket: s, Fields: []string{fieldDerived}}
		if err := emit(tx, api.KindTicketUpdated, n, a, data); err != nil {
			return err
		}
	}
	return nil
}

// distinctIDs returns the non-zero ids of ns without repetitions, in order.
func distinctIDs(ns []int64) []int64 {
	seen := make(map[int64]bool, len(ns))
	out := make([]int64, 0, len(ns))
	for _, n := range ns {
		if n != 0 && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// summariesOf returns the summaries of the given tickets by number, built
// with one call of summaries.
func (b *Board) summariesOf(q db.Queryer, ids []int64) (map[int64]api.TicketSummary, error) {
	raw, err := jsonIDs(ids)
	if err != nil {
		return nil, err
	}
	ss, err := b.summaries(q, `t.id`+idsIn, raw)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]api.TicketSummary, len(ss))
	for _, s := range ss {
		out[int64(s.Number)] = s
	}
	return out, nil
}

// jsonIDs encodes ticket numbers as a JSON array for json_each.
func jsonIDs(ids []int64) (string, error) {
	if ids == nil {
		ids = []int64{}
	}
	raw, err := json.Marshal(ids)
	return string(raw), err
}

// apiEvent builds the wire form of a stored board event of ticket n. The
// actor column holds the JSON actor; an unreadable one is left empty rather
// than failing the history.
func apiEvent(e api.Event, n int64, actor, detail, at string) (api.Event, error) {
	_ = json.Unmarshal([]byte(actor), &e.Actor)
	t := int(n)
	e.Ticket = &t
	if detail == "" {
		detail = "{}"
	}
	e.Data = json.RawMessage(detail)
	var err error
	e.At, err = parseTime(at)
	return e, err
}
