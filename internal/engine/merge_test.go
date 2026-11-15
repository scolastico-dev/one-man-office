package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// ---- helpers for merge, cleanup, export and hand-in tests ----

// needGit skips a test when the git on PATH is older than omo needs.
func needGit(t *testing.T) {
	t.Helper()
	major, minor, err := gitops.New().Version()
	if err != nil {
		t.Skipf("git is not usable: %v", err)
	}
	if err := gitops.CheckVersion(major, minor); err != nil {
		t.Skipf("skipped: %v", err)
	}
}

// gitHarness is newHarness for tests that merge: it skips without a git
// new enough.
func gitHarness(t *testing.T, opts ...func(*harnessConfig)) *harness {
	t.Helper()
	needGit(t)
	return newHarness(t, opts...)
}

// create creates a ticket as the user and returns its number.
func (h *harness) create(req api.CreateTicket) int64 {
	h.t.Helper()
	if len(req.Projects) == 0 {
		req.Projects = []string{h.project}
	}
	tk, err := h.b.Create(h.ctx, board.User, req)
	if err != nil {
		h.t.Fatal(err)
	}
	return int64(tk.Number)
}

// skippedTask creates a standalone task without review.
func (h *harness) skippedTask(title string) int64 {
	return h.create(api.CreateTicket{Type: api.TypeTask, Title: title, Review: api.ReviewSkipped})
}

// storyTask creates a task without review in story, in project.
func (h *harness) storyTask(story int64, project, title string) int64 {
	s := int(story)
	return h.create(api.CreateTicket{Type: api.TypeTask, Title: title, Projects: []string{project}, Story: &s, Review: api.ReviewSkipped})
}

// write writes a file below dir.
func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commit writes name with content in dir and commits it.
func commit(t *testing.T, dir, name, content string) string {
	t.Helper()
	write(t, dir, name, content)
	gitRun(t, dir, "add", name)
	gitRun(t, dir, "commit", "-q", "-m", "change "+name)
	return gitRun(t, dir, "rev-parse", "HEAD")
}

// worktree is the worktree folder of ticket n in project.
func (h *harness) worktree(project string, n int64) string {
	return WorktreeDir(h.layout.Worktrees, project, n)
}

// handIn hands ticket n in as its working agent.
func (h *harness) handIn(n int64) {
	h.t.Helper()
	tk := h.ticket(n)
	role := api.RoleDeveloper
	if tk.Type == api.TypeStory {
		role = api.RolePM
	}
	s := h.live(role, n)
	req := api.DoneRequest{Comment: "finished"}
	// As the server does: the hand-in is checked, then recorded.
	if err := h.e.CheckHandIn(h.ctx, h.actor(s.ID), req); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.b.Done(h.ctx, h.actor(s.ID), req); err != nil {
		h.t.Fatal(err)
	}
}

// steps runs n passes, each waiting for its workers.
func (h *harness) steps(n int) {
	for range n {
		h.step()
	}
}

// startTask approves task n, starts its developer and commits name in its
// worktree.
func (h *harness) startTask(n int64, project, name, content string) string {
	h.t.Helper()
	h.approve(n)
	h.step()
	if c := h.ticket(n).Column; c != api.ColumnInProgress {
		h.t.Fatalf("#%d is in %s after its start", n, c)
	}
	return commit(h.t, h.worktree(project, n), name, content)
}

// finishTask starts task n, commits name and hands it in; the next steps
// merge it.
func (h *harness) finishTask(n int64, project, name, content string) {
	h.t.Helper()
	h.startTask(n, project, name, content)
	h.handIn(n)
}

// startStory approves story s and starts its product manager.
func (h *harness) startStory(s int64) {
	h.t.Helper()
	h.approve(s)
	h.step()
	h.live(api.RolePM, s)
}

