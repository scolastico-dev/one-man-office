package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"github.com/scolastico-dev/one-man-office/internal/forge"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// exportingStory takes a story over every project of h, with a task merged
// into the first project and work committed straight into the others, to
// its export, with the pushes recorded as if they went to GitHub (tests
// never reach a forge; gh is looked up in a folder of fakes). It returns
// the story, its task and the live export job of every project.
func (h *harness) exportingStory(description string) (int64, int64, map[string]board.EngineSession) {
	h.t.Helper()
	for _, p := range h.ids {
		h.origin(p)
	}
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Sign in across the stack", Description: description,
		Projects: h.ids, Review: api.ReviewSkipped, WhenApproved: api.WhenApprovedExport})
	h.startStory(s)
	task := h.storyTask(s, h.ids[0], "Add the login endpoint")
	h.finishTask(task, h.ids[0], "login.txt", "login\n")
	h.steps(2)
	for _, p := range h.ids[1:] {
		commit(h.t, h.worktree(p, s), p+".txt", p+"\n")
	}
	h.handIn(s)
	h.steps(2)
	if tk := h.ticket(s); tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageExporting {
		h.t.Fatalf("story is %s/%s, want review/exporting", tk.Column, tk.ReviewStage)
	}
	jobs := map[string]board.EngineSession{}
	for _, sess := range h.snap().Sessions {
		if sess.Ticket == s && sess.Purpose == api.PurposeExport {
			jobs[sess.Project] = sess
		}
	}
	if len(jobs) != len(h.ids) {
		h.t.Fatalf("%d export jobs, want %d", len(jobs), len(h.ids))
	}
	for _, p := range h.ids {
		r, ok, err := h.e.pins.pushed(s, p)
		if err != nil || !ok {
			h.t.Fatalf("no push of %s: %v", p, err)
		}
		r.Address = "https://github.com/you/" + p + ".git"
		if err := h.e.pins.setPushed(s, p, r); err != nil {
			h.t.Fatal(err)
		}
	}
	return s, task, jobs
}

// fakeGH puts a gh on a folder that logs every argument on its own line and
// the content of the file --body-file names, and makes lookPath find it.
// It returns the folder.
func fakeGH(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
for a in "$@"; do
  printf 'ARG %s\n' "$a" >> "$FAKE_LOG"
  case "$a" in --body-file=*) printf 'BODY<<%s>>\n' "$(cat "${a#--body-file=}")" >> "$FAKE_LOG" ;; esac
done
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := lookPath
	lookPath = func(name string) (string, error) {
		if name == "gh" {
			return filepath.Join(bin, "gh"), nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPath = old })
	return bin
}

var shBlock = regexp.MustCompile("```sh\n\\s*(.*)\n")

