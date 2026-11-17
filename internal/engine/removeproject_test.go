package engine

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// stoppedStory returns a story over every project of h whose product
// manager started (so every project has its branch and worktree) and that
// is back in Planning, its agent ended.
func (h *harness) stoppedStory() int64 {
	h.t.Helper()
	s := h.story("Several repositories", h.ids...)
	h.startStory(s)
	h.e.end(h.ctx, h.live(api.RolePM, s).ID, "test")
	h.inject(fmt.Sprintf(`UPDATE tickets SET col = 'planning' WHERE id = %d`, s))
	for _, p := range h.ticket(s).Projects {
		if p.Branch == "" || !pathExists(p.Worktree) {
			h.t.Fatalf("project %s has no branch or worktree: %+v", p.Project, p)
		}
	}
	return s
}

// removeProject removes project from n as the user.
func (h *harness) removeProject(n int64, project string, discard bool) (api.Ticket, error) {
	return h.e.RemoveProject(h.ctx, board.User, n, api.RemoveProjectRequest{Project: project, Discard: discard})
}

func (h *harness) projectOf(n int64, project string) (api.TicketProject, bool) {
	for _, p := range h.ticket(n).Projects {
		if p.Project == project {
			return p, true
		}
	}
	return api.TicketProject{}, false
}

func TestRemoveProjectDeletesWorktreeAndBranch(t *testing.T) {
	h := gitHarness(t, withProjects(3))
	s := h.stoppedStory()
	p, _ := h.projectOf(s, h.ids[1])
	repo := h.repos[h.ids[1]]
	tip := gitRun(t, repo, "rev-parse", "refs/heads/"+p.Branch)

	got, err := h.removeProject(s, h.ids[1], false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 2 || got.Busy {
		t.Fatalf("projects %+v busy %v", got.Projects, got.Busy)
	}
	if pathExists(p.Worktree) || slices.Contains(h.branches(repo), p.Branch) {
		t.Fatal("the worktree or the branch is left")
	}
	if list := gitRun(t, repo, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Fatalf("worktrees left registered:\n%s", list)
	}
	body := h.lastComment(s).Body
	for _, want := range []string{"The user removed project `" + h.ids[1] + "`", "omo removed its worktree", "omo deleted its branch", tip} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment lacks %q:\n%s", want, body)
		}
	}
	// The other projects keep their work.
	for _, id := range []string{h.ids[0], h.ids[2]} {
		if q, ok := h.projectOf(s, id); !ok || !pathExists(q.Worktree) || !slices.Contains(h.branches(h.repos[id]), q.Branch) {
			t.Fatalf("project %s lost its work: %+v", id, q)
		}
	}
}

func TestRemoveProjectWithCommitsNeedsDiscard(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	s := h.stoppedStory()
	id := h.ids[1]
	p, _ := h.projectOf(s, id)
	repo := h.repos[id]
	work := commit(t, p.Worktree, "work.txt", "work\n")

	_, err := h.removeProject(s, id, false)
	var e *api.Error
	if !errors.As(err, &e) || e.Code != api.CodeConflict {
		t.Fatalf("error %v", err)
	}
	w, ok := e.Details.(api.ProjectWork)
	if !ok || len(w.Commits) != 1 || !strings.HasPrefix(w.Commits[0], work[:12]+" change work.txt") || len(w.Files) != 0 ||
		w.Branch != p.Branch || w.Base != "main" || w.Project != id {
		t.Fatalf("details %#v", e.Details)
	}
	if !strings.Contains(e.Message, "--discard") {
		t.Fatalf("message %q", e.Message)
	}
	if q, ok := h.projectOf(s, id); !ok || !pathExists(q.Worktree) || !slices.Contains(h.branches(repo), q.Branch) {
		t.Fatal("a refused removal changed something")
	}
	h.checkClaimEnded(s)

	if _, err := h.removeProject(s, id, true); err != nil {
		t.Fatal(err)
	}
	if pathExists(p.Worktree) || slices.Contains(h.branches(repo), p.Branch) {
		t.Fatal("the worktree or the branch is left")
	}
	body := h.lastComment(s).Body
	if !strings.Contains(body, "were not in `main`") || !strings.Contains(body, work[:12]) {
		t.Fatalf("comment does not list the deleted commits:\n%s", body)
	}
}