// mergeCount counts the merge commits reachable from branch.
func mergeCount(t *testing.T, repo, branch string) int {
	t.Helper()
	n, err := strconv.Atoi(gitRun(t, repo, "rev-list", "--merges", "--count", branch))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// subjectCount counts the commits reachable from branch whose subject is
// subject.
func subjectCount(t *testing.T, repo, branch, subject string) int {
	t.Helper()
	n := 0
	for _, s := range strings.Split(gitRun(t, repo, "log", "--format=%s", branch), "\n") {
		if s == subject {
			n++
		}
	}
	return n
}

// repoState captures everything of a repository folder a merge could
// change: every file below it (the git directory included, except the
// object store, which only ever gains objects, and logs), byte for byte.
// ORIG_HEAD is included: a fast-forward omo itself refuses writes nothing.
func repoState(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git/objects" || rel == ".git/logs" || rel == ".git/worktrees" {
				return filepath.SkipDir
			}
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameState(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for k, v := range before {
		if w, ok := after[k]; !ok {
			t.Errorf("%s: %s disappeared", what, k)
		} else if w != v {
			t.Errorf("%s: %s changed:\nbefore %q\nafter  %q", what, k, v, w)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("%s: %s appeared", what, k)
		}
	}
}

// lastComment returns the newest comment of a ticket.
func (h *harness) lastComment(n int64) api.Comment {
	h.t.Helper()
	cs, err := h.b.Comments(h.ctx, board.User, n)
	if err != nil {
		h.t.Fatal(err)
	}
	if len(cs) == 0 {
		h.t.Fatalf("#%d has no comment", n)
	}
	return cs[len(cs)-1]
}

// commentsWith counts the comments of n that contain text.
func (h *harness) commentsWith(n int64, text string) int {
	count := 0
	for _, c := range h.comments(n) {
		if strings.Contains(c, text) {
			count++
		}
	}
	return count
}

// restart replaces the engine by a fresh one on the same board, as a new
// omo process would: claims and sessions of the old run are released.
func (h *harness) restart() {
	h.t.Helper()
	old := h.engine()
	old.halt(context.Background(), "omo stopped")
	e := New(Options{
		Board: h.b, Settings: h.st, Git: h.git, Layout: h.layout, Runner: h.runner,
		Socket: func() string { return "" }, Log: log.New(h.logs, "", 0), Now: h.clock.Now,
	})
	e.minWait = 0
	h.mu.Lock()
	h.e = e
	h.mu.Unlock()
	h.t.Cleanup(func() { e.halt(context.Background(), "test over") })
	e.startup(h.ctx)
}

// checkClaimEnded fails when n is still claimed.
func (h *harness) checkClaimEnded(n int64) {
	h.t.Helper()
	if h.ticket(n).Busy {
		h.t.Fatalf("#%d is still claimed", n)
	}
}

// ---- merges ----

func TestTaskMergesIntoIntegrationBranchAndCleansUp(t *testing.T) {
	h := gitHarness(t)
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Story", Review: api.ReviewSkipped})
	h.startStory(s)
	mainBefore := gitRun(t, h.repo, "rev-parse", "main")
	task := h.storyTask(s, h.project, "Write the docs")
	h.finishTask(task, h.project, "docs.txt", "docs\n")
	taskDir := h.worktree(h.project, task)
	taskBranch := h.ticket(task).Projects[0].Branch
	h.steps(2)

	tk := h.ticket(task)
	if tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
		t.Fatalf("task is %s/%s, want done/completed", tk.Column, tk.Resolution)
	}
	h.checkClaimEnded(task)
	integration := h.ticket(s).Projects[0].Branch
	if msg := gitRun(t, h.repo, "log", "-1", "--format=%s", integration); msg != fmt.Sprintf("Merge #%d: Write the docs", task) {
		t.Fatalf("integration head message %q", msg)
	}
	if parents := strings.Fields(gitRun(t, h.repo, "log", "-1", "--format=%P", integration)); len(parents) != 2 {
		t.Fatalf("integration head has parents %v, want a merge commit", parents)
	}
	intDir := h.worktree(h.project, s)
	if got, _ := os.ReadFile(filepath.Join(intDir, "docs.txt")); string(got) != "docs\n" {
		t.Fatalf("integration worktree was not fast-forwarded: docs.txt = %q", got)
	}
	if out := gitRun(t, intDir, "status", "--porcelain"); out != "" {
		t.Fatalf("integration worktree is dirty after the merge:\n%s", out)
	}
	if pathExists(taskDir) {
		t.Fatal("the task worktree is still there")
	}
	if slices.Contains(h.branches(h.repo), taskBranch) {
		t.Fatalf("task branch %s still exists", taskBranch)
	}
	if p := h.ticket(task).Projects[0]; p.Branch != "" || p.Worktree != "" {
		t.Fatalf("ticket still records branch %q worktree %q", p.Branch, p.Worktree)
	}
	if _, ok := h.liveOK(api.RoleDeveloper, task); ok {
		t.Fatal("the developer still runs after the merge")
	}
	if got := gitRun(t, h.repo, "rev-parse", "main"); got != mainBefore {
		t.Fatal("main changed when a task merged into its story")
	}
}

