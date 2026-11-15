package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// The canaries are secret values; no answer, event or log line may hold them.
const (
	canaryGeneral = "sk-canary-general-5e1f"
	canaryProfile = "sk-canary-profile-77a2"
	canaryModel   = "sk-canary-model-0c9d"
)

var canaries = []string{canaryGeneral, canaryProfile, canaryModel}

// noCanary fails when text holds a canary.
func noCanary(t *testing.T, where, text string) {
	t.Helper()
	for _, c := range canaries {
		if strings.Contains(text, c) {
			t.Errorf("%s holds the secret %s: %s", where, c, text)
		}
	}
}

// secretServer opens a server with its log in logOut and stores the canaries
// as secrets next to values that are no secrets: a command, a reference to
// another variable and a plain variable.
func secretServer(t *testing.T, logOut io.Writer) *Server {
	t.Helper()
	s, err := newServer(Options{Layout: testHome(t), Config: testConfig(), Version: "test"}, logOut)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	g := s.settings.General()
	g.AgentEnv = map[string]string{
		"ANTHROPIC_API_KEY": canaryGeneral,
		"FOO_TOKEN":         "`pass x`",
		"REF_SECRET":        "$OTHER",
		"PATH":              "/usr/bin",
	}
	if err := s.settings.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	p := api.Profile{
		Name: "claude", CLI: "custom", Command: "claude",
		Env: map[string]string{"ANTHROPIC_API_KEY": canaryProfile, "PLAIN": "visible"},
		Models: map[string]api.ProfileModel{
			"sonnet": {Env: map[string]string{"OPENAI_API_KEY": canaryModel}},
			"opus":   {},
		},
	}
	if err := s.settings.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())
	return s
}

// readBody reads and returns a response body.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSecretValuesHiddenInEveryAnswer(t *testing.T) {
	s := secretServer(t, io.Discard)
	b := newBrowser(t, s)
	checkGeneral := func(where string, g api.General) {
		t.Helper()
		want := map[string]string{"ANTHROPIC_API_KEY": api.HiddenValue, "FOO_TOKEN": "`pass x`", "REF_SECRET": "$OTHER", "PATH": "/usr/bin"}
		for k, v := range want {
			if g.AgentEnv[k] != v {
				t.Errorf("%s: agent_env %s = %q, want %q", where, k, g.AgentEnv[k], v)
			}
		}
	}
	checkProfile := func(where string, p api.Profile) {
		t.Helper()
		if p.Env["ANTHROPIC_API_KEY"] != api.HiddenValue || p.Env["PLAIN"] != "visible" || p.Models["sonnet"].Env["OPENAI_API_KEY"] != api.HiddenValue {
			t.Errorf("%s: profile %+v", where, p)
		}
	}

	resp := asUser(b, s, "GET", "/api/settings", nil)
	raw := readBody(t, resp)
	noCanary(t, "GET /api/settings", raw)
	var got api.SettingsResponse
	json.Unmarshal([]byte(raw), &got)
	checkGeneral("GET /api/settings", got.Settings)

	resp = asUser(b, s, "PUT", "/api/settings", got.Settings)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	raw = readBody(t, resp)
	noCanary(t, "PUT /api/settings", raw)
	json.Unmarshal([]byte(raw), &got)
	checkGeneral("PUT /api/settings", got.Settings)

	resp = asUser(b, s, "PATCH", "/api/settings", map[string]any{"agent_env": got.Settings.AgentEnv})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	raw = readBody(t, resp)
	noCanary(t, "PATCH /api/settings", raw)
	json.Unmarshal([]byte(raw), &got)
	checkGeneral("PATCH /api/settings", got.Settings)

	// A new secret typed by the user is answered hidden at once.
	resp = asUser(b, s, "PATCH", "/api/settings", map[string]any{"max_running_agents": 3})
	raw = readBody(t, resp)
	noCanary(t, "PATCH /api/settings (other key)", raw)

	resp = asUser(b, s, "GET", "/api/profiles", nil)
	raw = readBody(t, resp)
	noCanary(t, "GET /api/profiles", raw)
	var list api.ProfilesResponse
	json.Unmarshal([]byte(raw), &list)
	if len(list.Profiles) != 1 {
		t.Fatalf("profiles %+v", list)
	}
	checkProfile("GET /api/profiles", list.Profiles[0])

	resp = asUser(b, s, "PUT", "/api/profiles/claude", list.Profiles[0])
	wantStatus(t, resp.StatusCode, http.StatusOK)
	raw = readBody(t, resp)
	noCanary(t, "PUT /api/profiles/claude", raw)
	var p api.Profile
	json.Unmarshal([]byte(raw), &p)
	checkProfile("PUT /api/profiles/claude", p)

	// A new profile with a literal secret: hidden in the answer.
	fresh := api.Profile{CLI: "custom", Command: "x", Env: map[string]string{"MY_PASSWORD": canaryProfile}, Models: map[string]api.ProfileModel{"m": {}}}
	resp = asUser(b, s, "PUT", "/api/profiles/fresh", fresh)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	noCanary(t, "PUT /api/profiles/fresh", readBody(t, resp))

	// The store's own values are unchanged by hiding.
	if s.settings.General().AgentEnv["ANTHROPIC_API_KEY"] != canaryGeneral {
		t.Fatal("hiding changed the stored agent environment")
	}
	if sp, _ := s.settings.Profile("claude"); sp.Models["sonnet"].Env["OPENAI_API_KEY"] != canaryModel || sp.Env["ANTHROPIC_API_KEY"] != canaryProfile {
		t.Fatal("hiding changed the stored profile")
	}
}

