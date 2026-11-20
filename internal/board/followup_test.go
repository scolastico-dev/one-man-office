package board

import (
	"context"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func followupExport(t *testing.T, b *Board) int64 {
	t.Helper()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "Export follow-up", Projects: []string{"api"}, Review: api.ReviewHuman, WhenApproved: api.WhenApprovedExport})
	n := int64(tk.Number)
	setBranch(t, b, tk.Number, "api", "feature")
	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageExporting)
	if err := b.RecordCommits(context.Background(), n, "api", commitA, commitB); err != nil {
		t.Fatal(err)
	}
	if err := b.RecordExportWatch(context.Background(), n, "api", "https://example.test/org/repo", "feature", "main", commitB, commitA); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Done(context.Background(), exportJob(t, b, tk.Number, "api"), api.DoneRequest{URL: "https://example.test/org/repo/pull/1"}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestExportFollowUpPreservesHistoryAndInvalidatesWatch(t *testing.T) {
	for _, role := range []string{"developer", "reviewer"} {
		t.Run(role, func(t *testing.T) {
			b, _ := newTestBoard(t)
			n := followupExport(t, b)
			before := get(t, b, int(n))
			got, err := b.RequestFollowUp(context.Background(), User, n, api.FollowUpRequest{Role: role, Comment: "Rebase onto origin/main"})
			if err != nil {
				t.Fatal(err)
			}
			if role == "developer" && got.Column != api.ColumnInProgress {
				t.Fatalf("developer follow-up = %s/%s", got.Column, got.ReviewStage)
			}
			if role == "reviewer" && (got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAgent || got.ReviewRound != before.ReviewRound+1) {
				t.Fatalf("reviewer follow-up = %+v", got.TicketSummary)
			}
			p := got.Projects[0]
			if p.LastExportCommit != commitB || p.LastExportURL != "https://example.test/org/repo/pull/1" || p.ExportURL != "" || p.ExportCommit != "" || got.ExportMerged {
				t.Fatalf("follow-up export history/watch = %+v", p)
			}
			wantCode(t, b.CompleteExportMerge(context.Background(), n, before.ReviewRound, map[string]string{"api": commitB}), api.CodeInvalidTransition)
		})
	}
}

func TestExportFollowUpValidatesTaskAndRecipient(t *testing.T) {
	b, _ := newTestBoard(t)
	n := followupExport(t, b)
	for _, req := range []api.FollowUpRequest{{Role: "developer"}, {Role: "ceo", Comment: "Change it"}} {
		_, err := b.RequestFollowUp(context.Background(), User, n, req)
		wantCode(t, err, api.CodeValidation)
	}
	_, err := b.RequestFollowUp(context.Background(), Actor{Role: api.RoleDeveloper}, n, api.FollowUpRequest{Role: "developer", Comment: "Change it"})
	wantCode(t, err, api.CodeForbidden)
	if got := get(t, b, int(n)); got.ReviewStage != api.ReviewStageAwaitingMerge {
		t.Fatal("refused request changed the ticket")
	}
}
