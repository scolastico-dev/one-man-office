package board

import (
	"context"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// A successful push and PR leave the ticket open until the merge is confirmed.
func TestExportWaitsForMergeBeforeDoneEffects(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "export", Projects: []string{"api"}})
	dependent := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "waiting", Projects: []string{"api"}, DependsOn: []int{tk.Number}})
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.CompleteExport(ctx, int64(tk.Number), "https://example.test/pr/1"); err != nil {
		t.Fatal(err)
	}
	got := get(t, b, tk.Number)
	if got.Column != api.ColumnReview || got.ReviewStage != "awaiting_merge" || got.Resolution != "" || got.ClosedAt != nil || got.ExportMerged {
		t.Fatalf("export prematurely closed: %+v", got.TicketSummary)
	}
	if len(get(t, b, dependent.Number).WaitingFor) != 1 {
		t.Fatal("export satisfied dependency before merge")
	}
	if _, err := b.MarkMerged(ctx, User, int64(tk.Number)); err != nil {
		t.Fatal(err)
	}
	got = get(t, b, tk.Number)
	if got.Column != api.ColumnDone || got.Resolution != api.ResolutionExported || !got.ExportMerged || got.ClosedAt == nil {
		t.Fatalf("merge did not close: %+v", got.TicketSummary)
	}
	if len(get(t, b, dependent.Number).WaitingFor) != 0 {
		t.Fatal("merged export still blocks dependency")
	}
}

func TestExportWatchPersistsAndRejectsStaleMerge(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "watch", Projects: []string{"api", "ui"}})
	n := int64(tk.Number)
	setBranch(t, b, tk.Number, "api", "feature")
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.RecordCommits(ctx, n, "api", commitA, commitB); err != nil {
		t.Fatal(err)
	}
	remote := "https://user:password@example.test/org/repo?token=secret"
	last := lastEvent(t, b)
	if err := b.RecordExportWatch(ctx, n, "api", remote, "feature", "main", commitB, commitC); err != nil {
		t.Fatal(err)
	}
	p := get(t, b, tk.Number).Projects[0]
	if p.ExportRemote != "https://user:***@example.test/org/repo?token=***" || p.ExportBranch != "feature" || p.ExportBase != "main" || p.ExportCommit != commitB || p.ExportTargetCommit != commitC {
		t.Fatalf("watch = %+v", p)
	}
	if f := updatedFields(t, eventsAfter(t, b, last)); len(f) != 1 || f[0] != "#1 projects" {
		t.Fatalf("watch events = %v", f)
	}
	last = lastEvent(t, b)
	if err := b.RecordExportWatch(ctx, n, "api", remote, "feature", "main", commitB, commitC); err != nil {
		t.Fatal(err)
	}
	if len(eventsAfter(t, b, last)) != 0 {
		t.Fatal("idempotent watch update emitted events")
	}
	wantCode(t, b.CompleteExportMerge(ctx, n, 1, map[string]string{"api": commitB}), api.CodeInvalidTransition)
	if _, err := b.Done(ctx, exportJob(t, b, tk.Number, "api"), api.DoneRequest{URL: "https://example.test/pr/1"}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetProjectExport(ctx, n, "ui", api.ExportLeftOut, ""); err != nil {
		t.Fatal(err)
	}
	if err := b.FinishExport(ctx, n); err != nil {
		t.Fatal(err)
	}
	got := get(t, b, tk.Number)
	snap, ok := snapTicket(snapshot(t, b), tk.Number)
	if !ok || snap.Projects[0].ExportRemote != "https://user:***@example.test/org/repo?token=***" || snap.Projects[0].ExportBranch != "feature" || snap.Projects[0].ExportBase != "main" || snap.Projects[0].ExportCommit != commitB || snap.Projects[0].ExportTargetCommit != commitC || snap.Projects[0].Start != commitA || snap.Projects[0].End != commitB {
		t.Fatalf("engine lost watch: %+v", snap)
	}
	if p := got.Projects[0]; p.StartCommit != commitA || p.EndCommit != commitB {
		t.Fatalf("awaiting diff hidden: %+v", p)
	}
	wantCode(t, b.RecordExportWatch(ctx, n, "api", remote, "feature", "main", commitB, commitC), api.CodeInvalidTransition)
	for _, commits := range []map[string]string{nil, {"api": commitC}, {"api": commitB, "ui": commitC}} {
		wantCode(t, b.CompleteExportMerge(ctx, n, got.ReviewRound, commits), api.CodeConflict)
	}
	wantCode(t, b.CompleteExportMerge(ctx, n, got.ReviewRound+1, map[string]string{"api": commitB}), api.CodeConflict)
	if get(t, b, tk.Number).Column != api.ColumnReview {
		t.Fatal("stale merge check closed export")
	}
	if err := b.CompleteExportMerge(ctx, n, got.ReviewRound, map[string]string{"api": commitB}); err != nil {
		t.Fatal(err)
	}
	got = get(t, b, tk.Number)
	if got.Column != api.ColumnDone || !got.ExportMerged || got.Resolution != api.ResolutionExported {
		t.Fatalf("confirmed merge = %+v", got.TicketSummary)
	}
}