func TestStandaloneTaskMergesIntoUserCheckoutOnBase(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	write(t, h.repo, "notes.txt", "user's own untracked notes\n")
	h.steps(2)

	if tk := h.ticket(n); tk.Column != api.ColumnDone {
		t.Fatalf("ticket is %s, want done", tk.Column)
	}
	if got, _ := os.ReadFile(filepath.Join(h.repo, "b.txt")); string(got) != "b\n" {
		t.Fatalf("the user's folder does not have b.txt after the merge: %q", got)
	}
	if got := gitRun(t, h.repo, "log", "-1", "--format=%s", "HEAD"); got != fmt.Sprintf("Merge #%d: Add b", n) {
		t.Fatalf("HEAD message %q", got)
	}
	if out := gitRun(t, h.repo, "status", "--porcelain"); out != "?? notes.txt" {
		t.Fatalf("status of the user's folder:\n%s", out)
	}
	if author := gitRun(t, h.repo, "log", "-1", "--format=%an <%ae>"); author == "" {
		t.Fatal("merge commit has no author")
	}
}

func TestStoryMergeWhenCheckoutOnOtherBranch(t *testing.T) {
	h := gitHarness(t)
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Story", Review: api.ReviewSkipped})
	h.startStory(s)
	task := h.storyTask(s, h.project, "Task")
	h.finishTask(task, h.project, "t.txt", "t\n")
	h.steps(2)
	if c := h.ticket(task).Column; c != api.ColumnDone {
		t.Fatalf("task is %s", c)
	}
	gitRun(t, h.repo, "switch", "-q", "-c", "other")
	write(t, h.repo, "a.txt", "user's edit\n")
	before := repoState(t, h.repo)
	h.handIn(s)
	h.steps(2)

	if tk := h.ticket(s); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
		t.Fatalf("story is %s/%s", tk.Column, tk.Resolution)
	}
	if got := gitRun(t, h.repo, "show", "main:t.txt"); got != "t" {
		t.Fatalf("main:t.txt = %q", got)
	}
	if got := gitRun(t, h.repo, "branch", "--show-current"); got != "other" {
		t.Fatalf("folder switched to %q", got)
	}
	after := repoState(t, h.repo)
	// Only the ref of main and omo's own branches may change.
	for _, m := range []map[string]string{before, after} {
		for k := range m {
			if k == ".git/refs/heads/main" || strings.HasPrefix(k, ".git/refs/heads/omo/") {
				delete(m, k)
			}
		}
	}
	sameState(t, "user folder", before, after)
	if pathExists(h.worktree(h.project, s)) {
		t.Fatal("the integration worktree is still there")
	}
}

