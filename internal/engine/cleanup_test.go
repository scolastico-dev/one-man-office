package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/claudetrust"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// cancel cancels n as the user and runs the passes that end its agents and
// clean it up.
func (h *harness) cancel(n int64, req api.CancelRequest) {
	h.t.Helper()
	if _, err := h.b.Cancel(h.ctx, board.User, n, req); err != nil {
		h.t.Fatal(err)
	}
	h.steps(3)
}

// startedTask returns a standalone task in progress with one commit, its
// worktree and branch.
func (h *harness) startedTask(title string) (int64, string, string) {
	h.t.Helper()
	n := h.skippedTask(title)
	h.startTask(n, h.project, "work.txt", "work\n")
	return n, h.worktree(h.project, n), h.ticket(n).Projects[0].Branch
}

func TestWorktreeWithUncommittedChangesIsNeverRemovedAfterMerge(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	dir := h.worktree(h.project, n)
	branch := h.ticket(n).Projects[0].Branch
	write(t, dir, "scratch.txt", "not committed\n")
	h.steps(4)
	h.e.healthTick(h.ctx, h.clock.Now()) // cleanups are tried again
	h.e.settle()
	h.steps(2)

	tk := h.ticket(n)
	if tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
		t.Fatalf("ticket is %s/%s", tk.Column, tk.Resolution)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "scratch.txt")); err != nil || string(got) != "not committed\n" {
		t.Fatalf("scratch.txt = %q, %v", got, err)
	}
	if !slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("the branch of the kept worktree was deleted")
	}
	if p := tk.Projects[0]; p.Worktree != dir || p.Branch != branch {
		t.Fatalf("ticket records %q %q", p.Worktree, p.Branch)
	}
	if c := h.commentsWith(n, "omo kept the worktree"); c != 1 {
		t.Fatalf("%d comments about the kept worktree, want 1", c)
	}
	body := h.lastComment(n).Body
	if !strings.Contains(body, "`scratch.txt`") || !strings.Contains(body, dir) {
		t.Fatalf("comment:\n%s", body)
	}

	// Once the user removed the file, the worktree goes and the merged
	// branch with it.
	if err := os.Remove(filepath.Join(dir, "scratch.txt")); err != nil {
		t.Fatal(err)
	}
	h.e.healthTick(h.ctx, h.clock.Now())
	h.e.settle()
	h.steps(1)
	if pathExists(dir) || slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("the clean worktree or its merged branch was not removed")
	}
}

func TestHeldWorktreeRemovedOnRelease(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	dir := h.worktree(h.project, n)
	release := h.e.HoldWorktree(dir)
	release2 := h.e.HoldWorktree(dir + "/")
	h.steps(3)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s; a hold must not stop the merge", c)
	}
	if !pathExists(dir) {
		t.Fatal("a held worktree was removed")
	}
	release()
	release() // a second call releases nothing
	h.steps(2)
	if !pathExists(dir) {
		t.Fatal("removed while another hold is open")
	}
	release2()
	select {
	case <-h.e.wake:
	default:
		t.Fatal("the last release did not wake the engine")
	}
	h.step()
	if pathExists(dir) {
		t.Fatal("the worktree stayed after the last hold was released")
	}
	if p := h.ticket(n).Projects[0]; p.Worktree != "" || p.Branch != "" {
		t.Fatalf("ticket records %q %q", p.Worktree, p.Branch)
	}
}

func TestCancelDeleteBranchRemovesCleanWorktreeAndBranch(t *testing.T) {
	h := gitHarness(t)
	n, dir, branch := h.startedTask("Abandoned")
	tip := gitRun(t, h.repo, "rev-parse", branch)
	h.cancel(n, api.CancelRequest{DeleteBranch: true})

	if pathExists(dir) || slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("worktree or branch left")
	}
	if p := h.ticket(n).Projects[0]; p.Worktree != "" || p.Branch != "" {
		t.Fatalf("ticket records %q %q", p.Worktree, p.Branch)
	}
	if body := h.lastComment(n).Body; !strings.Contains(body, tip) {
		t.Fatalf("comment does not say where the branch was:\n%s", body)
	}
}

func TestCancelDeleteBranchKeepsDirtyWorktreeAndBranch(t *testing.T) {
	h := gitHarness(t)
	n, dir, branch := h.startedTask("Abandoned")
	write(t, dir, "new.txt", "precious\n")
	before := repoState(t, dir)
	h.cancel(n, api.CancelRequest{DeleteBranch: true})
	h.e.healthTick(h.ctx, h.clock.Now())
	h.e.settle()
	h.steps(1)

	if tk := h.ticket(n); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCancelled {
		t.Fatalf("ticket is %s/%s", tk.Column, tk.Resolution)
	}
	delete(before, ".git")
	after := repoState(t, dir)
	delete(after, ".git")
	sameState(t, "worktree", before, after)
	if !slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("the branch was deleted")
	}
	if c := h.commentsWith(n, "discarded nothing"); c != 1 {
		t.Fatalf("%d comments about the kept worktree, want 1", c)
	}
	body := h.lastComment(n).Body
	if !strings.Contains(body, "`new.txt`") || !strings.Contains(body, dir) || !strings.Contains(body, branch) {
		t.Fatalf("comment:\n%s", body)
	}
}

func TestCancelDiscardUncommittedDeletesEverything(t *testing.T) {
	h := gitHarness(t)
	n, dir, branch := h.startedTask("Abandoned")
	write(t, dir, "new.txt", "scratch\n")
	write(t, dir, "a.txt", "edited\n")
	h.cancel(n, api.CancelRequest{DeleteBranch: true, DiscardUncommitted: true})

	if pathExists(dir) || slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("worktree or branch left")
	}
	body := h.lastComment(n).Body
	if !strings.Contains(body, "`new.txt`") || !strings.Contains(body, "`a.txt`") || !strings.Contains(body, "discarded") {
		t.Fatalf("comment does not list the discarded files:\n%s", body)
	}
}

