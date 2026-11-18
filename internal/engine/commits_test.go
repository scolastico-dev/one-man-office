package engine

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// keptCommits lists the commits omo's refs keep for ticket n in repo.
func keptCommits(t *testing.T, repo string, n int64) []string {
	t.Helper()
	out := gitRun(t, repo, "for-each-ref", "--format=%(objectname)", "refs/omo/tickets/"+strconv.FormatInt(n, 10)+"/")
	if out == "" {
		return nil
	}
	got := strings.Split(out, "\n")
	slices.Sort(got)
	return got
}

// collectGarbage drops every reflog entry and every object no ref reaches.
func collectGarbage(t *testing.T, repo string) {
	t.Helper()
	gitRun(t, repo, "reflog", "expire", "--expire=now", "--all")
	gitRun(t, repo, "gc", "-q", "--prune=now")
}

// wantRecorded fails unless project of done ticket n records start..end,
// omo's refs keep them in repo, and their diff lists files.
func (h *harness) wantRecorded(n int64, project, repo, start, end string, files ...string) {
	h.t.Helper()
	var p api.TicketProject
	for _, x := range h.ticket(n).Projects {
		if x.Project == project {
			p = x
		}
	}
	if p.StartCommit != start || p.EndCommit != end {
		h.t.Fatalf("#%d records %s..%s in %s, want %s..%s", n, p.StartCommit, p.EndCommit, project, start, end)
	}
	if kept := keptCommits(h.t, repo, n); !slices.Contains(kept, start) || !slices.Contains(kept, end) {
		h.t.Fatalf("#%d keeps %v, want %s and %s", n, kept, start, end)
	}
	res, err := h.git.RangeDiff(h.ctx, repo, start, end, gitops.DefaultDiffLimits)
	if err != nil {
		h.t.Fatal(err)
	}
	var got []string
	for _, f := range res.Files {
		got = append(got, f.Path)
	}
	if !slices.Equal(got, files) {
		h.t.Fatalf("diff of #%d in %s lists %v, want %v", n, project, got, files)
	}
}

// A merged and a squashed task record the diff their reviewer saw: from
// where the branch left main to the reviewed commit, not main's later
// commits. After the worktree and branch are gone and git collected its
// garbage, the diff is still there.
func TestDoneTaskRecordsItsDiff(t *testing.T) {
	for _, mode := range []string{api.MergeModeMerge, api.MergeModeSquash} {
		t.Run(mode, func(t *testing.T) {
			h := gitHarness(t)
			h.setMergeDefault("task", mode)
			start := gitRun(t, h.repo, "rev-parse", "main")
			n := h.skippedTask("Add b")
			h.finishTask(n, h.project, "b.txt", "b\n")
			end := gitRun(t, h.worktree(h.project, n), "rev-parse", "HEAD")
			branch := h.ticket(n).Projects[0].Branch
			commit(t, h.repo, "c.txt", "the user's commit meanwhile\n")
			h.steps(3)

			if tk := h.ticket(n); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
				t.Fatalf("ticket is %s/%s (attention %q)", tk.Column, tk.Resolution, tk.Attention)
			}
			if pathExists(h.worktree(h.project, n)) || slices.Contains(h.branches(h.repo), branch) {
				t.Fatal("the worktree or the branch stayed")
			}
			collectGarbage(t, h.repo)
			h.wantRecorded(n, h.project, h.repo, start, end, "b.txt")
		})
	}
}

// A task of a story starts on the story's branch; once the story is
// squashed into main and its branch is gone, the task's diff is still
// there, and so is the story's.
func TestDoneStoryAndItsTasksRecordTheirDiffs(t *testing.T) {
	h := gitHarness(t)
	h.setMergeDefault("story", api.MergeModeSquash)
	h.setMergeDefault("story_task", api.MergeModeMerge)
	mainBefore := gitRun(t, h.repo, "rev-parse", "main")
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Story", Review: api.ReviewSkipped})
	h.startStory(s)
	first := h.storyTask(s, h.project, "First")
	h.finishTask(first, h.project, "one.txt", "1\n")
	firstEnd := gitRun(t, h.worktree(h.project, first), "rev-parse", "HEAD")
	h.steps(2)
	second := h.storyTask(s, h.project, "Second")
	h.finishTask(second, h.project, "two.txt", "2\n")
	secondEnd := gitRun(t, h.worktree(h.project, second), "rev-parse", "HEAD")
	// The second task left the story's branch after the first was merged
	// into it: its diff starts at that merge.
	storyBranch := "refs/heads/" + h.ticket(s).Projects[0].Branch
	secondStart := gitRun(t, h.repo, "merge-base", storyBranch, secondEnd)
	if parents := strings.Fields(gitRun(t, h.repo, "log", "-1", "--format=%P", secondStart)); len(parents) != 2 || parents[1] != firstEnd {
		t.Fatalf("the second task did not start at the merge of the first: parents %v", parents)
	}
	h.steps(2)
	storyEnd := gitRun(t, h.repo, "rev-parse", storyBranch)
	h.handIn(s)
	h.steps(3)
	if tk := h.ticket(s); tk.Column != api.ColumnDone {
		t.Fatalf("story is %s (attention %q)", tk.Column, tk.Attention)
	}
	if got := gitRun(t, h.repo, "for-each-ref", "--format=%(refname)", "refs/heads/omo/"); got != "" {
		t.Fatalf("branches stayed: %s", got)
	}
	collectGarbage(t, h.repo)
	h.wantRecorded(first, h.project, h.repo, mainBefore, firstEnd, "one.txt")
	h.wantRecorded(second, h.project, h.repo, secondStart, secondEnd, "two.txt")
	h.wantRecorded(s, h.project, h.repo, mainBefore, storyEnd, "one.txt", "two.txt")
}

