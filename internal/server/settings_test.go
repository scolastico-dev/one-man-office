package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// settingsEvents returns the data of every logged event of kind after id,
// once the settings changes queued so far are recorded.
func settingsEvents(t *testing.T, s *Server, kind string, after int64) []json.RawMessage {
	t.Helper()
	s.waitSettingsEvents(context.Background())
	evs, err := db.EventsSince(s.db, after, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	var out []json.RawMessage
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, json.RawMessage(e.Detail))
		}
	}
	return out
}

func wantEvents(t *testing.T, s *Server, kind string, after int64, n int) []json.RawMessage {
	t.Helper()
	got := settingsEvents(t, s, kind, after)
	if len(got) != n {
		t.Fatalf("%d %s events, want %d", len(got), kind, n)
	}
	return got
}

// asUser sends a request through the browser handler with the login token.
func asUser(b *browser, s *Server, method, path string, body any) *http.Response {
	return b.do(method, path, body, bearer(s))
}

func body[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	return decodeBody[T](t, resp.Body)
}

func TestAddAndRemoveProjectOverHTTP(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	start := lastEventID(t, s)
	repo := newRepo(t)

	resp := asUser(b, s, "POST", "/api/projects", api.CreateProject{Path: repo, Name: "my/repo"})
	wantStatus(t, resp.StatusCode, http.StatusCreated)
	p := body[api.Project](t, resp)
	if p.ID != "my-repo" || p.Name != "my/repo" || !p.Enabled || p.Missing {
		t.Fatalf("project %+v", p)
	}
	ev := wantEvents(t, s, api.KindProjectChanged, start, 1)
	var data api.ProjectChangedData
	if err := json.Unmarshal(ev[0], &data); err != nil || data.Project.ID != p.ID {
		t.Fatalf("event %s (%v)", ev[0], err)
	}

	resp = asUser(b, s, "PATCH", "/api/projects/"+p.ID, map[string]any{"enabled": false})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if body[api.Project](t, resp).Enabled {
		t.Fatal("project still enabled")
	}
	wantEvents(t, s, api.KindProjectChanged, start, 2)

	// Disabled projects only show with all=true.
	list := body[[]api.Project](t, asUser(b, s, "GET", "/api/projects", nil))
	if len(list) != 0 {
		t.Fatalf("list %+v", list)
	}
	list = body[[]api.Project](t, asUser(b, s, "GET", "/api/projects?all=true&q=MY", nil))
	if len(list) != 1 {
		t.Fatalf("all list %+v", list)
	}

	resp = asUser(b, s, "DELETE", "/api/projects/"+p.ID, nil)
	wantStatus(t, resp.StatusCode, http.StatusNoContent)
	wantEvents(t, s, api.KindProjectRemoved, start, 1)
	resp = asUser(b, s, "DELETE", "/api/projects/"+p.ID, nil)
	wantStatus(t, resp.StatusCode, http.StatusNotFound)
	if code := errorCode(t, resp.Body); code != api.CodeNotFound {
		t.Fatalf("code %q", code)
	}
}

func TestProjectErrors(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	resp := asUser(b, s, "POST", "/api/projects", api.CreateProject{Path: t.TempDir()}) // not a git repository
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	resp = asUser(b, s, "POST", "/api/projects", map[string]any{"path": newRepo(t), "bogus": 1})
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	repo := newRepo(t)
	wantStatus(t, asUser(b, s, "POST", "/api/projects", api.CreateProject{Path: repo}).StatusCode, http.StatusCreated)
	resp = asUser(b, s, "POST", "/api/projects", api.CreateProject{Path: repo})
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("duplicate path status %d", resp.StatusCode)
	}
	resp = asUser(b, s, "PATCH", "/api/projects/nope", map[string]any{"name": "x"})
	wantStatus(t, resp.StatusCode, http.StatusNotFound)
	resp = asUser(b, s, "PATCH", "/api/projects/nope", map[string]any{"name": nil})
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	resp = asUser(b, s, "DELETE", "/api/projects", nil)
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	// No rescan route of our own: it still answers not implemented.
	wantStatus(t, asUser(b, s, "POST", "/api/projects/rescan", nil).StatusCode, http.StatusNotImplemented)
}

