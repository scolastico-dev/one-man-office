package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// depFixture is a small board with every kind of ticket a dependency can
// connect.
type depFixture struct {
	b              *Board
	storyS, storyT int
	s1, s2, t1     int // tasks in story S, S and T
	alone1, alone2 int // standalone tasks
	storyU         int // a standalone story without tasks
	ctx            context.Context
}

func newDepFixture(t *testing.T) *depFixture {
	t.Helper()
	b, _ := newTestBoard(t)
	f := &depFixture{b: b, ctx: context.Background()}
	story := func(title string) int {
		return mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: title, Projects: []string{"api"}}).Number
	}
	task := func(title string, in *int) int {
		return mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: title, Projects: []string{"api"}, Story: in}).Number
	}
	f.storyS, f.storyT, f.storyU = story("S"), story("T"), story("U")
	f.s1, f.s2 = task("s1", &f.storyS), task("s2", &f.storyS)
	f.t1 = task("t1", &f.storyT)
	f.alone1, f.alone2 = task("alone1", nil), task("alone2", nil)
	return f
}

// kind builders: a ticket creation request of each kind of "A" in the
// placement table.
func (f *depFixture) create(kind string, deps ...int) api.CreateTicket {
	req := api.CreateTicket{Title: "new " + kind, Projects: []string{"api"}, DependsOn: deps}
	switch kind {
	case "task in S":
		req.Type, req.Story = api.TypeTask, &f.storyS
	case "story":
		req.Type = api.TypeStory
	case "standalone":
		req.Type = api.TypeTask
	}
	return req
}

// existing returns a ticket of each kind that has no dependencies yet.
func (f *depFixture) existing(kind string) int {
	switch kind {
	case "task in S":
		return f.s2
	case "story":
		return f.storyU
	}
	return f.alone2
}

