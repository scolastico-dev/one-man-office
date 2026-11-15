package board

import (
	"context"
	"fmt"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// boardState is a column plus, in Review, a review stage.
type boardState struct {
	col   api.Column
	stage api.ReviewStage
}

func (s boardState) String() string {
	if s.stage == "" {
		return string(s.col)
	}
	return string(s.col) + "/" + string(s.stage)
}

var allStates = []boardState{
	{api.ColumnPlanning, ""}, {api.ColumnReady, ""}, {api.ColumnInProgress, ""},
	{api.ColumnReview, api.ReviewStageAgent}, {api.ColumnReview, api.ReviewStageHuman},
	{api.ColumnReview, api.ReviewStageMerging}, {api.ColumnReview, api.ReviewStageExporting},
	{api.ColumnDone, ""},
}

// TestEngineMatrix calls every engine transition on a standalone task in
// every state; each succeeds only from its one source state.
func TestEngineMatrix(t *testing.T) {
	methods := []struct {
		name string
		from boardState
		to   boardState
		call func(b *Board, n int64) error
	}{
		{"begin_work", boardState{api.ColumnReady, ""}, boardState{api.ColumnInProgress, ""},
			func(b *Board, n int64) error { return b.BeginWork(context.Background(), n) }},
		{"complete_merge", boardState{api.ColumnReview, api.ReviewStageMerging}, boardState{api.ColumnDone, ""},
			func(b *Board, n int64) error { return b.CompleteMerge(context.Background(), n) }},
		{"merge_conflict", boardState{api.ColumnReview, api.ReviewStageMerging}, boardState{api.ColumnInProgress, ""},
			func(b *Board, n int64) error { return b.MergeConflict(context.Background(), n, "conflict in a.go") }},
		{"complete_export", boardState{api.ColumnReview, api.ReviewStageExporting}, boardState{api.ColumnDone, ""},
			func(b *Board, n int64) error {
				return b.CompleteExport(context.Background(), n, "https://example.test/pr/1")
			}},
		{"export_failed", boardState{api.ColumnReview, api.ReviewStageExporting}, boardState{api.ColumnReview, api.ReviewStageHuman},
			func(b *Board, n int64) error { return b.ExportFailed(context.Background(), n, "gh: not logged in") }},
	}
	for _, m := range methods {
		for _, from := range allStates {
			t.Run(fmt.Sprintf("%s/%s", m.name, from), func(t *testing.T) {
				b, _ := newTestBoard(t)
				tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
				force(t, b, tk.Number, from.col, from.stage)
				err := m.call(b, int64(tk.Number))
				got := get(t, b, tk.Number)
				if from != m.from {
					wantCode(t, err, api.CodeInvalidTransition)
					if got.Column != from.col || got.ReviewStage != from.stage {
						t.Fatalf("failed call changed the state to %s/%s", got.Column, got.ReviewStage)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if got.Column != m.to.col || got.ReviewStage != m.to.stage {
					t.Fatalf("state %s/%q, want %s", got.Column, got.ReviewStage, m.to)
				}
			})
		}
	}
}

func TestEngineMergeAndConflict(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageMerging)
	if err := b.CompleteMerge(ctx, int64(tk.Number)); err != nil {
		t.Fatal(err)
	}
	got := get(t, b, tk.Number)
	if got.Column != api.ColumnDone || got.Resolution != api.ResolutionCompleted || got.ClosedAt == nil || got.ReviewStage != "" {
		t.Fatalf("got %s %s %v %q", got.Column, got.Resolution, got.ClosedAt, got.ReviewStage)
	}

	other := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "O", Projects: []string{"api"}})
	force(t, b, other.Number, api.ColumnReview, api.ReviewStageMerging)
	dev := agent(t, b, api.RoleDeveloper, int64(other.Number))
	if err := b.MergeConflict(ctx, int64(other.Number), " "); errCode(err) != api.CodeValidation {
		t.Fatalf("empty comment: %v", err)
	}
	last := lastEvent(t, b)
	if err := b.MergeConflict(ctx, int64(other.Number), "conflict in main.go"); err != nil {
		t.Fatal(err)
	}
	got = get(t, b, other.Number)
	if got.Column != api.ColumnInProgress || got.ReviewStage != "" || got.ClosedAt != nil {
		t.Fatalf("got %s %q", got.Column, got.ReviewStage)
	}
	c, _ := lastComment(t, b, other.Number)
	if c.Author.Role != api.RoleSystem || c.Body != "conflict in main.go" ||
		len(c.Mentions) != 1 || c.Mentions[0] != (api.Mention{Role: api.RoleDeveloper, Session: dev.Session}) {
		t.Fatalf("comment = %+v", c)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 1 || moved[0].Action != ActionConflict {
		t.Fatalf("moved = %+v", moved)
	}
}

func TestExportFailedFlagsForUser(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.ExportFailed(ctx, int64(tk.Number), "gh: not logged in"); err != nil {
		t.Fatal(err)
	}
	got := get(t, b, tk.Number)
	if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageHuman || got.Attention != "gh: not logged in" {
		t.Fatalf("got %s %q %q", got.Column, got.ReviewStage, got.Attention)
	}
	c, _ := lastComment(t, b, tk.Number)
	if c.Body != "Export failed: gh: not logged in" || len(c.Mentions) != 1 || c.Mentions[0].Role != api.RoleUser {
		t.Fatalf("comment = %+v", c)
	}
	// Approving again clears the flag.
	got, err := b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{})
	if err != nil || got.ReviewStage != api.ReviewStageExporting || got.Attention != "" {
		t.Fatalf("after approve: %q %q, %v", got.ReviewStage, got.Attention, err)
	}
	if err := b.CompleteExport(ctx, int64(tk.Number), "https://example.test/pr/7"); err != nil {
		t.Fatal(err)
	}
	got = get(t, b, tk.Number)
	if got.Resolution != api.ResolutionExported || got.ExportURL != "https://example.test/pr/7" || got.ExportMerged {
		t.Fatalf("got %s %q %v", got.Resolution, got.ExportURL, got.ExportMerged)
	}
}

