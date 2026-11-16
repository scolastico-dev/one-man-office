package server

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// settingsSnapshot reads every settings file of s: settings.yaml and the
// files of the profiles, roles, prompts and projects folders.
func settingsSnapshot(t *testing.T, s *Server) map[string]string {
	t.Helper()
	root := s.opts.Layout.Root
	out := map[string]string{}
	read := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = string(data)
	}
	read(filepath.Join(root, "settings.yaml"))
	for _, dir := range []string{"profiles", "roles", "prompts", "projects"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				read(filepath.Join(root, dir, e.Name()))
			}
		}
	}
	return out
}

// accessFixture prepares a server on which every settings write of the table
// would change something when it ran: a profile to delete, a customized
// prompt to reset, a project without tickets to remove, a missing project.
type accessFixture struct {
	s *Server
	// paths maps a route pattern to the concrete path a request uses.
	paths map[string]string
	// bodies maps a route pattern to a body that the handler would accept.
	bodies map[string]any
}

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	s := newTestServer(t)
	keep := api.Profile{Name: "keep", CLI: "custom", Command: "keep", Models: map[string]api.ProfileModel{"m": {}}}
	if err := s.settings.PutProfile(keep); err != nil {
		t.Fatal(err)
	}
	// A role lists the profile: deleting it with detach would change the
	// role as well.
	if err := s.settings.PutRole(api.RoleSettings{Name: "ceo", Models: []string{"keep/m"}, Assignment: "failover"}); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.PutPrompt("pm", "customized pm"); err != nil {
		t.Fatal(err)
	}
	free, err := s.settings.AddProject(newRepo(t), "free")
	if err != nil {
		t.Fatal(err)
	}
	goneDir := newRepo(t)
	if _, err := s.settings.AddProject(goneDir, "gone"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(goneDir); err != nil {
		t.Fatal(err)
	}
	g := api.General(s.settings.General())
	g.MaxRunningAgents = 9
	s.waitSettingsEvents(context.Background())
	return &accessFixture{
		s: s,
		paths: map[string]string{
			"PATCH /api/projects/{id}":    "/api/projects/" + free.ID,
			"DELETE /api/projects/{id}":   "/api/projects/" + free.ID,
			"DELETE /api/projects":        "/api/projects?missing=true",
			"PUT /api/profiles/{name}":    "/api/profiles/evil",
			"DELETE /api/profiles/{name}": "/api/profiles/keep?detach=true",
			"PUT /api/roles/{role}":       "/api/roles/pm",
			"GET /api/prompts/{name}":     "/api/prompts/pm",
			"PUT /api/prompts/{name}":     "/api/prompts/developer",
			"DELETE /api/prompts/{name}":  "/api/prompts/pm",
		},
		bodies: map[string]any{
			"POST /api/projects":       api.CreateProject{Path: newRepo(t), Name: "evil"},
			"PATCH /api/projects/{id}": map[string]any{"enabled": false},
			"POST /api/projects/batch": api.ProjectBatch{IDs: []string{free.ID}, Action: api.ProjectBatchDisable},
			"PUT /api/settings":        g,
			"PATCH /api/settings":      map[string]any{"max_running_agents": 9},
			"PUT /api/profiles/{name}": api.Profile{CLI: "custom", Command: "evil", Models: map[string]api.ProfileModel{"m": {}}},
			"PUT /api/roles/{role}":    api.RoleSettings{Models: []string{"keep/m"}, Assignment: "random"},
			"PUT /api/prompts/{name}":  api.PromptUpdate{Text: "evil"},
		},
	}
}

var wildcard = regexp.MustCompile(`\{[^}]+\}`)

// request returns the method, path and body the fixture uses for a row.
func (f *accessFixture) request(row apiRoute) (method, path string, body any) {
	method, path, _ = strings.Cut(row.pattern, " ")
	if p, ok := f.paths[row.pattern]; ok {
		path = p
	} else {
		path = wildcard.ReplaceAllString(path, "x")
	}
	body, ok := f.bodies[row.pattern]
	if !ok && method != http.MethodGet {
		body = map[string]any{}
	}
	return method, path, body
}

// family is the first path segment below /api of a pattern.
func family(pattern string) string {
	_, path, _ := strings.Cut(pattern, " ")
	rest := strings.TrimPrefix(path, "/api/")
	name, _, _ := strings.Cut(rest, "/")
	return name
}

var agentRoles = []api.Role{api.RoleDeveloper, api.RoleReviewer, api.RolePM, api.RoleCEO}

