package board

import (
	"context"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// reviewer starts a reviewer session for one project of ticket n in the
// ticket's current review round.
func reviewer(t *testing.T, b *Board, n int, project string) Actor {
	t.Helper()
	ctx := context.Background()
	s, err := b.CreateSession(ctx, NewSession{Role: api.RoleReviewer, Ticket: int64(n), Project: project, Name: "rev-" + project,
		Model: "claude/fable", ReviewRound: get(t, b, n).ReviewRound})
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.ResolveSession(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestHandInSetsReviewStage(t *testing.T) {
	cases := []struct {
		review api.Review
		when   api.WhenApproved
		want   api.ReviewStage
	}{
		{api.ReviewSkipped, "", api.ReviewStageMerging},
		{api.ReviewSkipped, api.WhenApprovedExport, api.ReviewStageExporting},
		{api.ReviewAgent, "", api.ReviewStageAgent},
		{api.ReviewHuman, "", api.ReviewStageHuman},
		{api.ReviewAgentThenHuman, "", api.ReviewStageAgent},
	}
	for _, c := range cases {
		t.Run(string(c.review)+"/"+string(c.when), func(t *testing.T) {
			b, _ := newTestBoard(t)
			tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Review: c.review, WhenApproved: c.when})
			force(t, b, tk.Number, api.ColumnInProgress, "")
			dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
			last := lastEvent(t, b)
			got, err := b.Done(context.Background(), dev, api.DoneRequest{Comment: "finished"})
			if err != nil {
				t.Fatal(err)
			}
			if got.Column != api.ColumnReview || got.ReviewStage != c.want || got.ReviewRound != 1 {
				t.Fatalf("got %s %q round %d", got.Column, got.ReviewStage, got.ReviewRound)
			}
			moved := movedEvents(t, eventsAfter(t, b, last))
			if len(moved) != 1 || moved[0].Action != ActionHandIn || moved[0].From != api.ColumnInProgress {
				t.Fatalf("moved = %+v", moved)
			}
			if c, ok := lastComment(t, b, tk.Number); !ok || c.Author.Role != api.RoleDeveloper || c.Body != "finished" {
				t.Fatalf("comment = %+v", c)
			}
		})
	}
}

func TestHandInRules(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
	_, err := b.Done(ctx, dev, api.DoneRequest{})
	wantCode(t, err, api.CodeInvalidTransition)
	force(t, b, tk.Number, api.ColumnInProgress, "")
	_, err = b.Done(ctx, dev, api.DoneRequest{Verdict: VerdictApprove})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Done(ctx, User, api.DoneRequest{})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Done(ctx, System, api.DoneRequest{})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Done(ctx, agent(t, b, api.RoleCEO, 0), api.DoneRequest{})
	wantCode(t, err, api.CodeForbidden)
	if _, err := b.Done(ctx, dev, api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	// Handed in once; a second done finds the ticket in review.
	_, err = b.Done(ctx, dev, api.DoneRequest{})
	wantCode(t, err, api.CodeInvalidTransition)
}

func TestStoryHandInNeedsAllTasksDone(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	force(t, b, s.Number, api.ColumnInProgress, "")
	pm := agent(t, b, api.RolePM, int64(s.Number))

	_, err := b.Done(ctx, pm, api.DoneRequest{})
	wantCode(t, err, api.CodeInvalidTransition) // no tasks at all

	first := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}, Story: intPtr(s.Number)})
	second := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "B", Projects: []string{"api"}, Story: intPtr(s.Number)})
	force(t, b, first.Number, api.ColumnReview, api.ReviewStageAgent)
	_, err = b.Done(ctx, pm, api.DoneRequest{})
	wantCode(t, err, api.CodeInvalidTransition)

	if _, err := b.Cancel(ctx, pm, int64(first.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Cancel(ctx, pm, int64(second.Number), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err = b.Done(ctx, pm, api.DoneRequest{})
	wantCode(t, err, api.CodeInvalidTransition) // all cancelled

	third := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "C", Projects: []string{"api"}, Story: intPtr(s.Number)})
	force(t, b, third.Number, api.ColumnDone, "")
	got, err := b.Done(ctx, pm, api.DoneRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAgent || got.ReviewRound != 1 {
		t.Fatalf("got %s %q %d", got.Column, got.ReviewStage, got.ReviewRound)
	}

	// A developer cannot hand in a story; a pm cannot hand in a task.
	_, err = b.Done(ctx, Actor{Role: api.RoleDeveloper, Ticket: int64(s.Number)}, api.DoneRequest{})
	wantCode(t, err, api.CodeForbidden)
}

func TestMultiProjectStoryNeedsEveryProjectApproved(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}})
	force(t, b, s.Number, api.ColumnReview, api.ReviewStageAgent)
	revAPI, revUI := reviewer(t, b, s.Number, "api"), reviewer(t, b, s.Number, "ui")

	last := lastEvent(t, b)
	got, err := b.Done(ctx, revAPI, api.DoneRequest{Verdict: VerdictApprove})
	if err != nil {
		t.Fatal(err)
	}
	if got.ReviewStage != api.ReviewStageAgent {
		t.Fatalf("after one approval: %q", got.ReviewStage)
	}
	evs := eventsAfter(t, b, last)
	if len(evs) != 1 || evs[0].Kind != api.KindReviewVerdict {
		t.Fatalf("events = %+v", evs)
	}
	_, err = b.Done(ctx, revAPI, api.DoneRequest{Verdict: VerdictApprove})
	wantCode(t, err, api.CodeInvalidTransition)

	got, err = b.Done(ctx, revUI, api.DoneRequest{Verdict: VerdictApprove, Comment: "looks good"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageMerging {
		t.Fatalf("after both: %s %q", got.Column, got.ReviewStage)
	}
	var verdicts int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM review_verdicts WHERE ticket_id = ? AND round = 1 AND verdict = 'approve'`, s.Number).Scan(&verdicts); err != nil || verdicts != 2 {
		t.Fatalf("verdicts = %d, %v", verdicts, err)
	}

	// A reviewer without a project of a multi-project ticket cannot decide.
	other := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "O", Projects: []string{"api", "ui"}})
	force(t, b, other.Number, api.ColumnReview, api.ReviewStageAgent)
	_, err = b.Done(ctx, Actor{Role: api.RoleReviewer, Ticket: int64(other.Number), ReviewRound: 1}, api.DoneRequest{Verdict: VerdictApprove})
	wantCode(t, err, api.CodeValidation)
}

func TestAgentThenHumanGoesToUserAfterApproval(t *testing.T) {
	b, _ := newTestBoard(t)
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Review: api.ReviewAgentThenHuman})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageAgent)
	got, err := b.Done(context.Background(), reviewer(t, b, tk.Number, "api"), api.DoneRequest{Verdict: VerdictApprove})
	if err != nil || got.ReviewStage != api.ReviewStageHuman {
		t.Fatalf("got %q, %v", got.ReviewStage, err)
	}
}

func TestAgentReviewOfExportedTicketExports(t *testing.T) {
	b, _ := newTestBoard(t)
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageAgent)
	got, err := b.Done(context.Background(), reviewer(t, b, tk.Number, ""), api.DoneRequest{Verdict: VerdictApprove})
	if err != nil || got.ReviewStage != api.ReviewStageExporting {
		t.Fatalf("got %q, %v", got.ReviewStage, err)
	}
}

func TestReviewerRejectReturnsToInProgressWithComment(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageAgent)
	dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
	rev := reviewer(t, b, tk.Number, "api")

	_, err := b.Done(ctx, rev, api.DoneRequest{Verdict: VerdictReject})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Done(ctx, rev, api.DoneRequest{Verdict: "maybe"})
	wantCode(t, err, api.CodeValidation)

	got, err := b.Done(ctx, rev, api.DoneRequest{Verdict: VerdictReject, Comment: "tests fail"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnInProgress || got.ReviewStage != "" || got.ReviewRound != 1 {
		t.Fatalf("got %s %q %d", got.Column, got.ReviewStage, got.ReviewRound)
	}
	c, ok := lastComment(t, b, tk.Number)
	if !ok || c.Author.Role != api.RoleReviewer || c.Author.Session != rev.Session || c.Body != "tests fail" {
		t.Fatalf("comment = %+v", c)
	}
	if len(c.Mentions) != 1 || c.Mentions[0] != (api.Mention{Role: api.RoleDeveloper, Session: dev.Session}) {
		t.Fatalf("mentions = %+v", c.Mentions)
	}
	var commentID int
	if err := b.db.QueryRow(`SELECT comment_id FROM review_verdicts WHERE ticket_id = ?`, tk.Number).Scan(&commentID); err != nil || commentID != c.ID {
		t.Fatalf("verdict comment %d, %v", commentID, err)
	}

	// The next hand-in starts round 2, where the reviewer decides again.
	if _, err := b.Done(ctx, dev, api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	got, err = b.Done(ctx, reviewer(t, b, tk.Number, "api"), api.DoneRequest{Verdict: VerdictApprove})
	if err != nil || got.ReviewRound != 2 || got.ReviewStage != api.ReviewStageMerging {
		t.Fatalf("round 2: %d %q, %v", got.ReviewRound, got.ReviewStage, err)
	}
}

func TestStoryRejectMentionsPM(t *testing.T) {
	b, _ := newTestBoard(t)
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	force(t, b, s.Number, api.ColumnReview, api.ReviewStageHuman)
	pm := agent(t, b, api.RolePM, int64(s.Number))
	if _, err := b.Reject(context.Background(), User, int64(s.Number), api.RejectRequest{Comment: "e2e fails"}); err != nil {
		t.Fatal(err)
	}
	c, _ := lastComment(t, b, s.Number)
	if c.Author.Role != api.RoleUser || len(c.Mentions) != 1 || c.Mentions[0] != (api.Mention{Role: api.RolePM, Session: pm.Session}) {
		t.Fatalf("comment = %+v", c)
	}
}

func TestUserRejectRules(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageHuman)
	_, err := b.Reject(ctx, User, int64(tk.Number), api.RejectRequest{Comment: "  "})
	wantCode(t, err, api.CodeValidation)
	_, err = b.Reject(ctx, agent(t, b, api.RoleReviewer, int64(tk.Number)), int64(tk.Number), api.RejectRequest{Comment: "no"})
	wantCode(t, err, api.CodeForbidden)
	for _, stage := range []api.ReviewStage{api.ReviewStageAgent, api.ReviewStageMerging, api.ReviewStageExporting} {
		force(t, b, tk.Number, api.ColumnReview, stage)
		_, err = b.Reject(ctx, User, int64(tk.Number), api.RejectRequest{Comment: "no"})
		wantCode(t, err, api.CodeInvalidTransition)
	}
}

func TestUserRejectsFailedMerge(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	n := int64(tk.Number)
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageMerging)
	// Merging without a failed step: the merge is the engine's.
	_, err := b.Reject(ctx, User, n, api.RejectRequest{Comment: "no"})
	wantCode(t, err, api.CodeInvalidTransition)
	// Another failed step is not the merge's either.
	if err := b.SetFailed(ctx, n, api.FailedStart, "start failed"); err != nil {
		t.Fatal(err)
	}
	_, err = b.Reject(ctx, User, n, api.RejectRequest{Comment: "no"})
	wantCode(t, err, api.CodeInvalidTransition)
	if _, err := b.Retry(ctx, User, n); err != nil {
		t.Fatal(err)
	}
	if err := b.SetFailed(ctx, n, api.FailedMerge, "the branch moved"); err != nil {
		t.Fatal(err)
	}
	_, err = b.Reject(ctx, agent(t, b, api.RoleReviewer, n), n, api.RejectRequest{Comment: "no"})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.Reject(ctx, User, n, api.RejectRequest{Comment: " "})
	wantCode(t, err, api.CodeValidation)
	last := lastEvent(t, b)
	got, err := b.Reject(ctx, User, n, api.RejectRequest{Comment: "Review the new commits too."})
	if err != nil {
		t.Fatal(err)
	}
	if got.Column != api.ColumnInProgress || got.FailedStep != api.FailedNone || got.ReviewStage != api.ReviewStageNone {
		t.Fatalf("got %s stage %q failed %q", got.Column, got.ReviewStage, got.FailedStep)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 1 || moved[0].Action != ActionReject || moved[0].From != api.ColumnReview || moved[0].To != api.ColumnInProgress {
		t.Fatalf("moved = %+v", moved)
	}
}

func TestUserApproveOnlyInHumanStage(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	inStory := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})
	alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})

	force(t, b, alone.Number, api.ColumnReview, api.ReviewStageAgent)
	_, err := b.Approve(ctx, User, int64(alone.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeInvalidTransition)
	_, err = b.Move(ctx, User, int64(alone.Number), api.MoveRequest{Column: api.ColumnDone})
	wantCode(t, err, api.CodeInvalidTransition)

	force(t, b, inStory.Number, api.ColumnReview, api.ReviewStageHuman)
	_, err = b.Approve(ctx, User, int64(inStory.Number), api.ApproveRequest{Export: true})
	wantCode(t, err, api.CodeValidation)
	got, err := b.Approve(ctx, User, int64(inStory.Number), api.ApproveRequest{})
	if err != nil || got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageMerging {
		t.Fatalf("got %s %q, %v", got.Column, got.ReviewStage, err)
	}
	_, err = b.Approve(ctx, User, int64(inStory.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeInvalidTransition) // already approved

	force(t, b, alone.Number, api.ColumnReview, api.ReviewStageHuman)
	_, err = b.Approve(ctx, agent(t, b, api.RoleReviewer, int64(alone.Number)), int64(alone.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeForbidden)
	last := lastEvent(t, b)
	got, err = b.Approve(ctx, User, int64(alone.Number), api.ApproveRequest{Export: true})
	if err != nil || got.ReviewStage != api.ReviewStageExporting {
		t.Fatalf("got %q, %v", got.ReviewStage, err)
	}
	moved := movedEvents(t, eventsAfter(t, b, last))
	if len(moved) != 1 || moved[0].Action != ActionApprove || moved[0].From != api.ColumnReview || moved[0].To != api.ColumnReview {
		t.Fatalf("moved = %+v", moved)
	}
}

func TestMarkMerged(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	exported := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "E", Projects: []string{"api"}})
	waiting := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "W", Projects: []string{"api"}, DependsOn: []int{exported.Number}})
	_, err := b.MarkMerged(ctx, User, int64(exported.Number))
	wantCode(t, err, api.CodeInvalidTransition)

	force(t, b, exported.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.CompleteExport(ctx, int64(exported.Number), "https://example.test/pr/1"); err != nil {
		t.Fatal(err)
	}
	if w := get(t, b, waiting.Number).WaitingFor; len(w) != 1 || w[0].Reason != api.WaitingExportNotMerged {
		t.Fatalf("waiting = %v", w)
	}
	_, err = b.MarkMerged(ctx, agent(t, b, api.RoleCEO, 0), int64(exported.Number))
	wantCode(t, err, api.CodeForbidden)

	last := lastEvent(t, b)
	got, err := b.MarkMerged(ctx, User, int64(exported.Number))
	if err != nil || !got.ExportMerged {
		t.Fatalf("got %v, %v", got.ExportMerged, err)
	}
	if d := derivedUpdates(t, eventsAfter(t, b, last)); len(d) != 1 || d[0] != waiting.Number {
		t.Fatalf("derived = %v", d)
	}
	if w := get(t, b, waiting.Number).WaitingFor; len(w) != 0 {
		t.Fatalf("still waiting = %v", w)
	}
	// Marking twice changes nothing.
	last = lastEvent(t, b)
	if _, err := b.MarkMerged(ctx, User, int64(exported.Number)); err != nil {
		t.Fatal(err)
	}
	if evs := eventsAfter(t, b, last); len(evs) != 0 {
		t.Fatalf("events = %+v", evs)
	}
}

func TestVerdictFromOlderRoundIsForbidden(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	force(t, b, tk.Number, api.ColumnInProgress, "")
	dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
	if _, err := b.Done(ctx, dev, api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	old := reviewer(t, b, tk.Number, "api")
	if old.ReviewRound != 1 {
		t.Fatalf("round = %d", old.ReviewRound)
	}
	if _, err := b.Done(ctx, old, api.DoneRequest{Verdict: VerdictReject, Comment: "fix the tests"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Done(ctx, dev, api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	// Round 2: the reviewer of round 1 is still live but cannot decide.
	last := lastEvent(t, b)
	_, err := b.Done(ctx, old, api.DoneRequest{Verdict: VerdictApprove})
	wantCode(t, err, api.CodeForbidden)
	if !strings.Contains(err.Error(), "round 1") || !strings.Contains(err.Error(), "round 2") {
		t.Fatalf("message = %v", err)
	}
	if evs := eventsAfter(t, b, last); len(evs) != 0 {
		t.Fatalf("events = %+v", evs)
	}
	got, err := b.Done(ctx, reviewer(t, b, tk.Number, "api"), api.DoneRequest{Verdict: VerdictApprove})
	if err != nil || got.ReviewRound != 2 || got.ReviewStage != api.ReviewStageMerging {
		t.Fatalf("round 2: %d %q, %v", got.ReviewRound, got.ReviewStage, err)
	}
}

// A review round is a unit: in a story with several projects only the
// reviewers of the current round decide it, and every project needs an
// approval in that round.
func TestLateReviewerCannotDecideNextRound(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}})
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})
	force(t, b, task.Number, api.ColumnDone, "")
	force(t, b, s.Number, api.ColumnInProgress, "")
	pm := agent(t, b, api.RolePM, int64(s.Number))
	if _, err := b.Done(ctx, pm, api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	apiOne, uiOne := reviewer(t, b, s.Number, "api"), reviewer(t, b, s.Number, "ui")
	if _, err := b.Done(ctx, apiOne, api.DoneRequest{Verdict: VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	// One rejection ends the round.
	got, err := b.Done(ctx, uiOne, api.DoneRequest{Verdict: VerdictReject, Comment: "the ui breaks"})
	if err != nil || got.Column != api.ColumnInProgress {
		t.Fatalf("reject: %s, %v", got.Column, err)
	}
	// The round is over: nobody of round 1 decides any more.
	_, err = b.Done(ctx, apiOne, api.DoneRequest{Verdict: VerdictApprove})
	wantCode(t, err, api.CodeInvalidTransition)

	// The next hand-in starts round 2. The reviewers of round 1 are still
	// live (the engine was slow to end them) and are refused.
	if _, err := b.Done(ctx, pm, api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	for _, late := range []Actor{apiOne, uiOne} {
		_, err := b.Done(ctx, late, api.DoneRequest{Verdict: VerdictApprove})
		wantCode(t, err, api.CodeForbidden)
	}
	// Round 1's approval of api does not carry over.
	uiTwo := reviewer(t, b, s.Number, "ui")
	got, err = b.Done(ctx, uiTwo, api.DoneRequest{Verdict: VerdictApprove})
	if err != nil || got.ReviewStage != api.ReviewStageAgent {
		t.Fatalf("after ui: %q, %v", got.ReviewStage, err)
	}
	// A mention of the reviewer reaches the current round's reviewer only.
	apiTwo := reviewer(t, b, s.Number, "api")
	res, err := b.Comment(ctx, User, int64(s.Number), api.CommentRequest{Body: "@reviewer please check the api too"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Comment.Mentions) != 1 || res.Comment.Mentions[0].Session != apiTwo.Session {
		t.Fatalf("mentions = %+v", res.Comment.Mentions)
	}
	got, err = b.Done(ctx, apiTwo, api.DoneRequest{Verdict: VerdictApprove})
	if err != nil || got.ReviewRound != 2 || got.ReviewStage != api.ReviewStageMerging {
		t.Fatalf("after api: %d %q, %v", got.ReviewRound, got.ReviewStage, err)
	}
}

// A task inside a story takes the review default of tasks in a story, a task
// without a story that of tasks; a story's own setting wins over both.
func TestStoryTaskReviewDefault(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	env.setPolicy(func(p *Policy) {
		p.ReviewStory, p.ReviewTask, p.ReviewStoryTask = api.ReviewAgentThenHuman, api.ReviewHuman, api.ReviewSkipped
	})
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	inStory := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "In", Projects: []string{"api"}, Story: intPtr(story.Number)})
	own := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "Own", Projects: []string{"api"}, Story: intPtr(story.Number), Review: api.ReviewHuman})
	alone := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "Alone", Projects: []string{"api"}})
	for _, c := range []struct {
		n    int
		want api.Review
	}{{story.Number, api.ReviewAgentThenHuman}, {inStory.Number, api.ReviewSkipped}, {own.Number, api.ReviewHuman}, {alone.Number, api.ReviewHuman}} {
		if got := get(t, b, c.n).EffectiveReview; got != c.want {
			t.Errorf("#%d effective review %q, want %q", c.n, got, c.want)
		}
	}
	snap, err := b.EngineSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range snap.Tickets {
		if tk.ID == int64(inStory.Number) && tk.Review != api.ReviewSkipped {
			t.Errorf("engine snapshot review %q", tk.Review)
		}
	}
	// The hand-in goes by it: skipped review merges at once.
	force(t, b, story.Number, api.ColumnInProgress, "")
	force(t, b, inStory.Number, api.ColumnInProgress, "")
	got, err := b.Done(ctx, agent(t, b, api.RoleDeveloper, int64(inStory.Number)), api.DoneRequest{})
	if err != nil || got.ReviewStage != api.ReviewStageMerging {
		t.Fatalf("hand-in: %q, %v", got.ReviewStage, err)
	}
}
