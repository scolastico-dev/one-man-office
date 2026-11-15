package board

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestTasksJoinOnlyOpenStories(t *testing.T) {
	type state struct {
		name  string
		col   api.Column
		stage api.ReviewStage
		res   api.Resolution
	}
	states := []state{
		{"planning", api.ColumnPlanning, "", ""},
		{"ready", api.ColumnReady, "", ""},
		{"in_progress", api.ColumnInProgress, "", ""},
		{"review agent", api.ColumnReview, api.ReviewStageAgent, ""},
		{"review human", api.ColumnReview, api.ReviewStageHuman, ""},
		{"review merging", api.ColumnReview, api.ReviewStageMerging, ""},
		{"done completed", api.ColumnDone, "", api.ResolutionCompleted},
		{"done cancelled", api.ColumnDone, "", api.ResolutionCancelled},
	}
	for _, st := range states {
		t.Run(st.name, func(t *testing.T) {
			b, _ := newTestBoard(t)
			ctx := context.Background()
			story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
			alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
			if st.res != "" {
				forceDone(t, b, story.Number, st.res)
			} else {
				force(t, b, story.Number, st.col, st.stage)
			}
			open := st.col == api.ColumnPlanning || st.col == api.ColumnReady || st.col == api.ColumnInProgress
			_, errCreate := b.Create(ctx, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story.Number)})
			_, errMove := b.Update(ctx, User, int64(alone.Number), api.UpdateTicket{Story: api.Some(story.Number)})
			if open {
				if errCreate != nil || errMove != nil {
					t.Fatalf("create %v, move %v", errCreate, errMove)
				}
				return
			}
			wantCode(t, errCreate, api.CodeInvalidTransition)
			wantCode(t, errMove, api.CodeInvalidTransition)
		})
	}
}