func TestStorySquashMerge(t *testing.T) {
	h := gitHarness(t)
	squash := api.StoryMergeSquash
	if _, err := h.st.UpdateProject(h.project, settings.ProjectUpdate{StoryMerge: &squash}); err != nil {
		t.Fatal(err)
	}
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Squashed story", Review: api.ReviewSkipped})
	h.startStory(s)
	t1 := h.storyTask(s, h.project, "First")
	h.finishTask(t1, h.project, "one.txt", "1\n")
	h.steps(2)
	t2 := h.storyTask(s, h.project, "Second")
	h.finishTask(t2, h.project, "two.txt", "2\n")
	h.steps(2)
	mainBefore := gitRun(t, h.repo, "rev-parse", "main")
	h.handIn(s)
	h.steps(2)

	if c := h.ticket(s).Column; c != api.ColumnDone {
		t.Fatalf("story is %s", c)
	}
	if n := gitRun(t, h.repo, "rev-list", "--count", mainBefore+"..main"); n != "1" {
		t.Fatalf("main gained %s commits, want 1", n)
	}
	if parents := strings.Fields(gitRun(t, h.repo, "log", "-1", "--format=%P", "main")); len(parents) != 1 {
		t.Fatalf("squash commit has parents %v", parents)
	}
	msg := gitRun(t, h.repo, "log", "-1", "--format=%B", "main")
	if !strings.HasPrefix(msg, fmt.Sprintf("Squashed story (#%d)", s)) ||
		!strings.Contains(msg, fmt.Sprintf("#%d: First", t1)) || !strings.Contains(msg, fmt.Sprintf("#%d: Second", t2)) {
		t.Fatalf("squash message:\n%s", msg)
	}
	for _, f := range []string{"one.txt", "two.txt"} {
		gitRun(t, h.repo, "cat-file", "-e", "main:"+f)
	}
	if len(h.branches(h.repo)) != 0 {
		t.Fatalf("branches left: %v", h.branches(h.repo))
	}
}

func TestMergeConflictSendsTaskBackWithFiles(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Edit a")
	h.finishTask(n, h.project, "a.txt", "task's a\n")
	commit(t, h.repo, "a.txt", "user's a\n")
	before := repoState(t, h.repo)
	h.step()

	tk := h.ticket(n)
	if tk.Column != api.ColumnInProgress || tk.FailedStep != api.FailedNone {
		t.Fatalf("ticket is %s (failed step %q), want in_progress", tk.Column, tk.FailedStep)
	}
	h.checkClaimEnded(n)
	c := h.lastComment(n)
	if !strings.Contains(c.Body, "`a.txt`") || !strings.Contains(c.Body, "`main`") {
		t.Fatalf("conflict comment does not name the file and branch:\n%s", c.Body)
	}
	if !slices.ContainsFunc(c.Mentions, func(m api.Mention) bool { return m.Role == api.RoleDeveloper }) {
		t.Fatalf("conflict comment does not mention the developer: %+v", c.Mentions)
	}
	sameState(t, "user folder", before, repoState(t, h.repo))
	if !pathExists(h.worktree(h.project, n)) {
		t.Fatal("the task worktree is gone")
	}
}

func TestStoryConflictCheckedForAllProjectsBeforeAnyMerge(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	a, b := h.ids[0], h.ids[1]
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{a, b}, Review: api.ReviewSkipped})
	h.startStory(s)
	ta := h.storyTask(s, a, "In a")
	h.finishTask(ta, a, "new.txt", "a\n")
	h.steps(2)
	commit(t, h.worktree(b, s), "a.txt", "story's a\n") // stands for a merged task in b
	commit(t, h.repos[b], "a.txt", "user's a\n")
	beforeA, beforeB := repoState(t, h.repos[a]), repoState(t, h.repos[b])
	h.handIn(s)
	h.step()

	tk := h.ticket(s)
	if tk.Column != api.ColumnInProgress {
		t.Fatalf("story is %s, want in_progress", tk.Column)
	}
	h.checkClaimEnded(s)
	sameState(t, "project a", beforeA, repoState(t, h.repos[a]))
	sameState(t, "project b", beforeB, repoState(t, h.repos[b]))
	c := h.lastComment(s)
	if !strings.Contains(c.Body, "`a.txt`") || !strings.Contains(c.Body, "Create a task in project `"+b+"`") {
		t.Fatalf("comment:\n%s", c.Body)
	}
	if !slices.ContainsFunc(c.Mentions, func(m api.Mention) bool { return m.Role == api.RolePM }) {
		t.Fatalf("conflict comment does not mention the pm: %+v", c.Mentions)
	}
}

