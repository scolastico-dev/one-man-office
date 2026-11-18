package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// A check must use remote target content, including rewritten commits, and
// retain the pushed commit after the remote removes the feature branch.
func TestExportRemoteMergeStyles(t *testing.T) {
	for _, style := range []string{"merge", "ff", "rebase", "squash"} {
		t.Run(style, func(t *testing.T) {
			h := gitHarness(t)
			n, job, _, branch := h.exportedTask("Remote " + style)
			if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
				t.Fatal(err)
			}
			h.steps(2)
			remote := h.origin(h.project)
			landed := filepath.Join(t.TempDir(), "landed")
			gitRun(t, h.repo, "clone", "-q", "-b", "main", remote, landed)
			gitRun(t, landed, "fetch", "-q", "origin", "refs/heads/"+branch)
			pushed := gitRun(t, landed, "rev-parse", "FETCH_HEAD")
			if style != "ff" {
				commit(t, landed, "other.txt", "other\n")
			}
			switch style {
			case "merge":
				gitRun(t, landed, "merge", "-q", "--no-ff", "-m", "merge", pushed)
			case "ff":
				gitRun(t, landed, "merge", "-q", "--ff-only", pushed)
			case "rebase":
				gitRun(t, landed, "cherry-pick", pushed)
			case "squash":
				gitRun(t, landed, "merge", "--squash", pushed)
				gitRun(t, landed, "commit", "-q", "-m", "squashed")
			}
			commit(t, landed, "later.txt", "later target work\n")
			gitRun(t, landed, "push", "-q", "origin", "refs/heads/main:refs/heads/main", ":refs/heads/"+branch)
			before := gitRun(t, h.repo, "show-ref", "--heads")
			h.e.checkExportsMerged(h.ctx)
			tk := h.ticket(n)
			if tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionExported || !tk.ExportMerged {
				t.Fatalf("not detected: %+v", tk.TicketSummary)
			}
			if got := gitRun(t, h.repo, "show-ref", "--heads"); got != before {
				t.Fatal("check changed local branches")
			}
			h.steps(2)
			if h.liveCount() != 0 {
				t.Fatal("Done did not end agents")
			}
		})
	}
}

func TestExportRoundFetchesEachTargetOnce(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "git-trace")
	t.Setenv("GIT_TRACE", trace)
	h := gitHarness(t)
	h.setLimit(10)
	for i := 0; i < 3; i++ {
		n, job, _, _ := h.exportedTask("Shared target")
		if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
			t.Fatal(err)
		}
		h.steps(2)
		if h.ticket(n).ReviewStage != "awaiting_merge" {
			t.Fatalf("export did not wait: %s", h.ticket(n).Column)
		}
	}
	if err := os.WriteFile(trace, nil, 0600); err != nil {
		t.Fatal(err)
	}
	h.e.checkExportsMerged(h.ctx)
	raw, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "built-in: git") && strings.Contains(line, " fetch ") {
			fetches++
		}
	}
	if fetches != 1 {
		t.Fatalf("got %d fetches, want one:\n%s", fetches, raw)
	}
}

// Manual requests while a check runs share the actual fetch, even with
// automatic checks disabled. A request must not queue another round.
func TestManualExportChecksCoalesceWithPeriodicChecksOff(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "git-trace")
	t.Setenv("GIT_TRACE", trace)
	h := gitHarness(t)
	n, job, _, _ := h.exportedTask("Manual check")
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	g := h.st.General()
	g.ExportCheckInterval = 0
	if err := h.st.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trace, nil, 0600); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	h.e.pause = func(point string) {
		if point == "export-check-fetch" {
			close(started)
			<-release
		}
	}
	if err := h.e.CheckExport(h.ctx, board.User, n); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("manual check never started")
	}
	for i := 0; i < 10; i++ {
		if err := h.e.CheckExport(h.ctx, board.User, n); err != nil {
			close(release)
			t.Fatal(err)
		}
	}
	close(release)
	h.e.settle()
	h.e.pause = nil
	raw, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "built-in: git") && strings.Contains(line, " fetch ") {
			fetches++
		}
	}
	if fetches != 1 {
		t.Fatalf("manual requests caused %d fetches, want one:\n%s", fetches, raw)
	}
}