func TestEmptyBaseBranchRefusedOutsidePlanning(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	inStory := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story.Number)})
	planned := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "P", Projects: []string{"api"}})
	for _, n := range []int{alone.Number, story.Number, inStory.Number} {
		force(t, b, n, api.ColumnReady, "")
	}
	env.setBranch("ui", "")
	env.setBranch("api", "")

	_, err := b.Update(ctx, User, int64(alone.Number), api.UpdateTicket{Projects: api.Some([]string{"ui"})})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Update(ctx, User, int64(story.Number), api.UpdateTicket{Projects: api.Some([]string{"api", "ui"})})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Update(ctx, User, int64(inStory.Number), api.UpdateTicket{Story: api.Null[int]()})
	wantCode(t, err, api.CodeValidation)

	// A base branch given in the same request is accepted.
	got, err := b.Update(ctx, User, int64(alone.Number), api.UpdateTicket{
		Projects: api.Some([]string{"ui"}), BaseBranches: api.Some(map[string]string{"ui": "dev"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Projects[0].BaseBranch != "dev" {
		t.Fatalf("projects = %+v", got.Projects)
	}
	if _, err := b.Update(ctx, User, int64(inStory.Number), api.UpdateTicket{
		Story: api.Null[int](), BaseBranches: api.Some(map[string]string{"api": "dev"}),
	}); err != nil {
		t.Fatal(err)
	}
	// In Planning an empty base is stored; approval asks for it later.
	got, err = b.Update(ctx, User, int64(planned.Number), api.UpdateTicket{Projects: api.Some([]string{"ui"})})
	if err != nil {
		t.Fatal(err)
	}
	if got.Projects[0].BaseBranch != "" {
		t.Fatalf("projects = %+v", got.Projects)
	}
}

// systemComments returns the system comments of ticket n.
func systemComments(t *testing.T, b *Board, n int) []api.Comment {
	t.Helper()
	cs, err := b.Comments(context.Background(), User, int64(n))
	if err != nil {
		t.Fatal(err)
	}
	var out []api.Comment
	for _, c := range cs {
		if c.Author.Role == api.RoleSystem {
			out = append(out, c)
		}
	}
	return out
}

func TestUserChangesToStoryTasksMentionPM(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	pm := agent(t, b, api.RolePM, int64(story.Number))
	pmMention := []api.Mention{{Role: api.RolePM, Session: pm.Session}}

	// Create.
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story.Number)})
	n := tk.Number
	cs := systemComments(t, b, n)
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "added") || !slices.Equal(cs[0].Mentions, pmMention) {
		t.Fatalf("after create: %+v", cs)
	}

	// Edit: one comment for several fields.
	if _, err := b.Update(ctx, User, int64(n), api.UpdateTicket{Title: api.Some("T2"), Model: api.Some("claude/fable")}); err != nil {
		t.Fatal(err)
	}
	cs = systemComments(t, b, n)
	if len(cs) != 2 || cs[1].Body != "Task #"+itoa(n)+" edited by the user: title, model." || !slices.Equal(cs[1].Mentions, pmMention) {
		t.Fatalf("after edit: %+v", cs)
	}
	// A request that changes nothing posts nothing.
	if _, err := b.Update(ctx, User, int64(n), api.UpdateTicket{Title: api.Some("T2")}); err != nil {
		t.Fatal(err)
	}
	if cs = systemComments(t, b, n); len(cs) != 2 {
		t.Fatalf("after no-op edit: %+v", cs)
	}

	// Description: one comment that mentions the working developer and the pm.
	dev := agent(t, b, api.RoleDeveloper, int64(n))
	if _, err := b.SetDescription(ctx, User, int64(n), api.DescriptionUpdate{Text: "new"}); err != nil {
		t.Fatal(err)
	}
	cs = systemComments(t, b, n)
	want := []api.Mention{{Role: api.RoleDeveloper, Session: dev.Session}, pmMention[0]}
	if len(cs) != 3 || !slices.Equal(cs[2].Mentions, want) {
		t.Fatalf("after description edit: %+v", cs)
	}
	if err := b.EndSession(ctx, dev.Session, "test"); err != nil {
		t.Fatal(err)
	}

	// Release.
	if _, err := b.Approve(ctx, User, int64(n), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	cs = systemComments(t, b, n)
	if len(cs) != 4 || !strings.Contains(cs[3].Body, "released") || !slices.Equal(cs[3].Mentions, pmMention) {
		t.Fatalf("after release: %+v", cs)
	}

	// The pm's own actions do not mention itself.
	own := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "P", Projects: []string{"api"}, Story: intPtr(story.Number)})
	if _, err := b.Update(ctx, pm, int64(own.Number), api.UpdateTicket{Title: api.Some("P2")}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetDescription(ctx, pm, int64(own.Number), api.DescriptionUpdate{Text: "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Approve(ctx, pm, int64(own.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	if cs := systemComments(t, b, own.Number); len(cs) != 0 {
		t.Fatalf("pm's own actions posted %+v", cs)
	}

	// Standalone tasks have no pm to tell.
	ceo := agent(t, b, api.RoleCEO, 0)
	alone := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
	if _, err := b.Update(ctx, User, int64(alone.Number), api.UpdateTicket{Title: api.Some("A2")}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Approve(ctx, ceo, int64(alone.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	if cs := systemComments(t, b, alone.Number); len(cs) != 0 {
		t.Fatalf("standalone task got %+v", cs)
	}
}

func TestStoryHandInStages(t *testing.T) {
	cases := []struct {
		review api.Review
		when   api.WhenApproved
		want   api.ReviewStage
	}{
		{api.ReviewSkipped, api.WhenApprovedMerge, api.ReviewStageMerging},
		{api.ReviewSkipped, api.WhenApprovedExport, api.ReviewStageExporting},
		{api.ReviewHuman, api.WhenApprovedMerge, api.ReviewStageHuman},
		{api.ReviewHuman, api.WhenApprovedExport, api.ReviewStageHuman},
		{api.ReviewAgentThenHuman, api.WhenApprovedMerge, api.ReviewStageAgent},
		{api.ReviewAgentThenHuman, api.WhenApprovedExport, api.ReviewStageAgent},
	}
	for _, c := range cases {
		t.Run(string(c.review)+"/"+string(c.when), func(t *testing.T) {
			b, _ := newTestBoard(t)
			story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"},
				Review: c.review, WhenApproved: c.when})
			tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story.Number)})
			forceDone(t, b, tk.Number, api.ResolutionCompleted)
			force(t, b, story.Number, api.ColumnInProgress, "")
			pm := agent(t, b, api.RolePM, int64(story.Number))
			got, err := b.Done(context.Background(), pm, api.DoneRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if got.Column != api.ColumnReview || got.ReviewStage != c.want {
				t.Fatalf("column %s stage %q, want review %q", got.Column, got.ReviewStage, c.want)
			}
		})
	}
}