// TestNoSecretInAnyAnswer walks the whole route table as the user, so a
// route added later is covered: no answer holds a stored secret, also not
// when the request is refused for a value that cannot be parsed.
func TestNoSecretInAnyAnswer(t *testing.T) {
	var logs syncBuffer
	s := secretServer(t, &logs)
	b := newBrowser(t, s)
	bad := []string{
		// A secret pasted into a field that cannot hold it: refusals never
		// quote the value back.
		`"` + canaryGeneral + `"`,
		`{"health":{"silent_after":"` + canaryGeneral + `"},"stop":{"handoff_timeout":"` + canaryProfile + `"}}`,
		`{"max_running_agents":"` + canaryGeneral + `"}`,
		`{"prompt_delivery":{"delay":"` + canaryModel + `"},"cli":"custom","command":"x","models":{"m":{}}}`,
		`{"agent_env":{"ANTHROPIC_API_KEY":"` + api.HiddenValue + `","NEW_KEY":"` + api.HiddenValue + `"}}`,
		`{"text":1}`,
	}
	for _, row := range s.routeTable {
		method, path, _ := strings.Cut(row.pattern, " ")
		path = wildcard.ReplaceAllString(path, "claude")
		if method == http.MethodGet {
			resp := asUser(b, s, method, path, nil)
			noCanary(t, row.pattern, readBody(t, resp))
			continue
		}
		for _, body := range bad {
			resp := rawDo(t, b, s, method, path, body)
			noCanary(t, row.pattern+" "+body, readBody(t, resp))
		}
	}
	// The secrets are still stored.
	if s.settings.General().AgentEnv["ANTHROPIC_API_KEY"] != canaryGeneral {
		t.Fatal("the stored secret was lost")
	}
	s.waitSettingsEvents(context.Background())
	noCanary(t, "the server log", logs.String())
}

func TestHiddenValueKeepsStoredSecret(t *testing.T) {
	s := secretServer(t, io.Discard)
	b := newBrowser(t, s)
	got := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil))
	wantStatus(t, asUser(b, s, "PUT", "/api/settings", got.Settings).StatusCode, http.StatusOK)
	got = body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil))
	wantStatus(t, asUser(b, s, "PATCH", "/api/settings", map[string]any{"agent_env": got.Settings.AgentEnv}).StatusCode, http.StatusOK)
	if file := readFile(t, s, "settings.yaml"); !strings.Contains(file, canaryGeneral) {
		t.Fatalf("settings.yaml lost the secret:\n%s", file)
	}
	if strings.Contains(readFile(t, s, "settings.yaml"), api.HiddenValue) {
		t.Fatal("settings.yaml holds the hidden marker")
	}

	list := body[api.ProfilesResponse](t, asUser(b, s, "GET", "/api/profiles", nil))
	p := list.Profiles[0]
	resp := withIfMatch(b, s, "PUT", "/api/profiles/claude", p, `"`+p.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	file := readFile(t, s, "profiles/claude.yaml")
	if !strings.Contains(file, canaryProfile) || !strings.Contains(file, canaryModel) || strings.Contains(file, api.HiddenValue) {
		t.Fatalf("profiles/claude.yaml:\n%s", file)
	}
	// Replacing a hidden value with a new one stores the new one.
	p = body[api.Profile](t, resp)
	p.Env["ANTHROPIC_API_KEY"] = "sk-replaced"
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/claude", p).StatusCode, http.StatusOK)
	if sp, _ := s.settings.Profile("claude"); sp.Env["ANTHROPIC_API_KEY"] != "sk-replaced" || sp.Models["sonnet"].Env["OPENAI_API_KEY"] != canaryModel {
		t.Fatalf("profile after replace %+v", sp)
	}
}

