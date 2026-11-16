package engine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/images"
)

// origin gives project a bare repository as remote origin, unless it has
// an origin, and returns origin's address.
func (h *harness) origin(project string) string {
	h.t.Helper()
	repo := h.repos[project]
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	cmd.Dir = repo
	if out, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	bare := filepath.Join(h.t.TempDir(), "origin.git")
	gitRun(h.t, h.t.TempDir(), "init", "-q", "--bare", bare)
	gitRun(h.t, repo, "remote", "add", "origin", bare)
	return bare
}

// remoteHead returns the commit branch is at in the bare repository, or "".
func remoteHead(t *testing.T, bare, branch string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "-q", "refs/heads/"+branch)
	cmd.Dir = bare
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// reviewedTask takes a standalone task that is exported once approved to
// the user's review and returns it, its branch and the commit handed in.
func (h *harness) reviewedTask(title, description string) (n int64, branch, reviewed string) {
	h.t.Helper()
	n = h.create(api.CreateTicket{Type: api.TypeTask, Title: title, Description: description,
		Review: api.ReviewHuman, WhenApproved: api.WhenApprovedExport})
	h.finishTask(n, h.project, "b.txt", "b\n")
	h.step()
	tk := h.ticket(n)
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageHuman {
		h.t.Fatalf("ticket is %s/%s, want review/human", tk.Column, tk.ReviewStage)
	}
	branch = tk.Projects[0].Branch
	return n, branch, gitRun(h.t, h.repo, "rev-parse", "refs/heads/"+branch)
}

// exportNow approves ticket n with export and runs a pass.
func (h *harness) exportNow(n int64) {
	h.t.Helper()
	if _, err := h.b.Approve(h.ctx, board.User, n, api.ApproveRequest{Export: true}); err != nil {
		h.t.Fatal(err)
	}
	h.step()
}

func (h *harness) exportFolderOf(n int64) string {
	return filepath.Join(h.layout.Run, "export", fmt.Sprintf("%d-%s", n, h.project))
}

func TestExportPushesReviewedCommit(t *testing.T) {
	h := gitHarness(t)
	bare := h.origin(h.project)
	n, branch, reviewed := h.reviewedTask("Export me", "")
	// The developer, still parked, commits after its hand-in.
	extra := commit(t, h.worktree(h.project, n), "late.txt", "not reviewed\n")
	h.exportNow(n)

	if got := remoteHead(t, bare, branch); got != reviewed {
		t.Fatalf("the remote has %s at %q, want the reviewed %s (the branch is at %s)", branch, got, reviewed, extra)
	}
	job, ok := h.exportSession(n)
	if !ok {
		t.Fatal("no export job started after the push")
	}
	if c := h.commentsWith(n, "Pushed `"+reviewed[:12]+"` to `origin` (`"+bare+"`) as `"+branch+"`."); c != 1 {
		t.Fatalf("%d push comments:\n%s", c, strings.Join(h.comments(n), "\n---\n"))
	}

	// A job that crashes is started again; its push changes nothing and
	// is not announced twice.
	h.agent(job.ID).exit(errors.New("exit status 3"))
	h.waitEnded(job.ID)
	h.step()
	if _, ok := h.exportSession(n); !ok {
		t.Fatal("no export job after the crash")
	}
	if got := remoteHead(t, bare, branch); got != reviewed {
		t.Fatalf("after the restart the remote has %q", got)
	}
	if c := h.commentsWith(n, "Pushed `"); c != 1 {
		t.Fatalf("%d push comments after the restart", c)
	}
}

func TestExportWithoutPinPushesHeadAndSaysSo(t *testing.T) {
	h := gitHarness(t)
	bare := h.origin(h.project)
	n, branch, reviewed := h.reviewedTask("Export me", "")
	if err := os.Remove(filepath.Join(h.layout.Run, pinsFile)); err != nil {
		t.Fatal(err)
	}
	h.e.pins = &pinStore{path: filepath.Join(h.layout.Run, pinsFile)}
	if _, err := h.b.Approve(h.ctx, board.User, n, api.ApproveRequest{Export: true}); err != nil {
		t.Fatal(err)
	}
	// Run the start directly: a pass would record the pin first.
	tk, _, _, err := h.e.read(h.ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.prepareExport(h.ctx, tk, h.project); err != nil {
		t.Fatal(err)
	}
	if got := remoteHead(t, bare, branch); got != reviewed {
		t.Fatalf("remote at %q, want %s", got, reviewed)
	}
	if h.commentsWith(n, "no record of the commit handed in") != 1 {
		t.Fatalf("the comment does not say the head was taken:\n%s", strings.Join(h.comments(n), "\n---\n"))
	}
	if pins, _ := h.e.pins.reviewed(n, tk.ReviewRound); pins[h.project] != reviewed {
		t.Fatalf("the commit was not recorded: %v", pins)
	}
}

func TestExportIgnoresWorktreeGitFile(t *testing.T) {
	h := gitHarness(t)
	bare := h.origin(h.project)
	n, branch, reviewed := h.reviewedTask("Export me", "")

	// The agent replaces its worktree's .git by a repository of its own
	// whose origin points elsewhere.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.git")
	gitRun(t, t.TempDir(), "init", "-q", "--bare", elsewhere)
	wt := h.worktree(h.project, n)
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, wt, "init", "-q", "-b", branch)
	gitRun(t, wt, "remote", "add", "origin", elsewhere)
	gitRun(t, wt, "add", "-A")
	gitRun(t, wt, "commit", "-q", "-m", "planted")

	h.exportNow(n)
	if got := remoteHead(t, bare, branch); got != reviewed {
		t.Fatalf("the project's remote has %q, want %s", got, reviewed)
	}
	if out := gitRun(t, elsewhere, "for-each-ref"); out != "" {
		t.Fatalf("the worktree's remote got refs:\n%s", out)
	}
	if _, ok := h.exportSession(n); !ok {
		t.Fatal("no export job")
	}
}

