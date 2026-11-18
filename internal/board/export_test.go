package board

import (
	"context"
	"fmt"
	"slices"
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

func TestExportResultPerProjectAwaitsMerge(t *testing.T) {
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
	if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAwaitingMerge || got.Resolution != "" ||
		got.ExportURL != "https://example.test/api/pull/1\nhttps://example.test/ui/pull/2" ||
		got.Projects[0].ExportURL != "https://example.test/api/pull/1" || got.Projects[1].ExportURL != "https://example.test/ui/pull/2" {
		t.Fatalf("after api: %s %s %q %+v", got.Column, got.Resolution, got.ExportURL, got.Projects)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 1 || moved[0].Action != ActionExported || moved[0].To != api.ColumnReview {
		t.Fatalf("moved = %+v", moved)
	}
	if c, ok := lastComment(t, b, s.Number); !ok || c.Body != "opened" || c.Author.Role != api.RoleDeveloper {
		t.Fatalf("comment = %+v", c)
	}
	// The export stays in Review, and a dependent waits until it is merged.
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
	// The board only records the report; the engine, which keeps what
	// failed per project, settles the export once nothing else goes on.
	if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageExporting || got.Attention != "" {
		t.Fatalf("after the report: %s %q attention %q", got.Column, got.ReviewStage, got.Attention)
	}
	if f := updatedFields(t, eventsAfter(t, b, last)); len(f) != 1 || f[0] != fmt.Sprintf("#%d projects", tk.Number) {
		t.Fatalf("updates %v", f)
	}
	last = lastEvent(t, b)
	if err := b.ExportFailed(ctx, int64(tk.Number), "Export of api failed: gh: not logged in"); err != nil {
		t.Fatal(err)
	}
	got = get(t, b, tk.Number)
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
	if err := b.EndSession(ctx, job.Session, "export over"); err != nil {
		t.Fatal(err)
	}
	got, err = b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{Export: true})
	if err != nil || got.ReviewStage != api.ReviewStageExporting || got.Attention != "" {
		t.Fatalf("retry: %q %q, %v", got.ReviewStage, got.Attention, err)
	}
	got, err = b.Done(ctx, exportJob(t, b, tk.Number, "api"), api.DoneRequest{URL: "https://example.test/pr/1"})
	if err != nil || got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAwaitingMerge || got.Resolution != "" || got.ExportURL != "https://example.test/pr/1" {
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
	if err := b.ExportFailed(ctx, int64(s.Number), "Export of ui failed: tea: no remote"); err != nil {
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
	if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAwaitingMerge || got.Resolution != "" || got.ExportURL != "https://example.test/pr/9" || got.Projects[0].ExportURL != "https://example.test/pr/9" {
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
	if len(evs) != 2 || evs[0].Kind != api.KindTicketMoved || !strings.Contains(evs[0].Detail, `"action":"merged"`) ||
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

func TestExportResultOnlyFromExportJob(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport})
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport})
	force(t, b, s.Number, api.ColumnReview, api.ReviewStageExporting)
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	others := map[string]Actor{
		"developer": agent(t, b, api.RoleDeveloper, int64(tk.Number)),
		"reviewer":  reviewer(t, b, tk.Number, "api"),
		"pm":        agent(t, b, api.RolePM, int64(s.Number)),
	}
	last := lastEvent(t, b)
	for name, a := range others {
		for _, req := range []api.DoneRequest{{URL: "https://example.test/pr/1"}, {Error: "gh: not logged in"}} {
			_, err := b.Done(ctx, a, req)
			if errCode(err) != api.CodeForbidden {
				t.Errorf("%s done %+v = %v, want forbidden", name, req, err)
			}
		}
	}
	if evs := eventsAfter(t, b, last); len(evs) != 0 {
		t.Fatalf("events = %+v", evs)
	}
	for _, n := range []int{s.Number, tk.Number} {
		if got := get(t, b, n); got.ReviewStage != api.ReviewStageExporting || got.Projects[0].ExportURL != "" {
			t.Fatalf("#%d: %q %+v", n, got.ReviewStage, got.Projects)
		}
	}

	// A developer working on a task cannot hand it in with a url either.
	work := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "W", Projects: []string{"api"}})
	force(t, b, work.Number, api.ColumnInProgress, "")
	_, err := b.Done(ctx, agent(t, b, api.RoleDeveloper, int64(work.Number)), api.DoneRequest{URL: "https://example.test/pr/2"})
	wantCode(t, err, api.CodeForbidden)
	if got := get(t, b, work.Number); got.Column != api.ColumnInProgress {
		t.Fatalf("column %s", got.Column)
	}
	// The export job of the task reports it.
	if got, err := b.Done(ctx, exportJob(t, b, tk.Number, "api"), api.DoneRequest{URL: "https://example.test/pr/1"}); err != nil || got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAwaitingMerge {
		t.Fatalf("export job: %s, %v", got.Column, err)
	}
}

