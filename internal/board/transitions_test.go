package board

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// force puts a ticket into a column directly in the database, bypassing the
// transition rules. Done means completed; Review starts round 1 at stage.
func force(t *testing.T, b *Board, n int, col api.Column, stage api.ReviewStage) {
	t.Helper()
	var res api.Resolution
	var closed any
	if col == api.ColumnDone {
		res, closed = api.ResolutionCompleted, b.stamp()
	}
	round := 0
	if col == api.ColumnReview {
		round = 1
	}
	if _, err := b.db.Exec(`UPDATE tickets SET col = ?, resolution = ?, closed_at = ?, review_stage = ?, review_round = ?
		WHERE id = ?`, col, res, closed, stage, round, n); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, b *Board, n int) api.Ticket {
	t.Helper()
	tk, err := b.Get(context.Background(), User, int64(n))
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func lastEvent(t *testing.T, b *Board) int64 {
	t.Helper()
	id, err := db.LastEventID(b.db)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func eventsAfter(t *testing.T, b *Board, last int64) []db.Event {
	t.Helper()
	evs, err := db.EventsSince(b.db, last, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// movedEvents decodes the ticket.moved events of evs.
func movedEvents(t *testing.T, evs []db.Event) []api.TicketMovedData {
	t.Helper()
	var out []api.TicketMovedData
	for _, e := range evs {
		if e.Kind != api.KindTicketMoved {
			continue
		}
		var d api.TicketMovedData
		if err := json.Unmarshal([]byte(e.Detail), &d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// derivedUpdates returns the tickets of the ticket.updated events of evs
// whose fields are ["derived"], in order.
func derivedUpdates(t *testing.T, evs []db.Event) []int {
	t.Helper()
	out := []int{}
	for _, e := range evs {
		if e.Kind != api.KindTicketUpdated {
			continue
		}
		var d api.TicketUpdatedData
		if err := json.Unmarshal([]byte(e.Detail), &d); err != nil {
			t.Fatal(err)
		}
		if len(d.Fields) == 1 && d.Fields[0] == fieldDerived {
			out = append(out, d.Ticket.Number)
		}
	}
	return out
}

var allColumns = []api.Column{api.ColumnPlanning, api.ColumnReady, api.ColumnInProgress, api.ColumnReview, api.ColumnDone}

// ticketKind builds one kind of ticket for the transition matrices and
// returns it with the story it belongs to (0 for none).
type ticketKind struct {
	name  string
	build func(t *testing.T, b *Board) (n, story int)
}

var ticketKinds = []ticketKind{
	{"story", func(t *testing.T, b *Board) (int, int) {
		s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
		return s.Number, s.Number
	}},
	{"standalone", func(t *testing.T, b *Board) (int, int) {
		tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
		return tk.Number, 0
	}},
	{"in_story", func(t *testing.T, b *Board) (int, int) {
		s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
		force(t, b, s.Number, api.ColumnInProgress, "")
		tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})
		return tk.Number, s.Number
	}},
}

// matrixActor returns an actor of role bound like a real session would be;
// no session row exists, so no agent counts as working on the ticket.
func matrixActor(role api.Role, n, story int) Actor {
	switch role {
	case api.RolePM:
		return Actor{Role: role, Session: "pm", Ticket: int64(story)}
	case api.RoleDeveloper, api.RoleReviewer:
		return Actor{Role: role, Session: string(role), Ticket: int64(n), Project: "api"}
	case api.RoleCEO:
		return Actor{Role: role, Session: "ceo"}
	}
	return Actor{Role: role}
}

// TestMoveMatrix drags every kind of ticket created by the user from every
// column to every column as every actor. A combination not listed as
// allowed must fail.
func TestMoveMatrix(t *testing.T) {
	actors := []api.Role{api.RoleUser, api.RoleCEO, api.RolePM, api.RoleDeveloper, api.RoleReviewer, api.RoleSystem}
	type outcome struct {
		code  string
		col   api.Column
		stage api.ReviewStage
	}
	userOutcome := func(from, to api.Column) outcome {
		switch {
		case from == api.ColumnDone:
			return outcome{code: api.CodeInvalidTransition}
		case from == to:
			return outcome{col: from, stage: stageFor(from)}
		case from == api.ColumnPlanning && to == api.ColumnReady,
			from == api.ColumnReady && to == api.ColumnPlanning,
			from == api.ColumnInProgress && (to == api.ColumnPlanning || to == api.ColumnReady):
			return outcome{col: to}
		case from == api.ColumnReview && to == api.ColumnDone:
			return outcome{col: api.ColumnReview, stage: api.ReviewStageMerging}
		case from == api.ColumnReview && to == api.ColumnInProgress:
			return outcome{code: api.CodeValidation}
		}
		return outcome{code: api.CodeInvalidTransition}
	}
	for _, kind := range ticketKinds {
		for _, from := range allColumns {
			for _, to := range allColumns {
				for _, role := range actors {
					name := fmt.Sprintf("%s/%s->%s/%s", kind.name, from, to, role)
					t.Run(name, func(t *testing.T) {
						b, _ := newTestBoard(t)
						n, story := kind.build(t, b)
						force(t, b, n, from, stageFor(from))
						want := userOutcome(from, to)
						// A product manager stops the tasks of its story (mayStop).
						// The user supervises the tickets of this matrix, so the
						// CEO stops none of them.
						pmStops := role == api.RolePM && kind.name == "in_story" && from == api.ColumnInProgress &&
							(to == api.ColumnPlanning || to == api.ColumnReady)
						if role != api.RoleUser && !pmStops && want.code != api.CodeInvalidTransition {
							want = outcome{code: api.CodeForbidden}
						}
						got, err := b.Move(context.Background(), matrixActor(role, n, story), int64(n), api.MoveRequest{Column: to})
						if want.code != "" {
							wantCode(t, err, want.code)
							if after := get(t, b, n); after.Column != from {
								t.Fatalf("failed move changed the column to %s", after.Column)
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						if got.Column != want.col || got.ReviewStage != want.stage {
							t.Fatalf("ticket in %s stage %q, want %s stage %q", got.Column, got.ReviewStage, want.col, want.stage)
						}
					})
				}
			}
		}
	}
}

// stageFor is the review stage the matrices put a ticket in Review into: the
// one where the user may act.
func stageFor(col api.Column) api.ReviewStage {
	if col == api.ColumnReview {
		return api.ReviewStageHuman
	}
	return ""
}

func TestMoveRejectsUnknownColumn(t *testing.T) {
	b, _ := newTestBoard(t)
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	_, err := b.Move(context.Background(), User, int64(tk.Number), api.MoveRequest{Column: "backlog"})
	wantCode(t, err, api.CodeValidation)
}

// TestApproveAndCancelMatrix calls Approve, Cancel and Reject on every kind
// of ticket in every column as the user.
func TestApproveAndCancelMatrix(t *testing.T) {
	for _, kind := range ticketKinds {
		for _, from := range allColumns {
			name := fmt.Sprintf("%s/%s", kind.name, from)
			t.Run("approve/"+name, func(t *testing.T) {
				b, _ := newTestBoard(t)
				n, _ := kind.build(t, b)
				force(t, b, n, from, stageFor(from))
				got, err := b.Approve(context.Background(), User, int64(n), api.ApproveRequest{})
				switch from {
				case api.ColumnPlanning:
					if err != nil || got.Column != api.ColumnReady {
						t.Fatalf("got %s, %v", got.Column, err)
					}
				case api.ColumnReview:
					if err != nil || got.ReviewStage != api.ReviewStageMerging {
						t.Fatalf("got %s %q, %v", got.Column, got.ReviewStage, err)
					}
				default:
					wantCode(t, err, api.CodeInvalidTransition)
				}
			})
			t.Run("cancel/"+name, func(t *testing.T) {
				b, _ := newTestBoard(t)
				n, _ := kind.build(t, b)
				force(t, b, n, from, stageFor(from))
				got, err := b.Cancel(context.Background(), User, int64(n), api.CancelRequest{})
				if from == api.ColumnDone {
					wantCode(t, err, api.CodeInvalidTransition)
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if got.Column != api.ColumnDone || got.Resolution != api.ResolutionCancelled || got.ClosedAt == nil || got.ReviewStage != "" {
					t.Fatalf("got %s %s closed %v stage %q", got.Column, got.Resolution, got.ClosedAt, got.ReviewStage)
				}
			})
			t.Run("reject/"+name, func(t *testing.T) {
				b, _ := newTestBoard(t)
				n, _ := kind.build(t, b)
				force(t, b, n, from, stageFor(from))
				got, err := b.Reject(context.Background(), User, int64(n), api.RejectRequest{Comment: "fix it"})
				if from != api.ColumnReview {
					wantCode(t, err, api.CodeInvalidTransition)
					return
				}
				if err != nil || got.Column != api.ColumnInProgress || got.ReviewStage != "" {
					t.Fatalf("got %s %q, %v", got.Column, got.ReviewStage, err)
				}
			})
		}
	}
}

func TestApproveRequiresProjectsAndBaseBranch(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	empty := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S"})
	_, err := b.Approve(ctx, User, int64(empty.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeValidation)

	env.setBranch("api", "")
	detached := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	_, err = b.Approve(ctx, User, int64(detached.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeValidation)
	if _, err := b.Update(ctx, User, int64(detached.Number), api.UpdateTicket{BaseBranches: api.Some(map[string]string{"api": "main"})}); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Approve(ctx, User, int64(detached.Number), api.ApproveRequest{}); err != nil || got.Column != api.ColumnReady {
		t.Fatalf("after setting the base: %s, %v", got.Column, err)
	}

	env.setBranch("api", "main")
	off := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}})
	env.mu.Lock()
	env.projects["ui"] = ProjectInfo{ID: "ui", Path: "/src/ui", Enabled: false}
	env.mu.Unlock()
	_, err = b.Approve(ctx, User, int64(off.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeProjectUnavailable)

	_, err = b.Approve(ctx, User, int64(off.Number), api.ApproveRequest{Export: true})
	wantCode(t, err, api.CodeValidation)
}

func TestApprovePlacesAtEndOfReady(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	first := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
	second := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "B", Projects: []string{"api"}})
	third := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "C", Projects: []string{"api"}})
	for _, n := range []int{second.Number, first.Number} {
		if _, err := b.Approve(ctx, User, int64(n), api.ApproveRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	if a, s := get(t, b, first.Number), get(t, b, second.Number); a.Rank <= s.Rank {
		t.Fatalf("approved later but ranked first: %v <= %v", a.Rank, s.Rank)
	}
	// A drag can drop the card at a place in the target column.
	got, err := b.Move(ctx, User, int64(third.Number), api.MoveRequest{Column: api.ColumnReady, Before: intPtr(second.Number)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Rank >= get(t, b, second.Number).Rank {
		t.Fatalf("not placed above #%d: %v", second.Number, got.Rank)
	}
	_, err = b.Move(ctx, User, int64(first.Number), api.MoveRequest{Column: api.ColumnPlanning, Before: intPtr(second.Number)})
	wantCode(t, err, api.CodeValidation)
}

func TestCEOStoryApprovalFollowsSetting(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	ceo := agent(t, b, api.RoleCEO, 0)
	s := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	_, err := b.Approve(ctx, ceo, int64(s.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeForbidden)
	env.setPolicy(func(p *Policy) { p.CEOMayApproveStories = true })
	got, err := b.Approve(ctx, ceo, int64(s.Number), api.ApproveRequest{})
	if err != nil || got.Column != api.ColumnReady {
		t.Fatalf("got %s, %v", got.Column, err)
	}
	if _, err := b.Move(ctx, ceo, int64(s.Number), api.MoveRequest{Column: api.ColumnPlanning}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	_, err = b.Approve(ctx, ceo, int64(s.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeForbidden)
}

func TestCEOReleasesOwnStandaloneTasks(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	ceo := agent(t, b, api.RoleCEO, 0)
	own := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	users := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "U", Projects: []string{"api"}})
	_, err := b.Approve(ctx, ceo, int64(users.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeForbidden)

	env.setPolicy(func(p *Policy) { p.TasksRequireUserRelease = true })
	_, err = b.Approve(ctx, ceo, int64(own.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeForbidden)
	env.setPolicy(func(p *Policy) { p.TasksRequireUserRelease = false })
	if _, err := b.Approve(ctx, ceo, int64(own.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestPMReleasesOwnTasksUnlessUserReleaseRequired(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	pm := agent(t, b, api.RolePM, int64(s.Number))
	own := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})
	second := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "T2", Projects: []string{"api"}, Story: intPtr(s.Number)})
	users := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "U", Projects: []string{"api"}, Story: intPtr(s.Number)})

	if got, err := b.Approve(ctx, pm, int64(own.Number), api.ApproveRequest{}); err != nil || got.Column != api.ColumnReady {
		t.Fatalf("pm releases its task: %s, %v", got.Column, err)
	}
	_, err := b.Approve(ctx, pm, int64(users.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeForbidden)

	if _, err := b.Update(ctx, User, int64(s.Number), api.UpdateTicket{TaskRelease: api.Some(api.TaskReleaseUser)}); err != nil {
		t.Fatal(err)
	}
	_, err = b.Approve(ctx, pm, int64(second.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Move(ctx, pm, int64(own.Number), api.MoveRequest{Column: api.ColumnPlanning})
	wantCode(t, err, api.CodeForbidden)
	if got, err := b.Approve(ctx, User, int64(second.Number), api.ApproveRequest{}); err != nil || got.Column != api.ColumnReady {
		t.Fatalf("user releases: %s, %v", got.Column, err)
	}

	// A pm of another story releases nothing here.
	other := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "O", Projects: []string{"api"}})
	otherPM := agent(t, b, api.RolePM, int64(other.Number))
	_, err = b.Move(ctx, otherPM, int64(users.Number), api.MoveRequest{Column: api.ColumnReady})
	wantCode(t, err, api.CodeForbidden)
}

func TestWithdrawBlockedByLiveSession(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	if _, err := b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
	_, err := b.Move(ctx, User, int64(tk.Number), api.MoveRequest{Column: api.ColumnPlanning})
	wantCode(t, err, api.CodeInvalidTransition)
	if err := b.EndSession(ctx, dev.Session, "stopped"); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Move(ctx, User, int64(tk.Number), api.MoveRequest{Column: api.ColumnPlanning}); err != nil || got.Column != api.ColumnPlanning {
		t.Fatalf("got %s, %v", got.Column, err)
	}

	// A story counts the sessions of its tasks too.
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})
	force(t, b, s.Number, api.ColumnReady, "")
	agent(t, b, api.RoleDeveloper, int64(task.Number))
	_, err = b.Move(ctx, User, int64(s.Number), api.MoveRequest{Column: api.ColumnPlanning})
	wantCode(t, err, api.CodeInvalidTransition)
}

func TestUserCannotMoveIntoInProgressOrReview(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	for _, from := range []api.Column{api.ColumnPlanning, api.ColumnReady} {
		force(t, b, tk.Number, from, "")
		for _, to := range []api.Column{api.ColumnInProgress, api.ColumnReview} {
			_, err := b.Move(ctx, User, int64(tk.Number), api.MoveRequest{Column: to})
			wantCode(t, err, api.CodeInvalidTransition)
		}
	}
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageHuman)
	_, err := b.Move(ctx, User, int64(tk.Number), api.MoveRequest{Column: api.ColumnInProgress})
	wantCode(t, err, api.CodeValidation)
}

func TestStopStoryReturnsActiveTasksToReady(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	task := func(title string) int {
		return mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: title, Projects: []string{"api"}, Story: intPtr(s.Number)}).Number
	}
	working, reviewed, done, planned := task("W"), task("R"), task("D"), task("P")
	force(t, b, s.Number, api.ColumnInProgress, "")
	force(t, b, working, api.ColumnInProgress, "")
	force(t, b, reviewed, api.ColumnReview, api.ReviewStageAgent)
	force(t, b, done, api.ColumnDone, "")
	if err := b.SetBranch(ctx, int64(s.Number), "api", "omo/1-s", "/wt/1"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetAttention(ctx, int64(working), "stalled"); err != nil {
		t.Fatal(err)
	}
	last := lastEvent(t, b)

	got, err := b.Move(ctx, User, int64(s.Number), api.MoveRequest{Column: api.ColumnPlanning})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnPlanning || got.Projects[0].Branch != "omo/1-s" {
		t.Fatalf("story %s, projects %+v", got.Column, got.Projects)
	}
	for n, want := range map[int]api.Column{working: api.ColumnReady, reviewed: api.ColumnReady, done: api.ColumnDone, planned: api.ColumnPlanning} {
		tk := get(t, b, n)
		if tk.Column != want || tk.ReviewStage != "" || tk.Attention != "" {
			t.Errorf("#%d: %s stage %q attention %q, want %s", n, tk.Column, tk.ReviewStage, tk.Attention, want)
		}
	}
	if r := get(t, b, done).Resolution; r != api.ResolutionCompleted {
		t.Errorf("done task resolution %q", r)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 3 {
		t.Fatalf("moved events = %+v", moved)
	}
	for _, m := range moved {
		if m.Action != ActionStop {
			t.Errorf("action %q", m.Action)
		}
	}
}

// Work on a ticket is stopped by the user or by its supervisor: a product
// manager stops the tasks of its story, the CEO the tickets it supervises.
// Nobody else does, and no supervisor stops a ticket of another one.
func TestStopIsForTheUserAndTheSupervisor(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	other := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "O", Projects: []string{"api"}})
	inStory := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story.Number)})
	alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
	ceoTask := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "C", Projects: []string{"api"}, Supervisor: api.RoleCEO})
	for _, n := range []int{story.Number, other.Number} {
		force(t, b, n, api.ColumnInProgress, "")
	}
	pm := agent(t, b, api.RolePM, int64(story.Number))
	otherPM := agent(t, b, api.RolePM, int64(other.Number))
	ceo := agent(t, b, api.RoleCEO, 0)

	stop := func(a Actor, n int, to api.Column) error {
		t.Helper()
		force(t, b, n, api.ColumnInProgress, "")
		_, err := b.Move(ctx, a, int64(n), api.MoveRequest{Column: to})
		return err
	}
	refused := func(name string, a Actor, n int) {
		t.Helper()
		if err := stop(a, n, api.ColumnReady); errCode(err) != api.CodeForbidden {
			t.Errorf("%s: error = %v, want forbidden", name, err)
		}
		if col := get(t, b, n).Column; col != api.ColumnInProgress {
			t.Errorf("%s: the ticket is in %s", name, col)
		}
	}
	allowed := func(name string, a Actor, n int, to api.Column) {
		t.Helper()
		if err := stop(a, n, to); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if col := get(t, b, n).Column; col != to {
			t.Errorf("%s: the ticket is in %s, want %s", name, col, to)
		}
	}

	refused("the task's developer", agent(t, b, api.RoleDeveloper, int64(inStory.Number)), inStory.Number)
	refused("a standalone task's developer", agent(t, b, api.RoleDeveloper, int64(alone.Number)), alone.Number)
	refused("the pm of another story", otherPM, inStory.Number)
	refused("a pm, a standalone task", pm, alone.Number)
	refused("a pm, its own story", pm, story.Number)
	refused("the CEO, a task its pm supervises", ceo, inStory.Number)
	refused("the CEO, a ticket the user supervises", ceo, alone.Number)

	allowed("the pm, a task of its story, to ready", pm, inStory.Number, api.ColumnReady)
	allowed("the pm, a task of its story, to planning", pm, inStory.Number, api.ColumnPlanning)
	allowed("the CEO, a ticket it supervises", ceo, ceoTask.Number, api.ColumnReady)
	allowed("the user", User, alone.Number, api.ColumnPlanning)

	// With the CEO switched off its tickets fall to the user: a CEO session
	// that still runs stops nothing.
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	force(t, b, ceoTask.Number, api.ColumnInProgress, "")
	if _, err := b.Move(ctx, ceo, int64(ceoTask.Number), api.MoveRequest{Column: api.ColumnReady}); err == nil {
		t.Error("the CEO stopped a ticket while it is switched off")
	}
}

