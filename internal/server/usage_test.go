package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/usage"
)

// Usage scripts never run for real in this package's tests: they would
// read the developer's credential files and call the real usage endpoints.
// A test that wants a run sets its own function with useRun.
func init() {
	newUsageRun = func() usage.RunFunc { return refusedUsageRun }
}

func refusedUsageRun(ctx context.Context, j usage.Job) (usage.Result, error) {
	return usage.Result{}, errors.New("usage scripts do not run in tests")
}

// useRun makes s run usage scripts with f, for the tracker and the test
// route.
func useRun(s *Server, f usage.RunFunc) { s.usage.setRun(f) }

// fakeRun answers usage runs per script name and counts them. With block
// set, a run waits until block is closed or its context ends.
type fakeRun struct {
	mu      sync.Mutex
	runs    int
	windows map[string][]usage.Window
	errs    map[string]string
	block   chan struct{}
	started chan struct{}
}

func newFakeRun() *fakeRun {
	return &fakeRun{windows: map[string][]usage.Window{}, errs: map[string]string{}, started: make(chan struct{}, 16)}
}

func (f *fakeRun) set(script string, ws ...usage.Window) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windows[script] = ws
}

func (f *fakeRun) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

func (f *fakeRun) run(ctx context.Context, j usage.Job) (usage.Result, error) {
	f.mu.Lock()
	f.runs++
	block := f.block
	ws := slices.Clone(f.windows[j.Script])
	msg := f.errs[j.Script]
	f.mu.Unlock()
	select {
	case f.started <- struct{}{}:
	default:
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return usage.Result{}, errors.New("run canceled")
		}
	}
	if msg != "" {
		return usage.Result{Log: []string{"tried"}}, errors.New(msg)
	}
	return usage.Result{Windows: ws, Log: []string{"ran " + j.Script}}, nil
}

const emptyScript = "function usage(account) return {} end\n"

// meter writes the usage script (unless it has a default) and a profile
// that uses it with the given models, each counting every window.
func meter(t *testing.T, s *Server, profile, script string, models ...string) {
	t.Helper()
	if _, ok := s.settings.UsageDefault(script); !ok {
		if _, _, exists := s.settings.UsageScript(script); !exists {
			if err := s.settings.PutUsageScriptIf("", script, emptyScript); err != nil {
				t.Fatal(err)
			}
		}
	}
	p := settings.Profile{Name: profile, CLI: api.CLICustom, Command: profile, UsageScript: script, Models: map[string]settings.Model{}}
	for _, m := range models {
		p.Models[m] = settings.Model{}
	}
	if err := s.settings.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())
}

func pct(v float64) *float64 { return &v }

func modelState(st api.UsageState, id string) (api.ModelUsage, bool) {
	for _, m := range st.Models {
		if m.Model == id {
			return m, true
		}
	}
	return api.ModelUsage{}, false
}

func withHeader(s *Server, k, v string) map[string]string {
	h := bearer(s)
	h[k] = v
	return h
}

func TestUsageStateRoute(t *testing.T) {
	s := newTestServer(t)
	f := newFakeRun()
	f.set("fake", usage.Window{Name: "a", UsedPercent: 10}, usage.Window{Name: "b", UsedPercent: 70})
	f.errs["broken"] = "line 3: no login"
	useRun(s, f.run)
	meter(t, s, "fake", "fake", "a", "b")
	meter(t, s, "other", "broken", "x")
	if err := s.settings.PutProfile(settings.Profile{Name: "plain", CLI: api.CLICustom, Command: "plain", Models: map[string]settings.Model{"m": {}}}); err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())
	b := newBrowser(t, s)
	wantStatus(t, asUser(b, s, "POST", "/api/usage/refresh", map[string]any{}).StatusCode, http.StatusOK)

	resp := asUser(b, s, "GET", "/api/usage", nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	st := body[api.UsageState](t, resp)
	if len(st.Readings) != 2 {
		t.Fatalf("readings %+v", st.Readings)
	}
	for _, r := range st.Readings {
		switch r.Script {
		case "fake":
			if len(r.Windows) != 2 || r.Error != "" || r.FetchedAt == nil || !slices.Equal(r.Profiles, []string{"fake"}) {
				t.Errorf("fake reading %+v", r)
			}
		case "broken":
			if r.Error != "line 3: no login" || len(r.Windows) != 0 {
				t.Errorf("broken reading %+v", r)
			}
		default:
			t.Errorf("reading of %q", r.Script)
		}
	}
	for id, want := range map[string]string{
		"fake/a": api.UsageOK, "fake/b": api.UsageOK, "other/x": api.UsageUnknown, "plain/m": api.UsageUnmetered,
	} {
		m, ok := modelState(st, id)
		if !ok || m.State != want {
			t.Errorf("%s: %+v, want state %s", id, m, want)
		}
	}
	if m, _ := modelState(st, "fake/b"); m.UsedPercent == nil || *m.UsedPercent != 70 || m.Window != "b" {
		t.Errorf("fake/b %+v", m)
	}
}