func TestExportCheckFailuresRetryAndFlag(t *testing.T) {
	h := gitHarness(t)
	n, job, _, _ := h.exportedTask("Retry merge check")
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	remote := h.origin(h.project)
	gitRun(t, remote, "update-ref", "-d", "refs/heads/main")
	for i := 0; i < 3; i++ {
		h.e.checkExportsMerged(h.ctx)
	}
	tk := h.ticket(n)
	if tk.Column != api.ColumnReview || tk.Attention == "" {
		t.Fatalf("failed check closed or did not flag ticket: %+v", tk.TicketSummary)
	}
	gitRun(t, h.repo, "push", "-q", "origin", "refs/heads/main:refs/heads/main")
	h.e.checkExportsMerged(h.ctx)
	if tk = h.ticket(n); tk.Column != api.ColumnReview || tk.ExportMerged || tk.Attention != "" {
		t.Fatalf("unchanged target counted merged or recovery stayed flagged: %+v", tk.TicketSummary)
	}
}

func TestExportChecksRespectPauseAndDisabledInterval(t *testing.T) {
	h := gitHarness(t)
	n, job, _, branch := h.exportedTask("Check manually")
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	gitRun(t, h.repo, "merge", "-q", "--no-ff", "-m", "land", branch)
	gitRun(t, h.repo, "push", "-q", "origin", "refs/heads/main:refs/heads/main")
	g := h.st.General()
	g.ExportCheckInterval = 0
	if err := h.st.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	h.e.checkExportsDue(h.ctx, h.clock.Now())
	h.e.settle()
	if h.ticket(n).Column == api.ColumnDone {
		t.Fatal("disabled checks ran")
	}
	g.ExportCheckInterval = api.Duration(time.Minute)
	if err := h.st.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	if err := h.b.SetPaused(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	h.e.checkExportsDue(h.ctx, h.clock.Now())
	h.e.settle()
	if h.ticket(n).Column == api.ColumnDone {
		t.Fatal("checks ran while paused")
	}
	if err := h.b.SetPaused(h.ctx, false); err != nil {
		t.Fatal(err)
	}
	h.e.checkExportsDue(h.ctx, h.clock.Now())
	h.e.settle()
	if h.ticket(n).Column != api.ColumnDone {
		t.Fatal("unpaused check did not detect merge")
	}
}

func TestExportCheckUsesRecordedRemoteAfterConfigChanges(t *testing.T) {
	for _, change := range []string{"removed", "replaced", "space path"} {
		t.Run(change, func(t *testing.T) {
			h := gitHarness(t)
			remote := h.origin(h.project)
			if change == "space path" {
				renamed := filepath.Join(t.TempDir(), "remote with spaces.git")
				if err := os.Rename(remote, renamed); err != nil {
					t.Fatal(err)
				}
				remote = renamed
				gitRun(t, h.repo, "remote", "set-url", "origin", remote)
			}
			n, job, _, branch := h.exportedTask("Saved destination")
			if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
				t.Fatal(err)
			}
			h.steps(2)
			gitRun(t, remote, "update-ref", "refs/heads/main", remoteHead(t, remote, branch))
			switch change {
			case "removed":
				gitRun(t, h.repo, "remote", "remove", "origin")
			case "replaced":
				gitRun(t, h.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
			}
			h.e.checkExportsMerged(h.ctx)
			if tk := h.ticket(n); tk.Column != api.ColumnDone || !tk.ExportMerged {
				t.Fatalf("recorded destination lost after %s: %+v", change, tk.TicketSummary)
			}
		})
	}
}