func TestBlockedMergeStaysInReviewWithRetry(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Edit a")
	h.finishTask(n, h.project, "a.txt", "task's a\n")
	write(t, h.repo, "a.txt", "user's uncommitted a\n")
	before := repoState(t, h.repo)
	h.step()

	tk := h.ticket(n)
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageMerging || tk.FailedStep != api.FailedMerge {
		t.Fatalf("ticket is %s/%s failed %q, want review/merging failed merge", tk.Column, tk.ReviewStage, tk.FailedStep)
	}
	h.checkClaimEnded(n)
	if !strings.Contains(tk.Attention, "a.txt") {
		t.Fatalf("attention does not name the file: %q", tk.Attention)
	}
	if !strings.Contains(h.lastComment(n).Body, h.repo) {
		t.Fatalf("comment does not name the folder:\n%s", h.lastComment(n).Body)
	}
	sameState(t, "user folder", before, repoState(t, h.repo))
	// Nothing is tried again by itself.
	h.steps(2)
	sameState(t, "user folder", before, repoState(t, h.repo))

	gitRun(t, h.repo, "checkout", "--", "a.txt")
	if _, err := h.b.Retry(h.ctx, board.User, n); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if tk := h.ticket(n); tk.Column != api.ColumnDone || tk.Attention != "" {
		t.Fatalf("after retry ticket is %s, attention %q", tk.Column, tk.Attention)
	}
	if got, _ := os.ReadFile(filepath.Join(h.repo, "a.txt")); string(got) != "task's a\n" {
		t.Fatalf("a.txt = %q", got)
	}
}

func TestOperationInProgressBlocksMerge(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	// The user is in the middle of a merge of their own, with a conflict.
	gitRun(t, h.repo, "switch", "-q", "-c", "side")
	commit(t, h.repo, "a.txt", "side\n")
	gitRun(t, h.repo, "switch", "-q", "main")
	commit(t, h.repo, "a.txt", "main\n")
	if err := exec.Command("git", "-C", h.repo, "merge", "side").Run(); err == nil {
		t.Fatal("the user's merge did not conflict")
	}
	before := repoState(t, h.repo)
	h.step()

	tk := h.ticket(n)
	if tk.FailedStep != api.FailedMerge || !strings.Contains(tk.Attention, "merge is in progress") {
		t.Fatalf("failed step %q attention %q", tk.FailedStep, tk.Attention)
	}
	h.checkClaimEnded(n)
	sameState(t, "user folder", before, repoState(t, h.repo))
}

