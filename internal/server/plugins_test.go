package server

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

// recordCommand is a command hook that appends its standard input (the
// event JSON) as one line to events.jsonl in the plugin's data folder.
var recordCommand = []string{"sh", "-c", `cat >> "$OMO_PLUGIN_DATA/events.jsonl"; echo >> "$OMO_PLUGIN_DATA/events.jsonl"`}

// recordManifest is the manifest of the test plugin rec: every board event
// is recorded, ticket_created also records the configuration, a ticket
// action answers "hello #<n>", a global action answers "pong" and logs a
// line, a global action "mark" writes the file ran in its data folder.
func recordManifest(name string, events ...string) map[string]any {
	hooks := []any{}
	for _, ev := range events {
		hooks = append(hooks, map[string]any{"event": ev, "command": recordCommand})
	}
	hooks = append(hooks,
		map[string]any{"event": plugins.EventTicketCreated, "command": []string{"sh", "-c", `printf '%s\n' "$OMO_PLUGIN_CONFIG" >> "$OMO_PLUGIN_DATA/config.txt"`}},
		map[string]any{"event": "manual", "name": "hello", "description": "Say hello", "scope": "ticket", "lua": "hello.lua"},
		map[string]any{"event": "manual", "name": "ping", "description": "Ping", "scope": "global", "lua": "ping.lua"},
		map[string]any{"event": "manual", "name": "mark", "description": "Mark", "scope": "global", "command": []string{"sh", "-c", `echo ran >> "$OMO_PLUGIN_DATA/ran"; printf marked`}},
	)
	return map[string]any{
		"name": name, "version": "1.0.0", "description": "records the board's events",
		"default_config": map[string]any{"greeting": "hello", "level": 1},
		"hooks":          hooks,
	}
}

// pluginRepo makes a local bare repository holding a plugin with the given
// manifest and the Lua files of recordManifest, and returns its folder.
func pluginRepo(t *testing.T, manifest map[string]any) string {
	t.Helper()
	work := t.TempDir()
	bare := filepath.Join(t.TempDir(), "plugin.git")
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"plugin.json": string(raw),
		"hello.lua":   `return "hello #" .. tostring(event.data.ticket.number)` + "\n",
		"ping.lua":    "omo.log(\"pinged\")\nreturn { message = \"pong\" }\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(work, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run(work, "init", "-q", "-b", "main")
	run(work, "add", ".")
	run(work, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "plugin")
	run(work, "clone", "-q", "--bare", work, bare)
	return bare
}

// allBoardEvents are the plugin events the board produces.
var allBoardEvents = []string{
	plugins.EventTicketCreated, plugins.EventTicketMoved, plugins.EventCommentWritten, plugins.EventQuestionAsked,
	plugins.EventNeedsAttention, plugins.EventAgentStarted, plugins.EventAgentExited,
}

// installRecorder installs the test plugin rec through the route and
// returns its info.
func installRecorder(t *testing.T, s *Server, b *browser) api.PluginInfo {
	t.Helper()
	repo := pluginRepo(t, recordManifest("rec", allBoardEvents...))
	resp := asUser(b, s, "POST", "/api/plugins", api.PluginSettings{Name: "rec", Source: repo})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("install: status %d: %s", resp.StatusCode, respText(t, resp))
	}
	return body[api.PluginInfo](t, resp)
}

