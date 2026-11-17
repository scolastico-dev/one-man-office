package e2e

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// ghCall is one call of the fake gh: its arguments and the body it read.
type ghCall struct {
	args []string
	body string
}

func (c ghCall) flag(name string) string {
	for _, a := range c.args {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}

var ghBody = regexp.MustCompile(`(?s)BODY<<(.*?)>>\n`)

// ghCalls reads the fake gh's logs in dir, a file per call.
func ghCalls(t *testing.T, dir string) []ghCall {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "call-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	var calls []ghCall
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		part := string(raw)
		var c ghCall
		if m := ghBody.FindStringSubmatch(part); m != nil {
			c.body = m[1]
			part = strings.Replace(part, m[0], "", 1)
		}
		for _, line := range strings.Split(part, "\n") {
			if a, ok := strings.CutPrefix(line, "ARG "); ok {
				c.args = append(c.args, a)
			}
		}
		calls = append(calls, c)
	}
	return calls
}

// TestExportTextPerRepositoryEndToEnd exports a story over three
// repositories on GitHub (git sends the pushes to bare repositories on this
// machine; gh is the fake on PATH). Each export job reads its own material,
// writes a title and body about its repository with the line naming the
// other two, has omo check them and runs the command of its prompt: gh gets
// three different texts, as file arguments, none naming an omo ticket,
// although the developer's commit message names its task.
func TestExportTextPerRepositoryEndToEnd(t *testing.T) {
	w := newWorld(t, config{})
	api1, _, bare1 := w.addProject("api", "https://github.com/you/api.git", false)
	ui, _, bare2 := w.addProject("ui", "https://github.com/you/ui.git", false)
	web, _, bare3 := w.addProject("web", "https://github.com/you/web.git", false)
	log := t.TempDir()
	w.stop(0)
	st := w.openSettings()
	g := st.General()
	g.AgentEnv[ghLogVariable] = log
	if err := st.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	w.setScripts(st, config{scripts: map[string]string{
		// The product manager also commits in the other two repositories,
		// so every project has something to export.
		"pm": strings.Join([]string{
			"ready",
			"git|-C|../../" + ui + "/$TICKET|commit|--allow-empty|-m|feat: the sign-in form",
			"git|-C|../../" + web + "/$TICKET|commit|--allow-empty|-m|feat: the sign-in page",
			"create-tasks|1",
			"release-all",
			"until-tasks-done|10m",
			"done|story complete",
			"wait-loop|10m",
		}, "\n"),
		"export": strings.Join([]string{
			"ready",
			"write|title.txt|feat($PROJECT): sign in",
			"write|body.md|Adds signing in to the $PROJECT repository.",
			"append|body.md",
			"append|body.md|Part of a change that also touches: $OTHERS",
			"export-text",
			"sh-block|1",
			"done-url|$OUTPUT",
			"wait|10m",
		}, "\n"),
	}})
	w.start()

	story := w.create(api.CreateTicket{Type: api.TypeStory, Title: "Sign in across three", Projects: []string{api1, ui, web},
		Review: api.ReviewSkipped, WhenApproved: api.WhenApprovedExport}).Number
	w.approve(story)
	tk := w.waitColumn(story, api.ColumnDone, "")
	if tk.Resolution != api.ResolutionExported {
		t.Fatalf("story is done as %q", tk.Resolution)
	}
	list, err := w.user.Tickets(context.Background(), api.TicketFilter{Story: &story, Type: api.TypeTask, Column: api.ColumnDone})
	if err != nil || len(list.Tickets) != 1 {
		t.Fatalf("tasks %+v %v", list.Tickets, err)
	}
	task := list.Tickets[0].Number
	// The task's commit names it, as the fake developer writes it.
	if subjects := strings.Join(w.subjectsIn(tk, api1), "\n"); !strings.Contains(subjects, "#"+itoa(task)) {
		t.Fatalf("the developer's commit does not name its task:\n%s", subjects)
	}

	names := map[string]string{api1: "api", ui: "ui", web: "web"}
	calls := ghCalls(t, log)
	if len(calls) != 3 {
		t.Fatalf("%d gh calls, want 3: %+v", len(calls), calls)
	}
	bodies := map[string]bool{}
	for _, c := range calls {
		if len(c.args) < 2 || c.args[0] != "pr" || c.args[1] != "create" {
			t.Fatalf("gh %q", c.args)
		}
		var id string
		for p, name := range names {
			if c.flag("--repo") == "you/"+name {
				id = p
			}
		}
		if id == "" {
			t.Fatalf("gh for an unknown repository: %q", c.args)
		}
		var others []string
		for _, p := range []string{api1, ui, web} {
			if p != id {
				others = append(others, p)
			}
		}
		wantTitle := "feat(" + id + "): sign in"
		wantBody := "Adds signing in to the " + id + " repository.\n\nPart of a change that also touches: " + strings.Join(others, ", ")
		if c.flag("--title") != wantTitle || c.body != wantBody || c.flag("--body-file") != "pr-body.md" {
			t.Errorf("gh for %s: title %q body %q args %q", id, c.flag("--title"), c.body, c.args)
		}
		bodies[c.body] = true
		for _, n := range []int{story, task} {
			for _, text := range []string{c.flag("--title"), c.body} {
				if regexp.MustCompile(`#` + itoa(n) + `\b`).MatchString(text) {
					t.Errorf("the pull request of %s names the omo ticket #%d: %q", id, n, text)
				}
			}
		}
		url := "https://github.com/you/" + names[id] + "/pull/1"
		p, _ := projectOf(tk, id)
		if p.ExportURL != url {
			t.Errorf("%s exported as %q, want %q", id, p.ExportURL, url)
		}
		if !w.hasComment(story, "Pull request of project `"+id+"`: "+url+"\n\nTitle: `"+wantTitle+"`\n\nThe export agent wrote the title and the description for this repository.") {
			t.Errorf("no comment linking the pull request of %s:\n%s", id, strings.Join(w.comments(story), "\n---\n"))
		}
	}
	if len(bodies) != 3 {
		t.Fatalf("the three pull requests share a body: %v", bodies)
	}
	for i, bare := range []string{bare1, bare2, bare3} {
		p := []string{api1, ui, web}[i]
		pp, _ := projectOf(tk, p)
		if head, _ := gitOut(bare, "rev-parse", "--verify", "-q", "refs/heads/"+pp.Branch); head == "" {
			t.Errorf("%s was not pushed to its origin", p)
		}
	}
	w.noCrashes(story, task)
}

// subjectsIn lists the commit subjects of the ticket's branch in project.
func (w *world) subjectsIn(tk api.Ticket, project string) []string {
	w.t.Helper()
	p, ok := projectOf(tk, project)
	if !ok {
		w.t.Fatalf("#%d has no project %s", tk.Number, project)
	}
	var path string
	for _, pr := range w.projects() {
		if pr.ID == project {
			path = pr.Path
		}
	}
	out, err := gitOut(path, "log", "--format=%s", "refs/heads/"+p.Branch)
	if err != nil {
		w.t.Fatal(err)
	}
	return strings.Split(out, "\n")
}

// projects lists the registered projects.
func (w *world) projects() []api.Project {
	w.t.Helper()
	var ps []api.Project
	w.do("GET", "/api/projects", nil, &ps)
	return ps
}