func TestPartialStoryMergeRetryIsIdempotent(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	a, b := h.ids[0], h.ids[1]
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{a, b}, Review: api.ReviewSkipped})
	h.startStory(s)
	ta := h.storyTask(s, a, "In a")
	h.finishTask(ta, a, "new.txt", "a\n")
	h.steps(2)
	tb := h.storyTask(s, b, "In b")
	h.finishTask(tb, b, "a.txt", "b's a\n")
	h.steps(2)
	write(t, h.repos[b], "a.txt", "user's uncommitted a\n")
	beforeB := repoState(t, h.repos[b])
	h.handIn(s)
	h.step()

	tk := h.ticket(s)
	if tk.Column != api.ColumnReview || tk.FailedStep != api.FailedMerge {
		t.Fatalf("story is %s, failed %q", tk.Column, tk.FailedStep)
	}
	h.checkClaimEnded(s)
	storyMerge := fmt.Sprintf("Merge #%d: Two", s)
	if n := subjectCount(t, h.repos[a], "main", storyMerge); n != 1 {
		t.Fatalf("project a has %d merges of the story on main, want 1", n)
	}
	sameState(t, "project b", beforeB, repoState(t, h.repos[b]))
	body := h.lastComment(s).Body
	if !strings.Contains(body, "Already merged: main of project "+a) || !strings.Contains(body, "Not merged yet: main of project "+b) {
		t.Fatalf("comment does not record the progress:\n%s", body)
	}
	mainA := gitRun(t, h.repos[a], "rev-parse", "main")

	h.restart()
	h.steps(2)
	if c := h.ticket(s).Column; c != api.ColumnReview {
		t.Fatalf("story moved to %s without a retry", c)
	}
	gitRun(t, h.repos[b], "checkout", "--", "a.txt")
	if _, err := h.b.Retry(h.ctx, board.User, s); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if tk := h.ticket(s); tk.Column != api.ColumnDone {
		t.Fatalf("story is %s after the retry (attention %q)", tk.Column, tk.Attention)
	}
	if got := gitRun(t, h.repos[a], "rev-parse", "main"); got != mainA {
		t.Fatalf("project a's main moved on the retry: %s -> %s", mainA, got)
	}
	if subjectCount(t, h.repos[a], "main", storyMerge) != 1 || subjectCount(t, h.repos[b], "main", storyMerge) != 1 {
		t.Fatal("a project was merged twice or not at all")
	}
}

func TestMergeIsIdempotentAfterCrash(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	// A run claims the ticket and advances main, then dies before it
	// records the merge.
	if err := h.b.ClaimMerge(h.ctx, n); err != nil {
		t.Fatal(err)
	}
	branch := h.ticket(n).Projects[0].Branch
	from := gitRun(t, h.repo, "rev-parse", "main")
	res, err := h.git.PrepareMerge(h.repo, from, branch, gitops.StyleMerge, fmt.Sprintf("Merge #%d: Add b", n))
	if err != nil || res.Commit == "" {
		t.Fatalf("prepare: %+v %v", res, err)
	}
	if err := h.git.AdvanceBranch(h.repo, "main", from, res.Commit); err != nil {
		t.Fatal(err)
	}
	h.restart()
	h.steps(2)

	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s", c)
	}
	if got := gitRun(t, h.repo, "rev-parse", "main"); got != res.Commit {
		t.Fatalf("main is at %s, want the first merge %s", got, res.Commit)
	}
	if mergeCount(t, h.repo, "main") != 1 {
		t.Fatal("merged twice")
	}
}

func TestMovedTargetIsMergedAgain(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	moved := 0
	h.e.pause = func(point string) {
		if point == "advance" && moved == 0 {
			moved++
			commit(t, h.repo, "c.txt", "user\n")
		}
	}
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s (attention %q)", c, h.ticket(n).Attention)
	}
	for _, f := range []string{"b.txt", "c.txt"} {
		gitRun(t, h.repo, "cat-file", "-e", "main:"+f)
	}
	if mergeCount(t, h.repo, "main") != 1 {
		t.Fatal("want one merge commit")
	}
}

func TestTargetThatKeepsMovingFailsMerge(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	i := 0
	h.e.pause = func(point string) {
		if point == "advance" {
			i++
			commit(t, h.repo, fmt.Sprintf("c%d.txt", i), "user\n")
		}
	}
	h.step()
	h.e.pause = nil
	tk := h.ticket(n)
	if tk.FailedStep != api.FailedMerge || !strings.Contains(tk.Attention, "kept moving") {
		t.Fatalf("failed %q attention %q", tk.FailedStep, tk.Attention)
	}
	if i != maxRebuilds+1 {
		t.Fatalf("advanced %d times, want %d", i, maxRebuilds+1)
	}
	h.checkClaimEnded(n)
	if mergeCount(t, h.repo, "main") != 0 {
		t.Fatal("a merge commit landed")
	}
}