func TestDeleteMissingProjects(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	start := lastEventID(t, s)
	gone, keep := newRepo(t), newRepo(t)
	pg := body[api.Project](t, asUser(b, s, "POST", "/api/projects", api.CreateProject{Path: gone, Name: "gone"}))
	pk := body[api.Project](t, asUser(b, s, "POST", "/api/projects", api.CreateProject{Path: keep, Name: "keep"}))
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	list := body[[]api.Project](t, asUser(b, s, "GET", "/api/projects?all=true", nil))
	if len(list) != 2 {
		t.Fatalf("list %+v", list)
	}
	resp := asUser(b, s, "DELETE", "/api/projects?missing=true", nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got := body[api.RemovedProjects](t, resp)
	if len(got.Removed) != 1 || got.Removed[0] != pg.ID {
		t.Fatalf("removed %+v", got)
	}
	wantEvents(t, s, api.KindProjectRemoved, start, 1)
	// Nothing missing: an empty list, not null.
	resp = asUser(b, s, "DELETE", "/api/projects?missing=true", nil)
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), `"removed":[]`) {
		t.Fatalf("body %s", raw)
	}
	if _, ok := s.settings.Project(pk.ID); !ok {
		t.Fatal("kept project removed")
	}
}

func TestPutSettingsValidates(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	start := lastEventID(t, s)
	cur := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil))
	if cur.Settings.MaxRunningAgents != 4 || len(cur.Errors) != 0 {
		t.Fatalf("settings %+v", cur)
	}

	bad := cur.Settings
	bad.MaxRunningAgents = 0
	resp := asUser(b, s, "PUT", "/api/settings", bad)
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if code := errorCode(t, resp.Body); code != api.CodeInvalidSettings {
		t.Fatalf("code %q", code)
	}
	wantEvents(t, s, api.KindSettingsChanged, start, 0)

	good := cur.Settings
	good.MaxRunningAgents = 7
	resp = asUser(b, s, "PUT", "/api/settings", good)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := body[api.SettingsResponse](t, resp); got.Settings.MaxRunningAgents != 7 || got.Errors == nil {
		t.Fatalf("put answer %+v", got)
	}
	ev := wantEvents(t, s, api.KindSettingsChanged, start, 1)
	var data api.SettingsChangedData
	json.Unmarshal(ev[0], &data)
	if data.Section != "general" || data.Name != "" {
		t.Fatalf("event %s", ev[0])
	}
	if s.settings.General().MaxRunningAgents != 7 {
		t.Fatal("not stored")
	}

	// Unknown keys are rejected, also nested ones.
	var raw map[string]any
	data2, _ := json.Marshal(good)
	json.Unmarshal(data2, &raw)
	raw["max_runing_agents"] = 2
	resp = asUser(b, s, "PUT", "/api/settings", raw)
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	raw = map[string]any{}
	json.Unmarshal(data2, &raw)
	raw["ceo"].(map[string]any)["enabld"] = false
	resp = asUser(b, s, "PUT", "/api/settings", raw)
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	wantEvents(t, s, api.KindSettingsChanged, start, 1)

	// A body that is not JSON content is refused centrally.
	resp = b.do("PUT", "/api/settings", good, map[string]string{"Authorization": "Bearer " + s.token, "Content-Type": "text/plain"})
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
}

func TestTurningCEOOffReassignsSupervisors(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	ceo := liveSession(t, s, api.RoleCEO)
	actor, err := s.board.ResolveSession(context.Background(), ceo.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.settings.AddProject(newRepo(t), "")
	if err != nil {
		t.Fatal(err)
	}
	story, err := s.board.Create(context.Background(), actor, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if story.Supervisor != api.RoleCEO {
		t.Fatalf("supervisor %q", story.Supervisor)
	}
	g := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil)).Settings
	g.CEO.Enabled = false
	wantStatus(t, asUser(b, s, "PUT", "/api/settings", g).StatusCode, http.StatusOK)
	got, err := s.board.Get(context.Background(), board.User, int64(story.Number))
	if err != nil {
		t.Fatal(err)
	}
	if got.Supervisor != api.RoleUser {
		t.Fatalf("supervisor after CEO off = %q", got.Supervisor)
	}
}

