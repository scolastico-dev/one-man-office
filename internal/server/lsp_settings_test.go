package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

func TestLSPSettingsAPI(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	start := lastEventID(t, s)
	initial := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil))
	if initial.Settings.LSP.Servers == nil || len(initial.Settings.LSP.Servers) != 0 || initial.Settings.LSP.ForceChat == nil || initial.Settings.LSP.Debounce != api.Duration(500*time.Millisecond) {
		t.Fatalf("default LSP = %+v", initial.Settings.LSP)
	}
	registration := api.LSPServer{
		Name: "go", Enabled: true, Command: "gopls", Args: []string{"serve"}, Env: map[string]string{"MODE": "${literal}"},
		Files:                 []api.LSPFileSelector{{Pattern: "*.go", LanguageID: "go"}},
		InitializationOptions: map[string]any{"nested": map[string]any{"list": []any{true, nil, "value"}}},
		Configuration:         map[string]any{"gopls": map[string]any{"staticcheck": true}},
	}
	want := api.LSPSettings{Servers: []api.LSPServer{registration}, Debounce: api.Duration(time.Second), ForceChat: []string{api.CLICodex}}
	put := initial.Settings
	put.LSP = want
	resp := withIfMatch(b, s, "PUT", "/api/settings", put, `"`+initial.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	saved := body[api.SettingsResponse](t, resp)
	if !reflect.DeepEqual(saved.Settings.LSP, want) || saved.Revision == initial.Revision {
		t.Fatalf("put = %+v", saved)
	}
	// Sibling keys inside the group survive partial updates.
	resp = withIfMatch(b, s, "PATCH", "/api/settings", map[string]any{"lsp": map[string]any{"debounce": "0s"}}, `"`+saved.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	saved = body[api.SettingsResponse](t, resp)
	want.Debounce = 0
	if !reflect.DeepEqual(saved.Settings.LSP, want) || saved.Settings.Limits != initial.Settings.Limits {
		t.Fatalf("partial patch = %+v", saved.Settings)
	}
	// A server list replaces whole registrations, including their opaque JSON
	// objects; nested null is a legitimate server option on both PUT and PATCH.
	registration.Configuration = map[string]any{"reset": nil}
	resp = withIfMatch(b, s, "PATCH", "/api/settings", map[string]any{"lsp": map[string]any{"servers": []api.LSPServer{registration}, "force_chat": []string{}}}, `"`+saved.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	saved = body[api.SettingsResponse](t, resp)
	want.Servers[0] = registration
	want.ForceChat = []string{}
	if !reflect.DeepEqual(saved.Settings.LSP, want) {
		t.Fatalf("list/object replacement = %+v", saved.Settings.LSP)
	}
	get := body[api.SettingsResponse](t, asUser(b, s, "GET", "/api/settings", nil))
	if !reflect.DeepEqual(get.Settings.LSP, want) || get.Revision != saved.Revision {
		t.Fatalf("get = %+v", get)
	}
	reopened, err := settings.Open(s.opts.Layout.Root, settings.Options{})
	if err != nil || len(reopened.Errors()) != 0 || !reflect.DeepEqual(reopened.General().LSP, want) {
		t.Fatalf("YAML state differs: %v", err)
	}
	for _, raw := range wantEvents(t, s, api.KindSettingsChanged, start, 3) {
		var e api.SettingsChangedData
		if err := json.Unmarshal(raw, &e); err != nil || e.Section != settings.SectionGeneral || e.Source != api.SourceApp {
			t.Fatalf("event = %s (%v)", raw, err)
		}
	}
	resp = withIfMatch(b, s, "PATCH", "/api/settings", map[string]any{"lsp": map[string]any{"servers": []any{}}}, `"`+initial.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if got := body[errorEnvelope](t, resp).Error.Code; got != api.CodeStaleSettings {
		t.Fatalf("stale patch code = %s", got)
	}
	// A hand edit participates in revision checks even before the poll runs.
	path := filepath.Join(s.opts.Layout.Root, "settings.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), "debounce: 0s", "debounce: 1s", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	resp = withIfMatch(b, s, "PUT", "/api/settings", saved.Settings, `"`+saved.Revision+`"`)
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	resp.Body.Close()
	changes := wantEvents(t, s, api.KindSettingsChanged, start, 4)
	var e api.SettingsChangedData
	json.Unmarshal(changes[3], &e)
	if e.Source != api.SourceDisk || s.settings.General().LSP.Debounce != api.Duration(time.Second) {
		t.Fatalf("hand edit not loaded/announced: %s", changes[3])
	}
}

func TestLSPSettingsAPIRefusals(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	start := lastEventID(t, s)
	before := s.settings.General().LSP
	for _, tc := range []struct {
		raw, field string
		status     int
	}{
		{`{"lsp":{"debounce":"-1s"}}`, "lsp.debounce", 422},
		{`{"lsp":{"debounce":"not-a-duration"}}`, "lsp.debounce", 400},
		{`{"lsp":{"force_chat":["other"]}}`, "lsp.force_chat.0", 422},
		{`{"lsp":{"force_chat":null}}`, "lsp.force_chat", 400},
		{`{"lsp":{"servers":null}}`, "lsp.servers", 400},
		{`{"lsp":{"servers":[null]}}`, "lsp.servers.0", 400},
		{`{"lsp":{"servers":[{"name":"go"}]}}`, "lsp.servers.0.command", 422},
		{`{"lsp":{"servers":[{"name":"go","command":"gopls","files":[{"pattern":"*.go","language_id":4}]}]}}`, "lsp.servers.0.files.0.language_id", 400},
		{`{"lsp":{"servers":[{"initialization_options":null}]}}`, "lsp.servers.0.initialization_options", 400},
		{`{"lsp":{"servers":[{"configuration":null}]}}`, "lsp.servers.0.configuration", 400},
		{`{"lsp":{"servers":[{"configuration":[]}]}}`, "lsp.servers.0.configuration", 400},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			resp := rawDo(t, b, s, "PATCH", "/api/settings", tc.raw)
			wantStatus(t, resp.StatusCode, tc.status)
			e := body[errorEnvelope](t, resp).Error
			if e.Details.Field != tc.field {
				t.Fatalf("field = %s, want %s (%s)", e.Details.Field, tc.field, e.Message)
			}
		})
	}
	for _, method := range []string{"PUT", "PATCH"} {
		for _, raw := range []string{
			`{"lsp":{"cwd":"/other"}}`,
			`{"lsp":{"servers":[{"cwd":"/other"}]}}`,
			`{"lsp":{"servers":[{"files":[{"typo":"*.go"}]}]}}`,
		} {
			resp := rawDo(t, b, s, method, "/api/settings", raw)
			wantStatus(t, resp.StatusCode, http.StatusBadRequest)
			resp.Body.Close()
		}
	}
	if !reflect.DeepEqual(s.settings.General().LSP, before) {
		t.Fatal("invalid write changed the active configuration")
	}
	wantEvents(t, s, api.KindSettingsChanged, start, 0)
}