func TestBeginWorkRules(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	first := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}, Story: intPtr(s.Number)})
	second := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "B", Projects: []string{"api"}, Story: intPtr(s.Number), DependsOn: []int{first.Number}})
	force(t, b, s.Number, api.ColumnReady, "")
	force(t, b, first.Number, api.ColumnReady, "")
	force(t, b, second.Number, api.ColumnReady, "")

	wantCode(t, b.BeginWork(ctx, int64(first.Number)), api.CodeInvalidTransition) // story not started
	if err := b.BeginWork(ctx, int64(s.Number)); err != nil {
		t.Fatal(err)
	}
	wantCode(t, b.BeginWork(ctx, int64(second.Number)), api.CodeInvalidTransition) // waits for first
	if err := b.BeginWork(ctx, int64(first.Number)); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, first.Number); got.Column != api.ColumnInProgress {
		t.Fatalf("got %s", got.Column)
	}
	wantCode(t, b.BeginWork(ctx, 999), api.CodeNotFound)
}

func TestSetBranch(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	wantCode(t, b.SetBranch(ctx, int64(tk.Number), "ui", "omo/1-t", "/wt/1"), api.CodeValidation)
	last := lastEvent(t, b)
	if err := b.SetBranch(ctx, int64(tk.Number), "api", "omo/1-t", "/wt/1"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetBranch(ctx, int64(tk.Number), "api", "omo/1-t", "/wt/1"); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, b, last); len(evs) != 1 || evs[0].Kind != api.KindTicketUpdated {
		t.Fatalf("events = %+v", evs)
	}
	got := get(t, b, tk.Number)
	if got.Projects[0].Branch != "omo/1-t" || got.Projects[0].Worktree != "/wt/1" {
		t.Fatalf("projects = %+v", got.Projects)
	}
	_, err := b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Projects: api.Some([]string{"ui"})})
	wantCode(t, err, api.CodeFieldLocked)
	if err := b.SetBranch(ctx, int64(tk.Number), "api", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Projects: api.Some([]string{"ui"})}); err != nil {
		t.Fatalf("after removing the branch: %v", err)
	}
}

func TestSetAttention(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	ceo := agent(t, b, api.RoleCEO, 0)
	tk := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	force(t, b, tk.Number, api.ColumnInProgress, "")
	if err := b.SetAttention(ctx, int64(tk.Number), "no output for 30m"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, tk.Number); got.Attention != "no output for 30m" {
		t.Fatalf("attention %q", got.Attention)
	}
	c, _ := lastComment(t, b, tk.Number)
	if c.Body != "Needs attention: no output for 30m" || len(c.Mentions) != 1 ||
		c.Mentions[0] != (api.Mention{Role: api.RoleCEO, Session: ceo.Session}) {
		t.Fatalf("comment = %+v", c)
	}
	last := lastEvent(t, b)
	if err := b.SetAttention(ctx, int64(tk.Number), "no output for 30m"); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, b, last); len(evs) != 0 {
		t.Fatalf("unchanged reason emitted %+v", evs)
	}
	if err := b.SetAttention(ctx, int64(tk.Number), ""); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, tk.Number); got.Attention != "" {
		t.Fatalf("attention %q", got.Attention)
	}
	force(t, b, tk.Number, api.ColumnDone, "")
	wantCode(t, b.SetAttention(ctx, int64(tk.Number), "late"), api.CodeInvalidTransition)
}