// runExportCommand runs the first (or, with edit, the second) command of
// the job's prompt in its folder with the fake gh of bin and returns what gh
// logged.
func (h *harness) runExportCommand(job board.EngineSession, bin string, edit bool) string {
	h.t.Helper()
	prompt, err := h.e.Ready(h.ctx, h.actor(job.ID))
	if err != nil {
		h.t.Fatal(err)
	}
	blocks := shBlock.FindAllStringSubmatch(prompt, -1)
	if len(blocks) != 2 {
		h.t.Fatalf("%d command blocks in the prompt:\n%s", len(blocks), prompt)
	}
	line := blocks[0][1]
	if edit {
		line = blocks[1][1]
	}
	log := filepath.Join(h.t.TempDir(), "gh.log")
	cmd := exec.Command("sh", "-c", line)
	cmd.Dir = job.Workdir
	cmd.Env = childenvtest.ShellEnv([]string{"PATH=" + bin + ":/usr/bin:/bin", "FAKE_LOG=" + log}, h.t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("%s: %v\n%s", line, err, out)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(raw)
}

func writeDraft(t *testing.T, job board.EngineSession, title, body string) {
	t.Helper()
	for name, text := range map[string]string{draftTitleFile: title, draftBodyFile: body} {
		if err := os.WriteFile(filepath.Join(job.Workdir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// reportExport reports address as job's pull request the way the server
// does: the hand-in check, then the board.
func (h *harness) reportExport(job board.EngineSession, address string) {
	h.t.Helper()
	req := api.DoneRequest{URL: address}
	if err := h.e.CheckHandIn(h.ctx, h.actor(job.ID), req); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), req); err != nil {
		h.t.Fatal(err)
	}
}

func needSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the forge command is a POSIX shell line")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
}

// Every project of a story gets the pull request its export job wrote for
// it: the material names the other projects and the task, the forge tool
// gets each project's own title and body as file arguments, and the comment
// that links each pull request quotes its title.
func TestExportTextWrittenPerProject(t *testing.T) {
	needSh(t)
	h := gitHarness(t, withProjects(3))
	bin := fakeGH(t)
	s, task, jobs := h.exportingStory("Users sign in with a password.")

	for i, p := range h.ids {
		job := jobs[p]
		material, err := os.ReadFile(filepath.Join(job.Workdir, materialFile))
		if err != nil {
			t.Fatal(err)
		}
		var others []string
		for _, o := range h.ids {
			if o != p {
				others = append(others, o)
			}
		}
		for _, want := range append([]string{"Users sign in with a password.", "Add the login endpoint", "## Other repositories"}, others...) {
			if !strings.Contains(string(material), want) {
				t.Errorf("material of %s lacks %q:\n%s", p, want, material)
			}
		}
		if i > 0 && !strings.Contains(string(material), "- "+p+".txt (added, +1 -0)") {
			t.Errorf("material of %s does not list its file:\n%s", p, material)
		}

		title := fmt.Sprintf("feat(%s): sign in, part %d", p, i+1)
		body := fmt.Sprintf("Adds the %s side of signing in.\n\nPart of a change that also touches: %s\n", p, strings.Join(others, ", "))
		writeDraft(t, job, title+"\n", body)
		got, err := h.e.ExportText(h.ctx, h.actor(job.ID))
		if err != nil {
			t.Fatal(err)
		}
		if got.TitleFrom != api.TextFromAgent || got.BodyFrom != api.TextFromAgent || got.Title != title || len(got.Problems) != 0 {
			t.Fatalf("%s: %+v", p, got)
		}
		log := h.runExportCommand(job, bin, false)
		for _, want := range []string{"ARG pr\nARG create\n", "ARG --repo=you/" + p + "\n", "ARG --title=" + title + "\n",
			"ARG --body-file=" + forge.BodyFile + "\n", "BODY<<" + strings.TrimSuffix(body, "\n") + ">>"} {
			if !strings.Contains(log, want) {
				t.Errorf("gh of %s did not get %q:\n%s", p, want, log)
			}
		}
		for _, n := range []int64{s, task} {
			if strings.Contains(log, fmt.Sprintf("#%d", n)) {
				t.Errorf("gh of %s got the omo number #%d:\n%s", p, n, log)
			}
		}
		h.reportExport(job, fmt.Sprintf("https://github.com/you/%s/pull/%d", p, i+1))
		want := fmt.Sprintf("Pull request of project `%s`: https://github.com/you/%s/pull/%d\n\nTitle: `%s`\n\nThe export agent wrote the title and the description for this repository.", p, p, i+1, title)
		if h.commentsWith(s, want) != 1 {
			t.Errorf("no comment %q:\n%s", want, strings.Join(h.comments(s), "\n---\n"))
		}
	}
	h.steps(2)
	if tk := h.ticket(s); tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageAwaitingMerge {
		t.Fatalf("story is %s/%s", tk.Column, tk.Resolution)
	}
}

// What the job wrote is used only when it is usable; omo's own text stays
// otherwise, the job hears why, and the comment says so. The export goes on
// either way.
func TestExportTextFallsBackToOmosOwn(t *testing.T) {
	needSh(t)
	h := gitHarness(t, withProjects(3))
	bin := fakeGH(t)
	s, task, jobs := h.exportingStory("Users sign in with a password.")
	a, b, c := h.ids[0], h.ids[1], h.ids[2]

	// a writes nothing.
	got, err := h.e.ExportText(h.ctx, h.actor(jobs[a].ID))
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleFrom != api.TextFromOmo || got.BodyFrom != api.TextFromOmo || got.Title != "Sign in across the stack" ||
		strings.Join(got.Problems, "; ") != "the export job wrote no title.txt; the export job wrote no body.md" {
		t.Fatalf("nothing written: %+v", got)
	}
	log := h.runExportCommand(jobs[a], bin, false)
	for _, want := range []string{"ARG --title=Sign in across the stack\n", "BODY<<Users sign in with a password.\n\n## Changes in this repository\n\n- ",
		"Part of a change that also touches: " + b + ", " + c + ">>"} {
		if !strings.Contains(log, want) {
			t.Errorf("gh of %s did not get %q:\n%s", a, want, log)
		}
	}
	for _, n := range []int64{s, task} {
		if strings.Contains(log, fmt.Sprintf("#%d", n)) {
			t.Errorf("omo's own text names #%d:\n%s", n, log)
		}
	}
	h.reportExport(jobs[a], "https://github.com/you/"+a+"/pull/1")
	if h.commentsWith(s, "The title and the description are omo's own text: the export job wrote no title.txt; the export job wrote no body.md.") != 1 {
		t.Errorf("the comment does not say omo's text was sent:\n%s", strings.Join(h.comments(s), "\n---\n"))
	}

	// b writes a title naming the story and a body over the limit, then
	// fixes the title and runs the check again.
	writeDraft(t, jobs[b], fmt.Sprintf("Sign in for #%d\n", s), strings.Repeat("y", maxExportBody+1))
	got, err = h.e.ExportText(h.ctx, h.actor(jobs[b].ID))
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleFrom != api.TextFromOmo || got.BodyFrom != api.TextFromOmo ||
		strings.Join(got.Problems, "; ") != fmt.Sprintf("title.txt names the omo ticket #%d; body.md is larger than 60 KiB", s) {
		t.Fatalf("bad text: %+v", got)
	}
	writeDraft(t, jobs[b], "Sign in for the web client\n", "\n \n")
	got, err = h.e.ExportText(h.ctx, h.actor(jobs[b].ID))
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleFrom != api.TextFromAgent || got.BodyFrom != api.TextFromOmo || strings.Join(got.Problems, "; ") != "body.md is empty" {
		t.Fatalf("fixed title, empty body: %+v", got)
	}
	log = h.runExportCommand(jobs[b], bin, false)
	if !strings.Contains(log, "ARG --title=Sign in for the web client\n") || !strings.Contains(log, "BODY<<Users sign in with a password.") {
		t.Errorf("gh of %s:\n%s", b, log)
	}
	h.reportExport(jobs[b], "https://github.com/you/"+b+"/pull/2")
	if h.commentsWith(s, "Title: `Sign in for the web client`\n\nThe export agent wrote the title; the description is omo's own text: body.md is empty.") != 1 {
		t.Errorf("the comment does not say whose text was sent:\n%s", strings.Join(h.comments(s), "\n---\n"))
	}

	// c never runs the check: omo's own text was in the files all along.
	log = h.runExportCommand(jobs[c], bin, false)
	if !strings.Contains(log, "ARG --title=Sign in across the stack\n") {
		t.Errorf("gh of %s:\n%s", c, log)
	}
	h.reportExport(jobs[c], "https://github.com/you/"+c+"/pull/3")
	if h.commentsWith(s, "The title and the description are omo's own text: the export job did not hand in a text of its own (omo ai export-text).") != 1 {
		t.Errorf("the comment does not say the check was skipped:\n%s", strings.Join(h.comments(s), "\n---\n"))
	}
	h.steps(2)
	if tk := h.ticket(s); tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageAwaitingMerge {
		t.Fatalf("story is %s/%s", tk.Column, tk.Resolution)
	}
}

// A project exported again after another project's export failed sees the
// pull request of the project that was exported in its material, and its
// prompt has the command that updates a pull request that exists already.
func TestExportAgainNamesTheExportedSibling(t *testing.T) {
	needSh(t)
	h := gitHarness(t, withProjects(2))
	bin := fakeGH(t)
	s, _, jobs := h.exportingStory("Two parts.")
	a, b := h.ids[0], h.ids[1]
	h.reportExport(jobs[a], "https://github.com/you/"+a+"/pull/7")
	req := api.DoneRequest{Error: "gh is not logged in"}
	if err := h.e.CheckHandIn(h.ctx, h.actor(jobs[b].ID), req); err != nil {
		t.Fatal(err)
	}
	if _, err := h.b.Done(h.ctx, h.actor(jobs[b].ID), req); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if tk := h.ticket(s); tk.ReviewStage != api.ReviewStageHuman {
		t.Fatalf("story is %s/%s after a failed project", tk.Column, tk.ReviewStage)
	}
	h.exportNow(s)
	h.step()
	job, ok := h.exportSession(s)
	if !ok || job.Project != b {
		t.Fatalf("export job %+v %v, want one for %s", job, ok, b)
	}
	material, err := os.ReadFile(filepath.Join(job.Workdir, materialFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(material), "- "+a+": pull request https://github.com/you/"+a+"/pull/7") {
		t.Fatalf("material does not name the sibling's pull request:\n%s", material)
	}
	r, _, _ := h.e.pins.pushed(s, b)
	r.Address = "https://github.com/you/" + b + ".git"
	if err := h.e.pins.setPushed(s, b, r); err != nil {
		t.Fatal(err)
	}
	writeDraft(t, job, "feat: the second part\n", "The second part.\n")
	if _, err := h.e.ExportText(h.ctx, h.actor(job.ID)); err != nil {
		t.Fatal(err)
	}
	log := h.runExportCommand(job, bin, true)
	if !strings.Contains(log, "ARG pr\nARG edit\nARG "+r.Branch+"\n") || !strings.Contains(log, "BODY<<The second part.>>") {
		t.Fatalf("the update command:\n%s", log)
	}
}

// Only the export job of the ticket may check a text, and only while the
// ticket exports.
func TestExportTextRefusesOthers(t *testing.T) {
	h := gitHarness(t)
	n, job, _, _ := h.exportedTask("Export me")
	dev := h.live(api.RoleDeveloper, n)
	if dev.ID == job.ID {
		t.Fatal("the developer is the export job")
	}
	for _, a := range []board.Actor{board.User, h.actor(dev.ID)} {
		var apiErr *api.Error
		if _, err := h.e.ExportText(h.ctx, a); !asAPIError(err, &apiErr) || apiErr.Code != api.CodeForbidden {
			t.Errorf("%s: %v", a.Role, err)
		}
	}
	if _, err := h.e.ExportText(h.ctx, h.actor(job.ID)); err != nil {
		t.Fatalf("the export job: %v", err)
	}
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
		t.Fatal(err)
	}
	var apiErr *api.Error
	if _, err := h.e.ExportText(h.ctx, h.actor(job.ID)); !asAPIError(err, &apiErr) || apiErr.Code != api.CodeConflict {
		t.Errorf("after the pull request: %v", err)
	}
}

func TestOmoNumbersAreFoundAndScrubbed(t *testing.T) {
	numbers := []int{12, 40}
	for text, want := range map[string]int{
		"Fixes #12": 12, "(#40)": 40, "#12.": 12, "see #120": 0, "a#12": 0, "&#12;": 0, "x/#12": 0, "#7 and #40": 40, "": 0,
	} {
		if got := namedNumber(text, numbers); got != want {
			t.Errorf("namedNumber(%q) = %d, want %d", text, got, want)
		}
	}
	if got := scrubNumbers("feat: fake work for #12 (#40, #9)", numbers); got != "feat: fake work for (, #9)" {
		t.Errorf("scrubbed %q", got)
	}
}

func TestFallbackBodyForSeveralProjects(t *testing.T) {
	sum := gitops.ChangeSummary{TotalCommits: 2, Commits: []gitops.Commit{{ID: "b", Subject: "feat: login for #12"}, {ID: "a", Subject: "Merge: task one"}}}
	siblings := []exportSibling{{id: "api", name: "api"}, {id: "old", name: "old", leftOut: true}, {id: "ui", name: "ui"}}
	got := fallbackBody("The story.", sum, true, siblings, []int{12})
	want := "The story.\n\n## Changes in this repository\n\n- feat: login for\n- Merge: task one\n\nPart of a change that also touches: api, ui\n"
	if got != want {
		t.Fatalf("body\n%q\nwant\n%q", got, want)
	}
	// One project: the description alone; without one, the commits.
	if got := fallbackBody("The task.", sum, true, nil, nil); got != "The task.\n" {
		t.Fatalf("single project %q", got)
	}
	if got := fallbackBody("", sum, true, nil, []int{12}); got != "## Changes in this repository\n\n- feat: login for\n- Merge: task one\n" {
		t.Fatalf("no description %q", got)
	}
	long := strings.Repeat("d\n", maxExportBody)
	if got := fallbackBody(long, sum, true, siblings, nil); len(got) > maxExportBody || !strings.HasSuffix(got, "also touches: api, ui\n") {
		t.Fatalf("a long description is cut to %d bytes, ends %q", len(got), got[len(got)-40:])
	}
}

func TestDraftTitleRules(t *testing.T) {
	dir := t.TempDir()
	for raw, want := range map[string]string{
		"feat: add\u202e it\n":                "",
		"two\nlines":                          draftTitleFile + " holds more than one line",
		"  \n":                                draftTitleFile + " is empty",
		strings.Repeat("t", maxExportTitle+1): fmt.Sprintf("%s is longer than %d characters", draftTitleFile, maxExportTitle),
		strings.Repeat("t", maxDraftTitle+1):  fmt.Sprintf("%s is larger than 4 KiB", draftTitleFile),
		"Close #3":                            draftTitleFile + " names the omo ticket #3",
	} {
		if err := os.WriteFile(filepath.Join(dir, draftTitleFile), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		title, problem := draftTitle(dir, []int{3})
		if problem != want {
			t.Errorf("%.30q: problem %q, want %q", raw, problem, want)
		}
		if want == "" && title != "feat: add it" {
			t.Errorf("title %q", title)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Remove(filepath.Join(dir, draftTitleFile)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/etc/hostname", filepath.Join(dir, draftTitleFile)); err != nil {
			t.Fatal(err)
		}
		if _, problem := draftTitle(dir, nil); problem != draftTitleFile+" is not a regular file" {
			t.Errorf("a link: %q", problem)
		}
	}
}
