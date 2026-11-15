package board

import (
	"context"
	"slices"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// scopeFixture is a board with one ticket per relation an agent can have to
// a ticket:
//
//	S  story (the pm's story), depends on story O
//	T  task in S (the developer's and task reviewer's task), depends on D
//	D  task in S that T depends on
//	U  task in S that depends on T
//	B  sibling task in S
//	O  another story, X a task of O
//	A  standalone task (the standalone developer's task)
type scopeFixture struct {
	b      *Board
	ticket map[string]int
	actor  map[string]Actor
}

var scopeRelations = []string{"S", "T", "D", "U", "B", "O", "X", "A"}

func newScopeFixture(t *testing.T) *scopeFixture {
	t.Helper()
	b, _ := newTestBoard(t)
	ctx := context.Background()
	f := &scopeFixture{b: b, ticket: map[string]int{}, actor: map[string]Actor{}}
	story := func(name string) {
		f.ticket[name] = mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: name, Projects: []string{"api"}}).Number
	}
	task := func(name, story string) {
		req := api.CreateTicket{Type: api.TypeTask, Title: name, Projects: []string{"api"}}
		if story != "" {
			req.Story = intPtr(f.ticket[story])
		}
		f.ticket[name] = mustCreate(t, b, User, req).Number
	}
	story("S")
	story("O")
	task("T", "S")
	task("D", "S")
	task("U", "S")
	task("B", "S")
	task("X", "O")
	task("A", "")
	link := func(from, to string) {
		if _, err := b.AddDependency(ctx, User, int64(f.ticket[from]), int64(f.ticket[to])); err != nil {
			t.Fatal(err)
		}
	}
	link("T", "D")
	link("U", "T")
	link("S", "O")
	for _, n := range f.ticket {
		if _, err := b.Comment(ctx, User, int64(n), api.CommentRequest{Body: "first"}); err != nil {
			t.Fatal(err)
		}
	}
	f.actor["user"] = User
	f.actor["ceo"] = agent(t, b, api.RoleCEO, 0)
	f.actor["pm"] = agent(t, b, api.RolePM, int64(f.ticket["S"]))
	f.actor["developer"] = agent(t, b, api.RoleDeveloper, int64(f.ticket["T"]))
	f.actor["task reviewer"] = agent(t, b, api.RoleReviewer, int64(f.ticket["T"]))
	f.actor["story reviewer"] = agent(t, b, api.RoleReviewer, int64(f.ticket["S"]))
	f.actor["standalone developer"] = agent(t, b, api.RoleDeveloper, int64(f.ticket["A"]))
	return f
}

// Every (actor, relation) pair not listed is denied.
var (
	scopeAll = map[string]bool{"S": true, "T": true, "D": true, "U": true, "B": true, "O": true, "X": true, "A": true}
	wantRead = map[string]map[string]bool{
		"user":                 scopeAll,
		"ceo":                  scopeAll,
		"pm":                   scopeAll,
		"developer":            {"T": true, "S": true, "D": true},
		"task reviewer":        {"T": true, "S": true, "D": true},
		"story reviewer":       {"S": true, "O": true, "T": true, "D": true, "U": true, "B": true},
		"standalone developer": {"A": true},
	}
	wantWrite = map[string]map[string]bool{
		"user":                 scopeAll,
		"ceo":                  scopeAll,
		"pm":                   {"S": true, "T": true, "D": true, "U": true, "B": true},
		"developer":            {"T": true},
		"task reviewer":        {"T": true},
		"story reviewer":       {"S": true},
		"standalone developer": {"A": true},
	}
	// wantEdit is who may edit the title, the description and the
	// dependencies. Every fixture ticket was created by the user, so the CEO
	// edits none of them; a pm edits the tasks of its story, not the story.
	wantEdit = map[string]map[string]bool{
		"user": scopeAll,
		"pm":   {"T": true, "D": true, "U": true, "B": true},
	}
	// wantReorder is who may move a card within its column.
	wantReorder = map[string]map[string]bool{
		"user": scopeAll,
	}
)

func TestReadScopeMatrix(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()
	for name, a := range f.actor {
		for _, rel := range scopeRelations {
			n := int64(f.ticket[rel])
			allowed := wantRead[name][rel]
			check := func(what string, err error) {
				t.Helper()
				if allowed && err != nil {
					t.Errorf("%s %s %s: %v", name, what, rel, err)
				}
				if !allowed && errCode(err) != api.CodeForbidden {
					t.Errorf("%s %s %s: error %v, want forbidden", name, what, rel, err)
				}
			}
			_, err := f.b.Get(ctx, a, n)
			check("get", err)
			_, err = f.b.Comments(ctx, a, n)
			check("comments", err)
			_, err = f.b.Descriptions(ctx, a, n)
			check("descriptions", err)
			_, err = f.b.TicketEvents(ctx, a, n)
			check("events", err)
			_, err = f.b.TicketSessions(ctx, a, n)
			check("sessions", err)
		}
		list, err := f.b.List(ctx, a, api.TicketFilter{})
		if err != nil {
			t.Fatal(err)
		}
		var want []int
		for _, rel := range scopeRelations {
			if wantRead[name][rel] {
				want = append(want, f.ticket[rel])
			}
		}
		got := numbers(list.Tickets)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s lists %v, want %v", name, got, want)
		}
	}
}