// respText reads an answer's body.
func respText(t *testing.T, resp *http.Response) string {
	t.Helper()
	text, err := readAll(resp)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// dataFile is a file of plugin name's data folder.
func dataFile(s *Server, name, file string) string {
	return filepath.Join(s.opts.Layout.Plugins, ".data", name, file)
}

// readLines reads the non-empty lines of path ("" lines for a missing file).
func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// waitLines waits until path holds at least n lines and returns them.
func waitLines(t *testing.T, path string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		lines := readLines(t, path)
		if len(lines) >= n {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s has %d lines, want %d: %q", path, len(lines), n, lines)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// recordedEvents decodes the lines of events.jsonl.
func recordedEvents(t *testing.T, lines []string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("recorded line %q: %v", l, err)
		}
		out = append(out, ev)
	}
	return out
}

func eventNames(evs []map[string]any) []string {
	var out []string
	for _, ev := range evs {
		out = append(out, ev["event"].(string))
	}
	return out
}

func TestPluginInstallRoute(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	start := lastEventID(t, s)

	resp := asUser(b, s, "GET", "/api/plugins", nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"plugins", "settings", "errors"} {
		if string(raw[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, raw[key])
		}
	}
	if _, ok := raw["bundled"]; ok || len(raw) != 3 {
		t.Errorf("keys of the answer: %v", slices.Sorted(maps.Keys(raw)))
	}

	info := installRecorder(t, s, b)
	if info.Name != "rec" || info.State != plugins.StateReady || info.Version != "1.0.0" || len(info.Commit) != 40 {
		t.Fatalf("installed %+v", info)
	}
	set, ok := s.settings.Plugin("rec")
	if !ok || !set.Enabled || set.Config["greeting"] != "hello" {
		t.Fatalf("settings %+v, want enabled with the default configuration", set)
	}
	if _, err := os.Stat(filepath.Join(s.opts.Layout.Plugins, "rec", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	// A second install of the same name is refused; nothing changes.
	resp = asUser(b, s, "POST", "/api/plugins", api.PluginSettings{Name: "rec", Source: pluginRepo(t, recordManifest("rec"))})
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if again, _ := s.settings.Plugin("rec"); again.Revision != set.Revision {
		t.Fatal("a refused install changed the settings")
	}

	// plugin.changed carries a PluginInfo and nothing else.
	evs := settingsEvents(t, s, api.KindPluginChanged, start)
	if len(evs) == 0 {
		t.Fatal("no plugin.changed event")
	}
	for _, raw := range evs {
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		var got api.PluginInfo
		if err := dec.Decode(&got); err != nil {
			t.Fatalf("plugin.changed %s: %v", raw, err)
		}
	}

	resp = asUser(b, s, "GET", "/api/plugins", nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	list := body[api.PluginsResponse](t, resp)
	if len(list.Plugins) != 1 || list.Plugins[0].Commit != info.Commit || len(list.Settings) != 1 {
		t.Fatalf("list %+v", list)
	}
}

func TestPluginInstallFailureChangesNothing(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	old := recordManifest("bad")
	old["hooks"] = []any{map[string]any{"event": "prompt_render", "command": recordCommand}}
	for _, c := range []struct {
		name  string
		p     api.PluginSettings
		field string
	}{
		{"transport", api.PluginSettings{Name: "bad", Source: "ext::sh -c touch% /tmp/x"}, "source"},
		{"option ref", api.PluginSettings{Name: "bad", Source: pluginRepo(t, recordManifest("bad")), Ref: "-x"}, "ref"},
		{"escaping subpath", api.PluginSettings{Name: "bad", Source: pluginRepo(t, recordManifest("bad")), Subpath: "../x"}, "subpath"},
		{"bad name", api.PluginSettings{Name: ".hidden", Source: pluginRepo(t, recordManifest("bad"))}, "name"},
		{"old event", api.PluginSettings{Name: "bad", Source: pluginRepo(t, old)}, ""},
		{"credentials", api.PluginSettings{Name: "bad", Source: "https://user:hunter2secret@127.0.0.1:1/x.git"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp := asUser(b, s, "POST", "/api/plugins", c.p)
			text := respText(t, resp)
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, want 422: %s", resp.StatusCode, text)
			}
			if strings.Contains(text, "hunter2secret") {
				t.Fatalf("the answer holds the password: %s", text)
			}
			var env struct {
				Error struct {
					Details struct {
						Field string `json:"field"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(text), &env); err != nil {
				t.Fatal(err)
			}
			if env.Error.Details.Field != c.field {
				t.Fatalf("field %q, want %q: %s", env.Error.Details.Field, c.field, text)
			}
			if _, ok := s.settings.Plugin(c.p.Name); ok {
				t.Fatal("a failed install wrote settings")
			}
			if _, err := os.Lstat(filepath.Join(s.opts.Layout.Plugins, c.p.Name)); err == nil {
				t.Fatal("a failed install left a tree")
			}
			if n := len(s.plugins.m.Infos()); n != 0 {
				t.Fatalf("the runtime has %d plugins", n)
			}
		})
	}
}

func TestPluginSettingsReload(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	m := newTicketMaker(t, s)
	events := dataFile(s, "rec", "events.jsonl")
	configs := dataFile(s, "rec", "config.txt")

	m.create()
	waitLines(t, events, 1)
	if got := waitLines(t, configs, 1); !strings.Contains(got[0], `"greeting":"hello"`) {
		t.Fatalf("config %q", got[0])
	}

	put := func(change func(*api.PluginSettings)) {
		t.Helper()
		cur, _ := s.settings.Plugin("rec")
		next := cur
		next.Config = maps.Clone(cur.Config)
		change(&next)
		h := bearer(s)
		h["If-Match"] = `"` + cur.Revision + `"`
		resp := b.do("PUT", "/api/plugins/rec", next, h)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT: %d %s", resp.StatusCode, respText(t, resp))
		}
	}
	// Disabled: hooks stop.
	put(func(p *api.PluginSettings) { p.Enabled = false })
	if info := s.pluginInfo("rec"); info.State != plugins.StateDisabled {
		t.Fatalf("state %s", info.State)
	}
	m.create()
	time.Sleep(300 * time.Millisecond)
	if n := len(readLines(t, events)); n != 1 {
		t.Fatalf("a disabled plugin got events: %d lines", n)
	}
	// Enabled with another configuration: the next run sees it.
	put(func(p *api.PluginSettings) { p.Enabled = true; p.Config["greeting"] = "moin" })
	m.create()
	if got := waitLines(t, configs, 2); !strings.Contains(got[1], `"greeting":"moin"`) {
		t.Fatalf("config %q", got[1])
	}

	// A stale revision is refused.
	h := bearer(s)
	h["If-Match"] = `"0123456789abcdef"`
	cur, _ := s.settings.Plugin("rec")
	resp := b.do("PUT", "/api/plugins/rec", cur, h)
	wantStatus(t, resp.StatusCode, http.StatusConflict)

	// A hand edit of the settings file reaches the runtime through the
	// settings feed, without any fetch.
	cached := filepath.Join(s.opts.Layout.Plugins, ".cache")
	before, _ := os.ReadDir(cached)
	path := filepath.Join(s.opts.Layout.Root, "plugins", "rec.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "enabled: true", "enabled: false", 1)
	if edited == string(raw) {
		t.Fatalf("no enabled line in %s", raw)
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for s.pluginInfo("rec").State != plugins.StateDisabled {
		if time.Now().After(deadline) {
			t.Fatalf("a hand edit did not reach the runtime: %+v", s.pluginInfo("rec"))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if after, _ := os.ReadDir(cached); len(after) != len(before) {
		t.Fatal("a hand edit fetched something")
	}
}

func TestPluginNotInstalledThenUpdate(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	repo := pluginRepo(t, recordManifest("hand", plugins.EventTicketCreated))
	// A settings file written by hand: nothing is fetched by itself.
	if err := s.settings.PutPluginIf(api.NoRevision, api.PluginSettings{Name: "hand", Source: repo, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())
	deadline := time.Now().Add(10 * time.Second)
	for s.pluginInfo("hand").State != plugins.StateNotInstalled {
		if time.Now().After(deadline) {
			t.Fatalf("state %+v", s.pluginInfo("hand"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Lstat(filepath.Join(s.opts.Layout.Plugins, "hand")); err == nil {
		t.Fatal("a plugin was installed without the user asking")
	}
	resp := asUser(b, s, "POST", "/api/plugins/hand/update", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: %d %s", resp.StatusCode, respText(t, resp))
	}
	info := body[api.PluginInfo](t, resp)
	if info.State != plugins.StateReady || len(info.Commit) != 40 {
		t.Fatalf("info %+v", info)
	}
	// The manifest's defaults are added to the configuration.
	if p, _ := s.settings.Plugin("hand"); p.Config["greeting"] != "hello" {
		t.Fatalf("config %+v", p.Config)
	}
	resp = asUser(b, s, "POST", "/api/plugins/nope/update", map[string]any{})
	wantStatus(t, resp.StatusCode, http.StatusNotFound)
}

func TestPluginOperationsOneAtATime(t *testing.T) {
	s := newTestServer(t)
	release, err := s.claimPlugin("rec")
	if err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, s)
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/plugins/rec/update"},
		{"DELETE", "/api/plugins/rec"},
	} {
		resp := asUser(b, s, c.method, c.path, map[string]any{})
		if c.method == "DELETE" {
			resp = asUser(b, s, c.method, c.path, nil)
		}
		wantStatus(t, resp.StatusCode, http.StatusConflict)
	}
	resp := asUser(b, s, "POST", "/api/plugins", api.PluginSettings{Name: "rec", Source: "/nowhere/rec.git"})
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	release()
}

func TestPluginActionRoute(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	n := newTicketMaker(t, s).create()

	resp := asUser(b, s, "POST", "/api/plugins/rec/actions/hello", api.PluginRun{Ticket: n})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := body[api.PluginRunResult](t, resp); got.Message != "hello #"+strconv.Itoa(n) {
		t.Fatalf("answer %q", got.Message)
	}
	resp = asUser(b, s, "POST", "/api/plugins/rec/actions/hello", api.PluginRun{})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if !strings.Contains(respText(t, resp), `"field":"ticket"`) {
		t.Fatal("a ticket action without ticket does not name the field ticket")
	}
	resp = asUser(b, s, "POST", "/api/plugins/rec/actions/ping", api.PluginRun{})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := body[api.PluginRunResult](t, resp); got.Message != "pong" {
		t.Fatalf("answer %q", got.Message)
	}
	for _, c := range []struct {
		path string
		run  api.PluginRun
		want int
	}{
		{"/api/plugins/rec/actions/nope", api.PluginRun{}, http.StatusNotFound},
		{"/api/plugins/nope/actions/ping", api.PluginRun{}, http.StatusNotFound},
		{"/api/plugins/rec/actions/hello", api.PluginRun{Ticket: 9999}, http.StatusNotFound},
		{"/api/plugins/rec/actions/hello", api.PluginRun{Ticket: -1}, http.StatusUnprocessableEntity},
	} {
		resp := asUser(b, s, "POST", c.path, c.run)
		if resp.StatusCode != c.want {
			t.Errorf("%s %+v: %d, want %d", c.path, c.run, resp.StatusCode, c.want)
		}
	}
}

func TestPluginLogRoute(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	resp := asUser(b, s, "POST", "/api/plugins/rec/actions/ping", api.PluginRun{})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	resp = asUser(b, s, "GET", "/api/plugins/rec/log", nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	lines := body[[]api.PluginLogLine](t, resp)
	if !slices.ContainsFunc(lines, func(l api.PluginLogLine) bool { return strings.Contains(l.Message, "pinged") }) {
		t.Fatalf("log %+v", lines)
	}
	resp = asUser(b, s, "GET", "/api/plugins/nope/log", nil)
	wantStatus(t, resp.StatusCode, http.StatusNotFound)
}

func TestPluginDeleteRoute(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	m := newTicketMaker(t, s)
	m.create()
	events := dataFile(s, "rec", "events.jsonl")
	waitLines(t, events, 1)

	// A stale revision is refused and removes nothing.
	h := bearer(s)
	h["If-Match"] = `"0123456789abcdef"`
	resp := b.do("DELETE", "/api/plugins/rec", nil, h)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if _, err := os.Stat(events); err != nil {
		t.Fatal("a refused removal deleted data")
	}

	resp = asUser(b, s, "DELETE", "/api/plugins/rec", nil)
	wantStatus(t, resp.StatusCode, http.StatusNoContent)
	if _, ok := s.settings.Plugin("rec"); ok {
		t.Fatal("settings kept")
	}
	for _, p := range []string{filepath.Join(s.opts.Layout.Plugins, "rec"), filepath.Dir(events), filepath.Join(s.opts.Layout.Root, "plugins", "rec.yaml")} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s kept", p)
		}
	}
	if lines, err := db.PluginLogTail(s.db, "rec", 10); err != nil || len(lines) != 0 {
		t.Fatalf("log kept: %v %v", lines, err)
	}
	if n := len(s.plugins.m.Infos()); n != 0 {
		t.Fatalf("the runtime still has %d plugins", n)
	}
	m.create()
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(events); err == nil {
		t.Fatal("a removed plugin got an event")
	}
	resp = asUser(b, s, "DELETE", "/api/plugins/rec", nil)
	wantStatus(t, resp.StatusCode, http.StatusNotFound)
}

// TestPluginRoutesRefuseAgents walks every plugin row of the route table
// with an agent session of every role, with bodies that would install,
// change, update, remove and run something: every request is refused with
// 403, and nothing is installed, loaded, run or written.
func TestPluginRoutesRefuseAgents(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	n := newTicketMaker(t, s).create()
	events := dataFile(s, "rec", "events.jsonl")
	waitLines(t, events, 1)

	sessions := map[api.Role]string{}
	for _, role := range agentRoles {
		sessions[role] = liveSession(t, s, role).ID
	}
	s.waitSettingsEvents(context.Background())
	time.Sleep(300 * time.Millisecond) // the sessions' own events reach the plugin first
	repo := pluginRepo(t, recordManifest("evil", allBoardEvents...))
	cur, _ := s.settings.Plugin("rec")
	changed := cur
	changed.Enabled = false
	changed.Source = repo
	bodies := map[string]any{
		"POST /api/plugins":                         api.PluginSettings{Name: "evil", Source: repo},
		"PUT /api/plugins/{name}":                   changed,
		"POST /api/plugins/{name}/update":           map[string]any{},
		"POST /api/plugins/{name}/actions/{action}": api.PluginRun{Ticket: n},
	}
	actions := []string{"mark", "hello", "ping"}

	before := snapshotPluginFiles(t, s)
	infos := s.plugins.m.Infos()
	lines := len(readLines(t, events))
	start := lastEventID(t, s)
	rows := 0
	for _, row := range s.routeTable {
		if family(row.pattern) != "plugins" {
			continue
		}
		rows++
		if row.access != userOnly {
			t.Errorf("%s is not user only", row.pattern)
		}
		for _, role := range agentRoles {
			paths := []string{}
			method, path, _ := strings.Cut(row.pattern, " ")
			path = strings.ReplaceAll(path, "{name}", "rec")
			if strings.Contains(path, "{action}") {
				for _, a := range actions {
					paths = append(paths, strings.ReplaceAll(path, "{action}", a))
				}
			} else {
				paths = append(paths, path)
			}
			for _, p := range paths {
				h := map[string]string{}
				if method == "PUT" || method == "DELETE" {
					h["If-Match"] = `"` + cur.Revision + `"`
				}
				rec := socketDoHeaders(t, s, method, p, bodies[row.pattern], sessions[role], h)
				if rec.Code != http.StatusForbidden || errorCode(t, rec.Body) != api.CodeForbidden {
					t.Errorf("%s %s as %s: %d, want 403", method, p, role, rec.Code)
				}
			}
		}
	}
	if rows != 7 {
		t.Fatalf("%d plugin rows in the route table, want 7", rows)
	}
	time.Sleep(300 * time.Millisecond)
	if after := snapshotPluginFiles(t, s); !maps.Equal(before, after) {
		t.Fatalf("agents changed plugin files:\nbefore %v\nafter  %v", slices.Sorted(maps.Keys(before)), slices.Sorted(maps.Keys(after)))
	}
	if got := s.plugins.m.Infos(); !equalJSON(t, got, infos) {
		t.Fatalf("the runtime changed:\nbefore %+v\nafter  %+v", infos, got)
	}
	if n := len(readLines(t, events)); n != lines {
		t.Fatalf("the plugin got %d events from refused requests", n-lines)
	}
	if _, err := os.Stat(dataFile(s, "rec", "ran")); err == nil {
		t.Fatal("an agent ran an action")
	}
	for _, kind := range []string{api.KindPluginChanged, api.KindSettingsChanged} {
		wantEvents(t, s, kind, start, 0)
	}
}

// socketDoHeaders is socketDo with extra request headers.
func socketDoHeaders(t *testing.T, s *Server, method, path string, body any, session string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(t, method, "http://omo"+path, body)
	if session != "" {
		req.Header.Set(SessionHeader, session)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	return rec
}

// snapshotPluginFiles reads every file below the plugin folder (settings,
// trees, cache, data) by path.
func snapshotPluginFiles(t *testing.T, s *Server) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := s.opts.Layout.Plugins
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(x) == string(y)
}

// TestPluginActionFromAgentRefused pins the manual action in particular:
// an agent session cannot run one, also not with a ticket of its own.
func TestPluginActionFromAgentRefused(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	sess := liveSession(t, s, api.RoleDeveloper)
	rec := socketDo(t, s, "POST", "/api/plugins/rec/actions/mark", api.PluginRun{Ticket: *sess.Ticket}, sess.ID)
	wantStatus(t, rec.Code, http.StatusForbidden)
	// The user on the socket (a request without a session, as a plugin's
	// own program sends it) may.
	rec = socketDo(t, s, "POST", "/api/plugins/rec/actions/mark", api.PluginRun{}, "")
	wantStatus(t, rec.Code, http.StatusOK)
	if got := waitLines(t, dataFile(s, "rec", "ran"), 1); len(got) != 1 {
		t.Fatalf("ran %q", got)
	}
}

// TestPluginReinstallUndoneWhenSettingsFail: a PUT that installs a new
// source and then cannot write the settings (here: the file was edited by
// hand during the install) leaves the earlier tree in place, so tree and
// settings still fit.
func TestPluginReinstallUndoneWhenSettingsFail(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	before, err := pluginmanager.Installed(s.plugins.o, "rec")
	if err != nil {
		t.Fatal(err)
	}
	other := pluginRepo(t, recordManifest("rec", plugins.EventTicketMoved))

	afterPluginInstall = func(name string) {
		cur, _ := s.settings.Plugin(name)
		cur.Config = maps.Clone(cur.Config)
		cur.Config["greeting"] = "edited by hand"
		if err := s.settings.PutPluginIf(cur.Revision, cur); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterPluginInstall = nil })
	cur, _ := s.settings.Plugin("rec")
	next := cur
	next.Source = other
	h := bearer(s)
	h["If-Match"] = `"` + cur.Revision + `"`
	resp := b.do("PUT", "/api/plugins/rec", next, h)
	wantStatus(t, resp.StatusCode, http.StatusConflict)

	after, err := pluginmanager.Installed(s.plugins.o, "rec")
	if err != nil {
		t.Fatal(err)
	}
	if after.Source != before.Source || after.Commit != before.Commit {
		t.Fatalf("installed %+v after the failed PUT, want the earlier %+v", after, before)
	}
	if got, _ := s.settings.Plugin("rec"); got.Source != before.Source || got.Config["greeting"] != "edited by hand" {
		t.Fatalf("settings %+v", got)
	}
	if left, _ := filepath.Glob(filepath.Join(s.opts.Layout.Plugins, ".old-rec-*")); len(left) != 0 {
		t.Fatalf("left over: %v", left)
	}
}