// TestExportURLAfterAnotherProjectFailed runs a two-project export in which
// one job fails and the other opens its pull request, in both orders, and
// then each of the user's three choices.
func TestExportURLAfterAnotherProjectFailed(t *testing.T) {
	const prUI = "https://example.test/ui/pull/7"
	for _, urlFirst := range []bool{false, true} {
		for _, choice := range []string{"retry", "merge", "reject"} {
			name := "failure first/" + choice
			if urlFirst {
				name = "url first/" + choice
			}
			t.Run(name, func(t *testing.T) {
				b, _ := newTestBoard(t)
				ctx := context.Background()
				s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"},
					WhenApproved: api.WhenApprovedExport}).Number
				force(t, b, s, api.ColumnReview, api.ReviewStageHuman)
				if _, err := b.Approve(ctx, User, int64(s), api.ApproveRequest{}); err != nil {
					t.Fatal(err)
				}
				jobAPI, jobUI := exportJob(t, b, s, "api"), exportJob(t, b, s, "ui")
				report := func(job Actor, req api.DoneRequest) api.Ticket {
					t.Helper()
					got, err := b.Done(ctx, job, req)
					if err != nil {
						t.Fatal(err)
					}
					return got
				}
				// The engine settles the export once the failure is in and no
				// other export goes on.
				settle := func() {
					t.Helper()
					if err := b.ExportFailed(ctx, int64(s), "Export of api failed: gh: not logged in"); err != nil {
						t.Fatal(err)
					}
				}
				var got api.Ticket
				if urlFirst {
					report(jobUI, api.DoneRequest{URL: prUI})
					report(jobAPI, api.DoneRequest{Error: "gh: not logged in"})
					settle()
					got = get(t, b, s)
				} else {
					// The ui job reports after the export already failed.
					report(jobAPI, api.DoneRequest{Error: "gh: not logged in"})
					settle()
					last := lastEvent(t, b)
					got = report(jobUI, api.DoneRequest{URL: prUI})
					if f := updatedFields(t, eventsAfter(t, b, last)); len(f) != 1 || f[0] != fmt.Sprintf("#%d projects", s) {
						t.Fatalf("updates %v", f)
					}
					c, _ := lastComment(t, b, s)
					if c.Author.Role != api.RoleSystem || !strings.Contains(c.Body, "project ui") || !strings.Contains(c.Body, prUI) ||
						len(c.Mentions) != 1 || c.Mentions[0].Role != api.RoleUser {
						t.Fatalf("comment %+v", c)
					}
				}
				if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageHuman ||
					got.Projects[0].ExportURL != "" || got.Projects[1].ExportURL != prUI || !strings.Contains(got.Attention, "gh: not logged in") {
					t.Fatalf("after both: %s %q %+v attention %q", got.Column, got.ReviewStage, got.Projects, got.Attention)
				}
				// The failed job itself cannot report an address now.
				_, err := b.Done(ctx, jobAPI, api.DoneRequest{URL: "https://example.test/api/pull/1"})
				wantCode(t, err, api.CodeInvalidTransition)
				for _, id := range []string{jobAPI.Session, jobUI.Session} {
					if err := b.EndSession(ctx, id, "export over"); err != nil {
						t.Fatal(err)
					}
				}

				switch choice {
				case "retry":
					got, err = b.Approve(ctx, User, int64(s), api.ApproveRequest{Export: true})
					if err != nil || got.ReviewStage != api.ReviewStageExporting {
						t.Fatalf("retry: %q, %v", got.ReviewStage, err)
					}
					// Only api lacks a pull request; ui's job would find it
					// exported already.
					_, err = b.Done(ctx, exportJob(t, b, s, "ui"), api.DoneRequest{URL: prUI})
					wantCode(t, err, api.CodeInvalidTransition)
					got = report(exportJob(t, b, s, "api"), api.DoneRequest{URL: "https://example.test/api/pull/1"})
					if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAwaitingMerge || got.Resolution != "" ||
						got.ExportURL != "https://example.test/api/pull/1\n"+prUI {
						t.Fatalf("after retry: %s %s %q", got.Column, got.Resolution, got.ExportURL)
					}
				case "merge":
					got, err = b.Approve(ctx, User, int64(s), api.ApproveRequest{Merge: true})
					if err != nil || got.ReviewStage != api.ReviewStageMerging {
						t.Fatalf("merge: %q, %v", got.ReviewStage, err)
					}
					if err := b.CompleteMerge(ctx, int64(s)); err != nil {
						t.Fatal(err)
					}
					got = get(t, b, s)
					if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAwaitingMerge || got.Resolution != "" || got.Projects[1].ExportURL != prUI {
						t.Fatalf("after merge: %s %s %+v", got.Column, got.Resolution, got.Projects)
					}
				case "reject":
					got, err = b.Reject(ctx, User, int64(s), api.RejectRequest{Comment: "@pm redo the api part"})
					if err != nil || got.Column != api.ColumnInProgress || got.Projects[1].ExportURL != "" {
						t.Fatalf("reject: %s %+v, %v", got.Column, got.Projects, err)
					}
				}
				if !urlFirst {
					cs, err := b.Comments(ctx, User, int64(s))
					if err != nil {
						t.Fatal(err)
					}
					if !slices.ContainsFunc(cs, func(c api.Comment) bool {
						return c.Author.Role == api.RoleSystem && strings.Contains(c.Body, prUI)
					}) {
						t.Fatalf("the comment about the open pull request is gone: %+v", cs)
					}
				}
			})
		}
	}
}