func TestAgentSeesOnlySelectableModels(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	f := false
	prof := api.Profile{
		CLI: "claude", Command: "claude",
		Models: map[string]api.ProfileModel{
			"open":   {},
			"hidden": {Selectable: &f},
		},
	}
	resp := asUser(b, s, "PUT", "/api/profiles/claude", prof)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	sess := liveSession(t, s, api.RoleDeveloper)

	rec := socketDo(t, s, "GET", "/api/models", nil, sess.ID)
	wantStatus(t, rec.Code, http.StatusOK)
	ms := decodeBody[[]api.Model](t, rec.Body)
	if len(ms) != 1 || ms[0].ID != "claude/open" {
		t.Fatalf("agent models %+v", ms)
	}
	ms = body[[]api.Model](t, asUser(b, s, "GET", "/api/models", nil))
	if len(ms) != 2 {
		t.Fatalf("user models %+v", ms)
	}
	// An empty list is [], never null.
	s2 := newTestServer(t)
	rec = socketDo(t, s2, "GET", "/api/models", nil, "")
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty models %q", rec.Body.String())
	}
}

func TestAgentCannotWriteSettings(t *testing.T) {
	s := newTestServer(t)
	repo := newRepo(t)
	p, _ := s.settings.AddProject(repo, "")
	sessions := map[api.Role]api.AgentSession{
		api.RoleDeveloper: liveSession(t, s, api.RoleDeveloper),
		api.RoleCEO:       liveSession(t, s, api.RoleCEO),
	}
	projects := len(s.settings.Projects(true))
	for role, sess := range sessions {
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/api/settings", nil},
			{"PUT", "/api/settings", s.settings.General()},
			{"POST", "/api/projects", api.CreateProject{Path: newRepo(t)}},
			{"PATCH", "/api/projects/" + p.ID, map[string]any{"enabled": false}},
			{"DELETE", "/api/projects/" + p.ID, nil},
			{"DELETE", "/api/projects?missing=true", nil},
			{"GET", "/api/profiles", nil},
			{"PUT", "/api/profiles/x", api.Profile{}},
			{"DELETE", "/api/profiles/x", nil},
			{"GET", "/api/roles", nil},
			{"PUT", "/api/roles/pm", api.RoleSettings{}},
			{"GET", "/api/prompts", nil},
			{"GET", "/api/prompts/x", nil},
			{"PUT", "/api/prompts/x", api.PromptUpdate{Text: "t"}},
			{"DELETE", "/api/prompts/x", nil},
		} {
			rec := socketDo(t, s, c.method, c.path, c.body, sess.ID)
			if rec.Code != http.StatusForbidden || errorCode(t, rec.Body) != api.CodeForbidden {
				t.Errorf("%s %s as %s: status %d", c.method, c.path, role, rec.Code)
			}
		}
	}
	if !s.settings.General().CEO.Enabled || len(s.settings.Projects(true)) != projects {
		t.Fatal("an agent changed settings")
	}
	// Agents may list and search projects.
	sess := liveSession(t, s, api.RoleDeveloper)
	rec := socketDo(t, s, "GET", "/api/projects?q=nothing-matches", nil, sess.ID)
	wantStatus(t, rec.Code, http.StatusOK)
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("search %q", rec.Body.String())
	}
	rec = socketDo(t, s, "GET", "/api/projects", nil, sess.ID)
	wantStatus(t, rec.Code, http.StatusOK)
	if got := decodeBody[[]api.Project](t, rec.Body); len(got) < 1 {
		t.Fatalf("projects %+v", got)
	}
}

