package board

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// setBranch records a branch for a ticket's project as if the engine had
// created it.
func setBranch(t *testing.T, b *Board, n int, project, branch string) {
	t.Helper()
	res, err := b.db.Exec(`UPDATE ticket_projects SET branch = ?, worktree = ? WHERE ticket_id = ? AND project_id = ?`,
		branch, "/wt/"+branch, n, project)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := res.RowsAffected(); c != 1 {
		t.Fatalf("no project %q on #%d", project, n)
	}
}

func projectIDs(tk api.Ticket) []string {
	out := []string{}
	for _, p := range tk.Projects {
		out = append(out, p.Project)
	}
	return out
}

func TestUpdateTitleEmitsChangedFields(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	last, err := db.LastEventID(b.db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Title: api.Some("  New  "), Model: api.Some("claude/opus")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "New" || got.Model != "claude/opus" {
		t.Fatalf("ticket = %q %q", got.Title, got.Model)
	}
	evs, err := db.EventsSince(b.db, last, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != api.KindTicketUpdated {
		t.Fatalf("events = %+v", evs)
	}
	var data api.TicketUpdatedData
	if err := json.Unmarshal([]byte(evs[0].Detail), &data); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(data.Fields, []string{"title", "model"}) || data.Ticket.Title != "New" {
		t.Fatalf("data = %+v", data)
	}

	// An update that changes nothing records nothing.
	if _, err := b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Title: api.Some("New")}); err != nil {
		t.Fatal(err)
	}
	evs, _ = db.EventsSince(b.db, last, 100, true)
	if len(evs) != 1 {
		t.Fatalf("no-op update emitted %d events", len(evs)-1)
	}

	_, err = b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Title: api.Some("  ")})
	wantCode(t, err, api.CodeValidation)
}

func TestUpdatePermissions(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})
	pm := agent(t, b, api.RolePM, int64(s.Number))
	dev := agent(t, b, api.RoleDeveloper, int64(task.Number))
	ceo := agent(t, b, api.RoleCEO, 0)

	if _, err := b.Update(ctx, pm, int64(task.Number), api.UpdateTicket{Title: api.Some("by pm")}); err != nil {
		t.Fatalf("pm title on task of its story: %v", err)
	}
	_, err := b.Update(ctx, pm, int64(s.Number), api.UpdateTicket{Title: api.Some("x")})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Update(ctx, dev, int64(task.Number), api.UpdateTicket{Title: api.Some("x")})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Update(ctx, ceo, int64(s.Number), api.UpdateTicket{Title: api.Some("x")})
	wantCode(t, err, api.CodeForbidden)
	// pm sets models only on tickets it created.
	_, err = b.Update(ctx, pm, int64(task.Number), api.UpdateTicket{Model: api.Some("claude/fable")})
	wantCode(t, err, api.CodeForbidden)
	own := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "P", Projects: []string{"api"}, Story: intPtr(s.Number)})
	if _, err := b.Update(ctx, pm, int64(own.Number), api.UpdateTicket{Model: api.Some("claude/fable")}); err != nil {
		t.Fatalf("pm model on own task: %v", err)
	}
	_, err = b.Update(ctx, pm, int64(own.Number), api.UpdateTicket{Model: api.Some("claude/opus")})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Update(ctx, pm, int64(own.Number), api.UpdateTicket{Review: api.Some(api.ReviewHuman)})
	wantCode(t, err, api.CodeForbidden)

	cs := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeStory, Title: "C"})
	if _, err := b.Update(ctx, ceo, int64(cs.Number), api.UpdateTicket{Title: api.Some("C2"), Projects: api.Some([]string{"ui"})}); err != nil {
		t.Fatalf("ceo on own story: %v", err)
	}
}

func TestProjectLockedWhileBranchExists(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	n := int64(tk.Number)
	setBranch(t, b, tk.Number, "api", "omo/t")
	// The ticket is in Planning (as after a stop and withdraw) and still locked.
	_, err := b.Update(ctx, User, n, api.UpdateTicket{Projects: api.Some([]string{"ui"})})
	wantCode(t, err, api.CodeFieldLocked)
	_, err = b.Update(ctx, User, n, api.UpdateTicket{Story: api.Some(s.Number)})
	wantCode(t, err, api.CodeFieldLocked)
	_, err = b.Update(ctx, User, n, api.UpdateTicket{BaseBranches: api.Some(map[string]string{"api": "dev"})})
	wantCode(t, err, api.CodeFieldLocked)
	// Other fields and unchanged values stay editable.
	if _, err := b.Update(ctx, User, n, api.UpdateTicket{Title: api.Some("ok"), Projects: api.Some([]string{"api"})}); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Get(ctx, User, n)
	if got.Projects[0].Branch != "omo/t" || got.Projects[0].Worktree != "/wt/omo/t" {
		t.Fatalf("branch lost: %+v", got.Projects)
	}
}

