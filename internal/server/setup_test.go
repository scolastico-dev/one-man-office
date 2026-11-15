package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentsetup"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// fakeLookPath finds the given commands at /opt/fake/<command> and counts
// its calls. It never looks at the real PATH.
type fakeLookPath struct {
	found []string
	calls atomic.Int32
}

func (f *fakeLookPath) look(command string) (string, error) {
	f.calls.Add(1)
	if slices.Contains(f.found, command) {
		return "/opt/fake/" + command, nil
	}
	return "", exec.ErrNotFound
}

// openDetecting opens a server on l whose first start looks up programs with
// look; the log goes to out.
func openDetecting(t *testing.T, l globalhome.Layout, look func(string) (string, error), out io.Writer) *Server {
	t.Helper()
	s, err := newServer(Options{Layout: l, Config: testConfig(), Version: "test", DetectAgents: look}, out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func roleModels(t *testing.T, s *Server, name string) []string {
	t.Helper()
	r, ok := s.settings.Role(name)
	if !ok {
		t.Fatalf("no role %s", name)
	}
	return r.Models
}

func TestDetectOnlyAtFirstStart(t *testing.T) {
	l := testHome(t)
	look := &fakeLookPath{found: []string{"claude"}}
	log := &syncBuffer{}
	s := openDetecting(t, l, look.look, log)
	p, ok := s.settings.Profile("claude")
	if !ok {
		t.Fatalf("no claude profile after the first start; log:\n%s", log.String())
	}
	if p.CLI != api.CLIClaude || p.Command != "claude" || len(p.Models) != 4 {
		t.Errorf("profile = %+v", p)
	}
	for role, want := range map[string][]string{
		"ceo": {"claude/fable"}, "pm": {"claude/opus"}, "developer": {"claude/sonnet"}, "reviewer": {"claude/opus"},
	} {
		if got := roleModels(t, s, role); !slices.Equal(got, want) {
			t.Errorf("role %s = %v, want %v", role, got, want)
		}
	}
	for _, line := range []string{"found claude at /opt/fake/claude", "created the profile claude", "assigned models to the role ceo"} {
		if !strings.Contains(log.String(), line) {
			t.Errorf("log lacks %q:\n%s", line, log.String())
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The second start on the same home looks for nothing, also after the
	// profile and the roles were removed.
	if err := os.Remove(filepath.Join(l.Root, "profiles", "claude.yaml")); err != nil {
		t.Fatal(err)
	}
	for _, r := range settings.RoleNames {
		if err := os.Remove(filepath.Join(l.Root, "roles", r+".yaml")); err != nil {
			t.Fatal(err)
		}
	}
	look.calls.Store(0)
	s2 := openDetecting(t, l, look.look, io.Discard)
	if n := look.calls.Load(); n != 0 {
		t.Errorf("the second start looked up %d programs", n)
	}
	if ps := s2.settings.Profiles(); len(ps) != 0 {
		t.Errorf("profiles after the second start: %v", ps)
	}

	// A home with a settings.yaml before the first start (the browser test
	// harness writes one) is no first start, nor is one with a profile.
	l3 := testHome(t)
	if err := os.WriteFile(filepath.Join(l3.Root, "settings.yaml"), []byte("# written by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	look.calls.Store(0)
	s3 := openDetecting(t, l3, look.look, io.Discard)
	if n := look.calls.Load(); n != 0 || len(s3.settings.Profiles()) != 0 {
		t.Errorf("a home with settings.yaml detected: %d lookups, profiles %v", n, s3.settings.Profiles())
	}
	l5 := testHome(t)
	if err := os.WriteFile(filepath.Join(l5.Root, "profiles", "mine.yaml"), []byte("cli: custom\ncommand: mine\nmodels:\n  m: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	look.calls.Store(0)
	s5 := openDetecting(t, l5, look.look, io.Discard)
	if n := look.calls.Load(); n != 0 {
		t.Errorf("a home with a profile detected: %d lookups, profiles %v", n, s5.settings.Profiles())
	}

	// Without DetectAgents nothing is looked up at a first start.
	s4 := newTestServer(t)
	if len(s4.settings.Profiles()) != 0 {
		t.Errorf("profiles without DetectAgents: %v", s4.settings.Profiles())
	}
}

func postDetect(t *testing.T, s *Server, b *browser) api.DetectResult {
	t.Helper()
	resp := b.do("POST", "/api/agent-setup/detect", struct{}{}, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	return decodeBody[api.DetectResult](t, resp.Body)
}

func TestDetectRouteCreatesMissing(t *testing.T) {
	s := newTestServer(t)
	look := &fakeLookPath{found: []string{"claude", "gemini"}}
	s.opts.DetectAgents = look.look
	b := newBrowser(t, s)

	res := postDetect(t, s, b)
	if len(res.Found) != 2 || res.Found[0].CLI != api.CLIClaude || res.Found[0].Path != "/opt/fake/claude" {
		t.Errorf("found = %+v", res.Found)
	}
	if !slices.Equal(res.Created, []string{"claude", "gemini"}) {
		t.Errorf("created = %v", res.Created)
	}
	if !slices.Equal(res.Assigned, []string{"ceo", "pm", "developer", "reviewer"}) {
		t.Errorf("assigned = %v", res.Assigned)
	}
	if res.Skipped == nil || len(res.Skipped) != 0 {
		t.Errorf("skipped = %#v", res.Skipped)
	}
	if got := roleModels(t, s, "ceo"); !slices.Equal(got, []string{"claude/fable", "gemini/carbon"}) {
		t.Errorf("ceo = %v", got)
	}

	// Once more: everything exists, nothing is written.
	res = postDetect(t, s, b)
	if len(res.Created) != 0 || len(res.Assigned) != 0 || len(res.Skipped) != 2 {
		t.Errorf("second detection = %+v", res)
	}
	// Without a body the request is refused.
	resp := b.do("POST", "/api/agent-setup/detect", nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
}

func TestDetectNeverOverwrites(t *testing.T) {
	s := newTestServer(t)
	mine := settings.Profile{Name: "claude", CLI: api.CLIClaude, Command: "my-claude", Models: map[string]settings.Model{"x": {}}}
	if err := s.settings.PutProfile(mine); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.PutRole(settings.Role{Name: "developer", Models: []string{"claude/x"}, Assignment: api.AssignmentRandom}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.settings.Profile("claude")
	devBefore, _ := s.settings.Role("developer")
	look := &fakeLookPath{found: []string{"claude", "gemini"}}
	s.opts.DetectAgents = look.look
	res := postDetect(t, s, newBrowser(t, s))

	if !slices.Equal(res.Created, []string{"gemini"}) {
		t.Errorf("created = %v", res.Created)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "claude") {
		t.Errorf("skipped = %v", res.Skipped)
	}
	if slices.Contains(res.Assigned, "developer") {
		t.Errorf("the developer role, which has models, was assigned: %v", res.Assigned)
	}
	after, _ := s.settings.Profile("claude")
	if after.Command != before.Command || len(after.Models) != 1 {
		t.Errorf("the claude profile changed: %+v", after)
	}
	devAfter, _ := s.settings.Role("developer")
	if !slices.Equal(devAfter.Models, devBefore.Models) || devAfter.Assignment != devBefore.Assignment {
		t.Errorf("the developer role changed: %+v", devAfter)
	}
	if got := roleModels(t, s, "pm"); !slices.Equal(got, []string{"gemini/argon"}) {
		t.Errorf("pm = %v", got)
	}
}

func TestDetectReportsConcurrentChanges(t *testing.T) {
	s := newTestServer(t)
	look := &fakeLookPath{found: []string{"codex"}}
	// The lookup runs before the plan's writes: a role changed by hand at
	// that moment must be left alone.
	rolesDir := filepath.Join(s.opts.Layout.Root, "roles")
	s.opts.DetectAgents = func(cmd string) (string, error) {
		if cmd == "codex" {
			if err := os.WriteFile(filepath.Join(rolesDir, "pm.yaml"), []byte("models: []\nassignment: random\n"), 0o600); err != nil {
				t.Error(err)
			}
		}
		return look.look(cmd)
	}
	res := postDetect(t, s, newBrowser(t, s))
	if !slices.Equal(res.Created, []string{"codex"}) || slices.Contains(res.Assigned, "pm") {
		t.Errorf("result = %+v", res)
	}
	found := false
	for _, line := range res.Skipped {
		found = found || strings.Contains(line, "pm") && strings.Contains(line, "changed")
	}
	if !found {
		t.Errorf("skipped does not name the changed role: %v", res.Skipped)
	}
	if r, _ := s.settings.Role("pm"); r.Assignment != api.AssignmentRandom || len(r.Models) != 0 {
		t.Errorf("the hand-edited pm role = %+v", r)
	}
}

// eventDetails returns the details of every event in the log.
func eventDetails(t *testing.T, s *Server) string {
	t.Helper()
	rows, err := s.db.Query(`SELECT kind || ' ' || detail FROM events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		b.WriteString(d + "\n")
	}
	return b.String()
}

func postEnvCheck(t *testing.T, s *Server, b *browser, env map[string]string) (*http.Response, string) {
	t.Helper()
	resp := b.do("POST", "/api/settings/agent-env/check", api.AgentEnvCheck{Env: env}, bearer(s))
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(raw)
}

func checks(t *testing.T, raw string) map[string]api.EnvVarCheck {
	t.Helper()
	var rep api.AgentEnvReport
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatal(err)
	}
	out := map[string]api.EnvVarCheck{}
	names := []string{}
	for _, v := range rep.Variables {
		out[v.Name] = v
		names = append(names, v.Name)
	}
	if !slices.IsSorted(names) {
		t.Errorf("variables not sorted: %v", names)
	}
	return out
}

func TestEnvCheckReportsNoValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh commands")
	}
	log := &syncBuffer{}
	s, err := newServer(Options{Layout: testHome(t), Config: testConfig(), Version: "test"}, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.setup.commandTimeout = 300 * time.Millisecond
	b := newBrowser(t, s)

	const canary = "canary-7f3a9c"
	env := map[string]string{
		"LITERAL_TOKEN": canary + "-literal",
		"FROM_COMMAND":  "`echo " + canary + "-command`",
		"REFERENCE":     "${LITERAL_TOKEN}-ref",
		"EMPTY":         "",
		"SILENT":        "`true`",
		"FAILS":         "`exit 3`",
		"FAILS_LOUD":    "`echo out; exit 4`",
		"HANGS":         "`sleep 5; echo " + canary + "-late`",
		"AFTER_HANG":    "x`echo " + canary + "-after`",
	}
	start := time.Now()
	resp, raw := postEnvCheck(t, s, b, env)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if time.Since(start) > 4*time.Second {
		t.Errorf("the check took %s", time.Since(start))
	}
	got := checks(t, raw)
	for name, want := range map[string]string{
		"LITERAL_TOKEN": "set", "FROM_COMMAND": "set", "REFERENCE": "set", "EMPTY": "empty", "SILENT": "empty",
		"FAILS": "failed", "FAILS_LOUD": "set",
		"HANGS": "failed", "AFTER_HANG": "set",
	} {
		if got[name].State != want {
			t.Errorf("%s = %+v, want %s", name, got[name], want)
		}
	}
	if msg := got["HANGS"].Message; msg != "its command did not finish within 300ms" {
		t.Errorf("HANGS message = %q", msg)
	}
	if msg := got["FAILS"].Message; msg != "its command exited with status 3 and printed nothing" {
		t.Errorf("FAILS message = %q", msg)
	}
	if len(got) != len(env) {
		t.Errorf("%d variables answered, want %d", len(got), len(env))
	}
	s.waitSettingsEvents(t.Context())
	for where, text := range map[string]string{"answer": raw, "log": log.String(), "events": eventDetails(t, s)} {
		if strings.Contains(text, canary) {
			t.Errorf("the %s contains a value:\n%s", where, text)
		}
	}
	// Nothing was saved.
	if _, ok := s.settings.General().AgentEnv["LITERAL_TOKEN"]; ok {
		t.Error("the check saved the draft")
	}
}

func TestEnvCheckTakesDraftAsSent(t *testing.T) {
	s := newTestServer(t)
	const stored = "sk-canary-9d1e"
	if _, err := s.settings.UpdateGeneral("", func(g *settings.General) error {
		g.AgentEnv["MY_API_KEY"] = stored
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, s)
	// The draft is checked as it is: a stored variable it leaves out is
	// not there, and no value is answered.
	resp, raw := postEnvCheck(t, s, b, map[string]string{"KEY": "literal", "COPY": "$KEY", "OLD": "$MY_API_KEY"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got := checks(t, raw)
	if got["KEY"].State != "set" || got["COPY"].State != "set" || got["OLD"].State != "empty" || strings.Contains(raw, stored) || strings.Contains(raw, "literal") {
		t.Errorf("answer = %s", raw)
	}

	// A draft the settings would refuse is refused, naming the field.
	resp, raw = postEnvCheck(t, s, b, map[string]string{"BAD=NAME": "x"})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if !strings.Contains(raw, `"field":"agent_env"`) {
		t.Errorf("refusal = %s", raw)
	}
}

func TestEnvCheckOneAtATime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh commands")
	}
	s := newTestServer(t)
	s.setup.commandTimeout = 2 * time.Second
	b := newBrowser(t, s)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp := b.do("POST", "/api/settings/agent-env/check", api.AgentEnvCheck{Env: map[string]string{"SLOW": "`sleep 1`"}}, bearer(s))
		if resp.StatusCode != http.StatusOK {
			t.Errorf("first check: %d", resp.StatusCode)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for s.setup.envCheck.TryLock() {
		s.setup.envCheck.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("the first check did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	resp, _ := postEnvCheck(t, s, b, map[string]string{"A": "b"})
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	wg.Wait()
	resp, _ = postEnvCheck(t, s, b, map[string]string{"A": "b"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
}

func TestEnvCheckStopsAtItsLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh commands")
	}
	s := newTestServer(t)
	s.setup.commandTimeout = time.Second
	s.setup.checkLimit = 300 * time.Millisecond
	b := newBrowser(t, s)
	start := time.Now()
	resp, raw := postEnvCheck(t, s, b, map[string]string{"A": "`sleep 3`", "B": "plain"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if time.Since(start) > 900*time.Millisecond {
		t.Errorf("the answer took %s", time.Since(start))
	}
	got := checks(t, raw)
	if got["A"].State != "failed" || got["B"].State != "failed" || !strings.Contains(got["B"].Message, "stopped") {
		t.Errorf("answer = %s", raw)
	}
	// The slot stays taken until the commands have ended.
	resp, _ = postEnvCheck(t, s, b, map[string]string{"A": "b"})
	wantStatus(t, resp.StatusCode, http.StatusConflict)
}

func TestPutPromptRefusesBrokenTemplateOverHTTP(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	resp := b.do("PUT", "/api/prompts/developer", api.PromptUpdate{Text: "Hello {{ .Nope }}"}, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	var env struct {
		Error struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details api.FieldDetail `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != api.CodeValidation || env.Error.Details.Field != "text" || !strings.Contains(env.Error.Message, "Nope") {
		t.Errorf("refusal = %+v", env.Error)
	}
	if _, customized, _ := s.settings.Prompt("developer"); customized {
		t.Error("the broken prompt was saved")
	}
	// What the preview refuses, the store refuses.
	resp = b.do("PUT", "/api/prompts/developer", api.PromptUpdate{Text: "{{range 100000000}}{{end}}"}, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
}

func TestPromptPreviewRoute(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	preview := func(name, text string) (int, api.PromptPreview) {
		resp := b.do("POST", "/api/prompts/"+name+"/preview", api.PromptUpdate{Text: text}, bearer(s))
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, api.PromptPreview{}
		}
		return resp.StatusCode, decodeBody[api.PromptPreview](t, resp.Body)
	}
	status, p := preview("start", "You are {{.Name}} on #{{.Ticket}}.")
	if status != http.StatusOK || p.Text != "You are Dana on #12." || p.Error != "" {
		t.Errorf("start preview = %d %+v", status, p)
	}
	status, p = preview("developer-instructions", "Write commit messages in German.")
	want, _ := prompts.Preview("developer-instructions", "Write commit messages in German.", prompts.Defaults()["developer"])
	if status != http.StatusOK || p.Text != want || !strings.Contains(p.Text, "Write commit messages in German.") {
		t.Errorf("instructions preview = %d %+v", status, p)
	}
	// A customized role prompt is the one in effect.
	if err := s.settings.PutPrompt("developer", "Custom role prompt.\n{{.Instructions}}"); err != nil {
		t.Fatal(err)
	}
	if _, p = preview("developer-instructions", "Be brief."); p.Text != "Custom role prompt.\nBe brief." {
		t.Errorf("instructions in the customized prompt = %q", p.Text)
	}
	status, p = preview("developer", "{{ .Nope }}")
	if status != http.StatusOK || p.Text != "" || !strings.Contains(p.Error, "Nope") {
		t.Errorf("broken preview = %d %+v", status, p)
	}
	if status, _ = preview("ghost", "x"); status != http.StatusNotFound {
		t.Errorf("unknown prompt: %d", status)
	}
}

func TestSetupRoutesAreUserOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh commands")
	}
	s := newTestServer(t)
	look := &fakeLookPath{found: []string{"claude"}}
	s.opts.DetectAgents = look.look
	marker := filepath.Join(t.TempDir(), "ran")
	routes := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/agent-setup/detect", struct{}{}},
		{"POST", "/api/settings/agent-env/check", api.AgentEnvCheck{Env: map[string]string{"X": "`touch " + marker + "`"}}},
		{"POST", "/api/prompts/developer/preview", api.PromptUpdate{Text: "x"}},
		{"POST", "/api/profiles/developer/catalog-models", struct{}{}},
	}
	for _, row := range s.routeTable {
		for _, r := range routes {
			if row.pattern == r.method+" "+strings.Replace(r.path, "/developer/", "/{name}/", 1) && row.access != userOnly {
				t.Errorf("%s is not user-only", row.pattern)
			}
		}
	}
	for _, role := range []api.Role{api.RoleCEO, api.RolePM, api.RoleDeveloper, api.RoleReviewer} {
		sess := liveSession(t, s, role)
		for _, r := range routes {
			rec := socketDo(t, s, r.method, r.path, r.body, sess.ID)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d, want 403", r.method, r.path, role, rec.Code)
			}
		}
	}
	if n := look.calls.Load(); n != 0 || len(s.settings.Profiles()) != 0 {
		t.Errorf("an agent's request detected: %d lookups, profiles %v", n, s.settings.Profiles())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("an agent's request ran a command: %v", err)
	}
	// The user may, on both listeners.
	rec := socketDo(t, s, "POST", "/api/prompts/developer/preview", api.PromptUpdate{Text: "x"}, "")
	wantStatus(t, rec.Code, http.StatusOK)
	resp := newBrowser(t, s).do("POST", "/api/prompts/developer/preview", api.PromptUpdate{Text: "x"}, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusOK)
}

// The catalog models route adds the catalog's models that a profile lacks,
// by name, and changes nothing else: not an existing model even where the
// catalog's differs, not the command, arguments, environment or roles.
func TestAddCatalogModels(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	mine := api.Profile{
		CLI: api.CLIGemini, Command: "my-gemini", Args: []string{"--sandbox"}, Env: map[string]string{"GEMINI_X": "1"},
		Models: map[string]api.ProfileModel{
			"default": {Args: []string{"--yolo"}},
			"argon":   {Args: []string{"--model", "my-argon"}, Description: "Mine."},
		},
	}
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/gemini", mine).StatusCode, http.StatusOK)
	wantStatus(t, asUser(b, s, "PUT", "/api/roles/pm", api.RoleSettings{Models: []string{"gemini/default"}, Assignment: "failover"}).StatusCode, http.StatusOK)
	before, _ := s.settings.Profile("gemini")
	pmBefore, _ := s.settings.Role("pm")

	resp := asUser(b, s, "POST", "/api/profiles/gemini/catalog-models", struct{}{})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	res := body[api.CatalogModelsAdded](t, resp)
	if !slices.Equal(res.Added, []string{"carbon", "hydrogen", "flash"}) {
		t.Fatalf("added = %v", res.Added)
	}
	if res.Profile.Revision == "" || res.Profile.Revision != s.settings.Revision(settings.SectionProfiles, "gemini") || len(res.Profile.Models) != 5 {
		t.Fatalf("profile = %+v", res.Profile)
	}
	after, _ := s.settings.Profile("gemini")
	cat, _ := agentsetup.Catalog(api.CLIGemini)
	for _, name := range res.Added {
		if !reflect.DeepEqual(after.Models[name], cat.Models[name]) {
			t.Errorf("%s = %+v, want the catalog's", name, after.Models[name])
		}
		delete(after.Models, name)
	}
	if !reflect.DeepEqual(after, before) {
		t.Errorf("the profile changed beyond the added models:\n%+v\n%+v", after, before)
	}
	if pm, _ := s.settings.Role("pm"); !reflect.DeepEqual(pm, pmBefore) {
		t.Errorf("the pm role changed: %+v", pm)
	}

	// Again: nothing is missing, nothing is written.
	rev := s.settings.Revision(settings.SectionProfiles, "gemini")
	again := body[api.CatalogModelsAdded](t, asUser(b, s, "POST", "/api/profiles/gemini/catalog-models", struct{}{}))
	if again.Added == nil || len(again.Added) != 0 || again.Profile.Revision != rev {
		t.Fatalf("second call = %+v", again)
	}

	// If-Match guards it like a profile write.
	wantStatus(t, withIfMatch(b, s, "POST", "/api/profiles/gemini/catalog-models", struct{}{}, `"`+api.NoRevision+`"`).StatusCode, http.StatusConflict)
	wantStatus(t, withIfMatch(b, s, "POST", "/api/profiles/gemini/catalog-models", struct{}{}, `"`+rev+`"`).StatusCode, http.StatusOK)

	// Unknown profile, custom CLI, missing body.
	wantStatus(t, asUser(b, s, "POST", "/api/profiles/nope/catalog-models", struct{}{}).StatusCode, http.StatusNotFound)
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/own", testProfile("m")).StatusCode, http.StatusOK)
	wantStatus(t, asUser(b, s, "POST", "/api/profiles/own/catalog-models", struct{}{}).StatusCode, http.StatusUnprocessableEntity)
	wantStatus(t, asUser(b, s, "POST", "/api/profiles/gemini/catalog-models", nil).StatusCode, http.StatusBadRequest)

	// Agents are refused and nothing changes.
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/codex", api.Profile{CLI: api.CLICodex, Command: "codex", Models: map[string]api.ProfileModel{"default": {}}}).StatusCode, http.StatusOK)
	sess := liveSession(t, s, api.RoleCEO)
	rec := socketDo(t, s, "POST", "/api/profiles/codex/catalog-models", struct{}{}, sess.ID)
	wantStatus(t, rec.Code, http.StatusForbidden)
	if codex, _ := s.settings.Profile("codex"); len(codex.Models) != 1 {
		t.Errorf("an agent's request added models: %+v", codex.Models)
	}
}