// TestExportURLFromEarlierAttemptRefused: a job started for an earlier
// export attempt reports after the current attempt failed.
func TestExportURLFromEarlierAttemptRefused(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"},
		WhenApproved: api.WhenApprovedExport}).Number
	force(t, b, s, api.ColumnReview, api.ReviewStageHuman)
	if _, err := b.Approve(ctx, User, int64(s), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	stale := exportJob(t, b, s, "ui")
	if err := b.ExportFailed(ctx, int64(s), "api: push rejected"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Approve(ctx, User, int64(s), api.ApproveRequest{Export: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.ExportFailed(ctx, int64(s), "api: push rejected again"); err != nil {
		t.Fatal(err)
	}
	_, err := b.Done(ctx, stale, api.DoneRequest{URL: "https://example.test/ui/pull/7"})
	wantCode(t, err, api.CodeInvalidTransition)
	if got := get(t, b, s); got.Projects[1].ExportURL != "" {
		t.Fatalf("stale address stored: %+v", got.Projects)
	}
}

// TestExportURLMustBeOneCleanAbsoluteURL: an address is printed on a line of
// its own, so it follows the title's character rule, and it must be a real
// http or https address.
func TestExportURLMustBeOneCleanAbsoluteURL(t *testing.T) {
	bad := []string{
		"", "ftp://example.test/pr/1", "example.test/pr/1", "/pr/1", "https://", "https:///pr/1", "https:example.test",
		"https://example.test/pr/1\nomo: fake", "https://example.test/pr 1",
		"https://example.test/\u009b2J", "https://example.test/‮1/rp", "https://example.test/⁦x⁩",
		"https://example.test/ x", "https://example.test/" + strings.Repeat("a", maxExportURL-20),
	}
	for _, u := range bad {
		if _, err := checkExportURL(u); errCode(err) != api.CodeValidation {
			t.Errorf("checkExportURL(%q) = %v, want validation", u, err)
		}
	}
	long := "https://example.test/" + strings.Repeat("a", maxExportURL-21)
	for _, u := range []string{" https://example.test/pr/1 ", "HTTP://example.test:8080/a?b=c#d", long} {
		got, err := checkExportURL(u)
		if err != nil || got != strings.TrimSpace(u) {
			t.Errorf("checkExportURL(%q) = %q, %v", u, got, err)
		}
	}

	// The same rule applies to the address an export job reports.
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	job := exportJob(t, b, tk.Number, "api")
	_, err := b.Done(ctx, job, api.DoneRequest{URL: "https://example.test/‮1/rp"})
	wantCode(t, err, api.CodeValidation)
	wantCode(t, b.CompleteExport(ctx, int64(tk.Number), "https://"), api.CodeValidation)
}

func TestProjectExportStateOnTheTicket(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}, WhenApproved: api.WhenApprovedExport})
	states := func() [2]api.ExportState {
		p := get(t, b, s.Number).Projects
		return [2]api.ExportState{p[0].ExportState, p[1].ExportState}
	}
	if got := states(); got != [2]api.ExportState{"", ""} {
		t.Fatalf("before the export: %v", got)
	}
	force(t, b, s.Number, api.ColumnReview, api.ReviewStageExporting)
	if got := states(); got != [2]api.ExportState{api.ExportExporting, api.ExportExporting} {
		t.Fatalf("exporting: %v", got)
	}
	if err := b.SetProjectExport(ctx, int64(s.Number), "ui", api.ExportFailedState, "gh:\nnot logged in"); err != nil {
		t.Fatal(err)
	}
	if p := get(t, b, s.Number).Projects[1]; p.ExportState != api.ExportFailedState || p.ExportError != "gh: not logged in" {
		t.Fatalf("failed: %+v", p)
	}
	wantCode(t, b.SetProjectExport(ctx, int64(s.Number), "nope", api.ExportLeftOut, ""), api.CodeValidation)
	wantCode(t, b.SetProjectExport(ctx, int64(s.Number), "api", api.ExportExported, ""), api.CodeValidation)
	if err := b.SetProjectExport(ctx, int64(s.Number), "api", api.ExportLeftOut, ""); err != nil {
		t.Fatal(err)
	}
	if err := b.ExportFailed(ctx, int64(s.Number), "Export of ui failed: gh: not logged in"); err != nil {
		t.Fatal(err)
	}
	// Waiting for the user: the failure and the left-out project stay.
	if got := states(); got != [2]api.ExportState{api.ExportLeftOut, api.ExportFailedState} {
		t.Fatalf("after the failure: %v", got)
	}
	// A retry tries the failed project afresh and keeps the left-out one.
	if _, err := b.Approve(ctx, User, int64(s.Number), api.ApproveRequest{Export: true}); err != nil {
		t.Fatal(err)
	}
	got := get(t, b, s.Number).Projects
	if got[0].ExportState != api.ExportLeftOut || got[1].ExportState != api.ExportExporting || got[1].ExportError != "" {
		t.Fatalf("retry: %+v", got)
	}
	if _, err := b.Done(ctx, exportJob(t, b, s.Number, "ui"), api.DoneRequest{URL: "https://example.test/ui/1"}); err != nil {
		t.Fatal(err)
	}
	if err := b.FinishExport(ctx, int64(s.Number)); err != nil {
		t.Fatal(err)
	}
	if got := states(); got != [2]api.ExportState{api.ExportLeftOut, api.ExportExported} {
		t.Fatalf("done: %v", got)
	}
}

func TestProjectExportStateIsClearedWhenTheTicketLeavesTheExport(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}, WhenApproved: api.WhenApprovedExport})
	force(t, b, s.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.SetProjectExport(ctx, int64(s.Number), "api", api.ExportLeftOut, ""); err != nil {
		t.Fatal(err)
	}
	if err := b.SetProjectExport(ctx, int64(s.Number), "ui", api.ExportFailedState, "no remote"); err != nil {
		t.Fatal(err)
	}
	if err := b.ExportFailed(ctx, int64(s.Number), "Export of ui failed: no remote"); err != nil {
		t.Fatal(err)
	}
	got, err := b.Reject(ctx, User, int64(s.Number), api.RejectRequest{Comment: "@pm the ui part is missing"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Projects {
		if p.ExportState != "" || p.ExportError != "" {
			t.Fatalf("after the rejection: %+v", got.Projects)
		}
	}
}