func TestRemoveProjectWithUncommittedChangesNeedsDiscard(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	s := h.stoppedStory()
	id := h.ids[1]
	p, _ := h.projectOf(s, id)
	write(t, p.Worktree, "scratch.txt", "not committed\n")

	_, err := h.removeProject(s, id, false)
	var e *api.Error
	if !errors.As(err, &e) || e.Code != api.CodeConflict {
		t.Fatalf("error %v", err)
	}
	if w := e.Details.(api.ProjectWork); len(w.Files) != 1 || w.Files[0] != "scratch.txt" || len(w.Commits) != 0 {
		t.Fatalf("details %#v", w)
	}
	if !pathExists(p.Worktree + "/scratch.txt") {
		t.Fatal("the uncommitted file is gone")
	}
	if _, err := h.removeProject(s, id, true); err != nil {
		t.Fatal(err)
	}
	if pathExists(p.Worktree) {
		t.Fatal("the worktree is left")
	}
	if body := h.lastComment(s).Body; !strings.Contains(body, "discarded these uncommitted changes") || !strings.Contains(body, "scratch.txt") {
		t.Fatalf("comment:\n%s", body)
	}
}

// A branch of the same name that another ticket records in the repository
// is not the ticket's own: it is kept, whatever it holds, and no discard is
// asked for since nothing of it is lost.
func TestRemoveProjectKeepsBranchItDoesNotOwn(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	s := h.stoppedStory()
	id := h.ids[1]
	p, _ := h.projectOf(s, id)
	repo := h.repos[id]
	other, err := h.b.Create(h.ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "Other", Projects: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.b.SetBranch(h.ctx, int64(other.Number), id, p.Branch, ""); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "update-ref", "refs/heads/"+p.Branch, commitOnSide(t, repo))
	tip := gitRun(t, repo, "rev-parse", "refs/heads/"+p.Branch)
	// The worktree still has its own HEAD checked out; it is clean.
	gitRun(t, p.Worktree, "reset", "-q", "--hard", tip)

	if _, err := h.removeProject(s, id, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.branches(repo), p.Branch) || gitRun(t, repo, "rev-parse", "refs/heads/"+p.Branch) != tip {
		t.Fatal("omo deleted or moved a branch it does not own")
	}
	if pathExists(p.Worktree) {
		t.Fatal("the clean worktree is left")
	}
	if body := h.lastComment(s).Body; !strings.Contains(body, "omo kept the branch `"+p.Branch+"`") || !strings.Contains(body, fmt.Sprintf("#%d", other.Number)) {
		t.Fatalf("comment:\n%s", body)
	}
}

// commitOnSide makes a commit on top of main in repo without moving any
// branch and returns it.
func commitOnSide(t *testing.T, repo string) string {
	t.Helper()
	tree := gitRun(t, repo, "rev-parse", "main^{tree}")
	return gitRun(t, repo, "commit-tree", "-p", "main", "-m", "someone else's", tree)
}

func TestRemoveProjectRefusesHeldWorktree(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	s := h.stoppedStory()
	id := h.ids[1]
	p, _ := h.projectOf(s, id)
	release := h.e.HoldWorktree(p.Worktree)
	_, err := h.removeProject(s, id, false)
	var e *api.Error
	if !errors.As(err, &e) || e.Code != api.CodeConflict || !strings.Contains(e.Message, "terminal") {
		t.Fatalf("error %v", err)
	}
	if !pathExists(p.Worktree) || len(h.ticket(s).Projects) != 2 {
		t.Fatal("a held worktree was removed")
	}
	h.checkClaimEnded(s)
	release()
	if _, err := h.removeProject(s, id, false); err != nil {
		t.Fatal(err)
	}
}

