package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// noProject creates a task without a project as the user.
func (h *harness) noProject(req api.CreateTicket) int64 {
	h.t.Helper()
	req.Type = api.TypeTask
	tk, err := h.b.Create(h.ctx, board.User, req)
	if err != nil {
		h.t.Fatal(err)
	}
	return int64(tk.Number)
}

// workdir is the working folder of ticket n.
func (h *harness) workdir(n int64) string { return WorkdirDir(h.layout.Workdirs, n) }

// specDir returns the folder the start spec of session id names.
func (h *harness) specDir(id string) string {
	h.t.Helper()
	for _, s := range h.runner.specList() {
		if s.SessionID == id {
			return s.Dir
		}
	}
	h.t.Fatalf("no spec for the session")
	return ""
}

// A task without a project runs from start to Done in its working folder:
// the developer and every reviewer work there, a rejection sends it back
// to the same developer, the second round's approval closes it as
// completed with nothing merged, and the folder stays with what the
// developer left there until the user removes it.
func TestTaskWithoutProjectRunsInItsWorkingFolder(t *testing.T) {
	h := newHarness(t)
	before := h.branches(h.repo)
	n := h.noProject(api.CreateTicket{Title: "Compare the caches", Review: api.ReviewAgent})
	h.approve(n)
	h.step()
	tk := h.ticket(n)
	dir := h.workdir(n)
	if tk.Column != api.ColumnInProgress || tk.Workdir != dir {
		t.Fatalf("after the start: %s, workdir %q, want %q", tk.Column, tk.Workdir, dir)
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("working folder: %v", err)
	}
	dev := h.live(api.RoleDeveloper, n)
	if dev.Workdir != dir || h.specDir(dev.ID) != dir || dev.Project != "" {
		t.Fatalf("developer works in %q (spec %q), project %q", dev.Workdir, h.specDir(dev.ID), dev.Project)
	}
	prompt, err := h.e.Ready(h.ctx, h.actor(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Your working folder is " + dir, h.repo, "Never change them"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("developer prompt lacks %q:\n%s", want, prompt)
		}
	}
	write(t, dir, "notes.md", "LRU wins\n")

	h.handIn(n)
	h.step()
	rev1 := h.roundReviewer(n, 1)
	if rev1.Workdir != dir || rev1.Project != "" {
		t.Fatalf("reviewer works in %q, project %q", rev1.Workdir, rev1.Project)
	}
	prompt, err = h.e.Ready(h.ctx, h.actor(rev1.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "there is no diff") || !strings.Contains(prompt, dir) {
		t.Fatalf("reviewer prompt:\n%s", prompt)
	}
	if _, err := h.b.Done(h.ctx, h.actor(rev1.ID), api.DoneRequest{Verdict: board.VerdictReject, Comment: "name the sources"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnInProgress {
		t.Fatalf("after the rejection: %s", c)
	}
	if got := h.live(api.RoleDeveloper, n); got.ID != dev.ID {
		t.Fatal("a fresh developer replaced the kept one")
	}

	write(t, dir, "sources.md", "the papers\n")
	h.handIn(n)
	h.step()
	rev2 := h.roundReviewer(n, 2)
	if reason := h.endReasonOf(rev1.ID); reason != newRoundText(n) {
		t.Fatalf("the round 1 reviewer ended with %q", reason)
	}
	if _, err := h.b.Done(h.ctx, h.actor(rev2.ID), api.DoneRequest{Verdict: board.VerdictApprove, Comment: "fine"}); err != nil {
		t.Fatal(err)
	}
	h.steps(3)
	tk = h.ticket(n)
	if tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted || tk.Workdir != dir {
		t.Fatalf("after the approval: %s/%s, workdir %q", tk.Column, tk.Resolution, tk.Workdir)
	}
	for _, id := range []string{dev.ID, rev2.ID} {
		if reason := h.endReasonOf(id); reason != "#1 is done" {
			t.Fatalf("an agent ended with %q", reason)
		}
	}
	for _, f := range []string{"notes.md", "sources.md"} {
		if !pathExists(filepath.Join(dir, f)) {
			t.Fatalf("%s is gone from the working folder after Done", f)
		}
	}
	if got := h.branches(h.repo); len(got) != len(before) {
		t.Fatalf("branches %v appeared in the project", got)
	}

	// The user removes the folder; a terminal in it refuses that first.
	release := h.e.HoldWorktree(dir)
	_, err = h.e.RemoveWorkdir(h.ctx, board.User, n)
	if code := errCodeOf(err); code != api.CodeConflict {
		t.Fatalf("removal with a terminal open: %v", err)
	}
	release()
	got, err := h.e.RemoveWorkdir(h.ctx, board.User, n)
	if err != nil {
		t.Fatal(err)
	}
	if got.Workdir != "" || pathExists(dir) {
		t.Fatalf("after the removal: workdir %q, folder there %v", got.Workdir, pathExists(dir))
	}
	_, err = h.e.RemoveWorkdir(h.ctx, board.User, n)
	if code := errCodeOf(err); code != api.CodeInvalidTransition {
		t.Fatalf("second removal: %v", err)
	}
}

// The user's review of a task without a project approves it into Done; a
// skipped review closes it at its hand-in.
func TestTaskWithoutProjectHumanAndSkippedReview(t *testing.T) {
	h := newHarness(t)
	human := h.noProject(api.CreateTicket{Title: "Look into it", Review: api.ReviewHuman})
	skipped := h.noProject(api.CreateTicket{Title: "Try it", Review: api.ReviewSkipped})
	h.approve(human)
	h.approve(skipped)
	h.step()
	h.handIn(human)
	h.handIn(skipped)
	h.steps(2)
	if tk := h.ticket(human); tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageHuman {
		t.Fatalf("human review: %s/%s", tk.Column, tk.ReviewStage)
	}
	if tk := h.ticket(skipped); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
		t.Fatalf("skipped review: %s/%s", tk.Column, tk.Resolution)
	}
	if _, err := h.b.Approve(h.ctx, board.User, human, api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if tk := h.ticket(human); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
		t.Fatalf("after the user's approval: %s/%s", tk.Column, tk.Resolution)
	}
	if h.liveCount() != 0 {
		t.Fatalf("%d sessions live after both are done", h.liveCount())
	}
	for _, n := range []int64{human, skipped} {
		if !pathExists(h.workdir(n)) {
			t.Fatalf("the working folder of #%d is gone", n)
		}
	}
}

// A cancel ends the agents and keeps the working folder; a restart of omo
// in the middle of the work starts the developer again in the same folder,
// made again when it went meanwhile, with the restart note.
func TestTaskWithoutProjectCancelAndRestart(t *testing.T) {
	h := newHarness(t)
	n := h.noProject(api.CreateTicket{Title: "Restarted"})
	c := h.noProject(api.CreateTicket{Title: "Cancelled"})
	h.approve(n)
	h.approve(c)
	h.step()
	first := h.live(api.RoleDeveloper, n)
	write(t, h.workdir(c), "half.txt", "half\n")

	h.cancel(c, api.CancelRequest{DeleteBranch: true, DiscardUncommitted: true})
	if tk := h.ticket(c); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCancelled || tk.Workdir == "" {
		t.Fatalf("after the cancel: %s/%s workdir %q", tk.Column, tk.Resolution, tk.Workdir)
	}
	if _, ok := h.liveOK(api.RoleDeveloper, c); ok {
		t.Fatal("the developer of the cancelled task still runs")
	}
	if !pathExists(filepath.Join(h.workdir(c), "half.txt")) {
		t.Fatal("the cancel removed the working folder")
	}

	if err := os.Remove(h.workdir(n)); err != nil {
		t.Fatal(err)
	}
	h.crash()
	h.boot(2)
	dev := h.live(api.RoleDeveloper, n)
	if dev.ID == first.ID || dev.Workdir != h.workdir(n) || !pathExists(h.workdir(n)) {
		t.Fatalf("after the restart: session %s in %q (folder there %v)", dev.ID, dev.Workdir, pathExists(h.workdir(n)))
	}
	prompt, err := h.e.Ready(h.ctx, h.actor(dev.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "look at what is in your working folder") {
		t.Fatalf("no restart note:\n%s", prompt)
	}
	h.cancel(n, api.CancelRequest{})
}

// A story with a research task without a project and a coding task in its
// project: the research task closes without touching the story's branch,
// the coding task merges into it, and the story merges into its base.
func TestStoryWithTaskWithoutProjectMerges(t *testing.T) {
	h := gitHarness(t)
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Story", Review: api.ReviewSkipped})
	h.startStory(s)
	st := int(s)
	research := h.noProject(api.CreateTicket{Title: "Research", Story: &st, Review: api.ReviewSkipped})
	code := h.storyTask(s, h.project, "Write the code")
	h.approve(research)
	h.finishTask(code, h.project, "code.txt", "code\n")
	h.step()
	write(t, h.workdir(research), "findings.md", "use x\n")
	h.handIn(research)
	h.steps(3)
	for _, n := range []int64{research, code} {
		if tk := h.ticket(n); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
			t.Fatalf("task #%d is %s/%s", n, tk.Column, tk.Resolution)
		}
	}
	integration := h.ticket(s).Projects[0].Branch
	if subjectCount(t, h.repo, integration, "Merge: Write the code") != 1 || subjectCount(t, h.repo, integration, "Merge: Research") != 0 {
		t.Fatalf("integration branch log:\n%s", gitRun(t, h.repo, "log", "--format=%s", integration))
	}
	h.handIn(s)
	h.steps(3)
	if tk := h.ticket(s); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
		t.Fatalf("story is %s/%s", tk.Column, tk.Resolution)
	}
	if subjectCount(t, h.repo, "main", "Merge: Story") != 1 {
		t.Fatalf("main log:\n%s", gitRun(t, h.repo, "log", "--format=%s", "main"))
	}
	if !pathExists(filepath.Join(h.workdir(research), "findings.md")) {
		t.Fatal("the research task's working folder is gone")
	}
}

// A folder where the working folder would go that omo did not record is
// never taken over: the start fails for a person.
func TestForeignFolderInPlaceOfTheWorkingFolderFailsTheStart(t *testing.T) {
	h := newHarness(t)
	n := h.noProject(api.CreateTicket{Title: "Taken"})
	if err := os.MkdirAll(h.workdir(n), 0o700); err != nil {
		t.Fatal(err)
	}
	h.approve(n)
	h.step()
	tk := h.ticket(n)
	if tk.FailedStep != api.FailedStart || !strings.Contains(tk.Attention, "already exists") || tk.Workdir != "" {
		t.Fatalf("failed %q, attention %q, workdir %q", tk.FailedStep, tk.Attention, tk.Workdir)
	}
}

// The working folder is recorded before it is made: a start interrupted
// in between (omo stopped) leaves a recorded folder that the next start
// makes and goes on with, never an unrecorded one that stops the ticket.
func TestStartContinuesWithRecordedWorkingFolder(t *testing.T) {
	h := newHarness(t)
	n := h.noProject(api.CreateTicket{Title: "Interrupted"})
	h.approve(n)
	checked := false
	h.e.pause = func(point string) {
		if point == "workdir-recorded" {
			checked = true
			if tk := h.ticket(n); tk.Workdir != h.workdir(n) || pathExists(h.workdir(n)) {
				t.Errorf("before the folder is made: recorded %q, exists %v", tk.Workdir, pathExists(h.workdir(n)))
			}
		}
	}
	h.step()
	h.e.pause = nil
	if !checked {
		t.Fatal("the start did not record the working folder")
	}

	// Another task as omo left it: recorded, not made, still Ready.
	m := h.noProject(api.CreateTicket{Title: "Left"})
	if err := h.b.SetWorkdir(h.ctx, m, h.workdir(m)); err != nil {
		t.Fatal(err)
	}
	h.approve(m)
	h.step()
	tk := h.ticket(m)
	if tk.FailedStep != api.FailedNone || tk.Column != api.ColumnInProgress || tk.Workdir != h.workdir(m) || !pathExists(h.workdir(m)) {
		t.Fatalf("failed %q (%s), column %s, workdir %q", tk.FailedStep, tk.Attention, tk.Column, tk.Workdir)
	}
	h.live(api.RoleDeveloper, m)
}
