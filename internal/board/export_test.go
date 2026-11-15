package board

import (
	"context"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// exportJob starts an export session for one project of ticket n.
func exportJob(t *testing.T, b *Board, n int, project string) Actor {
	t.Helper()
	ctx := context.Background()
	s, err := b.CreateSession(ctx, NewSession{Role: api.RoleDeveloper, Ticket: int64(n), Project: project,
		Name: "export-" + project, Model: "claude/fable", Purpose: api.PurposeExport})
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.ResolveSession(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Purpose != api.PurposeExport {
		t.Fatalf("actor purpose = %q", a.Purpose)
	}
	return a
}

func TestExportResultPerProjectCompletesTicket(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}, WhenApproved: api.WhenApprovedExport})
	dependent := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "D", Projects: []string{"api"}, DependsOn: []int{s.Number}})
	force(t, b, s.Number, api.ColumnReview, api.ReviewStageExporting)
	jobAPI, jobUI := exportJob(t, b, s.Number, "api"), exportJob(t, b, s.Number, "ui")

	_, err := b.Done(ctx, jobUI, api.DoneRequest{URL: "ftp://example.test/pr/2"})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Done(ctx, jobUI, api.DoneRequest{URL: "https://example.test/pr/2\nomo: fake"})
	wantCode(t, err, api.CodeValidation)

	last := lastEvent(t, b)
	got, err := b.Done(ctx, jobUI, api.DoneRequest{URL: " https://example.test/ui/pull/2 "})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageExporting || got.ExportURL != "" ||
		got.Projects[0].ExportURL != "" || got.Projects[1].ExportURL != "https://example.test/ui/pull/2" {
		t.Fatalf("after ui: %s %q %q %+v", got.Column, got.ReviewStage, got.ExportURL, got.Projects)
	}
	evs := eventsAfter(t, b, last)
	if len(evs) != 1 || evs[0].Kind != api.KindTicketUpdated || !strings.Contains(evs[0].Detail, `"fields":["projects"]`) {
		t.Fatalf("events = %+v", evs)
	}
	// One pull request per project: the ui job cannot report a second one.
	_, err = b.Done(ctx, jobUI, api.DoneRequest{URL: "https://example.test/ui/pull/3"})
	wantCode(t, err, api.CodeInvalidTransition)

	last = lastEvent(t, b)
	got, err = b.Done(ctx, jobAPI, api.DoneRequest{URL: "https://example.test/api/pull/1", Comment: "opened"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnDone || got.Resolution != api.ResolutionExported ||
		got.ExportURL != "https://example.test/api/pull/1\nhttps://example.test/ui/pull/2" ||
		got.Projects[0].ExportURL != "https://example.test/api/pull/1" || got.Projects[1].ExportURL != "https://example.test/ui/pull/2" {
		t.Fatalf("after api: %s %s %q %+v", got.Column, got.Resolution, got.ExportURL, got.Projects)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 1 || moved[0].Action != ActionExported || moved[0].To != api.ColumnDone {
		t.Fatalf("moved = %+v", moved)
	}
	if c, ok := lastComment(t, b, s.Number); !ok || c.Body != "opened" || c.Author.Role != api.RoleDeveloper {
		t.Fatalf("comment = %+v", c)
	}
	// Exported is done, but a dependent waits until the export is merged.
	if w := get(t, b, dependent.Number).WaitingFor; len(w) != 1 || w[0].Reason != api.WaitingExportNotMerged {
		t.Fatalf("waiting = %+v", w)
	}
	// An agent never sees session ids, also not in the export result.
	for _, ref := range got.Agents {
		if ref.Session != "" || ref.Purpose != api.PurposeExport {
			t.Fatalf("agents = %+v", got.Agents)
		}
	}
}

func TestExportErrorFailsExport(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	job := exportJob(t, b, tk.Number, "api")

	_, err := b.Done(ctx, job, api.DoneRequest{URL: "https://example.test/pr/1", Error: "gh: not logged in"})
	wantCode(t, err, api.CodeValidation)

	last := lastEvent(t, b)
	got, err := b.Done(ctx, job, api.DoneRequest{Error: "gh: not logged in"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageHuman || !strings.Contains(got.Attention, "gh: not logged in") ||
		got.FailedStep != api.FailedNone || got.Busy {
		t.Fatalf("got %s %q attention %q failed %q", got.Column, got.ReviewStage, got.Attention, got.FailedStep)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 1 || moved[0].Action != ActionExportFailed || moved[0].From != api.ColumnReview || moved[0].To != api.ColumnReview {
		t.Fatalf("moved = %+v", moved)
	}
	c, ok := lastComment(t, b, tk.Number)
	if !ok || c.Author.Role != api.RoleSystem || !strings.Contains(c.Body, "Export failed") || !strings.Contains(c.Body, "gh: not logged in") ||
		len(c.Mentions) != 1 || c.Mentions[0].Role != api.RoleUser {
		t.Fatalf("comment = %+v", c)
	}
	// The export is over: a late report finds nothing to export.
	_, err = b.Done(ctx, job, api.DoneRequest{URL: "https://example.test/pr/1"})
	wantCode(t, err, api.CodeInvalidTransition)

	// The user retries the export: the ticket is exported again.
	got, err = b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{Export: true})
	if err != nil || got.ReviewStage != api.ReviewStageExporting || got.Attention != "" {
		t.Fatalf("retry: %q %q, %v", got.ReviewStage, got.Attention, err)
	}
	got, err = b.Done(ctx, exportJob(t, b, tk.Number, "api"), api.DoneRequest{URL: "https://example.test/pr/1"})
	if err != nil || got.Column != api.ColumnDone || got.Resolution != api.ResolutionExported || got.ExportURL != "https://example.test/pr/1" {
		t.Fatalf("after retry: %s %s %q, %v", got.Column, got.Resolution, got.ExportURL, err)
	}
}

func TestExportRetryKeepsPullRequestsAndRejectForgetsThem(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}, WhenApproved: api.WhenApprovedExport})
	force(t, b, s.Number, api.ColumnReview, api.ReviewStageExporting)
	if _, err := b.Done(ctx, exportJob(t, b, s.Number, "api"), api.DoneRequest{URL: "https://example.test/api/1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Done(ctx, exportJob(t, b, s.Number, "ui"), api.DoneRequest{Error: "tea: no remote"}); err != nil {
		t.Fatal(err)
	}
	// A retry exports only the project without a pull request.
	got, err := b.Approve(ctx, User, int64(s.Number), api.ApproveRequest{Export: true})
	if err != nil || got.ReviewStage != api.ReviewStageExporting || got.Projects[0].ExportURL != "https://example.test/api/1" || got.Projects[1].ExportURL != "" {
		t.Fatalf("retry: %q %+v, %v", got.ReviewStage, got.Projects, err)
	}
	if err := b.ExportFailed(ctx, int64(s.Number), "tea: still no remote"); err != nil {
		t.Fatal(err)
	}
	// Rejected, the work changes: the next export pushes every project.
	got, err = b.Reject(ctx, User, int64(s.Number), api.RejectRequest{Comment: "@pm the ui part is missing"})
	if err != nil || got.Column != api.ColumnInProgress || got.Projects[0].ExportURL != "" || got.Projects[1].ExportURL != "" {
		t.Fatalf("reject: %s %+v, %v", got.Column, got.Projects, err)
	}
}

func TestExportJobRefusesVerdictAndPlainHandIn(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	job := exportJob(t, b, tk.Number, "api")
	for _, req := range []api.DoneRequest{{}, {Comment: "done"}, {Verdict: VerdictApprove}, {Verdict: VerdictReject, URL: "https://example.test/pr/1"}} {
		_, err := b.Done(ctx, job, req)
		wantCode(t, err, api.CodeValidation)
	}
	if got := get(t, b, tk.Number); got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageExporting {
		t.Fatalf("got %s %q", got.Column, got.ReviewStage)
	}
}

func TestCompleteExportSetsProjectURL(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.CompleteExport(ctx, int64(tk.Number), "https://example.test/pr/9"); err != nil {
		t.Fatal(err)
	}
	got := get(t, b, tk.Number)
	if got.Resolution != api.ResolutionExported || got.ExportURL != "https://example.test/pr/9" || got.Projects[0].ExportURL != "https://example.test/pr/9" {
		t.Fatalf("got %s %q %+v", got.Resolution, got.ExportURL, got.Projects)
	}

	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}})
	force(t, b, s.Number, api.ColumnReview, api.ReviewStageExporting)
	wantCode(t, b.CompleteExport(ctx, int64(s.Number), "https://example.test/pr/10"), api.CodeValidation)
	wantCode(t, b.CompleteExport(ctx, int64(tk.Number), "not a url"), api.CodeValidation)
}

