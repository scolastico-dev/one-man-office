package board

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// columnOrderOf returns the numbers of the open tickets of col in card order.
func columnOrderOf(t *testing.T, b *Board, col api.Column) []int {
	t.Helper()
	l, err := b.List(context.Background(), User, api.TicketFilter{Column: col})
	if err != nil {
		t.Fatal(err)
	}
	return numbers(l.Tickets)
}

func TestReorderAndRenumber(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	newTask := func() int {
		return mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}}).Number
	}
	a, x, c := newTask(), newTask(), newTask()

	last := lastEvent(t, b)
	got, err := b.Move(ctx, User, int64(c), api.MoveRequest{Column: api.ColumnPlanning, Before: intPtr(x)})
	if err != nil {
		t.Fatal(err)
	}
	if order := columnOrderOf(t, b, api.ColumnPlanning); !slices.Equal(order, []int{a, c, x}) {
		t.Fatalf("order = %v", order)
	}
	evs := eventsAfter(t, b, last)
	if len(evs) != 1 || evs[0].Kind != api.KindTicketReordered {
		t.Fatalf("events = %+v", evs)
	}
	var data api.TicketReorderedData
	if err := json.Unmarshal([]byte(evs[0].Detail), &data); err != nil || data.Rank != got.Rank || data.Column != api.ColumnPlanning {
		t.Fatalf("data = %+v, %v", data, err)
	}

	// Before the first ticket, and to the end.
	if _, err := b.Move(ctx, User, int64(x), api.MoveRequest{Column: api.ColumnPlanning, Before: intPtr(a)}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Move(ctx, User, int64(a), api.MoveRequest{Column: api.ColumnPlanning}); err != nil {
		t.Fatal(err)
	}
	if order := columnOrderOf(t, b, api.ColumnPlanning); !slices.Equal(order, []int{x, c, a}) {
		t.Fatalf("order = %v", order)
	}
	// Placing a ticket above itself changes nothing.
	last = lastEvent(t, b)
	if _, err := b.Move(ctx, User, int64(x), api.MoveRequest{Column: api.ColumnPlanning, Before: intPtr(x)}); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, b, last); len(evs) != 0 {
		t.Fatalf("events = %+v", evs)
	}

	// Repeated inserts directly above a run out of room and renumber.
	last = lastEvent(t, b)
	var inserted []int
	for range 60 {
		n := newTask()
		if _, err := b.Move(ctx, User, int64(n), api.MoveRequest{Column: api.ColumnPlanning, Before: intPtr(a)}); err != nil {
			t.Fatal(err)
		}
		inserted = append(inserted, n)
	}
	var renumbered *api.ColumnRenumberedData
	for _, e := range eventsAfter(t, b, last) {
		if e.Kind == api.KindColumnRenumbered {
			renumbered = &api.ColumnRenumberedData{}
			if err := json.Unmarshal([]byte(e.Detail), renumbered); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if renumbered == nil {
		t.Fatal("no column.renumbered event")
	}
	if renumbered.Column != api.ColumnPlanning {
		t.Fatalf("column %q", renumbered.Column)
	}
	ranks := slices.Sorted(func(yield func(float64) bool) {
		for _, r := range renumbered.Ranks {
			if !yield(r) {
				return
			}
		}
	})
	for i, r := range ranks {
		if r != float64((i+1)*rankStep) {
			t.Fatalf("ranks = %v", ranks)
		}
	}
	if renumbered.Ranks[strconv.Itoa(x)] != rankStep {
		t.Fatalf("#%d not first: %v", x, renumbered.Ranks)
	}
	want := append(append([]int{x, c}, inserted...), a)
	if order := columnOrderOf(t, b, api.ColumnPlanning); !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestReorderRules(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	a := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
	other := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "B", Projects: []string{"api"}})
	force(t, b, other.Number, api.ColumnReady, "")
	_, err := b.Move(ctx, User, int64(a.Number), api.MoveRequest{Column: api.ColumnPlanning, Before: intPtr(other.Number)})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Move(ctx, User, int64(a.Number), api.MoveRequest{Column: api.ColumnPlanning, Before: intPtr(99)})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Move(ctx, agent(t, b, api.RoleCEO, 0), int64(a.Number), api.MoveRequest{Column: api.ColumnPlanning})
	wantCode(t, err, api.CodeForbidden)
	force(t, b, a.Number, api.ColumnDone, "")
	_, err = b.Move(ctx, User, int64(a.Number), api.MoveRequest{Column: api.ColumnDone})
	wantCode(t, err, api.CodeInvalidTransition)
}
