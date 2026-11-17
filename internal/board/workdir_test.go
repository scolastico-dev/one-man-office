package board

import (
	"context"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestTaskWithoutProjectCreate(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "Research"})
	if len(alone.Projects) != 0 || alone.Workdir != "" {
		t.Fatalf("projects %+v, workdir %q", alone.Projects, alone.Workdir)
	}
	// Released like any task: nothing to check for projects.
	if _, err := b.Approve(ctx, User, int64(alone.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}

	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	force(t, b, story.Number, api.ColumnInProgress, "")
	pm := agent(t, b, api.RolePM, int64(story.Number))
	in := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "Look around", Story: intPtr(story.Number)})
	if in.Story == nil || *in.Story != story.Number || len(in.Projects) != 0 {
		t.Fatalf("task in story = %+v", in.TicketSummary)
	}

	// A story still needs a project to leave Planning.
	empty := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "E"})
	_, err := b.Approve(ctx, User, int64(empty.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeValidation)

	_, err = b.Create(ctx, User, api.CreateTicket{Type: api.TypeTask, Title: "X", WhenApproved: api.WhenApprovedExport})
	wantCode(t, err, api.CodeValidation)
	if !strings.Contains(err.Error(), "when_approved does not apply") {
		t.Fatalf("message = %v", err)
	}
}

func TestTaskProjectsCanBeDroppedAndAddedBeforeWork(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	got, err := b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Projects: api.Some([]string{})})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 0 {
		t.Fatalf("projects = %+v", got.Projects)
	}
	_, err = b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{BaseBranches: api.Some(map[string]string{})})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{WhenApproved: api.Some(api.WhenApprovedMerge)})
	wantCode(t, err, api.CodeValidation)

	if got, err = b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Projects: api.Some([]string{"ui"})}); err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 || got.Projects[0].Project != "ui" || got.Projects[0].BaseBranch != "main" {
		t.Fatalf("projects = %+v", got.Projects)
	}
	_, err = b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Projects: api.Some([]string{"ui", "api"})})
	wantCode(t, err, api.CodeValidation)

	// A task that loses its project cannot keep "when approved"; both
	// changes in one request are fine.
	if _, err := b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{WhenApproved: api.Some(api.WhenApprovedExport)}); err != nil {
		t.Fatal(err)
	}
	_, err = b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Projects: api.Some([]string{})})
	wantCode(t, err, api.CodeValidation)
	if _, err := b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Projects: api.Some([]string{}),
		WhenApproved: api.Null[api.WhenApproved]()}); err != nil {
		t.Fatal(err)
	}

	// Once work started in a working folder, projects and story are locked.
	force(t, b, tk.Number, api.ColumnInProgress, "")
	if err := b.SetWorkdir(ctx, int64(tk.Number), "/home/omo/workdirs/1"); err != nil {
		t.Fatal(err)
	}
	if w := get(t, b, tk.Number).Workdir; w != "/home/omo/workdirs/1" {
		t.Fatalf("workdir = %q", w)
	}
	_, err = b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Projects: api.Some([]string{"api"})})
	wantCode(t, err, api.CodeFieldLocked)
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	_, err = b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Story: api.Some(story.Number)})
	wantCode(t, err, api.CodeFieldLocked)
}

func TestSetWorkdirOnlyWithoutProject(t *testing.T) {
	b, _ := newTestBoard(t)
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	err := b.SetWorkdir(context.Background(), int64(tk.Number), "/x")
	wantCode(t, err, api.CodeValidation)
}

// handInWithoutProject puts a new task without a project with review into
// Review, handed in by its developer, and returns it and the developer.
func handInWithoutProject(t *testing.T, b *Board, review api.Review) (api.Ticket, Actor) {
	t.Helper()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "Research", Review: review})
	force(t, b, tk.Number, api.ColumnInProgress, "")
	dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
	got, err := b.Done(context.Background(), dev, api.DoneRequest{Comment: "the findings are in the description"})
	if err != nil {
		t.Fatal(err)
	}
	return got, dev
}