func TestAwaitingExportCanBeMovedDoneOrRejected(t *testing.T) {
	for _, action := range []string{"move", "reject"} {
		t.Run(action, func(t *testing.T) {
			b, _ := newTestBoard(t)
			ctx := context.Background()
			tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: action, Projects: []string{"api"}})
			force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
			if err := b.CompleteExport(ctx, int64(tk.Number), "https://example.test/pr/1"); err != nil {
				t.Fatal(err)
			}
			if action == "move" {
				got, err := b.Move(ctx, User, int64(tk.Number), api.MoveRequest{Column: api.ColumnDone})
				if err != nil {
					t.Fatal(err)
				}
				if got.Column != api.ColumnDone || !got.ExportMerged || got.Resolution != api.ResolutionExported {
					t.Fatalf("manual Done = %+v", got.TicketSummary)
				}
			} else {
				got, err := b.Reject(ctx, User, int64(tk.Number), api.RejectRequest{Comment: "needs changes"})
				if err != nil {
					t.Fatal(err)
				}
				if got.Column != api.ColumnInProgress || got.ExportURL != "" || got.Projects[0].ExportURL != "" {
					t.Fatalf("rejected export not cleared: %+v", got.TicketSummary)
				}
			}
		})
	}
}

func TestRejectedExportClearsWatchAndReexportReplacesIt(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "export", Projects: []string{"api"}})
	n := int64(tk.Number)
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.RecordExportWatch(ctx, n, "api", "https://example.test/original", "first", "main", commitA, commitB); err != nil {
		t.Fatal(err)
	}
	if err := b.CompleteExport(ctx, n, "https://example.test/pr/1"); err != nil {
		t.Fatal(err)
	}
	round := get(t, b, tk.Number).ReviewRound
	got, err := b.Reject(ctx, User, n, api.RejectRequest{Comment: "change it"})
	if err != nil {
		t.Fatal(err)
	}
	p := got.Projects[0]
	if got.ExportURL != "" || got.ExportMerged || p.ExportURL != "" || p.ExportRemote != "" || p.ExportBranch != "" || p.ExportBase != "" || p.ExportCommit != "" || p.ExportTargetCommit != "" {
		t.Fatalf("rejected export still watched: %+v", got.TicketSummary)
	}
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.RecordExportWatch(ctx, n, "api", "https://example.test/replacement", "second", "next", commitB, commitC); err != nil {
		t.Fatal(err)
	}
	if err := b.CompleteExport(ctx, n, "https://example.test/pr/2"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, b.CompleteExportMerge(ctx, n, round, map[string]string{"api": commitA}), api.CodeConflict)
	got = get(t, b, tk.Number)
	p = got.Projects[0]
	if got.ExportURL != "https://example.test/pr/2" || p.ExportCommit != commitB || p.ExportTargetCommit != commitC || p.ExportBranch != "second" || p.ExportBase != "next" || p.ExportRemote != "https://example.test/replacement" {
		t.Fatalf("new export did not replace watch: %+v", got.TicketSummary)
	}
}

