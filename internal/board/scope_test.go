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
//	B  sibling task in S
//	O  another story, X a task of O
//	A  standalone task
type scopeFixture struct {
	b      *Board
	ticket map[string]int
	actor  map[string]Actor
}

var scopeRelations = []string{"S", "T", "D", "B", "O", "X", "A"}

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
	task("B", "S")
	task("X", "O")
	task("A", "")
	link := func(from, to string) {
		if _, err := b.AddDependency(ctx, User, int64(f.ticket[from]), int64(f.ticket[to])); err != nil {
			t.Fatal(err)
		}
	}
	link("T", "D")
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
	return f
}

// Every (actor, relation) pair not listed is denied.
var (
	scopeAll = map[string]bool{"S": true, "T": true, "D": true, "B": true, "O": true, "X": true, "A": true}
	wantRead = map[string]map[string]bool{
		"user":           scopeAll,
		"ceo":            scopeAll,
		"pm":             scopeAll,
		"developer":      {"T": true, "S": true, "D": true},
		"task reviewer":  {"T": true, "S": true, "D": true},
		"story reviewer": {"S": true, "O": true, "T": true, "D": true, "B": true},
	}
	wantWrite = map[string]map[string]bool{
		"user":           scopeAll,
		"ceo":            scopeAll,
		"pm":             {"S": true, "T": true, "D": true, "B": true},
		"developer":      {"T": true},
		"task reviewer":  {"T": true},
		"story reviewer": {"S": true},
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
			_, err := f.b.Comment(ctx, a, int64(f.ticket[rel]), api.CommentRequest{Body: "note"})
			if allowed := wantWrite[name][rel]; allowed && err != nil {
				t.Errorf("%s comments on %s: %v", name, rel, err)
			} else if !allowed && errCode(err) != api.CodeForbidden {
				t.Errorf("%s comments on %s: error %v, want forbidden", name, rel, err)
			}
		}
	}
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