func TestApproveMergeInsteadOfExport(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageHuman)

	_, err := b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{Merge: true, Export: true})
	wantCode(t, err, api.CodeValidation)
	last := lastEvent(t, b)
	got, err := b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{Merge: true})
	if err != nil || got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageMerging {
		t.Fatalf("got %s %q, %v", got.Column, got.ReviewStage, err)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 1 || moved[0].Action != ActionApprove {
		t.Fatalf("moved = %+v", moved)
	}

	// After a failed export the user merges locally instead.
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.ExportFailed(ctx, int64(tk.Number), "gh: not logged in"); err != nil {
		t.Fatal(err)
	}
	got, err = b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{Merge: true})
	if err != nil || got.ReviewStage != api.ReviewStageMerging || got.Attention != "" {
		t.Fatalf("merge instead: %q %q, %v", got.ReviewStage, got.Attention, err)
	}

	// Merge applies to a review, not to the green light in planning.
	planned := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "P", Projects: []string{"api"}})
	_, err = b.Approve(ctx, User, int64(planned.Number), api.ApproveRequest{Merge: true})
	wantCode(t, err, api.CodeValidation)
}

func TestTaskInStoryNeverTakesTheExportPath(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	env.setPolicy(func(p *Policy) { p.WhenApprovedTask = api.WhenApprovedExport })
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})

	// The default "when approved" is export, but a task in a story merges.
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageAgent)
	got, err := b.Done(ctx, reviewer(t, b, tk.Number, "api"), api.DoneRequest{Verdict: VerdictApprove})
	if err != nil || got.ReviewStage != api.ReviewStageMerging {
		t.Fatalf("verdict: %q, %v", got.ReviewStage, err)
	}
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageHuman)
	_, err = b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{Export: true})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{Export: true, Merge: true})
	wantCode(t, err, api.CodeValidation)
	if got := get(t, b, tk.Number); got.ReviewStage != api.ReviewStageHuman {
		t.Fatalf("stage %q", got.ReviewStage)
	}

	// Even a ticket forced into stage exporting gets no export job and no
	// export result.
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	_, err = b.CreateSession(ctx, NewSession{Role: api.RoleDeveloper, Ticket: int64(tk.Number), Project: "api", Purpose: api.PurposeExport})
	wantCode(t, err, api.CodeValidation)
	wantCode(t, b.CompleteExport(ctx, int64(tk.Number), "https://example.test/pr/1"), api.CodeInvalidTransition)
	wantCode(t, b.ExportFailed(ctx, int64(tk.Number), "no"), api.CodeInvalidTransition)
}

