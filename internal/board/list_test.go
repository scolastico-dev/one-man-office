package board

import (
	"context"
	"fmt"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// forceDone closes a ticket directly in the database, bypassing the
// transition rules, with a distinct closing time per call.
func forceDone(t *testing.T, b *Board, n int, res api.Resolution) {
	t.Helper()
	if _, err := b.db.Exec(`UPDATE tickets SET col='done', resolution=?, closed_at=? WHERE id=?`, string(res), b.stamp(), n); err != nil {
		t.Fatal(err)
	}
}

func numbers(ts []api.TicketSummary) []int {
	out := make([]int, len(ts))
	for i, t := range ts {
		out[i] = t.Number
	}
	return out
}

func TestListDefaultsAndDonePaging(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	open := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "open"})
	for i := range 60 {
		tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: fmt.Sprintf("done %d", i), Projects: []string{"api"}})
		forceDone(t, b, tk.Number, api.ResolutionCompleted)
	}
	first, err := b.List(ctx, User, api.TicketFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tickets) != 51 {
		t.Fatalf("first page has %d tickets, want 1 open + 50 done", len(first.Tickets))
	}
	if first.Tickets[0].Number != open.Number {
		t.Fatalf("open ticket must come first: %v", numbers(first.Tickets))
	}
	// Done tickets come newest closed first.
	if first.Tickets[1].Number != 61 || first.Tickets[50].Number != 12 {
		t.Fatalf("done order = %v", numbers(first.Tickets[1:]))
	}
	if first.NextDoneBefore != 12 {
		t.Fatalf("NextDoneBefore = %d", first.NextDoneBefore)
	}
	second, err := b.List(ctx, User, api.TicketFilter{DoneBefore: first.NextDoneBefore})
	if err != nil {
		t.Fatal(err)
	}
	var done []api.TicketSummary
	for _, tk := range second.Tickets {
		if tk.Column == api.ColumnDone {
			done = append(done, tk)
		}
	}
	if len(done) != 10 || done[0].Number != 11 || done[9].Number != 2 {
		t.Fatalf("second page done = %v", numbers(done))
	}
	if second.NextDoneBefore != 0 {
		t.Fatalf("second NextDoneBefore = %d", second.NextDoneBefore)
	}
	small, err := b.List(ctx, User, api.TicketFilter{Column: api.ColumnDone, DoneLimit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(small.Tickets) != 5 || small.NextDoneBefore != 57 {
		t.Fatalf("limited page = %v next %d", numbers(small.Tickets), small.NextDoneBefore)
	}
}

func TestListEventIDIsSnapshotConsistent(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S"})
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S2"})
	list, err := b.List(ctx, User, api.TicketFilter{})
	if err != nil {
		t.Fatal(err)
	}
	last, err := db.LastEventID(b.db)
	if err != nil {
		t.Fatal(err)
	}
	if list.EventID != last || last == 0 {
		t.Fatalf("EventID = %d, last = %d", list.EventID, last)
	}
	empty, _ := newTestBoard(t)
	l2, err := empty.List(ctx, User, api.TicketFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if l2.EventID != 0 || l2.Tickets == nil || len(l2.Tickets) != 0 {
		t.Fatalf("empty list = %+v", l2)
	}
}

func TestListFilters(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	ceo := agent(t, b, api.RoleCEO, 0)
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "Checkout flow", Projects: []string{"api", "ui"}})
	inStory := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "Button", Projects: []string{"ui"}, Story: intPtr(story.Number)})
	alone := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "Logging", Description: "add structured zebra logs", Projects: []string{"api"}})

	cases := []struct {
		name string
		f    api.TicketFilter
		want []int
	}{
		{"all", api.TicketFilter{}, []int{story.Number, inStory.Number, alone.Number}},
		{"type", api.TicketFilter{Type: api.TypeTask}, []int{inStory.Number, alone.Number}},
		{"project", api.TicketFilter{Project: "ui"}, []int{story.Number, inStory.Number}},
		{"story", api.TicketFilter{Story: intPtr(story.Number)}, []int{story.Number, inStory.Number}},
		{"supervisor", api.TicketFilter{Supervisor: api.RoleCEO}, []int{alone.Number}},
		{"search title", api.TicketFilter{Search: "checkout"}, []int{story.Number}},
		{"search description", api.TicketFilter{Search: "Zebra"}, []int{alone.Number}},
		{"search number", api.TicketFilter{Search: fmt.Sprintf("#%d", inStory.Number)}, []int{inStory.Number}},
		{"column", api.TicketFilter{Column: api.ColumnReady}, []int{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := b.List(ctx, User, c.f)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(numbers(got.Tickets)) != fmt.Sprint(c.want) {
				t.Fatalf("got %v, want %v", numbers(got.Tickets), c.want)
			}
		})
	}
}

func TestGetUnknownTicketIsNotFound(t *testing.T) {
	b, _ := newTestBoard(t)
	_, err := b.Get(context.Background(), User, 42)
	wantCode(t, err, api.CodeNotFound)
}

func TestWaitingForReasonsInView(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	a := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "A"})
	c := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "C"})
	e := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "E"})
	m := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "M"})
	d := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "D", DependsOn: []int{a.Number, c.Number, e.Number, m.Number}})
	forceDone(t, b, c.Number, api.ResolutionCancelled)
	forceDone(t, b, e.Number, api.ResolutionExported)
	forceDone(t, b, m.Number, api.ResolutionExported)
	if _, err := b.db.Exec(`UPDATE tickets SET export_merged=1 WHERE id=?`, m.Number); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get(ctx, User, int64(d.Number))
	if err != nil {
		t.Fatal(err)
	}
	want := []api.Waiting{
		{Ticket: a.Number, Reason: api.WaitingNotDone},
		{Ticket: c.Number, Reason: api.WaitingCancelled},
		{Ticket: e.Number, Reason: api.WaitingExportNotMerged},
	}
	if fmt.Sprint(got.WaitingFor) != fmt.Sprint(want) {
		t.Fatalf("WaitingFor = %v, want %v", got.WaitingFor, want)
	}
}