func TestCancelStoryCancelsOpenTasks(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	task := func(title string) int {
		return mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: title, Projects: []string{"api"}, Story: intPtr(s.Number)}).Number
	}
	done, working, planned := task("D"), task("W"), task("P")
	force(t, b, s.Number, api.ColumnInProgress, "")
	force(t, b, done, api.ColumnDone, "")
	force(t, b, working, api.ColumnReview, api.ReviewStageAgent)
	last := lastEvent(t, b)

	got, err := b.Cancel(ctx, User, int64(s.Number), api.CancelRequest{DeleteBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnDone || got.Resolution != api.ResolutionCancelled {
		t.Fatalf("story %s %s", got.Column, got.Resolution)
	}
	if got.Progress == nil || *got.Progress != (api.Progress{Done: 1, Total: 1}) {
		t.Fatalf("progress %+v", got.Progress)
	}
	if r := get(t, b, done).Resolution; r != api.ResolutionCompleted {
		t.Errorf("done task became %q", r)
	}
	for _, n := range []int{working, planned} {
		if tk := get(t, b, n); tk.Resolution != api.ResolutionCancelled || tk.ReviewStage != "" {
			t.Errorf("#%d: %s %q", n, tk.Resolution, tk.ReviewStage)
		}
	}
	evs := eventsAfter(t, b, last)
	moved := movedEvents(t, evs)
	if len(moved) != 3 {
		t.Fatalf("moved = %+v", moved)
	}
	for _, m := range moved {
		if m.Action != ActionCancel || m.DeleteBranch == nil || !*m.DeleteBranch {
			t.Errorf("event %+v", m)
		}
	}
	// The story's own event carries the final progress; nobody else needs
	// a derived update.
	if d := derivedUpdates(t, evs); len(d) != 0 {
		t.Errorf("derived updates %v", d)
	}
}

func TestCancelPermissions(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	ceo := agent(t, b, api.RoleCEO, 0)
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	pm := agent(t, b, api.RolePM, int64(s.Number))
	pmTask := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "P", Projects: []string{"api"}, Story: intPtr(s.Number)})
	userTask := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "U", Projects: []string{"api"}, Story: intPtr(s.Number)})
	ceoTask := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "C", Projects: []string{"api"}})
	dev := agent(t, b, api.RoleDeveloper, int64(userTask.Number))

	_, err := b.Cancel(ctx, pm, int64(userTask.Number), api.CancelRequest{})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Cancel(ctx, pm, int64(s.Number), api.CancelRequest{})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Cancel(ctx, ceo, int64(s.Number), api.CancelRequest{})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Cancel(ctx, dev, int64(userTask.Number), api.CancelRequest{})
	wantCode(t, err, api.CodeForbidden)
	if _, err := b.Cancel(ctx, pm, int64(pmTask.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	_, err = b.Cancel(ctx, ceo, int64(ceoTask.Number), api.CancelRequest{})
	wantCode(t, err, api.CodeForbidden)
	env.setPolicy(func(p *Policy) { p.CEOEnabled = true })
	if _, err := b.Cancel(ctx, ceo, int64(ceoTask.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestTransitionTouchesStoryAndDependents(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	first := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}, Story: intPtr(s.Number)})
	second := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "B", Projects: []string{"api"}, Story: intPtr(s.Number), DependsOn: []int{first.Number}})
	force(t, b, first.Number, api.ColumnReview, api.ReviewStageMerging)
	last := lastEvent(t, b)
	if err := b.CompleteMerge(ctx, int64(first.Number)); err != nil {
		t.Fatal(err)
	}
	evs := eventsAfter(t, b, last)
	if got := derivedUpdates(t, evs); fmt.Sprint(got) != fmt.Sprint([]int{s.Number, second.Number}) {
		t.Fatalf("derived updates %v", got)
	}
	if p := get(t, b, s.Number).Progress; *p != (api.Progress{Done: 1, Total: 2}) {
		t.Fatalf("progress %+v", p)
	}
	if w := get(t, b, second.Number).WaitingFor; len(w) != 0 {
		t.Fatalf("waiting for %v", w)
	}
}
