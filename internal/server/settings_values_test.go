package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// The canaries are values stored in the settings. The settings and profile
// answers show them as stored; no refusal, event or log line may hold them.
const (
	canaryGeneral = "sk-canary-general-5e1f"
	canaryProfile = "sk-canary-profile-77a2"
	canaryModel   = "sk-canary-model-0c9d"
	// canaryNumber is a value pasted where a number belongs: too large
	// for any integer, so a parser that quotes it would show it.
	canaryNumber = "271828182845904523536028747135"
)

var canaries = []string{canaryGeneral, canaryProfile, canaryModel, canaryNumber}

// noCanary fails when text holds a canary.
func noCanary(t *testing.T, where, text string) {
	t.Helper()
	for _, c := range canaries {
		if strings.Contains(text, c) {
			t.Errorf("%s holds the value %s: %s", where, c, text)
		}
	}
}

// valueAnswers are the routes whose successful answer is the stored settings
// or profiles: they show environment values as stored. Every other answer,
// and every refusal of these routes, holds no setting value.
var valueAnswers = map[string]bool{
	"GET /api/settings":        true,
	"PUT /api/settings":        true,
	"PATCH /api/settings":      true,
	"GET /api/profiles":        true,
	"PUT /api/profiles/{name}": true,
	// The MCP servers as stored.
	"GET /api/mcp/servers":        true,
	"GET /api/mcp/servers/{name}": true,
	"PUT /api/mcp/servers/{name}": true,
}