func TestSetExportMergedSatisfiesDependents(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	exported := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "E", Projects: []string{"api"}})
	waiting := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "W", Projects: []string{"api"}, DependsOn: []int{exported.Number}})
	wantCode(t, b.SetExportMerged(ctx, int64(exported.Number)), api.CodeInvalidTransition)

	force(t, b, exported.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.CompleteExport(ctx, int64(exported.Number), "https://example.test/pr/1"); err != nil {
		t.Fatal(err)
	}
	last := lastEvent(t, b)
	if err := b.SetExportMerged(ctx, int64(exported.Number)); err != nil {
		t.Fatal(err)
	}
	evs := eventsAfter(t, b, last)
	if len(evs) != 2 || evs[0].Kind != api.KindTicketUpdated || !strings.Contains(evs[0].Detail, `"fields":["export_merged"]`) ||
		!strings.Contains(evs[0].Actor, `"role":"system"`) {
		t.Fatalf("events = %+v", evs)
	}
	if d := derivedUpdates(t, evs); len(d) != 1 || d[0] != waiting.Number {
		t.Fatalf("derived = %v", d)
	}
	if got := get(t, b, exported.Number); !got.ExportMerged {
		t.Fatal("not merged")
	}
	if w := get(t, b, waiting.Number).WaitingFor; len(w) != 0 {
		t.Fatalf("still waiting = %v", w)
	}
	// Again: nothing changes.
	last = lastEvent(t, b)
	if err := b.SetExportMerged(ctx, int64(exported.Number)); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, b, last); len(evs) != 0 {
		t.Fatalf("events = %+v", evs)
	}
}