func TestTaskWithoutProjectReviewRounds(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk, dev := handInWithoutProject(t, b, api.ReviewAgent)
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageAgent || tk.ReviewRound != 1 {
		t.Fatalf("after hand-in: %s %q round %d", tk.Column, tk.ReviewStage, tk.ReviewRound)
	}
	// One verdict, for the ticket itself, ends the round.
	rev := reviewer(t, b, tk.Number, NoProjectKey)
	got, err := b.Done(ctx, rev, api.DoneRequest{Verdict: VerdictReject, Comment: "cite the sources"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnInProgress {
		t.Fatalf("after the rejection: %s", got.Column)
	}
	if c, ok := lastComment(t, b, tk.Number); !ok || c.Body != "cite the sources" {
		t.Fatalf("rejection comment = %+v", c)
	}
	_, err = b.Done(ctx, rev, api.DoneRequest{Verdict: VerdictApprove})
	wantCode(t, err, api.CodeInvalidTransition) // round 1 is over

	if _, err := b.Done(ctx, dev, api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	rev = reviewer(t, b, tk.Number, NoProjectKey)
	last := lastEvent(t, b)
	got, err = b.Done(ctx, rev, api.DoneRequest{Verdict: VerdictApprove, Comment: "good"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnDone || got.Resolution != api.ResolutionCompleted || got.ReviewStage != "" {
		t.Fatalf("after the approval: %s %s %q", got.Column, got.Resolution, got.ReviewStage)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 1 || moved[0].From != api.ColumnReview || moved[0].To != api.ColumnDone || moved[0].Action != ActionVerdict {
		t.Fatalf("moved = %+v", moved)
	}
}

func TestTaskWithoutProjectHumanReview(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk, _ := handInWithoutProject(t, b, api.ReviewAgentThenHuman)
	if _, err := b.Done(ctx, reviewer(t, b, tk.Number, NoProjectKey), api.DoneRequest{Verdict: VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, tk.Number); got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageHuman {
		t.Fatalf("after the agent's approval: %s %q", got.Column, got.ReviewStage)
	}
	for _, req := range []api.ApproveRequest{{Export: true}, {Merge: true}} {
		_, err := b.Approve(ctx, User, int64(tk.Number), req)
		wantCode(t, err, api.CodeValidation)
		if !strings.Contains(err.Error(), "nothing to merge or export") {
			t.Fatalf("message = %v", err)
		}
	}
	got, err := b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnDone || got.Resolution != api.ResolutionCompleted {
		t.Fatalf("after the user's approval: %s %s", got.Column, got.Resolution)
	}

	// Dragging to Done approves the same way.
	other, _ := handInWithoutProject(t, b, api.ReviewHuman)
	if other.ReviewStage != api.ReviewStageHuman {
		t.Fatalf("stage = %q", other.ReviewStage)
	}
	if got, err = b.Move(ctx, User, int64(other.Number), api.MoveRequest{Column: api.ColumnDone}); err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnDone || got.Resolution != api.ResolutionCompleted {
		t.Fatalf("after the drag: %s %s", got.Column, got.Resolution)
	}
}

func TestTaskWithoutProjectSkippedReviewIsDoneAtHandIn(t *testing.T) {
	b, env := newTestBoard(t)
	// Even with export as the default for tasks: nothing to export.
	env.setPolicy(func(p *Policy) { p.WhenApprovedTask = api.WhenApprovedExport })
	tk, _ := handInWithoutProject(t, b, api.ReviewSkipped)
	if tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted || tk.ReviewRound != 1 {
		t.Fatalf("after hand-in: %s %s round %d", tk.Column, tk.Resolution, tk.ReviewRound)
	}
}

func TestStoryWithTaskWithoutProjectHandsIn(t *testing.T) {
	b, env := newTestBoard(t)
	env.setPolicy(func(p *Policy) { p.ReviewStoryTask = api.ReviewSkipped })
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	force(t, b, s.Number, api.ColumnInProgress, "")
	pm := agent(t, b, api.RolePM, int64(s.Number))
	research := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "Research", Story: intPtr(s.Number)})
	code := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "Code", Projects: []string{"api"}, Story: intPtr(s.Number)})
	force(t, b, research.Number, api.ColumnInProgress, "")
	if _, err := b.Done(ctx, agent(t, b, api.RoleDeveloper, int64(research.Number)), api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, research.Number); got.Column != api.ColumnDone {
		t.Fatalf("research task: %s", got.Column)
	}
	_, err := b.Done(ctx, pm, api.DoneRequest{})
	wantCode(t, err, api.CodeInvalidTransition) // the coding task is open
	force(t, b, code.Number, api.ColumnDone, "")
	got, err := b.Done(ctx, pm, api.DoneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAgent {
		t.Fatalf("story after hand-in: %s %q", got.Column, got.ReviewStage)
	}
}

func TestWorkdirRemoval(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "PoC"})
	_, err := b.CheckWorkdirRemoval(ctx, User, int64(tk.Number))
	wantCode(t, err, api.CodeInvalidTransition) // none recorded
	force(t, b, tk.Number, api.ColumnInProgress, "")
	const dir = "/home/omo/workdirs/1"
	if err := b.SetWorkdir(ctx, int64(tk.Number), dir); err != nil {
		t.Fatal(err)
	}
	_, err = b.CheckWorkdirRemoval(ctx, User, int64(tk.Number))
	wantCode(t, err, api.CodeInvalidTransition) // still in progress
	force(t, b, tk.Number, api.ColumnDone, "")
	_, err = b.CheckWorkdirRemoval(ctx, agent(t, b, api.RoleCEO, 0), int64(tk.Number))
	wantCode(t, err, api.CodeForbidden)
	got, err := b.CheckWorkdirRemoval(ctx, User, int64(tk.Number))
	if err != nil || got != dir {
		t.Fatalf("CheckWorkdirRemoval = %q, %v", got, err)
	}
	if err := b.ForgetWorkdir(ctx, int64(tk.Number), "/elsewhere"); err != nil {
		t.Fatal(err)
	}
	if w := get(t, b, tk.Number).Workdir; w != dir {
		t.Fatalf("another folder was forgotten: %q", w)
	}
	if err := b.ForgetWorkdir(ctx, int64(tk.Number), dir); err != nil {
		t.Fatal(err)
	}
	if w := get(t, b, tk.Number).Workdir; w != "" {
		t.Fatalf("workdir = %q", w)
	}
	if c, ok := lastComment(t, b, tk.Number); !ok || !strings.Contains(c.Body, dir) || c.Author.Role != api.RoleSystem {
		t.Fatalf("comment = %+v", c)
	}
}

func TestRemoveProjectKeepsTheLastProject(t *testing.T) {
	b, _ := newTestBoard(t)
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	_, err := b.ClaimProjectRemoval(context.Background(), User, int64(tk.Number), "api")
	wantCode(t, err, api.CodeValidation)
	if !strings.Contains(err.Error(), "does not become one without a project") {
		t.Fatalf("message = %v", err)
	}
}