func TestUsageRefreshRoute(t *testing.T) {
	s := newTestServer(t)
	f := newFakeRun()
	f.set("fake", usage.Window{Name: "a", UsedPercent: 10})
	useRun(s, f.run)
	meter(t, s, "fake", "fake", "a")
	b := newBrowser(t, s)
	before := f.count()
	resp := asUser(b, s, "POST", "/api/usage/refresh", map[string]any{})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	st := body[api.UsageState](t, resp)
	if f.count() <= before {
		t.Fatal("refresh ran no script")
	}
	if len(st.Readings) != 1 || st.Readings[0].Running || len(st.Readings[0].Windows) != 1 {
		t.Fatalf("answer %+v", st.Readings)
	}
	// The new reading is in the answer.
	f.set("fake", usage.Window{Name: "a", UsedPercent: 55})
	st = body[api.UsageState](t, asUser(b, s, "POST", "/api/usage/refresh", map[string]any{}))
	if m, _ := modelState(st, "fake/a"); m.UsedPercent == nil || *m.UsedPercent != 55 {
		t.Fatalf("after refresh %+v", m)
	}
	// The body is the empty object.
	resp = b.do("POST", "/api/usage/refresh", nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
}

func usageEvents(t *testing.T, s *Server, after int64) []api.UsageState {
	t.Helper()
	evs, err := db.EventsSince(s.db, after, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	var out []api.UsageState
	for _, e := range evs {
		if e.Kind != api.KindUsageChanged {
			continue
		}
		var st api.UsageState
		if err := json.Unmarshal([]byte(e.Detail), &st); err != nil {
			t.Fatal(err)
		}
		out = append(out, st)
	}
	return out
}

func TestUsageChangedEvent(t *testing.T) {
	s := newTestServer(t)
	f := newFakeRun()
	f.set("fake", usage.Window{Name: "a", UsedPercent: 42})
	useRun(s, f.run)
	start := lastEventID(t, s)
	meter(t, s, "fake", "fake", "a")
	s.usage.tracker.Refresh(context.Background())
	waitFor(t, "a usage.changed event with the reading", func() bool {
		evs := usageEvents(t, s, start)
		if len(evs) == 0 {
			return false
		}
		last := evs[len(evs)-1]
		m, ok := modelState(last, "fake/a")
		return ok && m.UsedPercent != nil && *m.UsedPercent == 42 && !last.Readings[0].Running
	})
	evs, err := db.EventsSince(s.db, start, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind != api.KindUsageChanged {
			continue
		}
		// Exactly the wire type: no field beyond UsageState's.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(e.Detail), &raw); err != nil {
			t.Fatal(err)
		}
		if len(raw) != 2 || raw["readings"] == nil || raw["models"] == nil {
			t.Fatalf("event data %s", e.Detail)
		}
		if !strings.Contains(e.Actor, `"system"`) {
			t.Fatalf("event actor %s", e.Actor)
		}
	}
}

func TestUsageScriptRoutes(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	list := body[[]api.UsageScriptInfo](t, asUser(b, s, "GET", "/api/usage/scripts", nil))
	if len(list) != 2 || list[0].Name != "claude" || !list[0].HasDefault || list[0].Customized || list[1].Name != "codex" {
		t.Fatalf("scripts %+v", list)
	}
	def := body[api.UsageScript](t, asUser(b, s, "GET", "/api/usage/scripts/claude", nil))
	if def.Text == "" || def.Text != def.Default || !def.HasDefault || def.Customized || def.Revision != api.NoRevision {
		t.Fatalf("claude %+v", def)
	}
	wantStatus(t, asUser(b, s, "GET", "/api/usage/scripts/nope", nil).StatusCode, http.StatusNotFound)

	// Create only with If-Match "0".
	resp := b.do("PUT", "/api/usage/scripts/mine", api.UsageScriptUpdate{Text: emptyScript}, withHeader(s, "If-Match", `"0"`))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	mine := body[api.UsageScript](t, resp)
	if mine.Name != "mine" || mine.Text != emptyScript || !mine.Customized || mine.HasDefault || mine.Revision == api.NoRevision {
		t.Fatalf("mine %+v", mine)
	}
	resp = b.do("PUT", "/api/usage/scripts/mine", api.UsageScriptUpdate{Text: emptyScript}, withHeader(s, "If-Match", `"0"`))
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if code := errorCode(t, resp.Body); code != api.CodeStaleSettings {
		t.Fatalf("code %s", code)
	}

	// Scripts that do not compile or define no usage function.
	for _, text := range []string{"function usage(", "local x = 1\n", "omo.http({url = 'https://example.invalid'})\nfunction usage() end"} {
		resp = b.do("PUT", "/api/usage/scripts/mine", api.UsageScriptUpdate{Text: text}, withHeader(s, "If-Match", `"`+mine.Revision+`"`))
		wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
		var env struct {
			Error struct {
				Code    string          `json:"code"`
				Details api.FieldDetail `json:"details"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatal(err)
		}
		if env.Error.Code != api.CodeValidation || env.Error.Details.Field != "text" {
			t.Fatalf("%q: %+v", text, env.Error)
		}
	}
	if got, _, _ := s.settings.UsageScript("mine"); got != emptyScript {
		t.Fatal("a refused script was written")
	}

	// A script a profile names cannot be deleted.
	meter(t, s, "p", "mine", "m")
	resp = b.do("DELETE", "/api/usage/scripts/mine", nil, withHeader(s, "If-Match", `"`+mine.Revision+`"`))
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if code := errorCode(t, resp.Body); code != api.CodeInUse {
		t.Fatalf("code %s", code)
	}
	info := body[[]api.UsageScriptInfo](t, asUser(b, s, "GET", "/api/usage/scripts", nil))
	if len(info) != 3 || info[2].Name != "mine" || !slices.Equal(info[2].UsedBy, []string{"p"}) {
		t.Fatalf("scripts %+v", info)
	}

	// Customize and reset a default.
	start := lastEventID(t, s)
	resp = b.do("PUT", "/api/usage/scripts/claude", api.UsageScriptUpdate{Text: emptyScript}, withHeader(s, "If-Match", `"0"`))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	custom := body[api.UsageScript](t, resp)
	if !custom.Customized || !custom.HasDefault || custom.Default != def.Default || custom.Text != emptyScript {
		t.Fatalf("customized claude %+v", custom)
	}
	ev := wantEvents(t, s, api.KindSettingsChanged, start, 1)
	var data api.SettingsChangedData
	if err := json.Unmarshal(ev[0], &data); err != nil || data.Section != api.SectionUsage || data.Name != "claude" || data.Source != api.SourceApp {
		t.Fatalf("event %s", ev[0])
	}
	resp = b.do("DELETE", "/api/usage/scripts/claude", nil, withHeader(s, "If-Match", `"`+custom.Revision+`"`))
	wantStatus(t, resp.StatusCode, http.StatusNoContent)
	if got := body[api.UsageScript](t, asUser(b, s, "GET", "/api/usage/scripts/claude", nil)); got.Customized || got.Text != def.Default {
		t.Fatalf("after reset %+v", got)
	}
}

// writeFile writes a test fixture file.
func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func luaString(s string) string { return fmt.Sprintf("%q", s) }

func TestUsageScriptTestRoute(t *testing.T) {
	s := newTestServer(t)
	home := t.TempDir()
	useRun(s, usage.NewRunFunc(usage.RunOptions{Home: home}))
	fixture := filepath.Join(home, "fake-usage.json")
	writeFile(t, fixture, `{"a": 10, "b": 70}`)
	meter(t, s, "fake", "fake", "a", "b")
	b := newBrowser(t, s)

	script := `function usage(account)
  local data = omo.json_decode(omo.read_file(` + luaString(fixture) + `))
  omo.log("profile " .. account.profile)
  return {
    { name = "a", used = data.a, resets_at = "2030-01-01T00:00:00Z" },
    { name = "b", used = data.b },
  }
end
`
	resp := asUser(b, s, "POST", "/api/usage/scripts/fake/test", api.UsageTest{Text: script, Profile: "fake"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	res := body[api.UsageTestResult](t, resp)
	if res.Error != "" || len(res.Windows) != 2 || res.Windows[0].Name != "a" || res.Windows[0].UsedPercent != 10 || res.Windows[1].UsedPercent != 70 {
		t.Fatalf("result %+v", res)
	}
	if res.Windows[0].ResetsAt == nil || res.Windows[1].ResetsAt != nil {
		t.Fatalf("resets %+v", res.Windows)
	}
	if !slices.Equal(res.Log, []string{"profile fake"}) || res.Millis < 0 {
		t.Fatalf("log %q, millis %d", res.Log, res.Millis)
	}
	// The draft is not saved.
	if text, _, _ := s.settings.UsageScript("fake"); text != emptyScript {
		t.Fatal("the test saved the draft")
	}

	// A failing draft is an answer, not a refusal.
	resp = asUser(b, s, "POST", "/api/usage/scripts/fake/test", api.UsageTest{Text: "function usage()\n  omo.log('before')\n  error('boom')\nend\n", Profile: "fake"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	res = body[api.UsageTestResult](t, resp)
	if !strings.Contains(res.Error, "boom") || !strings.Contains(res.Error, "line 3") || len(res.Windows) != 0 || !slices.Equal(res.Log, []string{"before"}) {
		t.Fatalf("failing result %+v", res)
	}
	resp = asUser(b, s, "POST", "/api/usage/scripts/fake/test", api.UsageTest{Text: "function usage(", Profile: "fake"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if res = body[api.UsageTestResult](t, resp); res.Error == "" || res.Windows == nil || res.Log == nil {
		t.Fatalf("syntax error result %+v", res)
	}

	// An unknown profile, a bad name and a script that is too long are refused.
	resp = asUser(b, s, "POST", "/api/usage/scripts/fake/test", api.UsageTest{Text: script, Profile: "nope"})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	resp = asUser(b, s, "POST", "/api/usage/scripts/Bad%20Name/test", api.UsageTest{Text: script, Profile: "fake"})
	wantStatus(t, resp.StatusCode, http.StatusNotFound)
	resp = asUser(b, s, "POST", "/api/usage/scripts/fake/test", api.UsageTest{Text: strings.Repeat("-", usage.MaxScript+1), Profile: "fake"})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)

	// One test at a time.
	f := newFakeRun()
	f.block = make(chan struct{})
	useRun(s, f.run)
	first := make(chan int, 1)
	go func() {
		rec := socketDo(t, s, "POST", "/api/usage/scripts/fake/test", api.UsageTest{Text: emptyScript, Profile: "fake"}, "")
		first <- rec.Code
	}()
	<-f.started
	resp = asUser(b, s, "POST", "/api/usage/scripts/fake/test", api.UsageTest{Text: emptyScript})
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if code := errorCode(t, resp.Body); code != api.CodeConflict {
		t.Fatalf("code %s", code)
	}
	close(f.block)
	if code := <-first; code != http.StatusOK {
		t.Fatalf("first test: %d", code)
	}
	// Without a profile the script runs with no account.
	resp = asUser(b, s, "POST", "/api/usage/scripts/fake/test", api.UsageTest{Text: emptyScript})
	wantStatus(t, resp.StatusCode, http.StatusOK)
}

// TestUsageAnswersHoldNoToken runs a script that reads a credential file
// holding a canary token, sends it to a server that echoes it, logs it and
// fails with it, through the test route and through the tracker. The token
// is in no answer, event, log line or kept file.
func TestUsageAnswersHoldNoToken(t *testing.T) {
	const canary = "omo-canary-0123456789abcdef-token"
	logs := &syncBuffer{}
	l := testHome(t)
	s, err := newServer(Options{Layout: l, Config: testConfig(), Version: "test"}, logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	home := t.TempDir()
	useRun(s, usage.NewRunFunc(usage.RunOptions{Home: home}))
	cred := filepath.Join(home, ".creds", "credentials.json")
	writeFile(t, cred, `{"token": "`+canary+`"}`)
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo", r.Header.Get("Authorization"))
		fmt.Fprintf(w, `{"seen": %q}`, r.Header.Get("Authorization"))
	}))
	t.Cleanup(echo.Close)

	script := `function usage(account)
  local tok = omo.json_decode(omo.read_file(` + luaString(cred) + `)).token
  omo.log("token " .. tok)
  local res = omo.http({ url = ` + luaString(echo.URL+"/usage") + `, headers = { Authorization = "Bearer " .. tok } })
  omo.log("answer " .. res.body)
  omo.log("header " .. tostring(res.headers["X-Echo"]))
  error("failed with " .. tok .. " and " .. res.body)
end
`
	b := newBrowser(t, s)
	resp := asUser(b, s, "POST", "/api/usage/scripts/leaky/test", api.UsageTest{Text: script})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	var answer strings.Builder
	res := body[api.UsageTestResult](t, resp)
	data, _ := json.Marshal(res)
	answer.Write(data)
	if res.Error == "" || len(res.Log) != 3 {
		t.Fatalf("the script did not run as written: %+v", res)
	}

	// The same script as a profile's script, run by the tracker.
	resp = b.do("PUT", "/api/usage/scripts/leaky", api.UsageScriptUpdate{Text: script}, withHeader(s, "If-Match", `"0"`))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	meter(t, s, "p", "leaky", "m")
	resp = asUser(b, s, "POST", "/api/usage/refresh", map[string]any{})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	st := body[api.UsageState](t, resp)
	if len(st.Readings) != 1 || st.Readings[0].Error == "" {
		t.Fatalf("readings %+v", st.Readings)
	}
	data, _ = json.Marshal(st)
	answer.Write(data)
	got := asUser(b, s, "GET", "/api/usage", nil)
	data, _ = json.Marshal(body[api.UsageState](t, got))
	answer.Write(data)
	if strings.Contains(answer.String(), canary) {
		t.Fatalf("an answer holds the token: %s", answer.String())
	}

	waitFor(t, "the usage event of the failed run", func() bool {
		for _, ev := range usageEvents(t, s, 0) {
			if len(ev.Readings) == 1 && ev.Readings[0].Error != "" {
				return true
			}
		}
		return false
	})
	evs, err := db.EventsSince(s.db, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if strings.Contains(e.Detail, canary) || strings.Contains(e.Actor, canary) {
			t.Fatalf("event %s holds the token: %s", e.Kind, e.Detail)
		}
	}
	s.Close()
	if strings.Contains(logs.String(), canary) {
		t.Fatalf("the server log holds the token:\n%s", logs.String())
	}
	if kept, err := os.ReadFile(filepath.Join(l.Run, "usage.json")); err == nil && strings.Contains(string(kept), canary) {
		t.Fatal("the kept readings hold the token")
	}
}

func TestPatchUsageReplacesWindows(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	patch := func(v any) *http.Response {
		return asUser(b, s, "PATCH", "/api/settings", v)
	}
	resp := patch(map[string]any{"usage": map[string]any{"windows": map[string]any{"a": map[string]any{"soft_percent": 50}}}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	resp = patch(map[string]any{"usage": map[string]any{"windows": map[string]any{"b": map[string]any{"hard_percent": 99}}}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	u := body[api.SettingsResponse](t, resp).Settings.Usage
	if len(u.Windows) != 1 || u.Windows["b"].HardPercent == nil || *u.Windows["b"].HardPercent != 99 || u.Windows["b"].SoftPercent != nil {
		t.Fatalf("windows not replaced whole: %+v", u.Windows)
	}
	if u.SoftPercent != 90 || u.HardPercent != 98 {
		t.Fatalf("other usage keys changed: %+v", u)
	}
	// Other usage keys merge and keep the windows.
	resp = patch(map[string]any{"usage": map[string]any{"soft_percent": 80}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	u = body[api.SettingsResponse](t, resp).Settings.Usage
	if u.SoftPercent != 80 || u.HardPercent != 98 || len(u.Windows) != 1 || u.Windows["b"].HardPercent == nil {
		t.Fatalf("after a soft_percent patch: %+v", u)
	}
	// A refused patch changes nothing.
	resp = patch(map[string]any{"usage": map[string]any{"windows": map[string]any{"c": map[string]any{"soft_percent": 0}}}})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if w := s.settings.General().Usage.Windows; len(w) != 1 || w["b"].HardPercent == nil {
		t.Fatalf("after a refused patch: %+v", w)
	}
	// An empty object removes every override.
	resp = patch(map[string]any{"usage": map[string]any{"windows": map[string]any{}}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if w := body[api.SettingsResponse](t, resp).Settings.Usage.Windows; len(w) != 0 {
		t.Fatalf("windows %+v", w)
	}
}

// TestPatchReplacesListsInsideObjects pins the class: a map or a list inside
// a merged object is replaced whole by a PATCH, never merged into the
// current one.
func TestPatchReplacesListsInsideObjects(t *testing.T) {
	s := newTestServer(t)
	g := s.settings.General()
	g.Usage.Windows = map[string]api.UsageThreshold{"x": {SoftPercent: pct(50)}, "y": {SoftPercent: pct(60)}}
	if err := s.settings.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, s)
	resp := asUser(b, s, "PATCH", "/api/settings", map[string]any{"usage": map[string]any{"windows": map[string]any{"y": map[string]any{"hard_percent": 99}}}})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	w := s.settings.General().Usage.Windows
	if len(w) != 1 || w["y"].SoftPercent != nil || w["y"].HardPercent == nil {
		t.Fatalf("windows %+v", w)
	}
}

func TestUsageSettingsReachTheTracker(t *testing.T) {
	s := newTestServer(t)
	f := newFakeRun()
	f.set("fake", usage.Window{Name: "a", UsedPercent: 85})
	useRun(s, f.run)
	meter(t, s, "fake", "fake", "a")
	s.usage.tracker.Refresh(context.Background())
	if m := s.usage.tracker.Model("fake/a"); m.State != api.UsageOK {
		t.Fatalf("at 85 %%: %+v", m)
	}
	b := newBrowser(t, s)
	// A threshold change through the API.
	wantStatus(t, asUser(b, s, "PATCH", "/api/settings", map[string]any{"usage": map[string]any{"soft_percent": 80}}).StatusCode, http.StatusOK)
	waitFor(t, "the tracker to see the new threshold", func() bool { return s.usage.tracker.Model("fake/a").State == api.UsageSoft })
	// A profile change: a new account (another environment) runs at once.
	runs := f.count()
	second := settings.Profile{Name: "second", CLI: api.CLICustom, Command: "second", UsageScript: "fake", Env: map[string]string{"ACCOUNT": "two"}, Models: map[string]settings.Model{"z": {}}}
	if err := s.settings.PutProfile(second); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the tracker to know the new profile's model", func() bool {
		return s.usage.tracker.Model("second/z").State != api.UsageUnmetered
	})
	waitFor(t, "a run for the new account", func() bool { return f.count() > runs })
	// A script change runs the account again.
	runs = f.count()
	if err := s.settings.PutUsageScriptIf("", "fake", "-- changed\n"+emptyScript); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a run after the script changed", func() bool { return f.count() > runs })
	// A hand edit of a script is seen too.
	runs = f.count()
	writeFile(t, filepath.Join(s.opts.Layout.Root, "usage", "fake.lua"), "-- by hand\n"+emptyScript)
	s.settings.Reload()
	waitFor(t, "a run after the hand edit", func() bool { return f.count() > runs })
}

func TestStartAnywayRoute(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), false)
	f := newFakeRun()
	f.set("fake", usage.Window{Name: "a", UsedPercent: 95})
	useRun(s, f.run)
	prof := fakeProfile()
	prof.UsageScript = "fake"
	if err := s.settings.PutUsageScriptIf("", "fake", emptyScript); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.PutProfile(prof); err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())
	s.usage.tracker.Refresh(context.Background())
	if m := s.usage.tracker.Model("fake/a"); m.State != api.UsageSoft {
		t.Fatalf("fake/a %+v", m)
	}
	s.StartEngine()
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "own model", Projects: []string{p}, Model: "fake/a"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "backlog", Projects: []string{p}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Move(ctx, board.User, int64(tk.Number), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the ticket to wait for usage", func() bool {
		got, err := s.board.Get(ctx, board.User, int64(tk.Number))
		return err == nil && got.StartBlocked == api.StartBlockedUsage
	})
	b := newBrowser(t, s)
	path := fmt.Sprintf("/api/tickets/%d/start-anyway", tk.Number)

	// A ticket that does not wait for usage on its own model.
	browserError(t, asUser(b, s, "POST", fmt.Sprintf("/api/tickets/%d/start-anyway", other.Number), api.StartAnyway{}), http.StatusConflict, api.CodeInvalidTransition)
	browserError(t, asUser(b, s, "POST", "/api/tickets/999/start-anyway", api.StartAnyway{}), http.StatusNotFound, api.CodeNotFound)
	if _, ok := liveOf(t, s, api.RoleDeveloper, tk.Number); ok {
		t.Fatal("a developer started on a model over its soft threshold")
	}

	resp := asUser(b, s, "POST", path, api.StartAnyway{})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := body[api.Ticket](t, resp); got.Number != tk.Number {
		t.Fatalf("answer %+v", got)
	}
	var dev api.AgentSession
	waitFor(t, "the developer on the ticket's own model", func() bool {
		var ok bool
		dev, ok = liveOf(t, s, api.RoleDeveloper, tk.Number)
		return ok && dev.Model == "fake/a"
	})
	// An agent may not. (A session made by hand would not do: the running
	// engine ends a session without a process of its own.)
	wantError(t, socketDo(t, s, "POST", path, api.StartAnyway{}, dev.ID), http.StatusForbidden, api.CodeForbidden)
}

func TestStartAnywayWithoutEngine(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	browserError(t, asUser(b, s, "POST", "/api/tickets/1/start-anyway", api.StartAnyway{}), http.StatusServiceUnavailable, api.CodeUnavailable)
}

// TestEngineSeesUsage: a model at its hard threshold starts no developer;
// once a refresh shows it below its limits, the engine starts one by itself.
func TestEngineSeesUsage(t *testing.T) {
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), false)
	f := newFakeRun()
	f.set("fake", usage.Window{Name: "a", UsedPercent: 99})
	useRun(s, f.run)
	prof := fakeProfile()
	prof.UsageScript = "fake"
	if err := s.settings.PutUsageScriptIf("", "fake", emptyScript); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.PutProfile(prof); err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())
	s.usage.tracker.Refresh(context.Background())
	s.StartEngine()
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{p}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Move(ctx, board.User, int64(tk.Number), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the ticket to wait for usage", func() bool {
		got, err := s.board.Get(ctx, board.User, int64(tk.Number))
		return err == nil && got.StartBlocked == api.StartBlockedUsage
	})
	if _, ok := liveOf(t, s, api.RoleDeveloper, tk.Number); ok {
		t.Fatal("a developer started on a model at its hard threshold")
	}
	f.set("fake", usage.Window{Name: "a", UsedPercent: 10})
	wantStatus(t, asUser(newBrowser(t, s), s, "POST", "/api/usage/refresh", map[string]any{}).StatusCode, http.StatusOK)
	waitFor(t, "the developer once the model is below its limits", func() bool {
		_, ok := liveOf(t, s, api.RoleDeveloper, tk.Number)
		return ok
	})
}

// TestUsageRoutesAgentsChangeNothing sends every usage route as an agent
// with a body that would change something: each is refused, no script runs
// and no file in usage/ changes.
func TestUsageRoutesAgentsChangeNothing(t *testing.T) {
	s := newTestServer(t)
	f := newFakeRun()
	useRun(s, f.run)
	meter(t, s, "p", "mine", "m")
	s.usage.tracker.Refresh(context.Background())
	sessions := map[api.Role]string{}
	for _, role := range agentRoles {
		sessions[role] = liveSession(t, s, role).ID
	}
	dir := filepath.Join(s.opts.Layout.Root, "usage")
	snapshot := func() map[string]string {
		out := map[string]string{}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			out[e.Name()] = string(data)
		}
		return out
	}
	before := snapshot()
	runs := f.count()
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/usage", nil},
		{"POST", "/api/usage/refresh", map[string]any{}},
		{"GET", "/api/usage/scripts", nil},
		{"GET", "/api/usage/scripts/mine", nil},
		{"PUT", "/api/usage/scripts/mine", api.UsageScriptUpdate{Text: "-- evil\n" + emptyScript}},
		{"PUT", "/api/usage/scripts/claude", api.UsageScriptUpdate{Text: "-- evil\n" + emptyScript}},
		{"DELETE", "/api/usage/scripts/mine", nil},
		{"POST", "/api/usage/scripts/mine/test", api.UsageTest{Text: emptyScript, Profile: "p"}},
		{"POST", "/api/tickets/1/start-anyway", api.StartAnyway{}},
	} {
		for _, role := range agentRoles {
			rec := socketDo(t, s, c.method, c.path, c.body, sessions[role])
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d, want 403", c.method, c.path, role, rec.Code)
			}
		}
	}
	time.Sleep(50 * time.Millisecond)
	if f.count() != runs {
		t.Fatal("an agent ran a usage script")
	}
	if after := snapshot(); !reflectEqual(before, after) {
		t.Fatalf("agents changed usage/: %v → %v", before, after)
	}
}

func reflectEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestDetectedProfilesUseShippedScripts: detection at a first start writes
// valid claude and codex profiles that name the shipped scripts, and the
// tracker knows their accounts (no script runs for real here).
func TestDetectedProfilesUseShippedScripts(t *testing.T) {
	l := testHome(t)
	look := &fakeLookPath{found: []string{"claude", "codex"}}
	s := openDetecting(t, l, look.look, nil)
	for name, script := range map[string]string{"claude": "claude", "codex": "codex"} {
		p, ok := s.settings.Profile(name)
		if !ok || p.UsageScript != script {
			t.Fatalf("profile %s: %+v (found %v)", name, p, ok)
		}
	}
	if errs := s.settings.Errors(); len(errs) != 0 {
		t.Fatalf("settings errors %+v", errs)
	}
	s.waitSettingsEvents(context.Background())
	waitFor(t, "readings of both accounts", func() bool {
		scripts := map[string]bool{}
		for _, r := range s.usage.tracker.State().Readings {
			scripts[r.Script] = true
		}
		return scripts["claude"] && scripts["codex"]
	})
	// A profile write that names a script that does not exist is refused.
	err := s.settings.PutProfile(settings.Profile{Name: "x", CLI: api.CLICustom, Command: "x", UsageScript: "nope", Models: map[string]settings.Model{"m": {}}})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.CodeValidation {
		t.Fatalf("profile with a missing script: %v", err)
	}
}

// usageGoroutines counts the goroutines of the usage tracker, its event
// writer and the test runs.
func usageGoroutines() int { return len(usageStacks()) }

func usageStacks() []string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "usage.(*Tracker)") || strings.Contains(g, "server.(*Server).writeUsageEvents") || strings.Contains(g, "server.(*fakeRun).run") {
			out = append(out, g)
		}
	}
	return out
}

// TestCloseStopsUsage closes a server while the tracker runs a script and
// a script test runs: Close cancels both and returns; no goroutine of the
// usage work is left.
func TestCloseStopsUsage(t *testing.T) {
	baseline := usageGoroutines()
	s := newTestServer(t)
	f := newFakeRun()
	f.block = make(chan struct{})
	t.Cleanup(func() { close(f.block) })
	useRun(s, f.run)
	meter(t, s, "p", "mine", "m")
	<-f.started // the tracker's run
	answered := make(chan int, 1)
	go func() {
		rec := socketDo(t, s, "POST", "/api/usage/scripts/mine/test", api.UsageTest{Text: emptyScript}, "")
		answered <- rec.Code
	}()
	<-f.started // the test's run
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close waits for usage runs")
	}
	select {
	case code := <-answered:
		if code != http.StatusOK && code != http.StatusServiceUnavailable {
			t.Fatalf("the test answered %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the script test did not end on Close")
	}
	deadline := time.Now().Add(10 * time.Second)
	for usageGoroutines() != baseline {
		if time.Now().After(deadline) {
			t.Fatalf("usage goroutines left after Close:\n%s", strings.Join(usageStacks(), "\n\n"))
		}
		time.Sleep(10 * time.Millisecond)
	}
	// After Close nothing starts.
	s.usage.tracker.SettingsChanged()
	s.usage.tracker.Refresh(context.Background())
	time.Sleep(20 * time.Millisecond)
	if n := usageGoroutines(); n != baseline {
		t.Fatalf("%d usage goroutines after a refresh past Close", n)
	}
}
