package board

import (
	"context"
	"slices"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestProjectIDsInUse(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()

	got, err := b.ProjectIDsInUse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("empty board: got %v", got)
	}

	open := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "open", Projects: []string{"api"}})
	cancelled := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "cancelled", Projects: []string{"ui"}})
	if _, err := b.Cancel(ctx, User, int64(cancelled.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	_ = open

	got, err = b.ProjectIDsInUse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["api"] || !got["ui"] {
		t.Fatalf("got %v, want api and ui (cancelled ticket counts)", got)
	}
	if got["off"] {
		t.Fatalf("unreferenced project listed: %v", got)
	}
}

func TestOpenTicketsWithProject(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()

	a := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "a", Projects: []string{"api"}})
	done := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "done", Projects: []string{"api"}})
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "s", Projects: []string{"ui", "api"}})
	other := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "o", Projects: []string{"ui"}})
	if _, err := b.Cancel(ctx, User, int64(done.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}

	got, err := b.OpenTicketsWithProject(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{int64(a.Number), int64(story.Number)}; !slices.Equal(got, want) {
		t.Fatalf("api: got %v, want %v", got, want)
	}
	got, err = b.OpenTicketsWithProject(ctx, "ui")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{int64(story.Number), int64(other.Number)}; !slices.Equal(got, want) {
		t.Fatalf("ui: got %v, want %v", got, want)
	}
	got, err = b.OpenTicketsWithProject(ctx, "nope")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("unknown project: got %#v, want empty non-nil slice", got)
	}
}

func TestOpenTicketsWithModels(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()

	mk := func(title, model string) api.Ticket {
		t.Helper()
		return mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: title, Projects: []string{"api"}, Model: model})
	}
	a := mk("a", "claude/fable")
	c := mk("c", "claude/fable")
	done := mk("done", "claude/opus")
	mk("plain", "")
	if _, err := b.Cancel(ctx, User, int64(done.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}

	got, err := b.OpenTicketsWithModels(ctx, []string{"claude/fable", "claude/opus", "ghost/none"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{int64(a.Number), int64(c.Number)}; !slices.Equal(got["claude/fable"], want) {
		t.Fatalf("fable: got %v, want %v", got["claude/fable"], want)
	}
	if len(got) != 1 {
		t.Fatalf("got %v, want only claude/fable (Done and unknown ids map to nothing)", got)
	}

	got, err = b.OpenTicketsWithModels(ctx, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no ids: got %#v, %v", got, err)
	}
	got, err = b.OpenTicketsWithModels(ctx, []string{""})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty id: got %#v, %v", got, err)
	}
}

func TestRecordedBranchesCoverEveryTicketOfTheProject(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	one := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "one", Projects: []string{"api"}})
	two := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "two", Projects: []string{"ui"}})
	none := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "none", Projects: []string{"api"}})
	_ = none
	if err := b.SetBranch(ctx, int64(one.Number), "api", "omo/1-one", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.SetBranch(ctx, int64(two.Number), "ui", "omo/two", ""); err != nil {
		t.Fatal(err)
	}
	got, err := b.RecordedBranches(ctx, "api")
	if err != nil || len(got) != 1 || !got["omo/1-one"] {
		t.Fatalf("api: %v %v", got, err)
	}
}