func TestLegacyDoneExportStillAcceptsMarkMerged(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := standaloneTask(t, b)
	forceDone(t, b, n, api.ResolutionExported)
	if got := get(t, b, n); got.ExportMerged {
		t.Fatal("fixture already merged")
	}
	got, err := b.MarkMerged(ctx, User, int64(n))
	if err != nil || got.Column != api.ColumnDone || got.Resolution != api.ResolutionExported || !got.ExportMerged {
		t.Fatalf("legacy export = %+v, %v", got.TicketSummary, err)
	}
}

func TestExportRemoteDisplayPreservesGitTransports(t *testing.T) {
	for _, remote := range []string{"git@example.test:org/repo.git", "/tmp/repos/my repo.git", "/tmp/repos/ssh://my repo.git", `C:\repos\my repo.git`} {
		t.Run(remote, func(t *testing.T) {
			b, _ := newTestBoard(t)
			tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "transport", Projects: []string{"api"}})
			force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
			if err := b.RecordExportWatch(context.Background(), int64(tk.Number), "api", remote, "feature", "main", commitA, commitB); err != nil {
				t.Fatal(err)
			}
			if got := get(t, b, tk.Number).Projects[0].ExportRemote; got != remote {
				t.Fatalf("display remote = %q, want %q", got, remote)
			}
		})
	}
}

func TestExportCheckAttentionRequiresCurrentAwaitingRound(t *testing.T) {
	for _, state := range []struct {
		name   string
		column api.Column
		stage  api.ReviewStage
		round  int
		code   string
	}{
		{"rejected", api.ColumnInProgress, "", 1, api.CodeInvalidTransition},
		{"exporting again", api.ColumnReview, api.ReviewStageExporting, 1, api.CodeInvalidTransition},
		{"new awaiting round", api.ColumnReview, api.ReviewStageAwaitingMerge, 2, api.CodeConflict},
	} {
		t.Run(state.name, func(t *testing.T) {
			b, _ := newTestBoard(t)
			n := standaloneTask(t, b)
			force(t, b, n, state.column, state.stage)
			if _, err := b.db.Exec(`UPDATE tickets SET review_round=? WHERE id=?`, state.round, n); err != nil {
				t.Fatal(err)
			}
			if err := b.SetAttention(context.Background(), int64(n), "current reason"); err != nil {
				t.Fatal(err)
			}
			last := lastEvent(t, b)
			for _, reason := range []string{"stale check failed", ""} {
				wantCode(t, b.SetExportCheckAttention(context.Background(), int64(n), 1, reason), state.code)
				if got := get(t, b, n).Attention; got != "current reason" {
					t.Fatalf("stale check changed attention: %q", got)
				}
				if len(eventsAfter(t, b, last)) != 0 {
					t.Fatal("stale check emitted events or comments")
				}
			}
		})
	}
}

func TestExportCheckAttentionFlagsAndClearsCurrentRound(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := standaloneTask(t, b)
	force(t, b, n, api.ColumnReview, api.ReviewStageAwaitingMerge)
	if err := b.SetExportCheckAttention(ctx, int64(n), 1, " Merge check failed: fetch failed\ntry later "); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, n).Attention; got != "Merge check failed: fetch failed try later" {
		t.Fatalf("attention = %q", got)
	}
	if c, ok := lastComment(t, b, n); !ok || c.Body != "Needs attention: Merge check failed: fetch failed\ntry later" || c.Author.Role != api.RoleSystem {
		t.Fatalf("attention comment = %+v", c)
	}
	last := lastEvent(t, b)
	if err := b.SetExportCheckAttention(ctx, int64(n), 1, " Merge check failed: fetch failed\ntry later "); err != nil {
		t.Fatal(err)
	}
	if len(eventsAfter(t, b, last)) != 0 {
		t.Fatal("repeated check repeated attention")
	}
	if err := b.SetExportCheckAttention(ctx, int64(n), 1, ""); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, n).Attention; got != "" {
		t.Fatalf("cleared attention = %q", got)
	}
	if f := updatedFields(t, eventsAfter(t, b, last)); len(f) != 1 || f[0] != "#1 attention" {
		t.Fatalf("attention clear events = %v", f)
	}
}