// TestSettingsRouteTableAccess walks every row of the route table, so a row
// added later is covered too: an agent session of every role is refused with
// 403 on the socket unless the row is agent-readable, and nothing is written;
// the user is never refused, on either listener.
func TestSettingsRouteTableAccess(t *testing.T) {
	f := newAccessFixture(t)
	s := f.s
	if len(s.routeTable) < 21 {
		t.Fatalf("the route table has %d rows; the settings and discovery routes are missing", len(s.routeTable))
	}
	sessions := map[api.Role]string{}
	// rolePaths gives the image rows a path inside each role's read and
	// write scope: an image on the session's own ticket (the CEO uses the
	// developer's), uploaded by the user.
	rolePaths := map[api.Role]map[string]string{}
	var anyTicket int
	for _, role := range agentRoles {
		sess := liveSession(t, s, role)
		sessions[role] = sess.ID
		n := anyTicket
		if sess.Ticket != nil {
			n = *sess.Ticket
			anyTicket = n
		}
		rec := socketUpload(t, s, n, "x.png", testPNG(t), "")
		wantStatus(t, rec.Code, http.StatusCreated)
		id := decodeBody[api.Image](t, rec.Body).ID
		ticket := "/api/tickets/" + strconv.Itoa(n) + "/images"
		rolePaths[role] = map[string]string{
			"POST /api/tickets/{n}/images": ticket,
			"GET /api/tickets/{n}/images":  ticket,
			"GET /api/images/{id}":         "/api/images/" + id,
			"GET /api/images/{id}/content": "/api/images/" + id + "/content",
			"DELETE /api/images/{id}":      "/api/images/" + id,
		}
	}
	s.waitSettingsEvents(context.Background())
	before := settingsSnapshot(t, s)
	start := lastEventID(t, s)
	scansBefore := s.scan.started()

	families := map[string][]apiRoute{}
	for _, row := range s.routeTable {
		families[family(row.pattern)] = append(families[family(row.pattern)], row)
	}
	for _, fam := range slices.Sorted(maps.Keys(families)) {
		for _, role := range agentRoles {
			t.Run(fam+"/"+string(role), func(t *testing.T) {
				for _, row := range families[fam] {
					method, path, body := f.request(row)
					if p, ok := rolePaths[role][row.pattern]; ok {
						path = p
					}
					var rec *httptest.ResponseRecorder
					if row.access == agentWrite {
						// The only agent-writable row is the image upload,
						// whose body is the image.
						n, _ := strconv.Atoi(strings.Split(path, "/")[3])
						rec = socketUpload(t, s, n, "x.png", testPNG(t), sessions[role])
					} else {
						rec = socketDo(t, s, method, path, body, sessions[role])
					}
					switch row.access {
					case agentRead:
						if rec.Code != http.StatusOK {
							t.Errorf("%s as %s: status %d, want 200: %s", row.pattern, role, rec.Code, rec.Body)
						}
					case agentWrite:
						if rec.Code != http.StatusCreated {
							t.Errorf("%s as %s: status %d, want 201: %s", row.pattern, role, rec.Code, rec.Body)
						}
					default:
						if rec.Code != http.StatusForbidden || errorCode(t, rec.Body) != api.CodeForbidden {
							t.Errorf("%s as %s: status %d, want 403", row.pattern, role, rec.Code)
						}
					}
				}
			})
		}
	}
	s.waitSettingsEvents(context.Background())
	if after := settingsSnapshot(t, s); !maps.Equal(before, after) {
		t.Fatalf("agents changed settings files:\nbefore %v\nafter  %v", before, after)
	}
	for _, kind := range []string{api.KindSettingsChanged, api.KindProjectChanged, api.KindProjectRemoved} {
		wantEvents(t, s, kind, start, 0)
	}
	if s.scan.started() != scansBefore {
		t.Fatal("an agent started a scan")
	}

	// The user is never refused: through the browser listener and on the
	// socket without a session header. GET rows first, so the writes do not
	// change what they read.
	rows := slices.Clone(s.routeTable)
	slices.SortStableFunc(rows, func(a, b apiRoute) int {
		ag, bg := strings.HasPrefix(a.pattern, "GET "), strings.HasPrefix(b.pattern, "GET ")
		switch {
		case ag && !bg:
			return -1
		case bg && !ag:
			return 1
		}
		return 0
	})
	b := newBrowser(t, s)
	for _, row := range rows {
		method, path, body := f.request(row)
		if resp := asUser(b, s, method, path, body); resp.StatusCode == http.StatusForbidden {
			t.Errorf("%s through the browser: 403 for the user", row.pattern)
		}
		if method == http.MethodGet {
			if rec := socketDo(t, s, method, path, body, ""); rec.Code == http.StatusForbidden {
				t.Errorf("%s on the socket: 403 for the user", row.pattern)
			}
		}
	}
}