func TestCancelDiscardKeepsCommitsOnlyHeadHolds(t *testing.T) {
	h := gitHarness(t)
	n, dir, branch := h.startedTask("Abandoned")
	gitRun(t, dir, "switch", "-q", "--detach")
	commit(t, dir, "detached.txt", "only here\n")
	h.cancel(n, api.CancelRequest{DeleteBranch: true, DiscardUncommitted: true})

	if !pathExists(filepath.Join(dir, "detached.txt")) || !slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("a worktree holding commits no branch has was removed")
	}
	if body := h.lastComment(n).Body; !strings.Contains(body, "holds commits") {
		t.Fatalf("comment:\n%s", body)
	}
}

func TestIgnoredFilesDoNotBlockRemoval(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Build")
	h.startTask(n, h.project, ".gitignore", "build/\n")
	dir := h.worktree(h.project, n)
	write(t, dir, "build/out.bin", "generated\n")
	h.handIn(n)
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s", c)
	}
	if pathExists(dir) {
		t.Fatal("ignored files kept the worktree")
	}
}

func TestRemovalForgetsClaudeTrust(t *testing.T) {
	h := gitHarness(t)
	account := filepath.Join(t.TempDir(), "claude-account")
	if err := h.st.PutProfile(settings.Profile{Name: "claude", CLI: api.CLIClaude, Command: "claude", AccountDir: account,
		Models: map[string]settings.Model{"m": {}}}); err != nil {
		t.Fatal(err)
	}
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	dir := h.worktree(h.project, n)
	env := map[string]string{"CLAUDE_CONFIG_DIR": account}
	if err := claudetrust.EnsureForEnv(env, dir); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	if err := claudetrust.EnsureForEnv(env, other); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if pathExists(dir) {
		t.Fatal("worktree not removed")
	}
	raw, err := os.ReadFile(filepath.Join(account, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Projects map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Projects[dir]; ok {
		t.Fatal("the trust entry of the removed worktree is still there")
	}
	if _, ok := cfg.Projects[other]; !ok {
		t.Fatal("another folder's trust entry was removed")
	}
}

func TestCancelKeepBranchKeepsDirtyWorktree(t *testing.T) {
	h := gitHarness(t)
	n, dir, branch := h.startedTask("Paused idea")
	write(t, dir, "notes.txt", "keep me\n")
	h.cancel(n, api.CancelRequest{})
	if !pathExists(filepath.Join(dir, "notes.txt")) || !slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("worktree or branch removed")
	}
	if c := h.commentsWith(n, "omo kept the worktree"); c != 1 {
		t.Fatalf("%d comments, want 1", c)
	}

	// Clean, the worktree goes and the branch stays.
	if err := os.Remove(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	h.e.healthTick(h.ctx, h.clock.Now())
	h.e.settle()
	h.step()
	if pathExists(dir) || !slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("want the worktree removed and the branch kept")
	}
	if p := h.ticket(n).Projects[0]; p.Worktree != "" || p.Branch != branch {
		t.Fatalf("ticket records %q %q", p.Worktree, p.Branch)
	}
}

func TestCancelStoryDeleteBranchNamesDiscardedTasks(t *testing.T) {
	h := gitHarness(t)
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Story", Review: api.ReviewSkipped})
	h.startStory(s)
	task := h.storyTask(s, h.project, "Merged task")
	h.finishTask(task, h.project, "t.txt", "t\n")
	h.steps(2)
	integration := h.ticket(s).Projects[0].Branch
	tip := gitRun(t, h.repo, "rev-parse", integration)
	h.cancel(s, api.CancelRequest{DeleteBranch: true})

	if slices.Contains(h.branches(h.repo), integration) || pathExists(h.worktree(h.project, s)) {
		t.Fatal("integration branch or worktree left")
	}
	body := h.lastComment(s).Body
	if !strings.Contains(body, tip) || !strings.Contains(body, "#"+strconv.FormatInt(task, 10)) {
		t.Fatalf("comment does not name the discarded task work:\n%s", body)
	}
}

func TestCancelStoryKeepsIntegrationBranch(t *testing.T) {
	h := gitHarness(t)
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Story", Review: api.ReviewSkipped})
	h.startStory(s)
	task := h.storyTask(s, h.project, "Merged task")
	h.finishTask(task, h.project, "t.txt", "t\n")
	h.steps(2)
	integration := h.ticket(s).Projects[0].Branch
	h.cancel(s, api.CancelRequest{})
	if !slices.Contains(h.branches(h.repo), integration) || pathExists(h.worktree(h.project, s)) {
		t.Fatal("want the integration branch kept and its clean worktree removed")
	}
}

func TestLockedWorktreeIsKeptOnDiscard(t *testing.T) {
	h := gitHarness(t)
	n, dir, branch := h.startedTask("Locked")
	write(t, dir, "new.txt", "x\n")
	gitRun(t, h.repo, "worktree", "lock", dir)
	h.cancel(n, api.CancelRequest{DeleteBranch: true, DiscardUncommitted: true})
	if !pathExists(filepath.Join(dir, "new.txt")) || !slices.Contains(h.branches(h.repo), branch) {
		t.Fatal("a locked worktree was removed")
	}
	if body := h.lastComment(n).Body; !strings.Contains(body, "locked") {
		t.Fatalf("comment:\n%s", body)
	}
}
