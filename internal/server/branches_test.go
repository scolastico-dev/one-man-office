package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// gitIn runs git in dir without the developer's configuration.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// committedRepo creates a repository whose first commit is on branch.
func committedRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", branch)
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "first")
	return dir
}

func TestProjectBranches(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)

	// On main, no remote: main is the default.
	onMain := committedRepo(t, "main")
	// On a feature branch of a repository whose only long-lived branch is
	// master.
	onFeature := committedRepo(t, "master")
	gitIn(t, onFeature, "switch", "-q", "-c", "feature")
	// The remote's recorded HEAD wins over an existing main.
	cloned := committedRepo(t, "main")
	gitIn(t, cloned, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	gitIn(t, cloned, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	// A detached HEAD and no main, master or remote: both unknown.
	detached := committedRepo(t, "work")
	gitIn(t, detached, "switch", "-q", "--detach")
	// A folder that disappeared: both unknown, and no git is asked.
	gone := committedRepo(t, "main")

	want := map[string][2]string{}
	for path, w := range map[string][2]string{
		onMain: {"main", "main"}, onFeature: {"feature", "master"}, cloned: {"main", "trunk"},
		detached: {"", ""}, gone: {"", ""},
	} {
		resp := asUser(b, s, "POST", "/api/projects", api.CreateProject{Path: path, Name: filepath.Base(path) + "-x"})
		wantStatus(t, resp.StatusCode, http.StatusCreated)
		p := body[api.Project](t, resp)
		if path != gone && (p.CurrentBranch != w[0] || p.DefaultBranch != w[1]) {
			t.Errorf("create %s: branches %q %q, want %q %q", path, p.CurrentBranch, p.DefaultBranch, w[0], w[1])
		}
		want[p.ID] = w
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	check := func(who string, raw []byte) {
		t.Helper()
		var ps []api.Project
		if err := json.Unmarshal(raw, &ps); err != nil {
			t.Fatal(err)
		}
		if len(ps) != len(want) {
			t.Fatalf("%s: %d projects, want %d", who, len(ps), len(want))
		}
		for _, p := range ps {
			if w := want[p.ID]; p.CurrentBranch != w[0] || p.DefaultBranch != w[1] {
				t.Errorf("%s: %s branches %q %q, want %q %q", who, p.ID, p.CurrentBranch, p.DefaultBranch, w[0], w[1])
			}
		}
		// Unknown branches are left out, not sent empty.
		if strings.Contains(string(raw), `"current_branch":""`) || strings.Contains(string(raw), `"default_branch":""`) {
			t.Errorf("%s: empty branch fields are sent: %s", who, raw)
		}
	}
	resp := asUser(b, s, "GET", "/api/projects", nil)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	check("user", raw)
	dev := liveSession(t, s, api.RoleDeveloper)
	delete(want, dev.Project) // liveSession added a project of its own
	rec := socketDo(t, s, "GET", "/api/projects", nil, dev.ID)
	wantStatus(t, rec.Code, http.StatusOK)
	var all []api.Project
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	var mine []api.Project
	for _, p := range all {
		if _, ok := want[p.ID]; ok {
			mine = append(mine, p)
		}
	}
	raw, _ = json.Marshal(mine)
	check("agent", raw)

	// The project.changed event carries them too.
	var id string
	for pid, w := range want {
		if w[1] == "trunk" {
			id = pid
		}
	}
	resp = asUser(b, s, "PATCH", "/api/projects/"+id, map[string]any{"name": "renamed"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if p := body[api.Project](t, resp); p.CurrentBranch != "main" || p.DefaultBranch != "trunk" {
		t.Errorf("patch: branches %q %q", p.CurrentBranch, p.DefaultBranch)
	}
	s.waitSettingsEvents(context.Background())
	evs, err := db.AllEvents(s.db)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range evs {
		if ev.Kind != api.KindProjectChanged {
			continue
		}
		var d api.ProjectChangedData
		if err := json.Unmarshal([]byte(ev.Detail), &d); err != nil {
			t.Fatal(err)
		}
		if d.Project.ID == id && d.Project.Name == "renamed" {
			found = true
			if d.Project.CurrentBranch != "main" || d.Project.DefaultBranch != "trunk" {
				t.Errorf("event: branches %q %q", d.Project.CurrentBranch, d.Project.DefaultBranch)
			}
		}
	}
	if !found {
		t.Fatal("no project.changed event for the rename")
	}
}