func TestHandEditInvalidFileEmitsEvent(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	start := lastEventID(t, s)
	path := filepath.Join(s.opts.Layout.Root, "settings.yaml")
	if err := os.WriteFile(path, []byte("max_running_agents: [oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.settings.Reload(); len(got) != 1 || got[0].Err == nil {
		t.Fatalf("reload %+v", got)
	}
	ev := wantEvents(t, s, api.KindSettingsInvalid, start, 1)
	var data api.SettingsInvalidData
	json.Unmarshal(ev[0], &data)
	if data.File != path || data.Message == "" {
		t.Fatalf("event %s", ev[0])
	}
	wantEvents(t, s, api.KindSettingsChanged, start, 0)

	resp := asUser(b, s, "GET", "/api/settings", nil)
	got := body[api.SettingsResponse](t, resp)
	if len(got.Errors) != 1 || got.Errors[0].File != path || got.Settings.MaxRunningAgents != 4 {
		t.Fatalf("answer %+v", got)
	}

	// Fixing the file clears the error and reports one change.
	if err := os.WriteFile(path, []byte("max_running_agents: 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.settings.Reload()
	wantEvents(t, s, api.KindSettingsChanged, start, 1)
	wantEvents(t, s, api.KindSettingsInvalid, start, 1)
	got = body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil))
	if len(got.Errors) != 0 || got.Settings.MaxRunningAgents != 9 {
		t.Fatalf("answer %+v", got)
	}
	// An unchanged file reports nothing more.
	s.settings.Reload()
	wantEvents(t, s, api.KindSettingsChanged, start, 1)
}

func TestHandEditReachesStreamOnce(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	st := openStream(t, b, s, "", bearer(s))
	defer st.close()
	prof := filepath.Join(s.opts.Layout.Root, "profiles", "hand.yaml")
	data := "cli: custom\ncommand: x\nmodels:\n  m: {}\n"
	if err := os.WriteFile(prof, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	s.settings.Reload()
	ev := st.nextEvent()
	var d api.SettingsChangedData
	json.Unmarshal(ev.Data, &d)
	if ev.Kind != api.KindSettingsChanged || d.Section != "profiles" || d.Name != "hand" || ev.Actor.Role != api.RoleSystem {
		t.Fatalf("event %+v", ev)
	}
	// Through the API: exactly one event, after the hand edit's.
	resp := asUser(b, s, "PUT", "/api/profiles/viaapi", api.Profile{CLI: "custom", Command: "y", Models: map[string]api.ProfileModel{"m": {}}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	ev = st.nextEvent()
	json.Unmarshal(ev.Data, &d)
	if d.Name != "viaapi" {
		t.Fatalf("event %+v", ev)
	}
	// A broken hand edit goes through the stream too.
	if err := os.WriteFile(prof, []byte("cli: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.settings.Reload()
	if ev = st.nextEvent(); ev.Kind != api.KindSettingsInvalid {
		t.Fatalf("event %+v", ev)
	}
	list := body[api.ProfilesResponse](t, asUser(b, s, "GET", "/api/profiles", nil))
	if len(list.Errors) != 1 || len(list.Profiles) != 2 {
		t.Fatalf("profiles %+v", list)
	}
}

func TestProfileRoutes(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	list := body[api.ProfilesResponse](t, asUser(b, s, "GET", "/api/profiles", nil))
	if list.Profiles == nil || list.Errors == nil {
		t.Fatalf("nulls in %+v", list)
	}
	p := api.Profile{Name: "ignored", CLI: "claude", Command: "claude", Models: map[string]api.ProfileModel{"fable": {Description: "d"}}}
	resp := asUser(b, s, "PUT", "/api/profiles/claude", p)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got := body[api.Profile](t, resp)
	if got.Name != "claude" || got.Models["fable"].Description != "d" || got.Args == nil || got.Env == nil {
		t.Fatalf("profile %+v", got)
	}
	// Invalid profile, bad name, unknown key.
	resp = asUser(b, s, "PUT", "/api/profiles/claude", api.Profile{CLI: "claude", Command: "claude"})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	resp = asUser(b, s, "PUT", "/api/profiles/Bad_Name", p)
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	resp = asUser(b, s, "PUT", "/api/profiles/claude", map[string]any{"cli": "claude", "command": "c", "models": map[string]any{"m": map[string]any{}}, "extra": 1})
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)

	// A role that lists a model makes the profile undeletable.
	wantStatus(t, asUser(b, s, "PUT", "/api/roles/developer", api.RoleSettings{Models: []string{"claude/fable"}, Assignment: "random"}).StatusCode, http.StatusOK)
	resp = asUser(b, s, "DELETE", "/api/profiles/claude", nil)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if code := errorCode(t, resp.Body); code != api.CodeInUse {
		t.Fatalf("code %q", code)
	}
	wantStatus(t, asUser(b, s, "PUT", "/api/roles/developer", api.RoleSettings{Models: []string{}, Assignment: "round_robin"}).StatusCode, http.StatusOK)
	wantStatus(t, asUser(b, s, "DELETE", "/api/profiles/claude", nil).StatusCode, http.StatusNoContent)
	wantStatus(t, asUser(b, s, "DELETE", "/api/profiles/claude", nil).StatusCode, http.StatusNotFound)
}

func TestRoleRoutes(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	roles := body[[]api.RoleSettings](t, asUser(b, s, "GET", "/api/roles", nil))
	if len(roles) != 4 {
		t.Fatalf("roles %+v", roles)
	}
	for _, r := range roles {
		if r.Models == nil {
			t.Fatalf("null models in %+v", r)
		}
	}
	resp := asUser(b, s, "PUT", "/api/roles/pm", api.RoleSettings{Models: []string{"nope/none"}, Assignment: "random"})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	resp = asUser(b, s, "PUT", "/api/roles/janitor", api.RoleSettings{Models: []string{}, Assignment: "random"})
	wantStatus(t, resp.StatusCode, http.StatusNotFound)
	resp = asUser(b, s, "PUT", "/api/roles/pm", map[string]any{"models": []string{}, "assignment": "random", "x": 1})
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	resp = asUser(b, s, "PUT", "/api/roles/pm", api.RoleSettings{Models: []string{}, Assignment: "random"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := body[api.RoleSettings](t, resp); got.Name != "pm" || got.Assignment != "random" {
		t.Fatalf("role %+v", got)
	}
}

func TestPromptRoutes(t *testing.T) {
	l := testHome(t)
	s, err := newServer(Options{Layout: l, Config: testConfig(), Version: "test", PromptDefaults: map[string]string{"pm": "default pm"}}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	b := newBrowser(t, s)
	start := lastEventID(t, s)

	infos := body[[]api.PromptInfo](t, asUser(b, s, "GET", "/api/prompts", nil))
	if len(infos) != 1 || infos[0].Name != "pm" || infos[0].Customized {
		t.Fatalf("prompts %+v", infos)
	}
	got := body[api.Prompt](t, asUser(b, s, "GET", "/api/prompts/pm", nil))
	if got.Text != "default pm" || got.Customized {
		t.Fatalf("prompt %+v", got)
	}
	resp := asUser(b, s, "PUT", "/api/prompts/pm", api.PromptUpdate{Text: "mine"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got = body[api.Prompt](t, resp); got.Text != "mine" || !got.Customized {
		t.Fatalf("prompt %+v", got)
	}
	wantEvents(t, s, api.KindSettingsChanged, start, 1)
	wantStatus(t, asUser(b, s, "DELETE", "/api/prompts/pm", nil).StatusCode, http.StatusNoContent)
	if got = body[api.Prompt](t, asUser(b, s, "GET", "/api/prompts/pm", nil)); got.Customized || got.Text != "default pm" {
		t.Fatalf("prompt after reset %+v", got)
	}
	wantEvents(t, s, api.KindSettingsChanged, start, 2)
	wantStatus(t, asUser(b, s, "GET", "/api/prompts/none", nil).StatusCode, http.StatusNotFound)
	wantStatus(t, asUser(b, s, "PUT", "/api/prompts/pm", api.PromptUpdate{Text: " "}).StatusCode, http.StatusUnprocessableEntity)
	wantStatus(t, asUser(b, s, "DELETE", "/api/prompts/none", nil).StatusCode, http.StatusNotFound)
	resp = asUser(b, s, "PUT", "/api/prompts/pm", map[string]any{"text": "x", "more": 1})
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
}

func TestSettingsWriteWhileClosingDoesNotPanic(t *testing.T) {
	s := newTestServer(t)
	s.Close()
	// The store still works; the change cannot be recorded any more.
	if err := s.settings.SetGeneral(s.settings.General()); err != nil {
		t.Fatal(err)
	}
}

func TestStartupHandsCEOWorkToUserWhenCEOIsOff(t *testing.T) {
	layout := testHome(t)
	s := openServer(t, layout, testConfig())
	actor, err := s.board.ResolveSession(context.Background(), liveSession(t, s, api.RoleCEO).ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.settings.AddProject(newRepo(t), "")
	if err != nil {
		t.Fatal(err)
	}
	story, err := s.board.Create(context.Background(), actor, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{p.ID}})
	if err != nil || story.Supervisor != api.RoleCEO {
		t.Fatalf("story %+v, %v", story, err)
	}
	s.Close()
	// The CEO is switched off by hand while the server is down.
	if err := os.WriteFile(filepath.Join(layout.Root, "settings.yaml"), []byte("ceo:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := openServer(t, layout, testConfig())
	got, err := s2.board.Get(context.Background(), board.User, int64(story.Number))
	if err != nil {
		t.Fatal(err)
	}
	if got.Supervisor != api.RoleUser {
		t.Fatalf("supervisor after start with CEO off = %q", got.Supervisor)
	}
}

func TestStartupEmitsInvalidSettingsEventPerBrokenFile(t *testing.T) {
	layout := testHome(t)
	general := filepath.Join(layout.Root, "settings.yaml")
	prof := filepath.Join(layout.Root, "profiles", "bad.yaml")
	if err := os.WriteFile(general, []byte("max_running_agents: [oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(prof), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prof, []byte("cli: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := openServer(t, layout, testConfig())
	files := map[string]int{}
	for _, raw := range settingsEvents(t, s, api.KindSettingsInvalid, 0) {
		var d api.SettingsInvalidData
		if err := json.Unmarshal(raw, &d); err != nil || d.Message == "" {
			t.Fatalf("event %s: %v", raw, err)
		}
		files[d.File]++
	}
	if len(files) != 2 || files[general] != 1 || files[prof] != 1 {
		t.Fatalf("events per file = %v, want one each for %s and %s", files, general, prof)
	}
	// An unchanged reload reports nothing more.
	s.settings.Reload()
	s.waitSettingsEvents(context.Background())
	if n := len(settingsEvents(t, s, api.KindSettingsInvalid, 0)); n != 2 {
		t.Fatalf("%d events after reload, want 2", n)
	}
}

func TestListProjectsRejectsUnparseableAll(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	resp := asUser(b, s, "GET", "/api/projects?all=maybe", nil)
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	if code := errorCode(t, resp.Body); code != api.CodeBadRequest {
		t.Fatalf("code %q", code)
	}
	for _, q := range []string{"", "?all=true", "?all=false", "?all=1"} {
		wantStatus(t, asUser(b, s, "GET", "/api/projects"+q, nil).StatusCode, http.StatusOK)
	}
}

func TestPutNameInBodyMustMatchPath(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	prof := map[string]any{"cli": "custom", "command": "x", "models": map[string]any{"m": map[string]any{}}}
	role := map[string]any{"models": []string{}, "assignment": "random"}
	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{"/api/profiles/one", prof},
		{"/api/roles/pm", role},
	} {
		bad := map[string]any{"name": "other"}
		for k, v := range c.body {
			bad[k] = v
		}
		resp := asUser(b, s, "PUT", c.path, bad)
		wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
		if code := errorCode(t, resp.Body); code != api.CodeValidation {
			t.Fatalf("%s: code %q", c.path, code)
		}
		same := map[string]any{"name": c.path[strings.LastIndex(c.path, "/")+1:]}
		for k, v := range c.body {
			same[k] = v
		}
		wantStatus(t, asUser(b, s, "PUT", c.path, same).StatusCode, http.StatusOK)
		wantStatus(t, asUser(b, s, "PUT", c.path, c.body).StatusCode, http.StatusOK)
	}
	if _, ok := s.settings.Profile("other"); ok {
		t.Fatal("rejected body created a profile")
	}
}

func TestPutRoleWithUnknownModelChangesNothing(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	before := body[[]api.RoleSettings](t, asUser(b, s, "GET", "/api/roles", nil))
	resp := asUser(b, s, "PUT", "/api/roles/developer", api.RoleSettings{Models: []string{"claude/fable", "nope/none"}, Assignment: "random"})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if code := errorCode(t, resp.Body); code != api.CodeValidation {
		t.Fatalf("code %q", code)
	}
	after := body[[]api.RoleSettings](t, asUser(b, s, "GET", "/api/roles", nil))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("roles changed: %+v -> %+v", before, after)
	}
}
