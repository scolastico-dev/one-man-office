package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/branchname"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/settings"
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
	// The rescan route needs its empty JSON object.
	wantStatus(t, asUser(b, s, "POST", "/api/projects/rescan", nil).StatusCode, http.StatusBadRequest)
	wantStatus(t, asUser(b, s, "POST", "/api/projects/rescan", map[string]any{}).StatusCode, http.StatusOK)
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

// nextSettingsEvent is the stream's next event other than usage.changed,
// which every change of the profiles may bring along.
func nextSettingsEvent(st *stream) api.Event {
	st.t.Helper()
	for {
		if ev := st.nextEvent(); ev.Kind != api.KindUsageChanged {
			return ev
		}
	}
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
	ev := nextSettingsEvent(st)
	var d api.SettingsChangedData
	json.Unmarshal(ev.Data, &d)
	if ev.Kind != api.KindSettingsChanged || d.Section != "profiles" || d.Name != "hand" || ev.Actor.Role != api.RoleSystem {
		t.Fatalf("event %+v", ev)
	}
	// Through the API: exactly one event, after the hand edit's.
	resp := asUser(b, s, "PUT", "/api/profiles/viaapi", api.Profile{CLI: "custom", Command: "y", Models: map[string]api.ProfileModel{"m": {}}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	ev = nextSettingsEvent(st)
	json.Unmarshal(ev.Data, &d)
	if d.Name != "viaapi" {
		t.Fatalf("event %+v", ev)
	}
	// A broken hand edit goes through the stream too.
	if err := os.WriteFile(prof, []byte("cli: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.settings.Reload()
	if ev = nextSettingsEvent(st); ev.Kind != api.KindSettingsInvalid {
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
	p := api.Profile{CLI: "claude", Command: "claude", Models: map[string]api.ProfileModel{"fable": {Description: "d"}}}
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

// A profile that roles use: the refusal names the roles for the web, and
// ?detach=true takes its models out of them and deletes it, still guarded by
// If-Match and still refused while open tickets use one of its models.
func TestDeleteProfileDetach(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/gemini", testProfile("default", "pro")).StatusCode, http.StatusOK)
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/claude", testProfile("opus")).StatusCode, http.StatusOK)
	for role, models := range map[string][]string{"ceo": {"gemini/pro"}, "pm": {"claude/opus", "gemini/default"}, "reviewer": {"claude/opus"}} {
		wantStatus(t, asUser(b, s, "PUT", "/api/roles/"+role, api.RoleSettings{Models: models, Assignment: "failover"}).StatusCode, http.StatusOK)
	}
	resp := asUser(b, s, "DELETE", "/api/profiles/gemini", nil)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	refused := body[struct {
		Error struct {
			Code    string           `json:"code"`
			Details api.ProfileInUse `json:"details"`
		} `json:"error"`
	}](t, resp)
	if refused.Error.Code != api.CodeInUse || !slices.Equal(refused.Error.Details.Roles, []string{"ceo", "pm"}) {
		t.Fatalf("refusal %+v", refused)
	}
	wantStatus(t, asUser(b, s, "DELETE", "/api/profiles/gemini?detach=maybe", nil).StatusCode, http.StatusUnprocessableEntity)
	other := `"` + s.settings.Revision(settings.SectionProfiles, "claude") + `"`
	resp = withIfMatch(b, s, "DELETE", "/api/profiles/gemini?detach=true", nil, other)
	if resp.StatusCode != http.StatusConflict || errorCode(t, resp.Body) != api.CodeStaleSettings {
		t.Fatalf("stale revision: %d", resp.StatusCode)
	}

	// An open ticket of one of its models refuses the delete, detach or not.
	p := addProject(t, s)
	resp = asUser(b, s, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{p}, Model: "gemini/default"})
	wantStatus(t, resp.StatusCode, http.StatusCreated)
	n := body[api.Ticket](t, resp).Number
	resp = asUser(b, s, "DELETE", "/api/profiles/gemini?detach=true", nil)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if code := errorCode(t, resp.Body); code != api.CodeInUse {
		t.Fatalf("code %q", code)
	}
	if ceo, _ := s.settings.Role("ceo"); !slices.Equal(ceo.Models, []string{"gemini/pro"}) {
		t.Fatalf("a refused delete detached the roles: %+v", ceo)
	}
	wantStatus(t, asUser(b, s, "PATCH", fmt.Sprintf("/api/tickets/%d", n), map[string]any{"model": nil}).StatusCode, http.StatusOK)

	rev := s.settings.Revision(settings.SectionProfiles, "gemini")
	resp = withIfMatch(b, s, "DELETE", "/api/profiles/gemini?detach=true", nil, `"`+rev+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got := body[api.ProfileDeleted](t, resp)
	want := []api.DetachedRole{{Role: "ceo", Models: []string{"gemini/pro"}}, {Role: "pm", Models: []string{"gemini/default"}}}
	if !reflect.DeepEqual(got.Detached, want) {
		t.Fatalf("detached %+v", got.Detached)
	}
	roles := body[[]api.RoleSettings](t, asUser(b, s, "GET", "/api/roles", nil))
	for _, r := range roles {
		wantModels := map[string][]string{"ceo": {}, "pm": {"claude/opus"}, "developer": {}, "reviewer": {"claude/opus"}}[r.Name]
		if !slices.Equal(r.Models, wantModels) {
			t.Fatalf("role %s: %v, want %v", r.Name, r.Models, wantModels)
		}
	}
	wantStatus(t, asUser(b, s, "DELETE", "/api/profiles/gemini?detach=true", nil).StatusCode, http.StatusNotFound)
	// Without roles to detach, the answer lists none.
	resp = asUser(b, s, "PUT", "/api/profiles/spare", testProfile("m"))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	resp = asUser(b, s, "DELETE", "/api/profiles/spare?detach=true", nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := body[api.ProfileDeleted](t, resp); got.Detached == nil || len(got.Detached) != 0 {
		t.Fatalf("detached %#v", got.Detached)
	}
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

// errorEnvelope decodes an error answer with its details.
type errorEnvelope struct {
	Error struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details api.FieldDetail `json:"details"`
	} `json:"error"`
}

// withIfMatch sends a request with an If-Match header.
func withIfMatch(b *browser, s *Server, method, path string, body any, ifMatch string) *http.Response {
	hdr := bearer(s)
	hdr["If-Match"] = ifMatch
	return b.do(method, path, body, hdr)
}

// rawDo sends body as it is, as the user through the browser listener.
func rawDo(t *testing.T, b *browser, s *Server, method, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, b.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = hostOmoLocalhost + ":" + b.port
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := b.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// readFile returns the content of a settings file below the home.
func readFile(t *testing.T, s *Server, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.opts.Layout.Root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func testProfile(models ...string) api.Profile {
	p := api.Profile{CLI: "custom", Command: "agent", Models: map[string]api.ProfileModel{}}
	for _, m := range models {
		p.Models[m] = api.ProfileModel{}
	}
	return p
}

func TestRevisionsInAnswers(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	set := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil))
	if set.Revision == "" || set.Revision != s.settings.Revision(settings.SectionGeneral, "") {
		t.Fatalf("settings revision %q, store %q", set.Revision, s.settings.Revision(settings.SectionGeneral, ""))
	}
	p := body[api.Profile](t, asUser(b, s, "PUT", "/api/profiles/one", testProfile("m")))
	if p.Revision == "" || p.Revision == api.NoRevision || p.Revision != s.settings.Revision(settings.SectionProfiles, "one") {
		t.Fatalf("profile revision %q", p.Revision)
	}
	list := body[api.ProfilesResponse](t, asUser(b, s, "GET", "/api/profiles", nil))
	if len(list.Profiles) != 1 || list.Profiles[0].Revision != p.Revision {
		t.Fatalf("profiles %+v", list)
	}
	roles := body[[]api.RoleSettings](t, asUser(b, s, "GET", "/api/roles", nil))
	for _, r := range roles {
		if r.Revision == "" || r.Revision != s.settings.Revision(settings.SectionRoles, r.Name) {
			t.Fatalf("role %+v", r)
		}
	}
	role := body[api.RoleSettings](t, asUser(b, s, "PUT", "/api/roles/pm", api.RoleSettings{Models: []string{"one/m"}, Assignment: "random"}))
	if role.Revision == "" || role.Revision == api.NoRevision || role.Revision != s.settings.Revision(settings.SectionRoles, "pm") {
		t.Fatalf("role %+v", role)
	}

	infos := body[[]api.PromptInfo](t, asUser(b, s, "GET", "/api/prompts", nil))
	kinds := map[string]bool{}
	for _, info := range infos {
		if info.Kind != prompts.Kind(info.Name) || info.Kind == "" || info.Revision != api.NoRevision || info.Customized {
			t.Fatalf("prompt info %+v", info)
		}
		kinds[info.Kind] = true
	}
	if !kinds[api.PromptKindRole] || !kinds[api.PromptKindInstructions] || !kinds[api.PromptKindMessage] {
		t.Fatalf("kinds %v", kinds)
	}
	pr := body[api.Prompt](t, asUser(b, s, "PUT", "/api/prompts/developer-instructions", api.PromptUpdate{Text: "Be brief."}))
	if pr.Kind != api.PromptKindInstructions || pr.Revision == api.NoRevision || pr.Revision != s.settings.Revision(settings.SectionPrompts, "developer-instructions") || !pr.Customized {
		t.Fatalf("prompt %+v", pr)
	}
	pr = body[api.Prompt](t, asUser(b, s, "GET", "/api/prompts/pm", nil))
	if pr.Kind != api.PromptKindRole || pr.Default == "" || pr.Default != s.opts.PromptDefaults["pm"] || pr.Revision != api.NoRevision {
		t.Fatalf("prompt %+v", pr)
	}
}

func TestIfMatchStale(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	first := body[api.Profile](t, asUser(b, s, "PUT", "/api/profiles/one", testProfile("m")))
	next := testProfile("m", "n")
	resp := withIfMatch(b, s, "PUT", "/api/profiles/one", next, `"`+first.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	second := body[api.Profile](t, resp)
	if second.Revision == first.Revision {
		t.Fatal("revision did not change")
	}
	// A write built on the old revision is refused.
	resp = withIfMatch(b, s, "PUT", "/api/profiles/one", testProfile("m"), `"`+first.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	env := body[errorEnvelope](t, resp)
	if env.Error.Code != api.CodeStaleSettings || !strings.Contains(env.Error.Message, "profiles/one.yaml changed since you loaded it") {
		t.Fatalf("refusal %+v", env)
	}
	// A hand edit right before the write, not yet found by polling.
	path := filepath.Join(s.opts.Layout.Root, "profiles", "one.yaml")
	if err := os.WriteFile(path, []byte("cli: custom\ncommand: by-hand\nmodels:\n  m: {}\n  n: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp = withIfMatch(b, s, "PUT", "/api/profiles/one", testProfile("m", "n"), `"`+second.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if code := errorCode(t, resp.Body); code != api.CodeStaleSettings {
		t.Fatalf("code %q", code)
	}
	if got := readFile(t, s, "profiles/one.yaml"); !strings.Contains(got, "by-hand") {
		t.Fatalf("the hand edit was overwritten: %s", got)
	}
	// The other guarded writes refuse a stale revision too.
	stale := `"0123456789abcdef"`
	before := settingsSnapshot(t, s)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/api/settings", s.settings.General()},
		{"PATCH", "/api/settings", map[string]any{"max_running_agents": 9}},
		{"DELETE", "/api/profiles/one", nil},
		{"PUT", "/api/roles/pm", api.RoleSettings{Models: []string{}, Assignment: "random"}},
		{"PUT", "/api/prompts/pm", api.PromptUpdate{Text: "x"}},
		{"DELETE", "/api/prompts/pm", nil},
	} {
		resp := withIfMatch(b, s, c.method, c.path, c.body, stale)
		if resp.StatusCode != http.StatusConflict || errorCode(t, resp.Body) != api.CodeStaleSettings {
			t.Errorf("%s %s with a stale revision: %d", c.method, c.path, resp.StatusCode)
		}
	}
	// Malformed If-Match values are refused before anything is read.
	for _, v := range []string{`W/"` + second.Revision + `"`, second.Revision, `"a", "b"`, `*`, `""`, `"ABC"`, `"x y"`} {
		resp := withIfMatch(b, s, "PATCH", "/api/settings", map[string]any{"max_running_agents": 9}, v)
		if resp.StatusCode != http.StatusBadRequest || errorCode(t, resp.Body) != api.CodeBadRequest {
			t.Errorf("If-Match %s: %d", v, resp.StatusCode)
		}
	}
	// Two If-Match headers.
	req := newRequest(t, "PATCH", b.srv.URL+"/api/settings", map[string]any{"max_running_agents": 9})
	req.Host = hostOmoLocalhost + ":" + b.port
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Add("If-Match", `"0"`)
	req.Header.Add("If-Match", `"0"`)
	two, err := b.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Body.Close()
	wantStatus(t, two.StatusCode, http.StatusBadRequest)
	s.waitSettingsEvents(context.Background())
	if after := settingsSnapshot(t, s); !maps.Equal(before, after) {
		t.Fatal("a refused write changed a file")
	}
	// The current revision passes.
	rev := s.settings.Revision(settings.SectionGeneral, "")
	resp = withIfMatch(b, s, "PATCH", "/api/settings", map[string]any{"max_running_agents": 9}, `"`+rev+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := body[api.SettingsResponse](t, resp); got.Settings.MaxRunningAgents != 9 || got.Revision == rev {
		t.Fatalf("answer %+v", got)
	}
}

func TestIfMatchCreateOnly(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	resp := withIfMatch(b, s, "PUT", "/api/profiles/fresh", testProfile("m"), `"0"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	resp = withIfMatch(b, s, "PUT", "/api/profiles/fresh", testProfile("m", "n"), `"0"`)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if code := errorCode(t, resp.Body); code != api.CodeStaleSettings {
		t.Fatalf("code %q", code)
	}
	if p, _ := s.settings.Profile("fresh"); len(p.Models) != 1 {
		t.Fatalf("create-only overwrote the profile: %+v", p)
	}
}

func TestPatchSettingsMergesObjects(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	before := s.settings.General()
	resp := asUser(b, s, "PATCH", "/api/settings", map[string]any{"health": map[string]any{"silent_after": "20m"}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got := body[api.SettingsResponse](t, resp).Settings
	want := api.General(before)
	want.Health.SilentAfter = api.Duration(20 * time.Minute)
	if !reflect.DeepEqual(api.General(s.settings.General()), want) || !reflect.DeepEqual(got, want) {
		t.Fatalf("after patch %+v, want %+v", s.settings.General(), want)
	}
	if before.Health.QuestionUnansweredAfter == 0 {
		t.Fatal("the default has no question_unanswered_after to keep")
	}
	// Two keys at once, one nested object partly.
	resp = asUser(b, s, "PATCH", "/api/settings", map[string]any{"max_running_agents": 2, "ceo": map[string]any{"may_approve_stories": true}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	g := s.settings.General()
	if g.MaxRunningAgents != 2 || !g.CEO.MayApproveStories || g.CEO.Enabled != before.CEO.Enabled || g.Health.SilentAfter != api.Duration(20*time.Minute) {
		t.Fatalf("after second patch %+v", g)
	}
}

func TestPatchSettingsReplacesLists(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	a, c := t.TempDir(), t.TempDir()
	g := s.settings.General()
	g.WorkspaceRoots = []api.WorkspaceRoot{{Path: a, MaxDepth: 2, Ignore: []string{"x"}}, {Path: t.TempDir(), MaxDepth: 3}}
	g.AgentEnv = map[string]string{"A": "1", "B": "2"}
	if err := s.settings.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	resp := asUser(b, s, "PATCH", "/api/settings", map[string]any{"workspace_roots": []map[string]any{{"path": c}}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	roots := s.settings.General().WorkspaceRoots
	if len(roots) != 1 || roots[0].Path != c || roots[0].MaxDepth != 4 || len(roots[0].Ignore) != 0 {
		t.Fatalf("roots %+v", roots)
	}
	resp = asUser(b, s, "PATCH", "/api/settings", map[string]any{"agent_env": map[string]string{"C": "3"}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if env := s.settings.General().AgentEnv; !maps.Equal(env, map[string]string{"C": "3"}) {
		t.Fatalf("agent_env %v", env)
	}
	// An empty list and an empty map clear them.
	resp = asUser(b, s, "PATCH", "/api/settings", map[string]any{"workspace_roots": []any{}, "agent_env": map[string]string{}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if g := s.settings.General(); len(g.WorkspaceRoots) != 0 || len(g.AgentEnv) != 0 {
		t.Fatalf("after clearing %+v", g)
	}
}

func TestPatchSettingsRefusesUnknownNestedKeys(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	before := settingsSnapshot(t, s)
	for _, c := range []struct {
		body  string
		field string
	}{
		{`{"ceo":{"enabld":false}}`, "ceo"},
		{`{"max_running_agents":7,"ceo":{"enabld":false}}`, "ceo"},
		{`{"max_runing_agents":2}`, "max_runing_agents"},
		{`{"review_defaults":{"story":"human","tsk":"human"}}`, "review_defaults"},
		{`{"workspace_roots":[{"path":"/tmp","max_depth":2,"bogus":1}]}`, "workspace_roots"},
		{`{"health":{"silent_after":"20m","x":{"y":1}}}`, "health"},
	} {
		resp := rawDo(t, b, s, "PATCH", "/api/settings", c.body)
		wantStatus(t, resp.StatusCode, http.StatusBadRequest)
		env := body[errorEnvelope](t, resp)
		if env.Error.Code != api.CodeBadRequest || env.Error.Details.Field != c.field {
			t.Errorf("%s: %+v", c.body, env)
		}
	}
	// Valid keys with an invalid value are refused all together.
	resp := asUser(b, s, "PATCH", "/api/settings", map[string]any{"max_running_agents": 7, "health": map[string]any{"silent_after": "1s"}})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	s.waitSettingsEvents(context.Background())
	if after := settingsSnapshot(t, s); !maps.Equal(before, after) || s.settings.General().MaxRunningAgents == 7 {
		t.Fatal("a refused patch wrote something")
	}
}

func TestPatchSettingsRefusesNull(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	before := settingsSnapshot(t, s)
	for _, c := range []struct {
		body  string
		field string
	}{
		{`{"health":null}`, "health"},
		{`{"health":{"silent_after":null}}`, "health.silent_after"},
		{`{"agent_env":{"A":null}}`, "agent_env.A"},
		{`{"workspace_roots":[{"path":"/tmp","ignore":[null]}]}`, "workspace_roots.0.ignore.0"},
		{`{"max_running_agents":null}`, "max_running_agents"},
		{`null`, ""},
		{`{}`, ""},
		{`[]`, ""},
		{`{"max_running_agents":2} {}`, ""},
	} {
		resp := rawDo(t, b, s, "PATCH", "/api/settings", c.body)
		wantStatus(t, resp.StatusCode, http.StatusBadRequest)
		if env := body[errorEnvelope](t, resp); env.Error.Details.Field != c.field {
			t.Errorf("%s: %+v", c.body, env)
		}
	}
	s.waitSettingsEvents(context.Background())
	if after := settingsSnapshot(t, s); !maps.Equal(before, after) {
		t.Fatal("a refused patch wrote something")
	}
}

func TestValidationErrorsCarryField(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	for _, c := range []struct {
		method, path string
		body         any
		status       int
		code, field  string
	}{
		{"PATCH", "/api/settings", map[string]any{"max_running_agents": 0}, 422, api.CodeInvalidSettings, "max_running_agents"},
		{"PATCH", "/api/settings", map[string]any{"health": map[string]any{"silent_after": "1s"}}, 422, api.CodeInvalidSettings, "health.silent_after"},
		{"PATCH", "/api/settings", map[string]any{"workspace_roots": []map[string]any{{"path": "relative"}}}, 422, api.CodeInvalidSettings, "workspace_roots.0.path"},
		{"PATCH", "/api/settings", map[string]any{"max_running_agents": "two"}, 400, api.CodeBadRequest, "max_running_agents"},
		{"PATCH", "/api/settings", map[string]any{"health": map[string]any{"silent_after": "soon"}}, 400, api.CodeBadRequest, "health.silent_after"},
		{"PUT", "/api/profiles/one", api.Profile{CLI: "custom", Command: "x"}, 422, api.CodeValidation, ""},
		{"PUT", "/api/profiles/one", map[string]any{"cli": "custom", "command": "x", "models": map[string]any{"m": map[string]any{"env": map[string]any{"A": 1}}}}, 400, api.CodeBadRequest, "models.m.env.A"},
	} {
		resp := asUser(b, s, c.method, c.path, c.body)
		env := body[errorEnvelope](t, resp)
		if resp.StatusCode != c.status || env.Error.Code != c.code || (c.field != "" && env.Error.Details.Field != c.field) {
			t.Errorf("%s %s %v: %d %+v", c.method, c.path, c.body, resp.StatusCode, env)
		}
	}
}

// TestBadTypedValueNamesFullKey: a value that cannot be decoded into its
// type, at any depth and inside list and map entries, is refused naming the
// full dotted key, never quoting the value.
func TestBadTypedValueNamesFullKey(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	const v = "soon-7c41e"
	for _, c := range []struct {
		method, path, body, field string
	}{
		{"PATCH", "/api/settings", `{"stop":{"handoff_timeout":"` + v + `"}}`, "stop.handoff_timeout"},
		{"PATCH", "/api/settings", `{"stop":{"handoff_timeout":5}}`, "stop.handoff_timeout"},
		{"PATCH", "/api/settings", `{"max_running_agents":2,"health":{"question_unanswered_after":"` + v + `"}}`, "health.question_unanswered_after"},
		{"PATCH", "/api/settings", `{"rescan_interval":"` + v + `"}`, "rescan_interval"},
		{"PATCH", "/api/settings", `{"workspace_roots":[{"path":"/a"},{"path":"/b","max_depth":"` + v + `"}]}`, "workspace_roots.1.max_depth"},
		{"PATCH", "/api/settings", `{"workspace_roots":[{"path":"/a","max_depth":1.5}]}`, "workspace_roots.0.max_depth"},
		{"PATCH", "/api/settings", `{"workspace_roots":[{"path":"/a","ignore":[1]}]}`, "workspace_roots.0.ignore.0"},
		{"PATCH", "/api/settings", `{"ceo":{"enabled":"` + v + `"}}`, "ceo.enabled"},
		{"PATCH", "/api/settings", `{"agent_env":{"A":true}}`, "agent_env.A"},
		{"PUT", "/api/settings", `{"stop":{"handoff_timeout":"` + v + `"}}`, "stop.handoff_timeout"},
		{"PUT", "/api/profiles/one", `{"cli":"custom","command":"x","prompt_delivery":{"delay":"` + v + `"},"models":{"m":{}}}`, "prompt_delivery.delay"},
		{"PUT", "/api/profiles/one", `{"cli":"custom","command":"x","prompt_delivery":{"inject":"` + v + `"},"models":{"m":{}}}`, "prompt_delivery.inject"},
		{"PUT", "/api/profiles/one", `{"cli":"custom","command":"x","models":{"m":{"selectable":"` + v + `"}}}`, "models.m.selectable"},
		{"PUT", "/api/profiles/one", `{"cli":"custom","command":"x","models":{"m":{"usage_windows":[5]}}}`, "models.m.usage_windows.0"},
	} {
		resp := rawDo(t, b, s, c.method, c.path, c.body)
		raw := readBody(t, resp)
		var env errorEnvelope
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest || env.Error.Code != api.CodeBadRequest || env.Error.Details.Field != c.field {
			t.Errorf("%s %s %s: %d %s, want field %s", c.method, c.path, c.body, resp.StatusCode, raw, c.field)
		}
		if strings.Contains(raw, v) {
			t.Errorf("%s: the refusal quotes the value: %s", c.body, raw)
		}
	}
}

func TestSettingsChangedSource(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	start := lastEventID(t, s)
	wantStatus(t, asUser(b, s, "PATCH", "/api/settings", map[string]any{"max_running_agents": 3}).StatusCode, http.StatusOK)
	ev := wantEvents(t, s, api.KindSettingsChanged, start, 1)
	var d api.SettingsChangedData
	if err := json.Unmarshal(ev[0], &d); err != nil || d.Source != api.SourceApp || d.Section != api.SectionGeneral {
		t.Fatalf("event %s (%v)", ev[0], err)
	}
	if err := os.WriteFile(filepath.Join(s.opts.Layout.Root, "settings.yaml"), []byte("max_running_agents: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.settings.Reload()
	ev = wantEvents(t, s, api.KindSettingsChanged, start, 2)
	if err := json.Unmarshal(ev[1], &d); err != nil || d.Source != api.SourceDisk {
		t.Fatalf("event %s (%v)", ev[1], err)
	}
	// A hand edit found by the re-read before a write comes first, from disk;
	// the write's own change follows, from the app.
	if err := os.WriteFile(filepath.Join(s.opts.Layout.Root, "settings.yaml"), []byte("max_running_agents: 6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, asUser(b, s, "PATCH", "/api/settings", map[string]any{"ceo": map[string]any{"may_approve_stories": true}}).StatusCode, http.StatusOK)
	ev = wantEvents(t, s, api.KindSettingsChanged, start, 4)
	var disk, app api.SettingsChangedData
	json.Unmarshal(ev[2], &disk)
	json.Unmarshal(ev[3], &app)
	if disk.Source != api.SourceDisk || app.Source != api.SourceApp || s.settings.General().MaxRunningAgents != 6 {
		t.Fatalf("events %s %s", ev[2], ev[3])
	}
}

// setDone moves a ticket to Done, cancelled.
func setDone(t *testing.T, s *Server, n int) {
	t.Helper()
	if _, err := s.board.Cancel(context.Background(), board.User, int64(n), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveProjectInUse(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	m := newTicketMaker(t, s)
	first := m.create()
	second := m.create()
	resp := asUser(b, s, "DELETE", "/api/projects/"+m.project, nil)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	env := body[errorEnvelope](t, resp)
	want := fmt.Sprintf("Open tickets use this project: #%d, #%d.", first, second)
	if env.Error.Code != api.CodeInUse || env.Error.Message != want {
		t.Fatalf("refusal %+v, want %q", env, want)
	}
	if _, ok := s.settings.Project(m.project); !ok {
		t.Fatal("project removed")
	}
	// A missing project with open tickets is kept by the bulk removal too.
	p, _ := s.settings.Project(m.project)
	if err := os.RemoveAll(p.Path); err != nil {
		t.Fatal(err)
	}
	resp = asUser(b, s, "DELETE", "/api/projects?missing=true", nil)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if code := errorCode(t, resp.Body); code != api.CodeInUse {
		t.Fatalf("code %q", code)
	}
	if _, ok := s.settings.Project(m.project); !ok {
		t.Fatal("project removed")
	}
	// Done tickets do not hold a project.
	setDone(t, s, first)
	setDone(t, s, second)
	wantStatus(t, asUser(b, s, "DELETE", "/api/projects/"+m.project, nil).StatusCode, http.StatusNoContent)
}

func TestTicketListIsCut(t *testing.T) {
	var nums []int64
	for i := range 13 {
		nums = append(nums, int64(i+1))
	}
	if got, want := ticketList(nums), "#1, #2, #3, #4, #5, #6, #7, #8, #9, #10 and 3 more"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := ticketList(nums[:1]); got != "#1" {
		t.Fatalf("got %q", got)
	}
}

func TestDropModelInUse(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/claude", testProfile("a", "b")).StatusCode, http.StatusOK)
	m := newTicketMaker(t, s)
	tk, err := s.board.Create(context.Background(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{m.project}, Model: "claude/b"})
	if err != nil {
		t.Fatal(err)
	}
	resp := asUser(b, s, "PUT", "/api/profiles/claude", testProfile("a"))
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	env := body[errorEnvelope](t, resp)
	want := fmt.Sprintf("Open tickets use the model claude/b: #%d.", tk.Number)
	if env.Error.Code != api.CodeInUse || env.Error.Message != want {
		t.Fatalf("refusal %+v, want %q", env, want)
	}
	resp = asUser(b, s, "DELETE", "/api/profiles/claude", nil)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if code := errorCode(t, resp.Body); code != api.CodeInUse {
		t.Fatalf("code %q", code)
	}
	if p, _ := s.settings.Profile("claude"); len(p.Models) != 2 {
		t.Fatalf("profile changed: %+v", p)
	}
	// Dropping the unused model is fine.
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/claude", testProfile("b")).StatusCode, http.StatusOK)
	// A Done ticket holds no model.
	setDone(t, s, tk.Number)
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/claude", testProfile("c")).StatusCode, http.StatusOK)
	wantStatus(t, asUser(b, s, "DELETE", "/api/profiles/claude", nil).StatusCode, http.StatusNoContent)
}

func TestHomeInSettings(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	for _, resp := range []*http.Response{
		asUser(b, s, "GET", "/api/settings", nil),
		asUser(b, s, "PATCH", "/api/settings", map[string]any{"max_running_agents": 3}),
		asUser(b, s, "PUT", "/api/settings", s.settings.General()),
	} {
		wantStatus(t, resp.StatusCode, http.StatusOK)
		if got := body[api.SettingsResponse](t, resp); got.Home != s.opts.Layout.Root || got.Home == "" {
			t.Fatalf("home %q, want %q", got.Home, s.opts.Layout.Root)
		}
	}
}

func TestPatchStoryTaskReviewDefaults(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	resp := asUser(b, s, "PATCH", "/api/settings", map[string]any{"review_defaults": map[string]any{"story_task": "maybe"}})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if env := body[errorEnvelope](t, resp); env.Error.Details.Field != "review_defaults.story_task" {
		t.Fatalf("error %+v", env)
	}
	resp = asUser(b, s, "PATCH", "/api/settings", map[string]any{"when_approved_defaults": map[string]any{"story_task": "export"}})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if env := body[errorEnvelope](t, resp); env.Error.Details.Field != "when_approved_defaults.story_task" {
		t.Fatalf("error %+v", env)
	}
	resp = asUser(b, s, "PATCH", "/api/settings", map[string]any{"review_defaults": map[string]any{"story_task": "human"}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	s.waitSettingsEvents(context.Background())
	g := s.settings.General()
	if g.ReviewDefaults.StoryTask != api.ReviewHuman || g.ReviewDefaults.Task != settings.Defaults().ReviewDefaults.Task {
		t.Fatalf("review defaults %+v", g.ReviewDefaults)
	}
	if got := (boardEnv{st: s.settings}).Policy().ReviewStoryTask; got != api.ReviewHuman {
		t.Fatalf("board policy story task review %q", got)
	}
}

func TestPatchOpenTicketsAsPage(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	if got := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil)).Settings; got.UI.OpenTicketsAsPage {
		t.Fatal("ui.open_tickets_as_page is on in a fresh home")
	}
	wantStatus(t, asUser(b, s, "PATCH", "/api/settings", map[string]any{"ui": map[string]any{"open_tickets_as_page": true}}).StatusCode, http.StatusOK)
	if !s.settings.General().UI.OpenTicketsAsPage {
		t.Fatal("the patch did not switch it on")
	}
	if !strings.Contains(readFile(t, s, "settings.yaml"), "open_tickets_as_page: true") {
		t.Fatal("settings.yaml does not hold the key")
	}
	// A text is no boolean; the setting stays as it was.
	resp := asUser(b, s, "PATCH", "/api/settings", map[string]any{"ui": map[string]any{"open_tickets_as_page": "yes"}})
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	if !s.settings.General().UI.OpenTicketsAsPage {
		t.Fatal("a refused patch changed the setting")
	}
}

func TestPatchBranchTemplate(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	if got := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil)).Settings; got.Branches.Template != "omo/{slug}" {
		t.Fatalf("template %q in a fresh home", got.Branches.Template)
	}
	wantStatus(t, asUser(b, s, "PATCH", "/api/settings", map[string]any{"branches": map[string]any{"template": "{user}/{slug}"}}).StatusCode, http.StatusOK)
	if s.settings.General().Branches.Template != "{user}/{slug}" || !strings.Contains(readFile(t, s, "settings.yaml"), "template: '{user}/{slug}'") {
		t.Fatalf("the patch did not set it:\n%s", readFile(t, s, "settings.yaml"))
	}
	// A refusal names the field and says why, as the page shows it.
	res := asUser(b, s, "PATCH", "/api/settings", map[string]any{"branches": map[string]any{"template": "main"}})
	wantStatus(t, res.StatusCode, http.StatusUnprocessableEntity)
	e := body[errorEnvelope](t, res).Error
	if e.Details.Field != "branches.template" || !strings.Contains(e.Message, "must contain {slug} or {number}") {
		t.Fatalf("refusal %+v", e)
	}
	res = asUser(b, s, "PATCH", "/api/settings", map[string]any{"branches": map[string]any{"template": "omo/{nope}"}})
	wantStatus(t, res.StatusCode, http.StatusUnprocessableEntity)
	if e := body[errorEnvelope](t, res).Error; !strings.Contains(e.Message, "unknown placeholder {nope}") {
		t.Fatalf("refusal %+v", e)
	}
	if s.settings.General().Branches.Template != "{user}/{slug}" {
		t.Fatal("a refused patch changed the setting")
	}
}

func TestBranchPreview(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	got := body[api.BranchPreview](t, asUser(b, s, "POST", "/api/settings/branch-preview", map[string]any{"template": "omo/{number}-{slug}"}))
	if got.Branch != "omo/42-fix-the-login" || got.Error != "" {
		t.Fatalf("preview %+v", got)
	}
	host, user := branchname.Machine()
	got = body[api.BranchPreview](t, asUser(b, s, "POST", "/api/settings/branch-preview", map[string]any{"template": "{user}/{host}/{type}/{slug}"}))
	if want, err := branchname.Expand("{user}/{host}/{type}/{slug}", branchname.Vars{Number: 42, Title: "Fix the login", Type: "task", Host: host, User: user}); err != nil || got.Branch != want {
		t.Fatalf("preview %+v, want %q", got, want)
	}
	// The message of a template that cannot be used is the one a save gets.
	got = body[api.BranchPreview](t, asUser(b, s, "POST", "/api/settings/branch-preview", map[string]any{"template": "main"}))
	res := asUser(b, s, "PATCH", "/api/settings", map[string]any{"branches": map[string]any{"template": "main"}})
	if e := body[errorEnvelope](t, res).Error; got.Branch != "" || got.Error == "" || !strings.Contains(e.Message, got.Error) {
		t.Fatalf("preview %+v, save refused with %q", got, e.Message)
	}
	wantStatus(t, asUser(b, s, "POST", "/api/settings/branch-preview", map[string]any{"text": "x"}).StatusCode, http.StatusBadRequest)
}
