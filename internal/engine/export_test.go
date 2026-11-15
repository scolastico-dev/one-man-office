package engine

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// exportSession returns the live export job of ticket n.
func (h *harness) exportSession(n int64) (board.EngineSession, bool) {
	for _, s := range h.snap().Sessions {
		if s.Ticket == n && s.Purpose == api.PurposeExport {
			return s, true
		}
	}
	return board.EngineSession{}, false
}

// exportedTask runs a standalone task that is exported once approved up to
// its export job and returns the task, the job's session, the worktree and
// the branch. The project gets a bare repository as origin first, unless it
// has one.
func (h *harness) exportedTask(title string) (int64, board.EngineSession, string, string) {
	h.t.Helper()
	h.origin(h.project)
	n := h.create(api.CreateTicket{Type: api.TypeTask, Title: title, Review: api.ReviewSkipped, WhenApproved: api.WhenApprovedExport})
	h.finishTask(n, h.project, "b.txt", "b\n")
	h.step()
	tk := h.ticket(n)
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageExporting {
		h.t.Fatalf("ticket is %s/%s, want review/exporting", tk.Column, tk.ReviewStage)
	}
	s, ok := h.exportSession(n)
	if !ok {
		h.t.Fatal("no export job started")
	}
	return n, s, h.worktree(h.project, n), tk.Projects[0].Branch
}

func TestExportJobsPerProjectAndSuccess(t *testing.T) {
	h := gitHarness(t)
	n, job, dir, branch := h.exportedTask("Export me")
	if job.Role != api.RoleDeveloper || job.Project != h.project || job.Workdir != filepath.Join(h.layout.Run, "export", fmt.Sprintf("%d-%s", n, h.project)) {
		t.Fatalf("export job %+v", job)
	}
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(3)

	tk := h.ticket(n)
	if tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionExported || tk.ExportURL != "https://example.invalid/pr/1" {
		t.Fatalf("ticket is %s/%s url %q", tk.Column, tk.Resolution, tk.ExportURL)
	}
	if pathExists(dir) {
		t.Fatal("the worktree of an exported ticket stayed")
	}
	if !slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("the exported branch was deleted")
	}
	if p := tk.Projects[0]; p.Worktree != "" || p.Branch != branch {
		t.Fatalf("ticket records %q %q", p.Worktree, p.Branch)
	}
	if h.liveCount() != 0 {
		t.Fatalf("%d sessions still live", h.liveCount())
	}
}

func TestExportCrashLoopFailsExport(t *testing.T) {
	h := gitHarness(t)
	n, _, _, _ := h.exportedTask("Export me")
	for i := 0; i < maxFailures; i++ {
		job, ok := h.exportSession(n)
		if !ok {
			t.Fatalf("no export job for attempt %d", i+1)
		}
		h.agent(job.ID).exit(errors.New("exit status 3"))
		h.waitEnded(job.ID)
		h.step()
	}
	h.step()
	tk := h.ticket(n)
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageHuman || tk.Attention == "" {
		t.Fatalf("ticket is %s/%s attention %q, want review/human with attention", tk.Column, tk.ReviewStage, tk.Attention)
	}
	if tk.FailedStep != api.FailedNone {
		t.Fatalf("failed step %q; a failed export waits for the user without one", tk.FailedStep)
	}
	if _, ok := h.exportSession(n); ok {
		t.Fatal("an export job runs after the export failed")
	}
	if c := h.commentsWith(n, "Export failed"); c != 1 {
		t.Fatalf("%d export failure comments, want 1", c)
	}
	h.steps(2)
	if got := len(h.runner.specList()); got != 1+maxFailures {
		t.Fatalf("%d agents started, want the developer and %d export jobs", got, maxFailures)
	}
}

func TestExportedBranchMergedLocallySatisfiesDependents(t *testing.T) {
	h := gitHarness(t)
	n, job, _, branch := h.exportedTask("Export me")
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/2"}); err != nil {
		t.Fatal(err)
	}
	h.steps(3)
	dep := h.create(api.CreateTicket{Type: api.TypeTask, Title: "Needs the export", DependsOn: []int{int(n)}})
	waiting := func() bool {
		return slices.ContainsFunc(h.ticket(dep).WaitingFor, func(w api.Waiting) bool {
			return w.Ticket == int(n) && w.Reason == api.WaitingExportNotMerged
		})
	}
	if !waiting() {
		t.Fatalf("dependent waits for %+v", h.ticket(dep).WaitingFor)
	}
	h.e.checkExportsMerged(h.ctx)
	if h.ticket(n).ExportMerged || !waiting() {
		t.Fatal("an unmerged export counted as merged")
	}

	gitRun(t, h.repo, "merge", "-q", "--no-ff", "-m", "merge the pull request", branch)
	h.e.checkExportsMerged(h.ctx)
	if !h.ticket(n).ExportMerged {
		t.Fatal("the export was not found merged")
	}
	if w := h.ticket(dep).WaitingFor; len(w) != 0 {
		t.Fatalf("dependent still waits for %+v", w)
	}
}

func TestExportCheckRunsOnRescanInterval(t *testing.T) {
	h := gitHarness(t)
	n, job, _, branch := h.exportedTask("Export me")
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/3"}); err != nil {
		t.Fatal(err)
	}
	h.steps(3)
	tick := func() {
		h.e.healthTick(h.ctx, h.clock.Now())
		h.e.settle()
	}
	tick() // the first tick checks
	gitRun(t, h.repo, "merge", "-q", "--no-ff", "-m", "merge the pull request", branch)
	h.clock.Advance(time.Minute)
	tick()
	if h.ticket(n).ExportMerged {
		t.Fatal("checked again before the rescan interval passed")
	}
	h.clock.Advance(time.Duration(h.st.General().RescanInterval))
	tick()
	if !h.ticket(n).ExportMerged {
		t.Fatal("not checked once the rescan interval passed")
	}
	if strings.Contains(h.logs.String(), "panicked") {
		t.Fatal("a worker panicked")
	}
}