func TestHiddenValueWithoutStoredSecretRefused(t *testing.T) {
	s := secretServer(t, io.Discard)
	b := newBrowser(t, s)
	before := settingsSnapshot(t, s)
	g := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil)).Settings
	renamed := map[string]string{"OTHER_KEY": api.HiddenValue, "PATH": "/usr/bin"}
	notSecret := map[string]string{"PATH": api.HiddenValue}
	g.AgentEnv = renamed
	p := body[api.ProfilesResponse](t, asUser(b, s, "GET", "/api/profiles", nil)).Profiles[0]
	newVar := p
	newVar.Env = map[string]string{"ANTHROPIC_API_KEY": api.HiddenValue, "NEW_TOKEN": api.HiddenValue}
	brandNew := newVar
	brandNew.Name = ""
	otherModel := p
	otherModel.Models = map[string]api.ProfileModel{
		"sonnet": p.Models["sonnet"],
		"opus":   {Env: map[string]string{"OPENAI_API_KEY": api.HiddenValue}},
	}
	for _, c := range []struct {
		method, path string
		body         any
		code, field  string
	}{
		{"PUT", "/api/settings", g, api.CodeInvalidSettings, "agent_env.OTHER_KEY"},
		{"PATCH", "/api/settings", map[string]any{"agent_env": renamed}, api.CodeInvalidSettings, "agent_env.OTHER_KEY"},
		{"PATCH", "/api/settings", map[string]any{"agent_env": notSecret}, api.CodeInvalidSettings, "agent_env.PATH"},
		{"PUT", "/api/profiles/claude", newVar, api.CodeValidation, "env.NEW_TOKEN"},
		{"PUT", "/api/profiles/claude", otherModel, api.CodeValidation, "models.opus.env.OPENAI_API_KEY"},
		{"PUT", "/api/profiles/brandnew", brandNew, api.CodeValidation, "env.ANTHROPIC_API_KEY"},
	} {
		resp := asUser(b, s, c.method, c.path, c.body)
		env := body[errorEnvelope](t, resp)
		if resp.StatusCode != http.StatusUnprocessableEntity || env.Error.Code != c.code || env.Error.Details.Field != c.field || !strings.Contains(env.Error.Message, "Enter the value of") {
			t.Errorf("%s %s: %d %+v", c.method, c.path, resp.StatusCode, env)
		}
	}
	s.waitSettingsEvents(context.Background())
	if after := settingsSnapshot(t, s); len(after) != len(before) {
		t.Fatal("a refused write added a file")
	} else {
		for k, v := range before {
			if after[k] != v {
				t.Fatalf("a refused write changed %s", k)
			}
		}
	}
}

func TestSecretNotInEventsOrLog(t *testing.T) {
	var logs syncBuffer
	s := secretServer(t, &logs)
	b := newBrowser(t, s)
	start := lastEventID(t, s)
	st := openStream(t, b, s, "?since="+strconv.FormatInt(start, 10), nil)
	defer st.close()

	// Writes through the API that carry the secrets.
	wantStatus(t, asUser(b, s, "PATCH", "/api/settings", map[string]any{"agent_env": map[string]string{"ANTHROPIC_API_KEY": canaryGeneral, "B_SECRET": canaryModel}}).StatusCode, http.StatusOK)
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/other", api.Profile{CLI: "custom", Command: "x", Env: map[string]string{"KEY": canaryProfile}, Models: map[string]api.ProfileModel{"m": {}}}).StatusCode, http.StatusOK)
	// Refused writes that carry them.
	rawDo(t, b, s, "PATCH", "/api/settings", `{"health":{"silent_after":"`+canaryGeneral+`"}}`)
	rawDo(t, b, s, "PATCH", "/api/settings", `{"max_running_agents":0,"agent_env":{"ANTHROPIC_API_KEY":"`+canaryGeneral+`"}}`)
	// Broken hand edits holding them.
	root := s.opts.Layout.Root
	if err := os.WriteFile(filepath.Join(root, "settings.yaml"), []byte("agent_env:\n  ANTHROPIC_API_KEY: "+canaryGeneral+"\n  [broken: "+canaryGeneral+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "profiles", "claude.yaml"), []byte("cli: custom\ncommand: x\nenv:\n  KEY: "+canaryProfile+"\nmodels: "+canaryModel+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.settings.Reload()
	s.waitSettingsEvents(context.Background())
	if got := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil)); len(got.Errors) != 2 {
		t.Fatalf("errors %+v", got.Errors)
	} else {
		raw, _ := json.Marshal(got)
		noCanary(t, "GET /api/settings with broken files", string(raw))
	}
	last := lastEventID(t, s)
	if last <= start {
		t.Fatal("no events")
	}

	// The event log as stored.
	invalid := 0
	for _, kind := range []string{api.KindSettingsChanged, api.KindSettingsInvalid, api.KindProjectChanged} {
		for _, d := range settingsEvents(t, s, kind, start) {
			noCanary(t, kind+" event", string(d))
			if kind == api.KindSettingsInvalid {
				invalid++
			}
		}
	}
	if invalid < 2 {
		t.Fatalf("%d settings.invalid events, want at least 2", invalid)
	}
	// The event stream as the browser receives it, up to the last event.
	deadline := time.Now().Add(streamTimeout)
	for time.Now().Before(deadline) {
		f := st.next()
		noCanary(t, "the event stream", f.data)
		if f.id == strconv.FormatInt(last, 10) {
			break
		}
	}
	noCanary(t, "the server log", logs.String())
}