func TestDependencyScopeMatrix(t *testing.T) {
	cases := []struct {
		from, to string
		ok       bool
	}{
		{"task in S", "task in S", true},
		{"task in S", "task in T", false},
		{"task in S", "story", false},
		{"task in S", "standalone", false},
		{"story", "story", true},
		{"story", "task in S", false},
		{"story", "standalone", false},
		{"standalone", "standalone", true},
		{"standalone", "story", true},
		{"standalone", "task in S", false},
	}
	for _, c := range cases {
		t.Run(c.from+" to "+c.to, func(t *testing.T) {
			check := func(path string, err error, from, to int) {
				t.Helper()
				if c.ok {
					if err != nil {
						t.Errorf("%s: %v", path, err)
					}
					return
				}
				wantCode(t, err, api.CodeDependencyScope)
				if !strings.Contains(err.Error(), "#"+itoa(to)) || (from != 0 && !strings.Contains(err.Error(), "#"+itoa(from))) {
					t.Errorf("%s: message %q names neither ticket", path, err)
				}
			}

			// Path 1: given at create.
			f := newDepFixture(t)
			to := mapTarget(f, c.to)
			_, err := f.b.Create(f.ctx, User, f.create(c.from, to))
			check("create", err, 0, to)
			if !c.ok {
				if tks, _ := f.b.List(f.ctx, User, api.TicketFilter{}); len(tks.Tickets) != 8 {
					t.Errorf("a rejected create left %d tickets", len(tks.Tickets))
				}
			}

			// Path 2: added later.
			f = newDepFixture(t)
			from := f.existing(c.from)
			to = mapTarget(f, c.to)
			_, err = f.b.AddDependency(f.ctx, User, int64(from), int64(to))
			check("add", err, from, to)
		})
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func mapTarget(f *depFixture, kind string) int {
	switch kind {
	case "task in S":
		return f.s1
	case "task in T":
		return f.t1
	case "story":
		return f.storyT
	}
	return f.alone1
}

func TestDependencyScopeOnMove(t *testing.T) {
	ctx := context.Background()
	moveTo := func(t *testing.T, f *depFixture, n int, story *int) error {
		t.Helper()
		req := api.UpdateTicket{Story: api.Null[int]()}
		if story != nil {
			req = api.UpdateTicket{Story: api.Some(*story)}
		}
		_, err := f.b.Update(ctx, User, int64(n), req)
		return err
	}
	t.Run("out of the story with a dependency inside", func(t *testing.T) {
		f := newDepFixture(t)
		if _, err := f.b.AddDependency(ctx, User, int64(f.s1), int64(f.s2)); err != nil {
			t.Fatal(err)
		}
		wantCode(t, moveTo(t, f, f.s1, nil), api.CodeDependencyScope)
		wantCode(t, moveTo(t, f, f.s2, nil), api.CodeDependencyScope)
	})
	t.Run("into a story with a standalone dependency", func(t *testing.T) {
		f := newDepFixture(t)
		if _, err := f.b.AddDependency(ctx, User, int64(f.alone2), int64(f.alone1)); err != nil {
			t.Fatal(err)
		}
		wantCode(t, moveTo(t, f, f.alone2, &f.storyS), api.CodeDependencyScope)
		// The one it depends on cannot move into a story either.
		wantCode(t, moveTo(t, f, f.alone1, &f.storyS), api.CodeDependencyScope)
	})
	t.Run("standalone task depending on a story", func(t *testing.T) {
		f := newDepFixture(t)
		if _, err := f.b.AddDependency(ctx, User, int64(f.alone1), int64(f.storyU)); err != nil {
			t.Fatal(err)
		}
		wantCode(t, moveTo(t, f, f.alone1, &f.storyS), api.CodeDependencyScope)
	})
	t.Run("between stories", func(t *testing.T) {
		f := newDepFixture(t)
		if _, err := f.b.AddDependency(ctx, User, int64(f.s1), int64(f.s2)); err != nil {
			t.Fatal(err)
		}
		wantCode(t, moveTo(t, f, f.s1, &f.storyT), api.CodeDependencyScope)
	})
	t.Run("without links the move works", func(t *testing.T) {
		f := newDepFixture(t)
		if err := moveTo(t, f, f.alone1, &f.storyS); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a link to a cancelled ticket does not block a valid move", func(t *testing.T) {
		f := newDepFixture(t)
		if _, err := f.b.AddDependency(ctx, User, int64(f.s1), int64(f.s2)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.b.Cancel(ctx, User, int64(f.s2), api.CancelRequest{}); err != nil {
			t.Fatal(err)
		}
		if err := moveTo(t, f, f.s1, &f.storyS); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDependencyCycleRejected(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	mk := func() int {
		return mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S"}).Number
	}
	one, two, three := mk(), mk(), mk()
	for _, l := range [][2]int{{one, two}, {two, three}} {
		if _, err := b.AddDependency(ctx, User, int64(l[0]), int64(l[1])); err != nil {
			t.Fatal(err)
		}
	}
	last, _ := db.LastEventID(b.db)
	_, err := b.AddDependency(ctx, User, int64(three), int64(one))
	wantCode(t, err, api.CodeDependencyCycle)
	want := "#3 → #1 → #2 → #3"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("message %q lacks %q", err, want)
	}
	if evs := eventsAfter(t, b, last); len(evs) != 0 {
		t.Fatalf("a rejected cycle recorded %d events", len(evs))
	}
	// A diamond is not a cycle.
	four := mk()
	for _, l := range [][2]int{{one, four}, {four, three}} {
		if _, err := b.AddDependency(ctx, User, int64(l[0]), int64(l[1])); err != nil {
			t.Fatal(err)
		}
	}
	_, err = b.AddDependency(ctx, User, int64(three), int64(four))
	wantCode(t, err, api.CodeDependencyCycle)
}

func TestCycleCheckHandlesLongChains(t *testing.T) {
	b, _ := newTestBoard(t)
	const n = 5000
	err := b.write(context.Background(), func(tx *sql.Tx) error {
		for i := 1; i <= n; i++ {
			if _, err := tx.Exec(`INSERT INTO tickets (type, title, col, rank, supervisor, created_by_role, created_at, updated_at)
				VALUES ('story', 'x', 'planning', ?, 'user', 'user', '2026-11-15T10:00:00Z', '2026-11-15T10:00:00Z')`, float64(i)); err != nil {
				return err
			}
		}
		for i := 1; i < n; i++ {
			if _, err := tx.Exec(`INSERT INTO dependencies (ticket_id, depends_on_id, created_at) VALUES (?, ?, '2026-11-15T10:00:00Z')`, i, i+1); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.AddDependency(context.Background(), User, n, 1)
	wantCode(t, err, api.CodeDependencyCycle)
	if !strings.HasPrefix(err.Error(), "dependency_cycle: #5000 cannot depend on #1") {
		t.Fatalf("message = %.120s", err)
	}
	if _, err := b.AddDependency(context.Background(), User, 1, 3); err != nil {
		t.Fatalf("shortcut along the chain: %v", err)
	}
	var cycle []int64
	err = b.read(context.Background(), func(tx *sql.Tx) error {
		var err error
		cycle, err = findCycle(tx, n, 1)
		return err
	})
	if err != nil || len(cycle) < n-1 {
		t.Fatalf("findCycle length = %d, %v", len(cycle), err)
	}
	if cycle[0] != 1 || cycle[len(cycle)-1] != n {
		t.Fatalf("chain runs %d..%d", cycle[0], cycle[len(cycle)-1])
	}
}

func TestSelfDependencyRejected(t *testing.T) {
	f := newDepFixture(t)
	_, err := f.b.AddDependency(f.ctx, User, int64(f.alone1), int64(f.alone1))
	wantCode(t, err, api.CodeDependencyScope)
	_, err = f.b.AddDependency(f.ctx, User, int64(f.storyS), int64(f.storyS))
	wantCode(t, err, api.CodeDependencyScope)
	// At create the new number cannot be named, so the guard is the same
	// check: it rejects the equal pair directly.
	row, err := loadTicket(f.b.db, int64(f.alone1))
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, checkDependency(f.b.db, row, row), api.CodeDependencyScope)
}

func TestDependencyOnCancelledTicket(t *testing.T) {
	f := newDepFixture(t)
	ctx := f.ctx
	// Linking to a cancelled ticket is rejected on both paths.
	if _, err := f.b.Cancel(ctx, User, int64(f.alone1), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err := f.b.AddDependency(ctx, User, int64(f.alone2), int64(f.alone1))
	wantCode(t, err, api.CodeDependencyScope)
	if !strings.Contains(err.Error(), "cannot depend on cancelled #"+itoa(f.alone1)) {
		t.Fatalf("message = %q", err)
	}
	_, err = f.b.Create(ctx, User, f.create("standalone", f.alone1))
	wantCode(t, err, api.CodeDependencyScope)

	// An existing link whose target is cancelled later shows why it waits.
	if _, err := f.b.AddDependency(ctx, User, int64(f.s1), int64(f.s2)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.b.Cancel(ctx, User, int64(f.s2), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	got, err := f.b.Get(ctx, User, int64(f.s1))
	if err != nil {
		t.Fatal(err)
	}
	want := []api.Waiting{{Ticket: f.s2, Reason: api.WaitingCancelled}}
	if !slices.Equal(got.WaitingFor, want) {
		t.Fatalf("WaitingFor = %v, want %v", got.WaitingFor, want)
	}
}

func TestDependencyFromDoneTicketRejected(t *testing.T) {
	f := newDepFixture(t)
	forceDone(t, f.b, f.alone2, api.ResolutionCompleted)
	_, err := f.b.AddDependency(f.ctx, User, int64(f.alone2), int64(f.alone1))
	wantCode(t, err, api.CodeDependencyScope)
}

func TestWaitingForReasons(t *testing.T) {
	f := newDepFixture(t)
	ctx := f.ctx
	if _, err := f.b.AddDependency(ctx, User, int64(f.alone2), int64(f.alone1)); err != nil {
		t.Fatal(err)
	}
	waiting := func() []api.Waiting {
		t.Helper()
		got, err := f.b.Get(ctx, User, int64(f.alone2))
		if err != nil {
			t.Fatal(err)
		}
		return got.WaitingFor
	}
	if got := waiting(); !slices.Equal(got, []api.Waiting{{Ticket: f.alone1, Reason: api.WaitingNotDone}}) {
		t.Fatalf("in planning: %v", got)
	}
	forceDone(t, f.b, f.alone1, api.ResolutionExported)
	if got := waiting(); !slices.Equal(got, []api.Waiting{{Ticket: f.alone1, Reason: api.WaitingExportNotMerged}}) {
		t.Fatalf("exported: %v", got)
	}
	last, _ := db.LastEventID(f.b.db)
	if _, err := f.b.MarkMerged(ctx, User, int64(f.alone1)); err != nil {
		t.Fatal(err)
	}
	if got := waiting(); len(got) != 0 || got == nil {
		t.Fatalf("merged: %#v", got)
	}
	if !updatedTickets(t, eventsAfter(t, f.b, last))[f.alone2] {
		t.Fatal("the dependent got no derived update when the export was marked merged")
	}

	// A completed target satisfies the dependency.
	if _, err := f.b.AddDependency(ctx, User, int64(f.alone2), int64(f.storyU)); err != nil {
		t.Fatal(err)
	}
	forceDone(t, f.b, f.storyU, api.ResolutionCompleted)
	if got := waiting(); len(got) != 0 {
		t.Fatalf("completed: %v", got)
	}
}

func TestBlocksIsInverse(t *testing.T) {
	f := newDepFixture(t)
	ctx := f.ctx
	if _, err := f.b.AddDependency(ctx, User, int64(f.s1), int64(f.s2)); err != nil {
		t.Fatal(err)
	}
	a, _ := f.b.Get(ctx, User, int64(f.s1))
	bb, _ := f.b.Get(ctx, User, int64(f.s2))
	if !slices.Equal(a.DependsOn, []int{f.s2}) || len(a.Blocks) != 0 {
		t.Fatalf("depends side: %v blocks %v", a.DependsOn, a.Blocks)
	}
	if !slices.Equal(bb.Blocks, []int{f.s1}) || len(bb.DependsOn) != 0 {
		t.Fatalf("blocking side: %v depends %v", bb.Blocks, bb.DependsOn)
	}
	got, err := f.b.RemoveDependency(ctx, User, int64(f.s1), int64(f.s2))
	if err != nil || len(got.DependsOn) != 0 || got.DependsOn == nil || got.WaitingFor == nil || got.Blocks == nil {
		t.Fatalf("after remove: %+v, %v", got, err)
	}
	bb, _ = f.b.Get(ctx, User, int64(f.s2))
	if len(bb.Blocks) != 0 {
		t.Fatalf("blocks after remove: %v", bb.Blocks)
	}
}

func updatedTickets(t *testing.T, evs []db.Event) map[int]bool {
	t.Helper()
	out := map[int]bool{}
	for _, e := range evs {
		if e.Kind != api.KindTicketUpdated {
			continue
		}
		var d api.TicketUpdatedData
		if err := json.Unmarshal([]byte(e.Detail), &d); err != nil {
			t.Fatal(err)
		}
		out[d.Ticket.Number] = true
	}
	return out
}

func TestDependencyEvents(t *testing.T) {
	f := newDepFixture(t)
	ctx := f.ctx
	last, _ := db.LastEventID(f.b.db)
	if _, err := f.b.AddDependency(ctx, User, int64(f.s1), int64(f.s2)); err != nil {
		t.Fatal(err)
	}
	evs := eventsAfter(t, f.b, last)
	if len(evs) != 3 || evs[0].Kind != api.KindDependencyAdded || evs[1].Kind != api.KindTicketUpdated || evs[2].Kind != api.KindTicketUpdated {
		t.Fatalf("events = %+v", evs)
	}
	var d api.DependencyData
	if err := json.Unmarshal([]byte(evs[0].Detail), &d); err != nil || d != (api.DependencyData{Ticket: f.s1, DependsOn: f.s2}) {
		t.Fatalf("data = %+v, %v", d, err)
	}
	var upd api.TicketUpdatedData
	if err := json.Unmarshal([]byte(evs[1].Detail), &upd); err != nil {
		t.Fatal(err)
	}
	if upd.Ticket.Number != f.s1 || !slices.Equal(upd.Fields, []string{fieldDerived}) ||
		!slices.Equal(upd.Ticket.DependsOn, []int{f.s2}) || len(upd.Ticket.WaitingFor) != 1 {
		t.Fatalf("first update = %+v", upd)
	}
	if err := json.Unmarshal([]byte(evs[2].Detail), &upd); err != nil || upd.Ticket.Number != f.s2 ||
		!slices.Equal(upd.Ticket.Blocks, []int{f.s1}) {
		t.Fatalf("second update = %+v, %v", upd, err)
	}

	// Adding it again changes nothing.
	last, _ = db.LastEventID(f.b.db)
	if _, err := f.b.AddDependency(ctx, User, int64(f.s1), int64(f.s2)); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, f.b, last); len(evs) != 0 {
		t.Fatalf("repeat add emitted %+v", evs)
	}

	if _, err := f.b.RemoveDependency(ctx, User, int64(f.s1), int64(f.s2)); err != nil {
		t.Fatal(err)
	}
	evs = eventsAfter(t, f.b, last)
	if len(evs) != 3 || evs[0].Kind != api.KindDependencyRemoved {
		t.Fatalf("events after remove = %+v", evs)
	}
	last, _ = db.LastEventID(f.b.db)
	if _, err := f.b.RemoveDependency(ctx, User, int64(f.s1), int64(f.s2)); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, f.b, last); len(evs) != 0 {
		t.Fatalf("repeat remove emitted %+v", evs)
	}
	_, err := f.b.AddDependency(ctx, User, 999, int64(f.s2))
	wantCode(t, err, api.CodeNotFound)
	_, err = f.b.AddDependency(ctx, User, int64(f.s1), 999)
	wantCode(t, err, api.CodeNotFound)
}

func TestCreateWithDependenciesChecksAfterInsert(t *testing.T) {
	f := newDepFixture(t)
	// A task in story S with a dependency on a task of S works; the check
	// needs the new ticket's own row.
	tk, err := f.b.Create(f.ctx, User, f.create("task in S", f.s1, f.s2))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tk.DependsOn, []int{f.s1, f.s2}) {
		t.Fatalf("DependsOn = %v", tk.DependsOn)
	}
	// One bad link rejects the whole create and leaves no trace.
	last, _ := db.LastEventID(f.b.db)
	_, err = f.b.Create(f.ctx, User, f.create("task in S", f.s1, f.t1))
	wantCode(t, err, api.CodeDependencyScope)
	if evs := eventsAfter(t, f.b, last); len(evs) != 0 {
		t.Fatalf("rejected create recorded %d events", len(evs))
	}
}

func TestDependencyPermissions(t *testing.T) {
	f := newDepFixture(t)
	ctx := f.ctx
	b := f.b
	ceoTask := mustCreate(t, b, agent(t, b, api.RoleCEO, 0), api.CreateTicket{Type: api.TypeTask, Title: "ceo", Projects: []string{"api"}}).Number
	pm := agent(t, b, api.RolePM, int64(f.storyS))
	otherPM := agent(t, b, api.RolePM, int64(f.storyT))
	ceo := agent(t, b, api.RoleCEO, 0)
	dev := agent(t, b, api.RoleDeveloper, int64(f.s1))

	_, err := b.AddDependency(ctx, dev, int64(f.s1), int64(f.s2))
	wantCode(t, err, api.CodeForbidden)
	_, err = b.AddDependency(ctx, otherPM, int64(f.s1), int64(f.s2))
	wantCode(t, err, api.CodeForbidden)
	_, err = b.AddDependency(ctx, pm, int64(f.alone1), int64(f.alone2))
	wantCode(t, err, api.CodeForbidden)
	if _, err := b.AddDependency(ctx, pm, int64(f.s1), int64(f.s2)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.RemoveDependency(ctx, pm, int64(f.s1), int64(f.s2)); err != nil {
		t.Fatal(err)
	}
	// The pm cannot link its task outside the story.
	_, err = b.AddDependency(ctx, pm, int64(f.s1), int64(f.t1))
	wantCode(t, err, api.CodeDependencyScope)

	_, err = b.AddDependency(ctx, ceo, int64(f.alone2), int64(f.alone1))
	wantCode(t, err, api.CodeForbidden)
	if _, err := b.AddDependency(ctx, ceo, int64(ceoTask), int64(f.alone1)); err != nil {
		t.Fatal(err)
	}
	_, err = b.RemoveDependency(ctx, ceo, int64(f.alone2), int64(f.alone1))
	wantCode(t, err, api.CodeForbidden)
}

func TestDependencyRefusedForCEOWhileOff(t *testing.T) {
	b, env := newTestBoard(t)
	ceo := agent(t, b, api.RoleCEO, 0)
	one := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeStory, Title: "1"}).Number
	two := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeStory, Title: "2"}).Number
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	_, err := b.AddDependency(context.Background(), ceo, int64(one), int64(two))
	wantCode(t, err, api.CodeForbidden)
}