// TestSettingsRoutesInTable pins the access rule of every settings route.
func TestSettingsRoutesInTable(t *testing.T) {
	s := newTestServer(t)
	got := map[string]routeAccess{}
	for _, row := range s.routeTable {
		got[row.pattern] = row.access
	}
	want := map[string]routeAccess{
		"GET /api/projects":           agentRead,
		"POST /api/projects":          userOnly,
		"PATCH /api/projects/{id}":    userOnly,
		"DELETE /api/projects/{id}":   userOnly,
		"DELETE /api/projects":        userOnly,
		"POST /api/projects/batch":    userOnly,
		"GET /api/settings":           userOnly,
		"PUT /api/settings":           userOnly,
		"PATCH /api/settings":         userOnly,
		"GET /api/profiles":           userOnly,
		"PUT /api/profiles/{name}":    userOnly,
		"DELETE /api/profiles/{name}": userOnly,
		"GET /api/models":             agentRead,
		"GET /api/roles":              userOnly,
		"PUT /api/roles/{role}":       userOnly,
		"GET /api/prompts":            userOnly,
		"GET /api/prompts/{name}":     userOnly,
		"PUT /api/prompts/{name}":     userOnly,
		"DELETE /api/prompts/{name}":  userOnly,
		"GET /api/projects/scan":      userOnly,
		"POST /api/projects/rescan":   userOnly,
		"GET /api/folders":            userOnly,
	}
	for pattern, access := range want {
		if got[pattern] != access {
			t.Errorf("%s: %s, want %s", pattern, got[pattern], access)
		}
	}
	// Of the settings only these two may ever be read by agents; the other
	// agent-readable rows are the images, which follow the board's read
	// scope, and the only row agents may write is the image upload, which
	// follows the board's rule for comments (TestImageRoutesInTable).
	agentRows := map[string]routeAccess{
		"GET /api/projects": agentRead, "GET /api/models": agentRead,
		"GET /api/tickets/{n}/images": agentRead, "GET /api/images/{id}": agentRead, "GET /api/images/{id}/content": agentRead,
		"POST /api/tickets/{n}/images": agentWrite,
	}
	for pattern, access := range got {
		if access != userOnly && agentRows[pattern] != access {
			t.Errorf("%s is %s", pattern, access)
		}
	}
}

// TestNoSettingsRouteOutsideTable sends every method to paths below the
// guarded prefixes that no row of the table serves: each is answered 404 or
// 405 by the mux, never by a handler.
func TestNoSettingsRouteOutsideTable(t *testing.T) {
	s := newTestServer(t)
	rows := map[string]bool{}
	paths := map[string]bool{}
	for _, row := range s.routeTable {
		rows[row.pattern] = true
		_, path, _ := strings.Cut(row.pattern, " ")
		paths[wildcard.ReplaceAllString(path, "x")] = true
	}
	for _, prefix := range guardedPrefixes {
		for _, p := range []string{prefix, prefix + "/", prefix + "/x", prefix + "/x/y", prefix + "/x/y/z"} {
			paths[p] = true
		}
	}
	checked := 0
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		if !guarded(path) {
			continue
		}
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			req := httptest.NewRequest(method, "http://omo"+path, nil)
			_, pattern := s.mux.Handler(req)
			if rows[pattern] {
				continue
			}
			if pattern != "" && pattern != "/api/" && pattern != "/api" {
				t.Errorf("%s %s is served by %q, which is not in the route table", method, path, pattern)
				continue
			}
			rec := socketDo(t, s, method, path, map[string]any{}, "")
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d, want 404 or 405", method, path, rec.Code)
			}
			checked++
		}
	}
	if checked < 50 {
		t.Fatalf("only %d requests checked", checked)
	}
}