func TestUpdateProjectsAndBaseBranches(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	n := int64(s.Number)
	env.setBranch("ui", "develop")
	got, err := b.Update(ctx, User, n, api.UpdateTicket{Projects: api.Some([]string{"ui", "api"})})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(projectIDs(got), []string{"ui", "api"}) || got.Projects[0].BaseBranch != "develop" || got.Projects[1].BaseBranch != "main" {
		t.Fatalf("projects = %+v", got.Projects)
	}
	got, err = b.Update(ctx, User, n, api.UpdateTicket{BaseBranches: api.Some(map[string]string{"api": "release"})})
	if err != nil {
		t.Fatal(err)
	}
	if got.Projects[1].BaseBranch != "release" {
		t.Fatalf("projects = %+v", got.Projects)
	}
	_, err = b.Update(ctx, User, n, api.UpdateTicket{BaseBranches: api.Some(map[string]string{"nope": "x"})})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Update(ctx, User, n, api.UpdateTicket{Projects: api.Some([]string{"off"})})
	wantCode(t, err, api.CodeProjectUnavailable)
	_, err = b.Update(ctx, User, n, api.UpdateTicket{Projects: api.Some([]string{"api", "api"})})
	wantCode(t, err, api.CodeValidation)

	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})
	_, err = b.Update(ctx, User, int64(task.Number), api.UpdateTicket{BaseBranches: api.Some(map[string]string{"api": "x"})})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Update(ctx, User, int64(task.Number), api.UpdateTicket{Projects: api.Some([]string{"api", "ui"})})
	wantCode(t, err, api.CodeValidation)
}

// A base branch is a local branch of the project's repository: a revision
// (main~3), a name the repository lacks or a tag is refused; an unchanged
// value is left as it is.
func TestBaseBranchMustBeABranchOfTheRepository(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	env.mu.Lock()
	env.gone = map[string][]string{"/src/api": {"nope", "v1.0"}}
	env.mu.Unlock()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	n := int64(s.Number)
	for _, bad := range []string{"main~3", "main^", "nope", "v1.0", "HEAD@{1}"} {
		_, err := b.Update(ctx, User, n, api.UpdateTicket{BaseBranches: api.Some(map[string]string{"api": bad})})
		wantCode(t, err, api.CodeValidation)
	}
	if got, err := b.Get(ctx, User, n); err != nil || got.Projects[0].BaseBranch != "main" {
		t.Fatalf("base %q, %v", got.Projects[0].BaseBranch, err)
	}
	got, err := b.Update(ctx, User, n, api.UpdateTicket{BaseBranches: api.Some(map[string]string{"api": "release"})})
	if err != nil || got.Projects[0].BaseBranch != "release" {
		t.Fatalf("base %+v, %v", got.Projects, err)
	}
	// The branch is gone by now; sending the value again changes nothing.
	env.mu.Lock()
	env.gone["/src/api"] = append(env.gone["/src/api"], "release")
	env.mu.Unlock()
	if _, err := b.Update(ctx, User, n, api.UpdateTicket{Title: api.Some("S2"), BaseBranches: api.Some(map[string]string{"api": "release"})}); err != nil {
		t.Fatal(err)
	}
}

func TestStoryCannotDropProjectUsedByTask(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}})
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"ui"}, Story: intPtr(s.Number)})
	_, err := b.Update(ctx, User, int64(s.Number), api.UpdateTicket{Projects: api.Some([]string{"api"})})
	wantCode(t, err, api.CodeValidation)
	if _, err := b.Update(ctx, User, int64(s.Number), api.UpdateTicket{Projects: api.Some([]string{"ui"})}); err != nil {
		t.Fatalf("dropping an unused project: %v", err)
	}
}

func TestMovingTaskBetweenStoriesResetsSupervisor(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s1 := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S1", Projects: []string{"api"}})
	s2 := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S2", Projects: []string{"api", "ui"}})
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	n := int64(tk.Number)
	if tk.Supervisor != api.RoleUser {
		t.Fatalf("Supervisor = %q", tk.Supervisor)
	}
	last, _ := db.LastEventID(b.db)

	got, err := b.Update(ctx, User, n, api.UpdateTicket{Story: api.Some(s1.Number)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Supervisor != api.RolePM || got.Story == nil || *got.Story != s1.Number || got.Projects[0].BaseBranch != "" {
		t.Fatalf("in story: %+v", got.TicketSummary)
	}
	// The story's progress changed.
	evs, _ := db.EventsSince(b.db, last, 100, true)
	touched := false
	for _, e := range evs {
		touched = touched || (e.Kind == api.KindTicketUpdated && e.TicketID == int64(s1.Number))
	}
	if !touched {
		t.Fatal("story not touched")
	}

	if got, err = b.Update(ctx, User, n, api.UpdateTicket{Story: api.Some(s2.Number)}); err != nil {
		t.Fatal(err)
	}
	if got.Supervisor != api.RolePM || *got.Story != s2.Number {
		t.Fatalf("other story: %+v", got.TicketSummary)
	}
	got, err = b.Update(ctx, User, n, api.UpdateTicket{Story: api.Null[int]()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Supervisor != api.RoleUser || got.Story != nil || got.Projects[0].BaseBranch != "main" {
		t.Fatalf("standalone: %+v", got.TicketSummary)
	}

	// Into a story whose projects do not include the task's project.
	ui := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "U", Projects: []string{"ui"}})
	_, err = b.Update(ctx, User, int64(ui.Number), api.UpdateTicket{Story: api.Some(s1.Number)})
	wantCode(t, err, api.CodeValidation)
	// Changing project and story together is checked against the result.
	if _, err := b.Update(ctx, User, int64(ui.Number), api.UpdateTicket{Story: api.Some(s1.Number), Projects: api.Some([]string{"api"})}); err != nil {
		t.Fatal(err)
	}
	// Not a story, and stories cannot be nested.
	_, err = b.Update(ctx, User, n, api.UpdateTicket{Story: api.Some(ui.Number)})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Update(ctx, User, int64(s2.Number), api.UpdateTicket{Story: api.Some(s1.Number)})
	wantCode(t, err, api.CodeValidation)
	// An exported-when-approved task cannot move into a story.
	ex := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "E", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport})
	_, err = b.Update(ctx, User, int64(ex.Number), api.UpdateTicket{Story: api.Some(s1.Number)})
	wantCode(t, err, api.CodeValidation)
}

