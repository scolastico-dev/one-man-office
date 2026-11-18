package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// addProject stops omo, registers a new repository (main with one commit)
// named name as a project and starts omo again. With address the
// repository's origin is that address, which git sends to a new bare
// repository on this machine (returned, "" without); with noAddress it
// names a remote origin without any address.
func (w *world) addProject(name, address string, noAddress bool) (id, repo, bare string) {
	w.t.Helper()
	w.stop(0)
	repo = newRepo(w.t)
	st := w.openSettings()
	g := st.General()
	if address != "" {
		bare = filepath.Join(w.t.TempDir(), name+".git")
		gitIn(w.t, filepath.Dir(bare), "init", "-q", "--bare", bare)
		gitIn(w.t, bare, "fetch", "-q", repo, "refs/heads/main:refs/heads/main")
		gitIn(w.t, repo, "remote", "add", "origin", address)
		n, _ := strconv.Atoi(g.AgentEnv["GIT_CONFIG_COUNT"])
		g.AgentEnv["GIT_CONFIG_COUNT"] = strconv.Itoa(n + 1)
		g.AgentEnv["GIT_CONFIG_KEY_"+strconv.Itoa(n)] = "url." + bare + ".insteadOf"
		g.AgentEnv["GIT_CONFIG_VALUE_"+strconv.Itoa(n)] = address
		if err := st.SetGeneralIf("", g); err != nil {
			w.t.Fatal(err)
		}
	}
	if noAddress {
		gitIn(w.t, repo, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	}
	p, err := st.AddProject(repo, name)
	if err != nil {
		w.t.Fatal(err)
	}
	w.start()
	return p.ID, repo, bare
}

// headIn returns the commit branch is at in the repository at dir, or "".
func headIn(dir, branch string) string {
	out, err := gitOut(dir, "rev-parse", "--verify", "-q", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return out
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// openCEO starts the CEO and returns a client that acts as it.
func (w *world) openCEO() *apiclient.Client {
	w.t.Helper()
	var sess api.AgentSession
	w.do("POST", "/api/ceo", struct{}{}, &sess)
	if sess.ID == "" {
		w.t.Fatal("the CEO session has no id")
	}
	return apiclient.New(w.srv.SocketEndpoint, sess.ID)
}

func projectOf(tk api.Ticket, id string) (api.TicketProject, bool) {
	for _, p := range tk.Projects {
		if p.Project == id {
			return p, true
		}
	}
	return api.TicketProject{}, false
}

// TestRemoveProjectFromStoryEndToEnd is the story of a project without a
// remote: a story over three repositories is reviewed and exported; the
// export of the one whose origin names no address fails and says what the
// user can do, while the others get their pull requests. The CEO, on the
// user's word, takes that project off the story: omo refuses while the
// project's branch has commits its base lacks, and with discard ends the
// reviewer that waits there, removes the worktree and deletes the branch.
// Exporting again waits for the remaining remote merges before finishing.
func TestRemoveProjectFromStoryEndToEnd(t *testing.T) {
	w := newWorld(t, config{origin: true})
	app2, repo2, bare2 := w.addProject("app2", "https://example.invalid/you/app2.git", false)
	preview, previewRepo, _ := w.addProject("preview", "", true)
	w.stop(0)
	st := w.openSettings()
	w.setScripts(st, config{scripts: map[string]string{
		// The product manager also commits in the other two repositories,
		// so every project has something to export.
		"pm": strings.Join([]string{
			"ready",
			"git|-C|../../" + app2 + "/$TICKET|rm|README.md",
			"git|-C|../../" + app2 + "/$TICKET|commit|-m|work in app2",
			"git|-C|../../" + preview + "/$TICKET|commit|--allow-empty|-m|work in preview",
			"create-tasks|1",
			"release-all",
			"until-tasks-done|10m",
			"done|story complete",
			"wait-loop|10m",
		}, "\n"),
		"export": "ready\ndone-url|https://example.invalid/you/$PROJECT/pull/$TICKET\nwait|10m",
	}})
	w.start()

	story := w.create(api.CreateTicket{Type: api.TypeStory, Title: "Across three", Projects: []string{w.project, app2, preview},
		Review: api.ReviewAgentThenHuman, Supervisor: api.RoleCEO}).Number
	w.approve(story)
	w.waitColumn(story, api.ColumnReview, api.ReviewStageHuman)
	w.do("POST", "/api/tickets/"+itoa(story)+"/approve", api.ApproveRequest{Export: true}, nil)
	var tk api.Ticket
	w.waitFor("the export to fail for "+preview, func() bool {
		tk = w.ticket(story)
		return tk.Column == api.ColumnReview && tk.ReviewStage == api.ReviewStageHuman && tk.Attention != ""
	})
	if !strings.Contains(tk.Attention, "Export of "+preview+" failed") || strings.Contains(tk.Attention, "Export of "+app2) {
		t.Fatalf("attention %q", tk.Attention)
	}
	for _, id := range []string{w.project, app2} {
		if p, _ := projectOf(tk, id); p.ExportURL != "https://example.invalid/you/"+id+"/pull/"+itoa(story) {
			t.Fatalf("project %s: %+v", id, p)
		}
	}
	if !w.hasComment(story, "omo ai ticket remove-project "+itoa(story)+" "+preview) || !w.hasComment(story, "remote add origin") {
		t.Fatalf("no comment says what to do:\n%s", strings.Join(w.comments(story), "\n---\n"))
	}
	branch := tk.Projects[0].Branch
	if remoteHead2 := headIn(bare2, branch); remoteHead2 == "" {
		t.Fatal("app2 was not pushed")
	}

	// The CEO asks without discard: refused, the commit is named.
	ceo := w.openCEO()
	p, _ := projectOf(tk, preview)
	_, err := ceo.RemoveProject(context.Background(), story, api.RemoveProjectRequest{Project: preview})
	var e *api.Error
	if !errors.As(err, &e) || e.Code != api.CodeConflict || !strings.Contains(e.Message, "work in preview") {
		t.Fatalf("without discard: %v", err)
	}
	if _, ok := projectOf(w.ticket(story), preview); !ok || headIn(previewRepo, p.Branch) == "" || !dirExists(p.Worktree) {
		t.Fatal("a refused removal changed something")
	}
	reviewer := ""
	for _, s := range w.sessions(story) {
		if s.Role == api.RoleReviewer && s.Project == preview && s.State != api.SessionEnded {
			reviewer = s.Name
		}
	}
	if reviewer == "" {
		t.Fatal("no reviewer waits in the preview project")
	}

	tk, err = ceo.RemoveProject(context.Background(), story, api.RemoveProjectRequest{Project: preview, Discard: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := projectOf(tk, preview); ok || len(tk.Projects) != 2 || tk.Attention != "" {
		t.Fatalf("after the removal: projects %+v attention %q", tk.Projects, tk.Attention)
	}
	if headIn(previewRepo, p.Branch) != "" || dirExists(p.Worktree) {
		t.Fatal("the branch or the worktree of preview is left")
	}
	for _, s := range w.sessions(story) {
		if s.Name == reviewer && s.State != api.SessionEnded {
			t.Fatalf("%s still runs", reviewer)
		}
	}
	if !w.hasComment(story, "The CEO (") || !w.hasComment(story, "removed project `"+preview+"`") {
		t.Fatalf("no comment names who removed what:\n%s", strings.Join(w.comments(story), "\n---\n"))
	}
	// The other repositories keep their branches.
	if headIn(w.repo, branch) == "" || headIn(repo2, branch) == "" {
		t.Fatal("a remaining project lost its branch")
	}

	// Exporting again waits for the remote merges: both pull requests are kept.
	w.do("POST", "/api/tickets/"+itoa(story)+"/approve", api.ApproveRequest{Export: true}, nil)
	tk = w.waitColumn(story, api.ColumnReview, api.ReviewStageAwaitingMerge)
	if tk.Resolution != "" ||
		tk.ExportURL != "https://example.invalid/you/"+w.project+"/pull/"+itoa(story)+"\nhttps://example.invalid/you/"+app2+"/pull/"+itoa(story) {
		t.Fatalf("resolution %q url %q", tk.Resolution, tk.ExportURL)
	}
	for i, id := range []string{w.project, app2} {
		p, _ := projectOf(tk, id)
		gitIn(t, []string{w.origin, bare2}[i], "update-ref", "refs/heads/"+p.ExportBase, p.ExportCommit)
	}
	w.do("POST", "/api/tickets/"+itoa(story)+"/check-export", struct{}{}, nil)
	done := w.waitColumn(story, api.ColumnDone, "")
	if done.Resolution != api.ResolutionExported || !done.ExportMerged {
		t.Fatalf("story not completed after remaining merges: %q, merged %v", done.Resolution, done.ExportMerged)
	}
	w.noCrashes(story)
}

// TestRemoveProjectRefusedWhileAgentWorksEndToEnd: a reviewer that gave its
// verdict and keeps running (it does not wait) holds its project: the
// removal is refused and names it, and nothing changes.
func TestRemoveProjectRefusedWhileAgentWorksEndToEnd(t *testing.T) {
	w := newWorld(t, config{scripts: map[string]string{"reviewer": "ready\nverdict|approve|fine\nhang"}})
	app2, _, _ := w.addProject("app2", "", false)
	story := w.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{w.project, app2},
		Review: api.ReviewAgentThenHuman}).Number
	w.approve(story)
	tk := w.waitColumn(story, api.ColumnReview, api.ReviewStageHuman)
	// The tasks worked in the first project, so its reviewer runs there.
	reviewer := ""
	for _, s := range w.sessions(story) {
		if s.Role == api.RoleReviewer && s.Project == w.project && s.State == api.SessionRunning {
			reviewer = s.Name
		}
	}
	if reviewer == "" {
		t.Fatalf("no reviewer runs in %s: %+v", w.project, w.sessions(story))
	}
	p, _ := projectOf(tk, w.project)
	_, err := w.user.RemoveProject(context.Background(), story, api.RemoveProjectRequest{Project: w.project, Discard: true})
	var e *api.Error
	if !errors.As(err, &e) || e.Code != api.CodeConflict || !strings.Contains(e.Message, reviewer+" (reviewer) is working in project") {
		t.Fatalf("error %v", err)
	}
	if _, ok := projectOf(w.ticket(story), w.project); !ok || headIn(w.repo, p.Branch) == "" || !dirExists(p.Worktree) || w.ticket(story).Busy {
		t.Fatal("a refused removal changed something")
	}
}

// TestRemoveProjectKeepsForeignBranchEndToEnd: when the branch of the
// project is not the story's alone (here another ticket records the same
// name in that repository), omo removes the clean worktree but never
// deletes the branch, and says why.
func TestRemoveProjectKeepsForeignBranchEndToEnd(t *testing.T) {
	w := newWorld(t, config{})
	app2, repo2, _ := w.addProject("app2", "", false)
	story := w.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{w.project, app2},
		Review: api.ReviewHuman}).Number
	w.approve(story)
	tk := w.waitColumn(story, api.ColumnReview, api.ReviewStageHuman)
	p, _ := projectOf(tk, app2)
	tip := headIn(repo2, p.Branch)
	other := w.create(api.CreateTicket{Type: api.TypeTask, Title: "Other", Projects: []string{app2}}).Number
	// Another ticket records the same branch name in that repository, as
	// a monorepo's second project could.
	d, err := db.Open(w.layout.DB)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Exec(`UPDATE ticket_projects SET branch = ? WHERE ticket_id = ?`, p.Branch, other)
	d.Close()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.user.RemoveProject(context.Background(), story, api.RemoveProjectRequest{Project: app2}); err != nil {
		t.Fatal(err)
	}
	if headIn(repo2, p.Branch) != tip {
		t.Fatal("omo deleted or moved a branch that is not the story's alone")
	}
	if dirExists(p.Worktree) {
		t.Fatal("the clean worktree is left")
	}
	if !w.hasComment(story, "omo kept the branch `"+p.Branch+"`") || !w.hasComment(story, fmt.Sprintf("#%d", other)) {
		t.Fatalf("no comment says why the branch was kept:\n%s", strings.Join(w.comments(story), "\n---\n"))
	}
}

func dirExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