// TestAgentReadsNoCodeExecutionSettings checks what the two agent-readable
// settings routes answer: the project list and the selectable models carry
// no command, argument, environment value or account folder, and no field
// beyond the ones listed here (a field added later fails this test until it
// is judged).
func TestAgentReadsNoCodeExecutionSettings(t *testing.T) {
	s := newTestServer(t)
	f := false
	canaries := []string{"cmd-canary", "arg-canary", "env-canary", "acct-canary", "model-arg-canary", "model-env-canary", "usage-canary"}
	prof := api.Profile{
		Name: "claude", CLI: "custom", Command: "cmd-canary", Args: []string{"arg-canary"},
		Env: map[string]string{"PLAIN": "env-canary"}, AccountDir: "/acct-canary", UsageScript: "usage-canary",
		Models: map[string]api.ProfileModel{
			"open":   {Args: []string{"model-arg-canary"}, Env: map[string]string{"M": "model-env-canary"}, Description: "fast"},
			"hidden": {Selectable: &f},
		},
	}
	if err := s.settings.PutUsageScriptIf("", "usage-canary", "function usage() return {} end\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.PutProfile(prof); err != nil {
		t.Fatal(err)
	}
	g := s.settings.General()
	g.AgentEnv = map[string]string{"GLOBAL": "env-canary"}
	if err := s.settings.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	for _, role := range agentRoles {
		sess := liveSession(t, s, role)
		for _, c := range []struct {
			path string
			keys []string
		}{
			{"/api/models", []string{"id", "profile", "name", "cli", "description", "selectable"}},
			{"/api/projects", []string{"id", "name", "path", "enabled", "missing", "root", "merge", "effective_merge", "story_merge", "current_branch", "default_branch"}},
		} {
			rec := socketDo(t, s, "GET", c.path, nil, sess.ID)
			wantStatus(t, rec.Code, http.StatusOK)
			raw := rec.Body.String()
			for _, canary := range canaries {
				if strings.Contains(raw, canary) {
					t.Errorf("%s as %s holds %q: %s", c.path, role, canary, raw)
				}
			}
			var items []map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &items); err != nil {
				t.Fatal(err)
			}
			if len(items) == 0 {
				t.Fatalf("%s as %s: empty list", c.path, role)
			}
			for _, item := range items {
				for k := range item {
					if !slices.Contains(c.keys, k) {
						t.Errorf("%s as %s answers the field %q", c.path, role, k)
					}
				}
			}
			if c.path == "/api/models" && (len(items) != 1 || string(items[0]["id"]) != `"claude/open"`) {
				t.Errorf("agent models %s", raw)
			}
		}
	}
}

// TestForeignOriginRejectedOnSettingsWrites: a page of another origin cannot
// change settings with any write method, and the body limit and the JSON
// content type apply to the new routes.
func TestForeignOriginRejectedOnSettingsWrites(t *testing.T) {
	s := newTestServer(t)
	if err := s.settings.PutPrompt("pm", "mine"); err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, s)
	before := settingsSnapshot(t, s)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/api/settings", map[string]any{"max_running_agents": 9}},
		{"PUT", "/api/settings", s.settings.General()},
		{"PUT", "/api/profiles/evil", api.Profile{CLI: "custom", Command: "evil", Models: map[string]api.ProfileModel{"m": {}}}},
		{"PUT", "/api/prompts/pm", api.PromptUpdate{Text: "evil"}},
		{"DELETE", "/api/prompts/pm", nil},
		{"PUT", "/api/roles/pm", api.RoleSettings{Models: []string{}, Assignment: "random"}},
	} {
		hdr := bearer(s)
		hdr["Origin"] = "http://evil.com"
		resp := b.do(c.method, c.path, c.body, hdr)
		wantStatus(t, resp.StatusCode, http.StatusForbidden)
		if code := errorCode(t, resp.Body); code != api.CodeForbidden {
			t.Fatalf("%s %s: code %q", c.method, c.path, code)
		}
	}
	// A body over the limit and a body that is not JSON.
	hdr := bearer(s)
	big := map[string]any{"agent_env": map[string]string{"BIG": strings.Repeat("x", maxBody)}}
	resp := b.do("PATCH", "/api/settings", big, hdr)
	wantStatus(t, resp.StatusCode, http.StatusRequestEntityTooLarge)
	hdr["Content-Type"] = "text/plain"
	resp = b.do("PATCH", "/api/settings", map[string]any{"max_running_agents": 9}, hdr)
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	s.waitSettingsEvents(context.Background())
	if after := settingsSnapshot(t, s); !maps.Equal(before, after) {
		t.Fatal("a refused request changed settings files")
	}
	if s.settings.General().MaxRunningAgents == 9 {
		t.Fatal("max_running_agents changed")
	}
	if _, ok := s.settings.Profile("evil"); ok {
		t.Fatal("profile created")
	}
}