func TestExportCheckAttentionDoesNotClearUnrelatedAttention(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := standaloneTask(t, b)
	force(t, b, n, api.ColumnReview, api.ReviewStageAwaitingMerge)
	if err := b.SetAttention(ctx, int64(n), "Agent needs help"); err != nil {
		t.Fatal(err)
	}
	last := lastEvent(t, b)
	if err := b.SetExportCheckAttention(ctx, int64(n), 1, ""); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, n).Attention; got != "Agent needs help" {
		t.Fatalf("cleared unrelated attention: %q", got)
	}
	if len(eventsAfter(t, b, last)) != 0 {
		t.Fatal("clearing unrelated attention emitted events")
	}
}

func TestLocalMergeAfterPartialExportStillAwaitsRemoteMerge(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "mixed merge", Projects: []string{"api", "ui"}})
	dep := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "dependent", Projects: []string{"api"}, DependsOn: []int{tk.Number}})
	n := int64(tk.Number)
	setBranch(t, b, tk.Number, "api", "exported")
	setBranch(t, b, tk.Number, "ui", "local")
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.RecordCommits(ctx, n, "api", commitA, commitB); err != nil {
		t.Fatal(err)
	}
	if err := b.RecordExportWatch(ctx, n, "api", "https://example.test/repo", "exported", "main", commitB, commitA); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Done(ctx, exportJob(t, b, tk.Number, "api"), api.DoneRequest{URL: "https://example.test/pr/1"}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetProjectExport(ctx, n, "ui", api.ExportFailedState, "cannot export"); err != nil {
		t.Fatal(err)
	}
	if err := b.ExportFailed(ctx, n, "ui cannot export"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Approve(ctx, User, n, api.ApproveRequest{Merge: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.RecordCommits(ctx, n, "ui", commitA, commitC); err != nil {
		t.Fatal(err)
	}
	if err := b.CompleteMerge(ctx, n); err != nil {
		t.Fatal(err)
	}
	got := get(t, b, tk.Number)
	if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAwaitingMerge || got.Resolution != "" || got.ClosedAt != nil || got.ExportMerged {
		t.Fatalf("local part prematurely completed export: %+v", got.TicketSummary)
	}
	if got.Projects[1].ExportState != api.ExportNone || got.Projects[1].ExportError != "" {
		t.Fatalf("locally merged project retains export failure: %+v", got.Projects[1])
	}
	if got.ExportURL != "https://example.test/pr/1" || got.Projects[0].ExportURL != "https://example.test/pr/1" {
		t.Fatalf("lost retained pull request: %+v", got.TicketSummary)
	}
	if waiting := get(t, b, dep.Number).WaitingFor; len(waiting) != 1 || waiting[0].Reason != api.WaitingExportNotMerged {
		t.Fatalf("partial merge dependencies = %+v", waiting)
	}
	if err := b.CompleteExportMerge(ctx, n, got.ReviewRound, map[string]string{"api": commitB}); err != nil {
		t.Fatal(err)
	}
	got = get(t, b, tk.Number)
	if got.Column != api.ColumnDone || got.Resolution != api.ResolutionExported || !got.ExportMerged {
		t.Fatalf("confirmed mixed merge not done: %+v", got.TicketSummary)
	}
	if len(get(t, b, dep.Number).WaitingFor) != 0 {
		t.Fatal("mixed merge still blocks dependent")
	}
}

func TestExportMergeChecksOnlyProjectsWithPullRequests(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "mixed", Projects: []string{"api", "ui"}})
	n := int64(tk.Number)
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.RecordExportWatch(ctx, n, "api", "https://example.test/repo", "exported", "main", commitB, commitA); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Done(ctx, exportJob(t, b, tk.Number, "api"), api.DoneRequest{URL: "https://example.test/pr/1"}); err != nil {
		t.Fatal(err)
	}
	// The other project was merged locally; it has no watch and was not left out.
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageAwaitingMerge)
	wantCode(t, b.CompleteExportMerge(ctx, n, 1, nil), api.CodeConflict)
	wantCode(t, b.CompleteExportMerge(ctx, n, 1, map[string]string{"api": commitB, "ui": commitC}), api.CodeConflict)
	if err := b.CompleteExportMerge(ctx, n, 1, map[string]string{"api": commitB}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, b, tk.Number); got.Column != api.ColumnDone || !got.ExportMerged {
		t.Fatalf("mixed completion = %+v", got.TicketSummary)
	}
}