func TestSigningFailureFailsMerge(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	gitRun(t, h.repo, "config", "commit.gpgsign", "true")
	gitRun(t, h.repo, "config", "gpg.program", "false")
	before := repoState(t, h.repo)
	h.step()
	tk := h.ticket(n)
	if tk.FailedStep != api.FailedMerge || !strings.Contains(h.lastComment(n).Body, "signing key") {
		t.Fatalf("failed %q, comment:\n%s", tk.FailedStep, h.lastComment(n).Body)
	}
	h.checkClaimEnded(n)
	sameState(t, "user folder", before, repoState(t, h.repo))

	gitRun(t, h.repo, "config", "--unset", "commit.gpgsign")
	if _, err := h.b.Retry(h.ctx, board.User, n); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s after the retry", c)
	}
}

func TestUnverifiedSignatureFailsMerge(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	gitRun(t, h.repo, "config", "merge.verifySignatures", "true")
	before := repoState(t, h.repo)
	h.step()
	tk := h.ticket(n)
	if tk.FailedStep != api.FailedMerge || !strings.Contains(tk.Attention, "verifySignatures") {
		t.Fatalf("failed %q attention %q", tk.FailedStep, tk.Attention)
	}
	h.checkClaimEnded(n)
	// git itself refused this fast-forward, and such a refusal records
	// ORIG_HEAD; it is the only file it writes.
	after := repoState(t, h.repo)
	delete(before, ".git/ORIG_HEAD")
	delete(after, ".git/ORIG_HEAD")
	sameState(t, "user folder", before, after)
}

func TestMissingProjectFailsMerge(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	moved := h.repo + ".away"
	if err := os.Rename(h.repo, moved); err != nil {
		t.Fatal(err)
	}
	h.step()
	tk := h.ticket(n)
	if tk.FailedStep != api.FailedMerge || !strings.Contains(tk.Attention, "missing or disabled") {
		t.Fatalf("failed %q attention %q", tk.FailedStep, tk.Attention)
	}
	h.checkClaimEnded(n)
	if err := os.Rename(moved, h.repo); err != nil {
		t.Fatal(err)
	}
	if _, err := h.b.Retry(h.ctx, board.User, n); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s after the retry", c)
	}
}

func TestMergeFailureReasons(t *testing.T) {
	tg := &mergeTarget{target: "main", project: "app"}
	cases := []struct {
		err  error
		want []string
	}{
		{&gitops.BlockedError{Worktree: "/w", Files: []string{"a.txt", "b.txt"}}, []string{"a.txt, b.txt", "/w", "Commit, stash or discard"}},
		{&gitops.BlockedError{Worktree: "/w", Output: "a rebase is in progress"}, []string{"/w: a rebase is in progress", "Finish or abort"}},
		{&gitops.SwitchedError{Worktree: "/w", Branch: "main", At: "abc", Head: "other"}, []string{"switched to other", "fast-forwarded other", "main did not move"}},
		{&gitops.StaleCheckoutError{Worktree: "/w", Branch: "main", From: "1111111111111111", To: "2222"}, []string{"/w", "git read-tree -m -u 1111111111111111 2222"}},
		{fmt.Errorf("x: %w", gitops.ErrSignatureNotVerified), []string{"verifySignatures"}},
		{fmt.Errorf("git merge: %w after 1m (index.lock remains)", gitops.ErrTimeout), []string{"timed out", "index.lock"}},
		{errors.New("gpg failed to sign the data"), []string{"signing key"}},
	}
	for _, c := range cases {
		got := failure(tg, c.err).Error()
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("reason for %v:\n%s\nmissing %q", c.err, got, w)
			}
		}
		if !strings.Contains(got, "retry") {
			t.Errorf("reason for %v does not say how to go on:\n%s", c.err, got)
		}
	}
}