// In Review waiting for the user, the product manager waits in the first
// project's worktree: removing that project ends it first. A running one
// refuses the removal.
func TestRemoveProjectEndsWaitingAgentsAndRefusesWorkingOnes(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	s := h.story("Two repositories", h.ids...)
	h.startStory(s)
	pm := h.live(api.RolePM, s)
	h.inject(fmt.Sprintf(`UPDATE tickets SET col = 'review', review_stage = 'human', review_round = 1 WHERE id = %d`, s))
	if err := h.b.SetSessionState(h.ctx, pm.ID, api.SessionRunning); err != nil {
		t.Fatal(err)
	}
	_, err := h.removeProject(s, h.ids[0], false)
	var e *api.Error
	if !errors.As(err, &e) || e.Code != api.CodeConflict || !strings.Contains(e.Message, pm.Name) {
		t.Fatalf("error %v", err)
	}
	if h.agent(pm.ID).isKilled() {
		t.Fatal("a refused removal ended the working agent")
	}
	if err := h.b.SetSessionState(h.ctx, pm.ID, api.SessionWaiting); err != nil {
		t.Fatal(err)
	}
	p, _ := h.projectOf(s, h.ids[0])
	if _, err := h.removeProject(s, h.ids[0], false); err != nil {
		t.Fatal(err)
	}
	if !h.agent(pm.ID).isKilled() {
		t.Fatal("the waiting product manager was not ended")
	}
	if _, ok := h.liveOK(api.RolePM, s); ok {
		t.Fatal("the product manager's session is still live")
	}
	if pathExists(p.Worktree) {
		t.Fatal("the worktree is left")
	}
	if reason := h.endReasonOf(pm.ID); !strings.Contains(reason, "was removed from #") {
		t.Fatalf("end reason %q", reason)
	}
	if body := h.lastComment(s).Body; !strings.Contains(body, "omo ended "+pm.Name) {
		t.Fatalf("comment:\n%s", body)
	}
}

// A project whose folder is gone cannot be checked: its record goes, its
// worktree folder stays where it is and the comment says so.
func TestRemoveMissingProjectKeepsItsFolder(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	s := h.stoppedStory()
	id := h.ids[1]
	p, _ := h.projectOf(s, id)
	if err := os.Rename(h.repos[id], h.repos[id]+".moved"); err != nil {
		t.Fatal(err)
	}
	got, err := h.removeProject(s, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 {
		t.Fatalf("projects %+v", got.Projects)
	}
	if !pathExists(p.Worktree) {
		t.Fatal("omo removed a folder it could not check")
	}
	if body := h.lastComment(s).Body; !strings.Contains(body, "folder is missing") || !strings.Contains(body, p.Worktree) || !strings.Contains(body, p.Branch) {
		t.Fatalf("comment:\n%s", body)
	}
}

// Everything kept for a removed project goes with it: what was handed in,
// pushed, approved, left out or failed.
func TestRemoveProjectForgetsItsPins(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	s := h.stoppedStory()
	id := h.ids[1]
	pins := h.e.pins
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pins.setReviewed(s, 1, map[string]string{h.ids[0]: "a", id: "b"}))
	must(pins.setApprovals(s, map[string]approval{h.ids[0]: {}, id: {}}))
	must(pins.setPushed(s, id, pushRecord{Commit: "b"}))
	must(pins.setLeftOut(s, 1, id))
	must(pins.setExportFailed(s, id, "no remote"))
	must(pins.setSquashed(s, id, "c"))
	if _, err := h.removeProject(s, id, false); err != nil {
		t.Fatal(err)
	}
	reviewed, _ := pins.reviewed(s, 1)
	approvals, _ := pins.approvals(s)
	_, pushed, _ := pins.pushed(s, id)
	left, failed, _ := pins.exportOutcome(s, 1, id)
	squashed, _ := pins.squashed(s, id)
	if _, ok := reviewed[id]; ok || reviewed[h.ids[0]] != "a" {
		t.Fatalf("reviewed %v", reviewed)
	}
	if _, ok := approvals[id]; ok || len(approvals) != 1 {
		t.Fatalf("approvals %v", approvals)
	}
	if pushed || left || failed != "" || squashed != "" {
		t.Fatalf("pushed %v left %v failed %q squashed %q", pushed, left, failed, squashed)
	}
}