// valueServer opens a server with its log in logOut and stores the canaries
// as environment values next to a command, a reference to another variable
// and a plain variable.
func valueServer(t *testing.T, logOut io.Writer) *Server {
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
	if err := s.settings.SetGeneralIf("", g); err != nil {
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
	if err := s.settings.PutProfileIf("", p); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.PutMCPServerIf("", api.MCPServer{Name: "claude", Transport: api.MCPTransportHTTP, URL: "https://mcp.example.invalid/",
		Headers: map[string]string{"Authorization": canaryProfile}, Env: map[string]string{"KEY": canaryModel}}); err != nil {
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

func TestEnvValuesAnsweredAsStored(t *testing.T) {
	s := valueServer(t, io.Discard)
	b := newBrowser(t, s)
	wantEnv := map[string]string{"ANTHROPIC_API_KEY": canaryGeneral, "FOO_TOKEN": "`pass x`", "REF_SECRET": "$OTHER", "PATH": "/usr/bin"}
	checkGeneral := func(where string, g api.General) {
		t.Helper()
		if !reflect.DeepEqual(g.AgentEnv, wantEnv) {
			t.Errorf("%s: agent_env = %v, want %v", where, g.AgentEnv, wantEnv)
		}
	}
	checkProfile := func(where string, p api.Profile) {
		t.Helper()
		if p.Env["ANTHROPIC_API_KEY"] != canaryProfile || p.Env["PLAIN"] != "visible" || p.Models["sonnet"].Env["OPENAI_API_KEY"] != canaryModel {
			t.Errorf("%s: profile %+v", where, p)
		}
	}

	got := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil))
	checkGeneral("GET /api/settings", got.Settings)

	// The answer sent back unchanged keeps every value.
	resp := withIfMatch(b, s, "PUT", "/api/settings", got.Settings, `"`+got.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got = body[api.SettingsResponse](t, resp)
	checkGeneral("PUT /api/settings", got.Settings)

	resp = asUser(b, s, "PATCH", "/api/settings", map[string]any{"agent_env": got.Settings.AgentEnv})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got = body[api.SettingsResponse](t, resp)
	checkGeneral("PATCH /api/settings", got.Settings)
	if file := readFile(t, s, "settings.yaml"); !strings.Contains(file, canaryGeneral) {
		t.Fatalf("settings.yaml lost a value:\n%s", file)
	}

	list := body[api.ProfilesResponse](t, asUser(b, s, "GET", "/api/profiles", nil))
	if len(list.Profiles) != 1 {
		t.Fatalf("profiles %+v", list)
	}
	checkProfile("GET /api/profiles", list.Profiles[0])
	p := list.Profiles[0]
	resp = withIfMatch(b, s, "PUT", "/api/profiles/claude", p, `"`+p.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	p = body[api.Profile](t, resp)
	checkProfile("PUT /api/profiles/claude", p)
	if file := readFile(t, s, "profiles/claude.yaml"); !strings.Contains(file, canaryProfile) || !strings.Contains(file, canaryModel) {
		t.Fatalf("profiles/claude.yaml:\n%s", file)
	}

	// A changed value is stored and answered as sent.
	p.Env["ANTHROPIC_API_KEY"] = "sk-replaced"
	resp = asUser(b, s, "PUT", "/api/profiles/claude", p)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if out := body[api.Profile](t, resp); out.Env["ANTHROPIC_API_KEY"] != "sk-replaced" || out.Models["sonnet"].Env["OPENAI_API_KEY"] != canaryModel {
		t.Fatalf("profile after change %+v", out)
	}
	if sp, _ := s.settings.Profile("claude"); sp.Env["ANTHROPIC_API_KEY"] != "sk-replaced" {
		t.Fatalf("stored profile %+v", sp)
	}
}

// TestNoValueInAnyRefusal walks the whole route table as the user, so a route
// added later is covered: no refusal holds a stored value or one sent in the
// request, also not when the request is refused for a value that cannot be
// parsed, and no answer outside valueAnswers holds a stored value.
func TestNoValueInAnyRefusal(t *testing.T) {
	var logs syncBuffer
	s := valueServer(t, &logs)
	b := newBrowser(t, s)
	bad := []string{
		// A value pasted into a field that cannot hold it: refusals never
		// quote the value back.
		`"` + canaryGeneral + `"`,
		`{"health":{"silent_after":"` + canaryGeneral + `"},"stop":{"handoff_timeout":"` + canaryProfile + `"}}`,
		`{"limits":{"tasks":"` + canaryGeneral + `"}}`,
		`{"prompt_delivery":{"delay":"` + canaryModel + `"},"cli":"custom","command":"x","models":{"m":{}}}`,
		`{"agent_env":{"BAD=NAME":"` + canaryGeneral + `"}}`,
		`{"text":1}`,
		`{"limits":{"tasks":` + canaryNumber + `}}`,
		`{"max_parallel":` + canaryNumber + `,"env":{"A":` + canaryNumber + `}}`,
		`{"env":{"A":"` + canaryGeneral + `"},"bogus":` + canaryNumber + `}`,
		`{"workspace_roots":[{"path":` + absJSON("/a") + `,"max_depth":"` + canaryModel + `"}]}`,
		// Template errors: Go words them with the piece of the template
		// they stopped at.
		`{"text":"{{ .Title }} {{ \"` + canaryGeneral + `\" \"` + canaryProfile + `\" }}"}`,
		`{"text":"{{ printf \"` + canaryModel + `\" .Nope }}"}`,
		`{"text":"{{ \"` + canaryGeneral + `\" | nope }}"}`,
	}
	// Every route, with a name that exists for each kind of path value
	// (profile and project "claude", prompt and role "developer").
	for _, name := range []string{"claude", "developer"} {
		for _, row := range s.routeTable {
			method, path, _ := strings.Cut(row.pattern, " ")
			path = wildcard.ReplaceAllString(path, name)
			if method == http.MethodGet {
				resp := asUser(b, s, method, path, nil)
				raw := readBody(t, resp)
				if resp.StatusCode >= 400 || !valueAnswers[row.pattern] {
					noCanary(t, row.pattern, raw)
				}
				continue
			}
			for _, body := range bad {
				resp := rawDo(t, b, s, method, path, body)
				raw := readBody(t, resp)
				if resp.StatusCode < 400 && valueAnswers[row.pattern] {
					t.Errorf("%s %s: accepted with %d", row.pattern, body, resp.StatusCode)
					continue
				}
				noCanary(t, row.pattern+" "+body, raw)
			}
		}
	}
	// The values are still stored.
	if s.settings.General().AgentEnv["ANTHROPIC_API_KEY"] != canaryGeneral {
		t.Fatal("the stored value was lost")
	}
	s.waitSettingsEvents(context.Background())
	noCanary(t, "the server log", logs.String())
}

// TestValuesNotInEventsOrLog writes values through the API, is refused for
// others and loads broken hand edits holding them: no event, as stored or as
// streamed, no file error and no log line holds a value.
func TestValuesNotInEventsOrLog(t *testing.T) {
	var logs syncBuffer
	s := valueServer(t, &logs)
	b := newBrowser(t, s)
	start := lastEventID(t, s)
	st := openStream(t, b, s, "?since="+strconv.FormatInt(start, 10), nil)
	defer st.close()

	// Writes through the API that carry the values.
	wantStatus(t, asUser(b, s, "PATCH", "/api/settings", map[string]any{"agent_env": map[string]string{"ANTHROPIC_API_KEY": canaryGeneral, "B_SECRET": canaryModel}}).StatusCode, http.StatusOK)
	wantStatus(t, asUser(b, s, "PUT", "/api/profiles/other", api.Profile{CLI: "custom", Command: "x", Env: map[string]string{"KEY": canaryProfile}, Models: map[string]api.ProfileModel{"m": {}}}).StatusCode, http.StatusOK)
	// Refused writes that carry them.
	rawDo(t, b, s, "PATCH", "/api/settings", `{"health":{"silent_after":"`+canaryGeneral+`"}}`)
	rawDo(t, b, s, "PATCH", "/api/settings", `{"limits":{"tasks":0},"agent_env":{"ANTHROPIC_API_KEY":"`+canaryGeneral+`"}}`)
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
		// The last good settings stay in effect and are answered as
		// stored; the reports of the broken files quote nothing.
		raw, _ := json.Marshal(got.Errors)
		noCanary(t, "the file errors of GET /api/settings", string(raw))
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
