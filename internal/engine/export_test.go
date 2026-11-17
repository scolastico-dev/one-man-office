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

// An export handles projects one by one: a project without commits of its
// own is left out (one comment), a project without a usable remote does not
// stop the others, which are pushed and get their pull request; the ticket
// then waits in Review with attention naming what could not be exported.
// Exporting again pushes and opens pull requests only for the rest.
func TestExportGoesOnPerProject(t *testing.T) {
	h := gitHarness(t, withProjects(3))
	a, b, c := h.ids[0], h.ids[1], h.ids[2]
	bareA := h.origin(a)
	h.origin(c)
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Three", Projects: []string{a, b, c},
		Review: api.ReviewSkipped, WhenApproved: api.WhenApprovedExport})
	h.startStory(s)
	for _, p := range []string{a, b} {
		task := h.storyTask(s, p, "In "+p)
		h.finishTask(task, p, "new.txt", p+"\n")
		h.steps(2)
	}
	h.handIn(s)
	h.steps(2)
	tk := h.ticket(s)
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageExporting {
		t.Fatalf("ticket is %s/%s attention %q", tk.Column, tk.ReviewStage, tk.Attention)
	}
	if n := h.commentsWith(s, "leaves out"); n != 1 || h.commentsWith(s, c+": no commits of its own") != 1 {
		t.Fatalf("left-out comments:\n%s", strings.Join(h.comments(s), "\n---\n"))
	}
	// b has no remote: it failed at once, while a's job goes on.
	h.wantExportStates(s, api.ExportExporting, api.ExportFailedState, api.ExportLeftOut)
	job, ok := h.exportSession(s)
	if !ok || job.Project != a {
		t.Fatalf("export job %+v (%v), want one for %s", job, ok, a)
	}
	branchA := tk.Projects[0].Branch
	pushedA := remoteHead(t, bareA, branchA)
	if pushedA == "" {
		t.Fatal("project a was not pushed")
	}
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/a/pull/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	tk = h.ticket(s)
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageHuman ||
		!strings.Contains(tk.Attention, "Export of "+b+" failed") || strings.Contains(tk.Attention, a) && !strings.Contains(tk.Attention, b) {
		t.Fatalf("ticket is %s/%s attention %q", tk.Column, tk.ReviewStage, tk.Attention)
	}
	if tk.Projects[0].ExportURL != "https://example.invalid/a/pull/1" {
		t.Fatalf("projects %+v", tk.Projects)
	}
	h.wantExportStates(s, api.ExportExported, api.ExportFailedState, api.ExportLeftOut)
	if e := tk.Projects[1].ExportError; e == "" || !strings.Contains(tk.Attention, e) {
		t.Fatalf("the failed project's reason %q, attention %q", e, tk.Attention)
	}
	if h.liveCount() != 1 { // the story's pm, parked
		t.Fatalf("%d sessions live", h.liveCount())
	}

	// The user gives b a remote and exports again: only b is pushed and
	// gets a job; a keeps its pull request and is not pushed again.
	bareB := h.origin(b)
	pushes := h.commentsWith(s, "Pushed `")
	h.exportNow(s)
	h.step()
	job, ok = h.exportSession(s)
	if !ok || job.Project != b {
		t.Fatalf("export job %+v (%v), want one for %s", job, ok, b)
	}
	if got := h.commentsWith(s, "Pushed `"); got != pushes+1 || remoteHead(t, bareB, tk.Projects[1].Branch) == "" {
		t.Fatalf("%d push comments, before %d", got, pushes)
	}
	h.wantExportStates(s, api.ExportExported, api.ExportExporting, api.ExportLeftOut)
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/b/pull/2"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	tk = h.ticket(s)
	if tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionExported ||
		tk.ExportURL != "https://example.invalid/a/pull/1\nhttps://example.invalid/b/pull/2" {
		t.Fatalf("ticket is %s/%s url %q", tk.Column, tk.Resolution, tk.ExportURL)
	}
	h.wantExportStates(s, api.ExportExported, api.ExportExported, api.ExportLeftOut)
}

// wantExportStates fails unless the export states of ticket n's projects,
// in order, are want.
func (h *harness) wantExportStates(n int64, want ...api.ExportState) {
	h.t.Helper()
	var got []api.ExportState
	for _, p := range h.ticket(n).Projects {
		got = append(got, p.ExportState)
	}
	if !slices.Equal(got, want) {
		h.t.Fatalf("export states of #%d are %q, want %q", n, got, want)
	}
}

// After a partial export, "Merge locally instead" merges the projects
// without a pull request and leaves the exported ones to theirs.
func TestMergeLocallyAfterPartialExport(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	a, b := h.ids[0], h.ids[1]
	h.origin(a)
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{a, b},
		Review: api.ReviewSkipped, WhenApproved: api.WhenApprovedExport})
	h.startStory(s)
	for _, p := range []string{a, b} {
		task := h.storyTask(s, p, "In "+p)
		h.finishTask(task, p, "new.txt", p+"\n")
		h.steps(2)
	}
	mainA := gitRun(t, h.repos[a], "rev-parse", "main")
	h.handIn(s)
	h.steps(2)
	job, ok := h.exportSession(s)
	if !ok || job.Project != a {
		t.Fatalf("export job %+v (%v)", job, ok)
	}
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/a/pull/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if tk := h.ticket(s); tk.ReviewStage != api.ReviewStageHuman {
		t.Fatalf("stage %q", tk.ReviewStage)
	}
	if _, err := h.b.Approve(h.ctx, board.User, s, api.ApproveRequest{Merge: true}); err != nil {
		t.Fatal(err)
	}
	h.steps(3)
	tk := h.ticket(s)
	if tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
		t.Fatalf("ticket is %s/%s (attention %q)", tk.Column, tk.Resolution, tk.Attention)
	}
	if got := gitRun(t, h.repos[a], "rev-parse", "main"); got != mainA {
		t.Fatal("the exported project was merged locally too")
	}
	if got := gitRun(t, h.repos[b], "show", "main:new.txt"); got != b {
		t.Fatalf("project b main has new.txt %q", got)
	}
}
