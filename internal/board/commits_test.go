package board

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

const (
	commitA = "1111111111111111111111111111111111111111"
	commitB = "2222222222222222222222222222222222222222"
	commitC = "3333333333333333333333333333333333333333"
)

func TestRecordCommitsRules(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api", "ui"}})
	setBranch(t, b, tk.Number, "api", "omo/s")
	n := int64(tk.Number)

	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageHuman)
	wantCode(t, b.RecordCommits(ctx, n, "api", commitA, commitB), api.CodeInvalidTransition)

	force(t, b, tk.Number, api.ColumnReview, api.ReviewStageMerging)
	for _, bad := range [][2]string{{"", commitB}, {commitA, "abc"}, {strings.Repeat("A", 40), commitB}, {commitA, commitB + "0"}} {
		wantCode(t, b.RecordCommits(ctx, n, "api", bad[0], bad[1]), api.CodeValidation)
	}
	// ui has no branch, other is no project of the ticket.
	wantCode(t, b.RecordCommits(ctx, n, "ui", commitA, commitB), api.CodeValidation)
	wantCode(t, b.RecordCommits(ctx, n, "other", commitA, commitB), api.CodeValidation)
	if err := b.RecordCommits(ctx, n, "api", commitA, commitB); err != nil {
		t.Fatal(err)
	}
	// Not done yet: the answer does not show them.
	if p := get(t, b, tk.Number).Projects[0]; p.StartCommit != "" || p.EndCommit != "" {
		t.Fatalf("commits shown before done: %+v", p)
	}
	sha256 := strings.Repeat("ab", 32)
	if err := b.RecordCommits(ctx, n, "api", sha256, sha256); err != nil {
		t.Fatalf("SHA-256 ids: %v", err)
	}
}

// TestEveryWayToDoneNeedsRecordedCommits walks each way a ticket with a
// branch closes as completed or exported: without the commits of its diff
// recorded for the current round it is refused and stays where it is; with
// them it is done and its projects answer them. Cancelling needs none.
func TestEveryWayToDoneNeedsRecordedCommits(t *testing.T) {
	ctx := context.Background()
	jobs := map[*Board]Actor{}
	ways := []struct {
		name   string
		stage  api.ReviewStage
		ticket api.CreateTicket
		close  func(t *testing.T, b *Board, n int) error
	}{
		{"merge", api.ReviewStageMerging, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}},
			func(t *testing.T, b *Board, n int) error { return b.CompleteMerge(ctx, int64(n)) }},
		{"export of one project", api.ReviewStageExporting,
			api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport},
			func(t *testing.T, b *Board, n int) error {
				return b.CompleteExport(ctx, int64(n), "https://example.test/pr/1")
			}},
		{"export job's report", api.ReviewStageExporting,
			api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport},
			func(t *testing.T, b *Board, n int) error {
				if _, ok := jobs[b]; !ok {
					jobs[b] = exportJob(t, b, n, "api")
				}
				_, err := b.Done(ctx, jobs[b], api.DoneRequest{URL: "https://example.test/pr/2"})
				return err
			}},
		{"export with nothing to export", api.ReviewStageExporting,
			api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, WhenApproved: api.WhenApprovedExport},
			func(t *testing.T, b *Board, n int) error { return b.FinishExport(ctx, int64(n)) }},
	}
	for _, w := range ways {
		t.Run(w.name, func(t *testing.T) {
			b, _ := newTestBoard(t)
			tk := mustCreate(t, b, User, w.ticket)
			setBranch(t, b, tk.Number, "api", "omo/t")
			force(t, b, tk.Number, api.ColumnReview, w.stage)

			wantCode(t, w.close(t, b, tk.Number), api.CodeConflict)
			if got := get(t, b, tk.Number); got.Column != api.ColumnReview || got.ReviewStage != w.stage {
				t.Fatalf("refused close moved the ticket to %s/%q", got.Column, got.ReviewStage)
			}

			// Recorded in an earlier round: still refused.
			if err := b.RecordCommits(ctx, int64(tk.Number), "api", commitA, commitB); err != nil {
				t.Fatal(err)
			}
			if _, err := b.db.Exec(`UPDATE tickets SET review_round = review_round + 1 WHERE id = ?`, tk.Number); err != nil {
				t.Fatal(err)
			}
			wantCode(t, w.close(t, b, tk.Number), api.CodeConflict)

			if err := b.RecordCommits(ctx, int64(tk.Number), "api", commitB, commitC); err != nil {
				t.Fatal(err)
			}
			if err := w.close(t, b, tk.Number); err != nil {
				t.Fatal(err)
			}
			got := get(t, b, tk.Number)
			if w.name == "export of one project" || w.name == "export job's report" {
				if got.Column != api.ColumnReview || got.ReviewStage != api.ReviewStageAwaitingMerge {
					t.Fatalf("export = %s/%s", got.Column, got.ReviewStage)
				}
			} else if got.Column != api.ColumnDone {
				t.Fatalf("column = %s", got.Column)
			}
			if p := got.Projects[0]; p.StartCommit != commitB || p.EndCommit != commitC {
				t.Fatalf("recorded commits = %q..%q", p.StartCommit, p.EndCommit)
			}
		})
	}

	t.Run("cancel", func(t *testing.T) {
		b, _ := newTestBoard(t)
		tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
		setBranch(t, b, tk.Number, "api", "omo/t")
		force(t, b, tk.Number, api.ColumnReview, api.ReviewStageHuman)
		got, err := b.Cancel(ctx, User, int64(tk.Number), api.CancelRequest{})
		if err != nil || got.Column != api.ColumnDone || got.Resolution != api.ResolutionCancelled {
			t.Fatalf("cancel: %s %s, %v", got.Column, got.Resolution, err)
		}
	})

	t.Run("a project without a branch records nothing", func(t *testing.T) {
		b, _ := newTestBoard(t)
		tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
		force(t, b, tk.Number, api.ColumnReview, api.ReviewStageMerging)
		if err := b.CompleteMerge(ctx, int64(tk.Number)); err != nil {
			t.Fatal(err)
		}
	})
}

// TestOnlySetColumnWritesTheColumn is the guard behind checkRecorded: every
// move of a ticket, and so every way to Done, goes through setColumn, which
// refuses to close a ticket whose diff's commits are not recorded. A new
// statement that writes the column elsewhere fails here.
func TestOnlySetColumnWritesTheColumn(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	write := regexp.MustCompile(`(?is)UPDATE\s+tickets\s+SET[^` + "`" + `]*\bcol\s*=`)
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range write.FindAllIndex(src, -1) {
			found++
			before := string(src[:m[0]])
			fn := before[strings.LastIndex(before, "\nfunc "):]
			if !strings.HasPrefix(fn, "\nfunc (b *Board) setColumn(") {
				t.Errorf("%s: a ticket's column is written outside setColumn: %s", f, strings.SplitN(fn[1:], "\n", 2)[0])
			}
		}
	}
	if found != 1 {
		t.Fatalf("found %d writes of the column, want the one in setColumn", found)
	}
}