// prune drops what is kept for a project a ticket no longer has, however
// the project left it.
func TestPrunePinsOfProjectsNoLongerOnTheTicket(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	s := h.stoppedStory()
	pins := h.e.pins
	if err := pins.setApprovals(s, map[string]approval{h.ids[0]: {}, "gone": {}}); err != nil {
		t.Fatal(err)
	}
	if err := pins.prune(h.snap()); err != nil {
		t.Fatal(err)
	}
	if a, _ := pins.approvals(s); len(a) != 1 {
		t.Fatalf("approvals %v", a)
	}
}

// A story whose export failed for a project without a usable remote: the
// failure says what the user can do, and once that project is off the
// ticket, exporting again finishes the export with the others.
func TestExportAfterRemovingProjectWithoutRemote(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	a, b := h.ids[0], h.ids[1]
	h.origin(a)
	// b names a remote origin without an address, as a repository can.
	gitRun(t, h.repos[b], "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
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
	tk := h.ticket(s)
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageHuman || !strings.Contains(tk.Attention, "Export of "+b+" failed") {
		t.Fatalf("ticket is %s/%s attention %q", tk.Column, tk.ReviewStage, tk.Attention)
	}
	failure := strings.Join(h.comments(s), "\n---\n")
	for _, want := range []string{"no address for the remote origin", "git -C " + h.repos[b] + " remote add origin", "omo ai ticket remove-project " + fmt.Sprint(s) + " " + b} {
		if !strings.Contains(failure, want) {
			t.Fatalf("the failure lacks %q:\n%s", want, failure)
		}
	}

	// The pm waits parked in the first project's worktree, not in b's.
	if _, err := h.removeProject(s, b, true); err != nil {
		t.Fatal(err)
	}
	tk = h.ticket(s)
	if tk.Attention != "" || len(tk.Projects) != 1 || tk.Projects[0].ExportURL != "https://example.invalid/a/pull/1" {
		t.Fatalf("after the removal: attention %q projects %+v", tk.Attention, tk.Projects)
	}
	h.exportNow(s)
	h.steps(2)
	if tk = h.ticket(s); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionExported || tk.ExportURL != "https://example.invalid/a/pull/1" {
		t.Fatalf("ticket is %s/%s url %q", tk.Column, tk.Resolution, tk.ExportURL)
	}
}

// A removal requested while omo starts (a tab reconnected at once) keeps
// its claim: the start's release of the claims a crash left waits for it.
func TestRemoveProjectKeepsItsClaimWhileARunStarts(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	s := h.stoppedStory()
	startupDone := make(chan struct{})
	h.e.pause = func(point string) {
		if point != "project-removal-claimed" {
			return
		}
		go func() {
			h.e.startup(h.ctx)
			close(startupDone)
		}()
		select {
		case <-startupDone:
			t.Error("the start released the claims during a removal")
		case <-time.After(100 * time.Millisecond):
		}
		if !h.ticket(s).Busy {
			t.Error("the removal's claim was released")
		}
	}
	got, err := h.removeProject(s, h.ids[1], false)
	h.e.pause = nil
	if err != nil {
		t.Fatal(err)
	}
	h.awaitClosed("the start", startupDone)
	if len(got.Projects) != 1 || h.ticket(s).Busy {
		t.Fatalf("projects %+v busy %v", got.Projects, h.ticket(s).Busy)
	}
}