func TestExportFilesHoldTitleAndDescriptionOnly(t *testing.T) {
	h := gitHarness(t)
	h.origin(h.project)
	var desc strings.Builder
	desc.WriteString("Intro with hidden\u202e and\u200b marks\r\n")
	line := strings.Repeat("x", 99) + "\n"
	for desc.Len() < 100<<10 {
		desc.WriteString(line)
	}
	n, _, _ := h.reviewedTask("Add the login form", desc.String())
	if _, err := h.b.Comment(h.ctx, board.User, n, api.CommentRequest{Body: "a comment that stays on omo"}); err != nil {
		t.Fatal(err)
	}
	h.exportNow(n)
	if _, ok := h.exportSession(n); !ok {
		t.Fatal("no export job")
	}
	folder := h.exportFolderOf(n)
	title, err := os.ReadFile(filepath.Join(folder, "title.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(title) != "Add the login form\n" {
		t.Fatalf("title.txt = %q", title)
	}
	raw, err := os.ReadFile(filepath.Join(folder, "body.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	footer := fmt.Sprintf("\n\nTicket #%d on omo.\n", n)
	text, ok := strings.CutSuffix(body, footer)
	if !ok {
		t.Fatalf("body does not end with the ticket line: %q", body[max(0, len(body)-80):])
	}
	if len(text) > maxExportBody || !strings.HasPrefix(text, "Intro with hidden and marks\n") || !strings.HasSuffix(text, strings.TrimSuffix(line, "\n")) {
		t.Fatalf("body is %d bytes, starts %q, ends %q", len(text), text[:40], text[len(text)-20:])
	}
	for _, bad := range []string{"\u202e", "\u200b", "\r", "a comment", h.worktree(h.project, n), h.repo} {
		if strings.Contains(body, bad) {
			t.Errorf("body holds %q", bad)
		}
	}
	for path, want := range map[string]os.FileMode{
		filepath.Dir(folder):               0o700,
		folder:                             0o700,
		filepath.Join(folder, "title.txt"): 0o600,
		filepath.Join(folder, "body.md"):   0o600,
	} {
		fi, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s has mode %v, want %v", path, fi.Mode().Perm(), want)
		}
	}
}

func TestExportBodyCut(t *testing.T) {
	if got := exportBody("", 3); got != "Ticket #3 on omo.\n" {
		t.Fatalf("empty description: %q", got)
	}
	long := strings.Repeat("é", maxExportBody) // no line break at all
	got := exportBody(long, 3)
	text := strings.TrimSuffix(got, "\n\nTicket #3 on omo.\n")
	if len(text) > maxExportBody || !strings.HasPrefix(long, text) {
		t.Fatalf("a description without line breaks is cut to %d bytes", len(text))
	}
	if got := exportTitle(" a\u202eb\tc "); got != "abc" {
		t.Fatalf("title %q", got)
	}
}

// An image in the description is kept in omo: the pull request's body names
// it by its alt text instead of a reference no forge can show.
func TestExportBodyNamesImagesInsteadOfReferencingThem(t *testing.T) {
	id := images.NewID()
	got := exportBody("Before:\n\n![screen shot.png](omo-image:"+id+")\n", 4)
	want := "Before:\n\n[image: screen shot.png, not exported]\n\nTicket #4 on omo.\n"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestExportFailureHidesCredentials(t *testing.T) {
	h := gitHarness(t)
	gitRun(t, h.repo, "remote", "add", "origin", "https://user:SECRET@127.0.0.1:1/you/app.git")
	n, branch, reviewed := h.reviewedTask("Export me", "")
	h.exportNow(n)

	tk := h.ticket(n)
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageHuman || tk.Attention == "" {
		t.Fatalf("ticket is %s/%s attention %q, want review/human waiting for the user", tk.Column, tk.ReviewStage, tk.Attention)
	}
	if _, ok := h.exportSession(n); ok {
		t.Fatal("an export job started although the push failed")
	}
	if h.commentsWith(n, "Export failed: Export of "+h.project+" failed") != 1 {
		t.Fatalf("no failure comment:\n%s", strings.Join(h.comments(n), "\n---\n"))
	}
	for _, s := range append(h.comments(n), tk.Attention, h.logs.String()) {
		if strings.Contains(s, "SECRET") {
			t.Fatalf("the credentials leaked: %q", s)
		}
	}
	if pathExists(h.exportFolderOf(n)) {
		t.Fatal("an export folder was made for a failed push")
	}

	// Retry once the remote is fixed: the export goes through.
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitRun(t, t.TempDir(), "init", "-q", "--bare", bare)
	gitRun(t, h.repo, "remote", "set-url", "origin", bare)
	h.exportNow(n)
	if got := remoteHead(t, bare, branch); got != reviewed {
		t.Fatalf("the retry pushed %q, want %s", got, reviewed)
	}
	if _, ok := h.exportSession(n); !ok {
		t.Fatal("no export job after the retry")
	}
}

func TestExportRejectedPushWaitsForUser(t *testing.T) {
	h := gitHarness(t)
	bare := h.origin(h.project)
	n, branch, reviewed := h.reviewedTask("Export me", "")
	// The remote's branch holds a commit the reviewed one does not
	// descend from.
	other := gitRun(t, h.repo, "commit-tree", "-m", "someone else", "HEAD^{tree}")
	gitRun(t, h.repo, "push", "-q", bare, other+":refs/heads/"+branch)
	h.exportNow(n)

	if got := remoteHead(t, bare, branch); got != other {
		t.Fatalf("the remote branch changed to %q (forced?)", got)
	}
	tk := h.ticket(n)
	if tk.ReviewStage != api.ReviewStageHuman || h.commentsWith(n, "refused the push") != 1 {
		t.Fatalf("stage %s, comments:\n%s", tk.ReviewStage, strings.Join(h.comments(n), "\n---\n"))
	}
	if _, ok := h.exportSession(n); ok {
		t.Fatal("an export job started after a refused push")
	}

	gitRun(t, bare, "update-ref", "-d", "refs/heads/"+branch)
	h.exportNow(n)
	if got := remoteHead(t, bare, branch); got != reviewed {
		t.Fatalf("after the retry the remote has %q", got)
	}
	if _, ok := h.exportSession(n); !ok {
		t.Fatal("no export job after the retry")
	}
}

func TestExportJobRunsOutsideWorktree(t *testing.T) {
	h := gitHarness(t)
	n, job, dir, _ := h.exportedTask("Export me")
	folder := h.exportFolderOf(n)
	if job.Workdir != folder || job.Workdir == dir {
		t.Fatalf("export job works in %s, want %s", job.Workdir, folder)
	}
	if spec := h.runner.specList(); spec[len(spec)-1].Dir != folder {
		t.Fatalf("export job started in %s", spec[len(spec)-1].Dir)
	}
	for _, f := range []string{"title.txt", "body.md"} {
		if !pathExists(filepath.Join(folder, f)) {
			t.Fatalf("%s missing", f)
		}
	}
	// A leftover folder of a ticket that no longer exports goes too.
	stray := filepath.Join(h.layout.Run, "export", "999-x")
	if err := os.MkdirAll(stray, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(3)
	if h.ticket(n).Column != api.ColumnDone {
		t.Fatal("not done")
	}
	if pathExists(folder) || pathExists(stray) {
		t.Fatal("export folders stayed after the export")
	}
	if _, ok, _ := h.e.pins.pushed(n, h.project); ok {
		t.Fatal("the push stays recorded after the export")
	}
}

func TestExportPromptAndAddress(t *testing.T) {
	h := gitHarness(t)
	n, job, dir, branch := h.exportedTask("Export me")
	bare := h.origin(h.project)
	bin := t.TempDir()
	old := lookPath
	lookPath = func(name string) (string, error) {
		if name == "gh" {
			return filepath.Join(bin, "gh"), nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPath = old })

	reviewed := gitRun(t, h.repo, "rev-parse", "refs/heads/"+branch)
	prompt, err := h.e.Ready(h.ctx, h.actor(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{reviewed, h.exportFolderOf(n), "remote origin", `omo ai done --failed "the remote is a folder on this machine, not a forge"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, dir) {
		t.Errorf("prompt names the worktree:\n%s", prompt)
	}

	// omo pushed to a folder on this machine: no address fits, the job is
	// told to report the failure instead.
	exp := h.actor(job.ID)
	for _, addr := range []string{"https://example.invalid/pr/1", "https://github.com/you/app/pull/4"} {
		var apiErr *api.Error
		if err := h.e.CheckHandIn(h.ctx, exp, api.DoneRequest{URL: addr}); !asAPIError(err, &apiErr) || apiErr.Code != api.CodeValidation ||
			!strings.Contains(apiErr.Message, `omo ai done --failed "the remote is a folder on this machine, not a forge"`) {
			t.Errorf("local remote, address %q: %v", addr, err)
		}
	}

	// The job repoints the project's remote to GitHub after the push: the
	// prompt and the check still go by what omo pushed.
	gitRun(t, h.repo, "remote", "set-url", "origin", "https://github.com/you/app.git")
	prompt, err = h.e.Ready(h.ctx, h.actor(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "gh pr create") || !strings.Contains(prompt, bare) {
		t.Fatalf("the prompt follows the repointed remote:\n%s", prompt)
	}
	var apiErr *api.Error
	if err := h.e.CheckHandIn(h.ctx, exp, api.DoneRequest{URL: "https://github.com/you/app/pull/4"}); !asAPIError(err, &apiErr) || apiErr.Code != api.CodeValidation {
		t.Errorf("an address on the repointed remote: %v", err)
	}

	// As if omo had pushed to GitHub (tests never reach a forge), then the
	// job repointed the remote to another repository on the same host.
	if err := h.e.pins.setPushed(n, h.project, pushRecord{Commit: reviewed, Branch: branch, Remote: "origin", Address: "https://github.com/you/app.git"}); err != nil {
		t.Fatal(err)
	}
	gitRun(t, h.repo, "remote", "set-url", "origin", "https://you:SECRET@github.com/evil/app.git")
	prompt, err = h.e.Ready(h.ctx, h.actor(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	want := "gh pr create --repo='you/app' --head='" + branch + "' --base='main' --title=\"$(cat title.txt)\" --body-file='body.md'"
	if !strings.Contains(prompt, want) || strings.Contains(prompt, "SECRET") || strings.Contains(prompt, "evil") {
		t.Fatalf("prompt:\n%s", prompt)
	}
	if err := h.e.CheckHandIn(h.ctx, exp, api.DoneRequest{URL: "https://github.com/you/app/pull/4"}); err != nil {
		t.Fatalf("the pushed remote's own address was refused: %v", err)
	}
	for _, bad := range []string{
		"https://evil.example/you/app/pull/4", "http://github.com/you/app/pull/4", "https://github.com/evil/app/pull/4",
		"https://github.com/you/other/pull/4", "https://github.com/settings/tokens", "https://x@github.com/you/app/pull/4",
	} {
		var apiErr *api.Error
		if err := h.e.CheckHandIn(h.ctx, exp, api.DoneRequest{URL: bad}); !asAPIError(err, &apiErr) || apiErr.Code != api.CodeValidation {
			t.Errorf("address %q: %v", bad, err)
		}
	}
	if err := h.e.CheckHandIn(h.ctx, exp, api.DoneRequest{Error: "gh is not logged in"}); err != nil {
		t.Fatalf("a reported failure was refused: %v", err)
	}
}

func TestExportFolderCleanupRefusesALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symbolic links")
	}
	h := newHarness(t)
	elsewhere := t.TempDir()
	keep := filepath.Join(elsewhere, "1-x")
	if err := os.Mkdir(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(h.layout.Run, exportDir)
	if err := os.MkdirAll(h.layout.Run, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	h.step()
	if !pathExists(keep) {
		t.Fatal("the cleanup followed the link and removed a folder elsewhere")
	}
	if fi, err := os.Lstat(link); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the link stays")
	}
}

func TestExportPushAnnouncedOnceAcrossRestart(t *testing.T) {
	h := gitHarness(t)
	h.origin(h.project)
	n, _, _ := h.reviewedTask("Export me", "")
	h.exportNow(n)
	if c := h.commentsWith(n, "Pushed `"); c != 1 {
		t.Fatalf("%d push comments", c)
	}
	// omo restarts while the push runs: what it kept in memory is gone,
	// the pins file stays, and the export is prepared again.
	h.e.disk.mu.Lock()
	h.e.disk.noted = nil
	h.e.disk.mu.Unlock()
	h.e.pins = &pinStore{path: filepath.Join(h.layout.Run, pinsFile)}
	tk, _, _, err := h.e.read(h.ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.prepareExport(h.ctx, tk, h.project); err != nil {
		t.Fatal(err)
	}
	if c := h.commentsWith(n, "Pushed `"); c != 1 {
		t.Fatalf("%d push comments after a restart:\n%s", c, strings.Join(h.comments(n), "\n---\n"))
	}
}