// An exported task records the pushed commit since it left main.
func TestExportedTaskRecordsItsDiff(t *testing.T) {
	h := gitHarness(t)
	start := gitRun(t, h.repo, "rev-parse", "main")
	n, job, dir, _ := h.exportedTask("Export me")
	end := gitRun(t, dir, "rev-parse", "HEAD")
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(3)
	if tk := h.ticket(n); tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageAwaitingMerge {
		t.Fatalf("ticket is %s/%s", tk.Column, tk.Resolution)
	}
	h.wantRecorded(n, h.project, h.repo, start, end, "b.txt")
	out := gitRun(t, h.origin(h.project), "for-each-ref", "--format=%(refname)")
	if strings.Contains(out, "refs/omo/") {
		t.Fatalf("omo's refs reached the remote:\n%s", out)
	}
}

// A story exported in one project, left out in one (no commits of its
// own) and merged locally in the third after its export failed records
// all three.
func TestPartlyExportedStoryRecordsEveryProject(t *testing.T) {
	h := gitHarness(t, withProjects(3))
	a, b, c := h.ids[0], h.ids[1], h.ids[2]
	h.origin(a)
	before := map[string]string{}
	for _, p := range h.ids {
		before[p] = gitRun(t, h.repos[p], "rev-parse", "main")
	}
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Three", Projects: []string{a, b, c},
		Review: api.ReviewSkipped, WhenApproved: api.WhenApprovedExport})
	h.startStory(s)
	for _, p := range []string{a, b} {
		task := h.storyTask(s, p, "In "+p)
		h.finishTask(task, p, "new.txt", p+"\n")
		h.steps(2)
	}
	tips := map[string]string{}
	for _, p := range h.ticket(s).Projects {
		tips[p.Project] = gitRun(t, h.repos[p.Project], "rev-parse", "refs/heads/"+p.Branch)
	}
	h.handIn(s)
	h.steps(2)
	h.wantExportStates(s, api.ExportExporting, api.ExportFailedState, api.ExportLeftOut)
	job, ok := h.exportSession(s)
	if !ok {
		t.Fatal("no export job")
	}
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/a/pull/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if _, err := h.b.Approve(h.ctx, board.User, s, api.ApproveRequest{Merge: true}); err != nil {
		t.Fatal(err)
	}
	h.steps(3)
	if tk := h.ticket(s); tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageAwaitingMerge {
		t.Fatalf("story is %s/%s (attention %q)", tk.Column, tk.Resolution, tk.Attention)
	}
	remote := h.origin(a)
	gitRun(t, remote, "update-ref", "refs/heads/main", tips[a])
	h.e.checkExportsMerged(h.ctx)
	if tk := h.ticket(s); tk.Column != api.ColumnDone || !tk.ExportMerged {
		t.Fatalf("mixed export did not finish after remote merge: %+v", tk.TicketSummary)
	}
	h.wantRecorded(s, a, h.repos[a], before[a], tips[a], "new.txt")
	h.wantRecorded(s, b, h.repos[b], before[b], tips[b], "new.txt")
	h.wantRecorded(s, c, h.repos[c], before[c], before[c])
}

// A retry after a merge that went into one project and was blocked in the
// other keeps the start recorded for the first: its main holds the work
// by then.
func TestRetriedMergeKeepsTheRecordedStart(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	a, b := h.ids[0], h.ids[1]
	before := gitRun(t, h.repos[a], "rev-parse", "main")
	n := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{a, b}, Review: api.ReviewSkipped})
	h.startStory(n)
	for p, file := range map[string]string{a: "new.txt", b: "a.txt"} {
		task := h.storyTask(n, p, "In "+p)
		h.finishTask(task, p, file, p+"\n")
		h.steps(2)
	}
	tipA := gitRun(t, h.repos[a], "rev-parse", "refs/heads/"+h.ticket(n).Projects[0].Branch)
	write(t, h.repos[b], "a.txt", "user's uncommitted a\n")
	h.handIn(n)
	h.step()
	if tk := h.ticket(n); tk.FailedStep != api.FailedMerge {
		t.Fatalf("story is %s, failed %q", tk.Column, tk.FailedStep)
	}
	if kept := keptCommits(t, h.repos[b], n); len(kept) != 0 {
		t.Fatalf("the blocked project keeps %v", kept)
	}
	gitRun(t, h.repos[b], "checkout", "--", "a.txt")
	if _, err := h.b.Retry(h.ctx, board.User, n); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if tk := h.ticket(n); tk.Column != api.ColumnDone {
		t.Fatalf("story is %s (attention %q)", tk.Column, tk.Attention)
	}
	h.wantRecorded(n, a, h.repos[a], before, tipA, "new.txt")
}

// Removing a project from a ticket drops the commits omo kept for it there
// (here those of an export that went through before another project's
// failed).
func TestRemovedProjectLetsItsCommitsGo(t *testing.T) {
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
	if kept := keptCommits(t, h.repos[a], s); len(kept) != 2 {
		t.Fatalf("project a keeps %v after its export", kept)
	}
	if err := h.b.SetSessionState(h.ctx, h.live(api.RolePM, s).ID, api.SessionWaiting); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.RemoveProject(h.ctx, board.User, s, api.RemoveProjectRequest{Project: a, Discard: true}); err != nil {
		t.Fatal(err)
	}
	if kept := keptCommits(t, h.repos[a], s); len(kept) != 0 {
		t.Fatalf("project a still keeps %v after its removal", kept)
	}
}