func TestTaskInStoryNeverExports(t *testing.T) {
	for _, review := range []api.Review{api.ReviewSkipped, api.ReviewHuman} {
		t.Run(string(review), func(t *testing.T) {
			b, env := newTestBoard(t)
			ctx := context.Background()
			env.setPolicy(func(p *Policy) { p.WhenApprovedTask = api.WhenApprovedExport })
			story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
			tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"},
				Story: intPtr(story.Number), Review: review})
			force(t, b, story.Number, api.ColumnInProgress, "")
			force(t, b, tk.Number, api.ColumnInProgress, "")
			dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
			got, err := b.Done(ctx, dev, api.DoneRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if review == api.ReviewHuman {
				if got.ReviewStage != api.ReviewStageHuman {
					t.Fatalf("stage after hand-in %q", got.ReviewStage)
				}
				if got, err = b.Approve(ctx, User, int64(tk.Number), api.ApproveRequest{}); err != nil {
					t.Fatal(err)
				}
			}
			if got.ReviewStage != api.ReviewStageMerging {
				t.Fatalf("stage %q, want merging", got.ReviewStage)
			}
		})
	}
}

func TestCompleteExportRefusesTaskInStory(t *testing.T) {
	b, _ := newTestBoard(t)
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story.Number)})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	err := b.CompleteExport(context.Background(), int64(tk.Number), "https://example.invalid/pr/1")
	wantCode(t, err, api.CodeInvalidTransition)
	if got := get(t, b, tk.Number); got.Column != api.ColumnReview {
		t.Fatalf("column %s", got.Column)
	}
}

func TestAgentsWithdrawWhatTheyReleased(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	env.setPolicy(func(p *Policy) { p.CEOMayApproveStories = true })
	ceo := agent(t, b, api.RoleCEO, 0)
	story := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	if _, err := b.Approve(ctx, ceo, int64(story.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	got, err := b.Move(ctx, ceo, int64(story.Number), api.MoveRequest{Column: api.ColumnPlanning})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnPlanning {
		t.Fatalf("story column %s", got.Column)
	}

	pm := agent(t, b, api.RolePM, int64(story.Number))
	tk := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story.Number)})
	if _, err := b.Approve(ctx, pm, int64(tk.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	got, err = b.Move(ctx, pm, int64(tk.Number), api.MoveRequest{Column: api.ColumnPlanning})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnPlanning {
		t.Fatalf("task column %s", got.Column)
	}
}

func TestStoryLockedWhileIntegrationBranchExists(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	n := int64(story.Number)
	setBranch(t, b, story.Number, "api", "omo/story-1")
	_, err := b.Update(ctx, User, n, api.UpdateTicket{Projects: api.Some([]string{"api", "ui"})})
	wantCode(t, err, api.CodeFieldLocked)
	_, err = b.Update(ctx, User, n, api.UpdateTicket{BaseBranches: api.Some(map[string]string{"api": "dev"})})
	wantCode(t, err, api.CodeFieldLocked)
	if _, err := b.Update(ctx, User, n, api.UpdateTicket{Title: api.Some("still editable")}); err != nil {
		t.Fatal(err)
	}
}