func TestSupervisorRules(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})
	alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})

	_, err := b.Update(ctx, User, int64(alone.Number), api.UpdateTicket{Supervisor: api.Some(api.RolePM)})
	wantCode(t, err, api.CodeValidation)
	got, err := b.Update(ctx, User, int64(task.Number), api.UpdateTicket{Supervisor: api.Some(api.RoleUser)})
	if err != nil || got.Supervisor != api.RoleUser {
		t.Fatalf("user supervisor on task: %v %q", err, got.Supervisor)
	}
	got, err = b.Update(ctx, User, int64(task.Number), api.UpdateTicket{Supervisor: api.Null[api.Role]()})
	if err != nil || got.Supervisor != api.RolePM {
		t.Fatalf("reset supervisor: %v %q", err, got.Supervisor)
	}

	pm := agent(t, b, api.RolePM, int64(s.Number))
	_, err = b.Update(ctx, pm, int64(task.Number), api.UpdateTicket{Supervisor: api.Some(api.RoleUser)})
	wantCode(t, err, api.CodeForbidden)

	env.setPolicy(func(p *Policy) { p.CEOEnabled = false })
	_, err = b.Update(ctx, User, int64(alone.Number), api.UpdateTicket{Supervisor: api.Some(api.RoleCEO)})
	wantCode(t, err, api.CodeValidation)
}

func TestUserOnlySettings(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})

	got, err := b.Update(ctx, User, int64(s.Number), api.UpdateTicket{
		Review: api.Some(api.ReviewHuman), WhenApproved: api.Some(api.WhenApprovedExport),
		TaskRelease: api.Some(api.TaskReleaseUser), MaxParallel: api.Some(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Review == nil || *got.Review != api.ReviewHuman || got.EffectiveWhenApproved != api.WhenApprovedExport ||
		got.TaskRelease == nil || *got.TaskRelease != api.TaskReleaseUser || got.MaxParallel == nil || *got.MaxParallel != 2 {
		t.Fatalf("story = %+v", got.TicketSummary)
	}
	got, err = b.Update(ctx, User, int64(s.Number), api.UpdateTicket{Review: api.Null[api.Review](), MaxParallel: api.Null[int]()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Review != nil || got.EffectiveReview != api.ReviewAgent || got.MaxParallel != nil {
		t.Fatalf("inherit = %+v", got.TicketSummary)
	}

	_, err = b.Update(ctx, User, int64(task.Number), api.UpdateTicket{TaskRelease: api.Some(api.TaskReleaseUser)})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Update(ctx, User, int64(task.Number), api.UpdateTicket{MaxParallel: api.Some(1)})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Update(ctx, User, int64(task.Number), api.UpdateTicket{WhenApproved: api.Some(api.WhenApprovedExport)})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Update(ctx, User, int64(s.Number), api.UpdateTicket{MaxParallel: api.Some(0)})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Update(ctx, User, int64(s.Number), api.UpdateTicket{Review: api.Some(api.Review("maybe"))})
	wantCode(t, err, api.CodeValidation)
}

func TestDoneTicketsAreReadOnly(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	forceDone(t, b, tk.Number, api.ResolutionCompleted)
	_, err := b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Title: api.Some("late")})
	wantCode(t, err, api.CodeInvalidTransition)
	_, err = b.Update(ctx, User, 999, api.UpdateTicket{Title: api.Some("x")})
	wantCode(t, err, api.CodeNotFound)
}

func TestMovingTaskOutOfStoryChecksDependencies(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	t1 := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T1", Projects: []string{"api"}, Story: intPtr(s.Number)})
	t2 := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T2", Projects: []string{"api"}, Story: intPtr(s.Number), DependsOn: []int{t1.Number}})
	_, err := b.Update(ctx, User, int64(t2.Number), api.UpdateTicket{Story: api.Null[int]()})
	wantCode(t, err, api.CodeDependencyScope)
	_, err = b.Update(ctx, User, int64(t1.Number), api.UpdateTicket{Story: api.Null[int]()})
	wantCode(t, err, api.CodeDependencyScope)
}
