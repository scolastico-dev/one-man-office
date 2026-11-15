package server

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

// isolateGit keeps the server's git, and the test's, away from the
// developer's git configuration.
func isolateGit(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "omo-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "omo@test")
	t.Setenv("GIT_COMMITTER_NAME", "omo-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "omo@test")
}

// skillRepo makes a local bare repository holding SKILLS.md and returns
// its folder and commit.
func skillRepo(t *testing.T) (string, string) {
	t.Helper()
	work := t.TempDir()
	bare := filepath.Join(t.TempDir(), "skills.git")
	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(work, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(work, "SKILLS.md"), []byte("skills\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(work, "add", "SKILLS.md")
	run(work, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "skills")
	run(work, "clone", "-q", "--bare", work, bare)
	return bare, run(bare, "rev-parse", "main")
}

// waitSkillSet waits until the set's state is want.
func waitSkillSet(t *testing.T, b *browser, s *Server, name, want string) api.SkillSetState {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp := b.do("GET", "/api/skillsets", nil, bearer(s))
		wantStatus(t, resp.StatusCode, http.StatusOK)
		got := decodeBody[api.SkillSetsResponse](t, resp.Body)
		for _, st := range got.States {
			if st.Name == name && st.State == want {
				return st
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("states = %+v, want %s %s", got.States, name, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSkillSetRoutes(t *testing.T) {
	isolateGit(t)
	repo, commit := skillRepo(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	hdr := func(rev string) map[string]string {
		h := bearer(s)
		h["If-Match"] = `"` + rev + `"`
		return h
	}
	set := api.SkillSet{Repository: repo, Update: api.SkillSetAtStart, Enabled: true,
		Instructions: map[string]string{"developer": "Read SKILLS.md first."}}

	resp := b.do("GET", "/api/skillsets", nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"skill_sets", "states", "errors"} {
		if string(raw[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, raw[key])
		}
	}

	resp = b.do("PUT", "/api/skillsets/sp", set, hdr(api.NoRevision))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	put := decodeBody[api.SkillSet](t, resp.Body)
	if put.Name != "sp" || put.Revision == "" || put.Instructions["developer"] != "Read SKILLS.md first." {
		t.Fatalf("PUT answered %+v", put)
	}
	st := waitSkillSet(t, b, s, "sp", "ok")
	if st.Commit != commit || st.Path != filepath.Join(s.opts.Layout.Skills, "sp") || st.UpdatedAt == nil {
		t.Fatalf("state = %+v, want commit %s", st, commit)
	}

	// Create only: a second create is stale.
	resp = b.do("PUT", "/api/skillsets/sp", set, hdr(api.NoRevision))
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if code := errorCode(t, resp.Body); code != api.CodeStaleSettings {
		t.Fatalf("code = %s", code)
	}
	// A source that runs a program is refused, naming the field.
	bad := set
	bad.Repository = "ext::sh -c touch% /tmp/x"
	resp = b.do("PUT", "/api/skillsets/bad", bad, hdr(api.NoRevision))
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)

	resp = b.do("POST", "/api/skillsets/sp/update", map[string]any{}, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := decodeBody[api.SkillSetState](t, resp.Body); got.State != "ok" || got.Commit != commit {
		t.Fatalf("update answered %+v", got)
	}
	resp = b.do("POST", "/api/skillsets/ghost/update", map[string]any{}, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusNotFound)

	evs := settingsEvents(t, s, api.KindSkillSetChanged, 0)
	if len(evs) < 2 {
		t.Fatalf("skillset.changed events = %d, want at least 2", len(evs))
	}
	var last api.SkillSetState
	if err := json.Unmarshal(evs[len(evs)-1], &last); err != nil || last.Name != "sp" || last.State != "ok" {
		t.Fatalf("last event = %s", evs[len(evs)-1])
	}

	rev := s.settings.Revision("skillsets", "sp")
	resp = b.do("DELETE", "/api/skillsets/sp", nil, hdr("0123456789abcdef"))
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	resp = b.do("DELETE", "/api/skillsets/sp", nil, hdr(rev))
	wantStatus(t, resp.StatusCode, http.StatusNoContent)
	if _, err := os.Lstat(filepath.Join(s.opts.Layout.Skills, "sp")); err == nil {
		t.Fatal("the checkout outlived its skill set")
	}
	resp = b.do("GET", "/api/skillsets", nil, bearer(s))
	if got := decodeBody[api.SkillSetsResponse](t, resp.Body); len(got.SkillSets) != 0 || len(got.States) != 0 {
		t.Fatalf("after delete: %+v", got)
	}
}

func TestSkillSetRoutesRefuseAgents(t *testing.T) {
	s := newTestServer(t)
	for _, role := range []api.Role{api.RoleCEO, api.RolePM, api.RoleDeveloper, api.RoleReviewer} {
		sess := liveSession(t, s, role)
		for _, r := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/api/skillsets", nil},
			{"PUT", "/api/skillsets/x", api.SkillSet{Repository: "/srv/x.git", Update: api.SkillSetPinned}},
			{"DELETE", "/api/skillsets/x", nil},
			{"POST", "/api/skillsets/x/update", map[string]any{}},
		} {
			rec := socketDo(t, s, r.method, r.path, r.body, sess.ID)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s by %s = %d, want 403", r.method, r.path, role, rec.Code)
			}
		}
	}
	if _, ok := s.settings.SkillSet("x"); ok {
		t.Fatal("an agent wrote a skill set")
	}
}

func TestPreviewShowsSkillSets(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	if err := s.settings.PutSkillSetIf("", api.SkillSet{Name: "sp", Repository: "/srv/skills.git", Update: api.SkillSetPinned, Enabled: true,
		Instructions: map[string]string{"developer": "Read SKILLS.md first."}}); err != nil {
		t.Fatal(err)
	}
	preview := func(name, text string) api.PromptPreview {
		t.Helper()
		resp := b.do("POST", "/api/prompts/"+name+"/preview", api.PromptUpdate{Text: text}, bearer(s))
		wantStatus(t, resp.StatusCode, http.StatusOK)
		return decodeBody[api.PromptPreview](t, resp.Body)
	}
	// Without a checkout there is no block.
	if p := preview("developer-instructions", "Be brief."); strings.Contains(p.Text, "Skill set") {
		t.Fatalf("a set without checkout is in the preview:\n%s", p.Text)
	}
	checkout := filepath.Join(s.opts.Layout.Skills, "sp")
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	block := "Skill set sp (checked out at " + checkout + "; read-only, omo updates it):\nRead SKILLS.md first."
	if p := preview("developer-instructions", "Be brief."); p.Error != "" || !strings.HasSuffix(p.Text, "Be brief.\n\n"+block) {
		t.Fatalf("instructions preview = %+v", p)
	}
	if p := preview("developer", prompts.Defaults()["developer"]); p.Error != "" || !strings.HasSuffix(p.Text, "## Your instructions (from the user)\n"+block) {
		t.Fatalf("role preview = %+v", p)
	}
	if p := preview("developer", "Custom.\n{{.Instructions}}"); p.Text != "Custom.\n"+block {
		t.Fatalf("customized role preview = %+v", p)
	}
	// The reviewer has no text: its preview is as before.
	want, _ := prompts.Preview("reviewer-instructions", "Be brief.", prompts.Defaults()["reviewer"])
	if p := preview("reviewer-instructions", "Be brief."); p.Text != want {
		t.Fatalf("reviewer preview = %+v", p)
	}
	// Template errors are still reported.
	if p := preview("developer", "{{ .Nope }}"); p.Text != "" || !strings.Contains(p.Error, "Nope") {
		t.Fatalf("broken preview = %+v", p)
	}
	if p := preview("start", "Hi {{.Name}}."); p.Text != "Hi Dana." {
		t.Fatalf("message preview = %+v", p)
	}
}

// TestSkillSetFileChangeReachesCheckouts: a skill set written past the
// routes (a hand edit of its file) is fetched through the settings feed,
// without waiting for the manager's own poll.
func TestSkillSetFileChangeReachesCheckouts(t *testing.T) {
	isolateGit(t)
	repo, commit := skillRepo(t)
	old := skillSetPoll
	skillSetPoll = time.Hour
	t.Cleanup(func() { skillSetPoll = old })
	s := newTestServer(t)
	b := newBrowser(t, s)
	set := api.SkillSet{Name: "hand", Repository: repo, Update: api.SkillSetAtStart, Enabled: true}
	if err := s.settings.PutSkillSetIf(api.NoRevision, set); err != nil {
		t.Fatal(err)
	}
	if st := waitSkillSet(t, b, s, "hand", "ok"); st.Commit != commit {
		t.Fatalf("state = %+v", st)
	}
}