func TestWriteScopeMatrix(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()
	for name, a := range f.actor {
		for _, rel := range scopeRelations {
			n := int64(f.ticket[rel])
			check := func(what string, want map[string]map[string]bool, err error) {
				t.Helper()
				if allowed := want[name][rel]; allowed && err != nil {
					t.Errorf("%s %s %s: %v", name, what, rel, err)
				} else if !allowed && errCode(err) != api.CodeForbidden {
					t.Errorf("%s %s %s: error %v, want forbidden", name, what, rel, err)
				}
			}
			_, err := f.b.Comment(ctx, a, n, api.CommentRequest{Body: "note"})
			check("comments on", wantWrite, err)
			_, err = f.b.Update(ctx, a, n, api.UpdateTicket{Title: api.Some("renamed " + rel)})
			check("renames", wantEdit, err)
			_, err = f.b.SetDescription(ctx, a, n, api.DescriptionUpdate{Text: "by " + name})
			check("describes", wantEdit, err)
			// Removing a link that does not exist changes nothing once it
			// is allowed.
			_, err = f.b.RemoveDependency(ctx, a, n, n)
			check("unlinks", wantEdit, err)
			_, err = f.b.Move(ctx, a, n, api.MoveRequest{Column: api.ColumnPlanning})
			check("reorders", wantReorder, err)
		}
	}
}

// A developer or reviewer gets forbidden on every write path for a ticket
// that does not exist and for a closed ticket outside its scope, never
// not_found or invalid_transition; the user gets the real answer.
func TestWritePathsHideTicketsOutsideScope(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()
	forceDone(t, f.b, f.ticket["X"], api.ResolutionCompleted)
	closed := int64(f.ticket["X"])
	own := int64(f.ticket["T"])
	calls := map[string]func(a Actor, n int64) error{
		"update": func(a Actor, n int64) error {
			_, err := f.b.Update(ctx, a, n, api.UpdateTicket{Title: api.Some("x")})
			return err
		},
		"set description": func(a Actor, n int64) error {
			_, err := f.b.SetDescription(ctx, a, n, api.DescriptionUpdate{Text: "x"})
			return err
		},
		"restore description": func(a Actor, n int64) error {
			_, err := f.b.RestoreDescription(ctx, a, n, 1)
			return err
		},
		"add dependency": func(a Actor, n int64) error {
			_, err := f.b.AddDependency(ctx, a, n, own)
			return err
		},
		"remove dependency": func(a Actor, n int64) error {
			_, err := f.b.RemoveDependency(ctx, a, n, own)
			return err
		},
		"depend on": func(a Actor, n int64) error {
			_, err := f.b.AddDependency(ctx, a, own, n)
			return err
		},
		"move": func(a Actor, n int64) error {
			_, err := f.b.Move(ctx, a, n, api.MoveRequest{Column: api.ColumnReady})
			return err
		},
		"approve": func(a Actor, n int64) error {
			_, err := f.b.Approve(ctx, a, n, api.ApproveRequest{})
			return err
		},
		"cancel": func(a Actor, n int64) error {
			_, err := f.b.Cancel(ctx, a, n, api.CancelRequest{})
			return err
		},
		"list done_before": func(a Actor, n int64) error {
			_, err := f.b.List(ctx, a, api.TicketFilter{Column: api.ColumnDone, DoneBefore: int(n)})
			return err
		},
	}
	for _, name := range []string{"developer", "task reviewer", "story reviewer", "standalone developer"} {
		for what, call := range calls {
			for _, n := range []int64{999, closed} {
				if err := call(f.actor[name], n); errCode(err) != api.CodeForbidden {
					t.Errorf("%s %s #%d: error %v, want forbidden", name, what, n, err)
				}
			}
		}
	}
	for what, call := range calls {
		if what == "list done_before" {
			continue
		}
		wantCode(t, call(User, 999), api.CodeNotFound)
	}
	_, err := f.b.Cancel(ctx, User, closed, api.CancelRequest{})
	wantCode(t, err, api.CodeInvalidTransition)
}

func TestReadScopeForDeveloper(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()
	dev := f.actor["developer"]
	for _, rel := range []string{"T", "S", "D"} {
		if _, err := f.b.Get(ctx, dev, int64(f.ticket[rel])); err != nil {
			t.Fatalf("Get(%s): %v", rel, err)
		}
	}
	_, err := f.b.Get(ctx, dev, int64(f.ticket["B"]))
	wantCode(t, err, api.CodeForbidden)
	list, err := f.b.List(ctx, dev, api.TicketFilter{})
	if err != nil {
		t.Fatal(err)
	}
	got := numbers(list.Tickets)
	slices.Sort(got)
	want := []int{f.ticket["S"], f.ticket["T"], f.ticket["D"]}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
}

// A ticket that does not exist looks the same as one outside the scope to
// an agent with a limited scope; the others see not_found.
func TestUnknownTicketOutsideScope(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()
	for name, a := range f.actor {
		_, err := f.b.Get(ctx, a, 999)
		want := api.CodeNotFound
		if !wantRead[name]["X"] {
			want = api.CodeForbidden
		}
		if errCode(err) != want {
			t.Errorf("%s Get(999) = %v, want %s", name, err, want)
		}
		_, err = f.b.Comment(ctx, a, 999, api.CommentRequest{Body: "x"})
		if errCode(err) != want {
			t.Errorf("%s Comment(999) = %v, want %s", name, err, want)
		}
	}
}

func TestScopedListKeepsFilters(t *testing.T) {
	f := newScopeFixture(t)
	list, err := f.b.List(context.Background(), f.actor["developer"], api.TicketFilter{Type: api.TypeTask})
	if err != nil {
		t.Fatal(err)
	}
	got := numbers(list.Tickets)
	slices.Sort(got)
	if want := []int{f.ticket["T"], f.ticket["D"]}; !slices.Equal(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
}
