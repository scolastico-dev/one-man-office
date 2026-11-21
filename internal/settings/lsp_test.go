package settings

import (
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

const lspJSON = `{"servers":[{"name":"go","enabled":true,"command":"gopls","args":["serve"],"env":{"MODE":"${LITERAL}"},"files":[{"pattern":"*.go","language_id":"go"},{"pattern":"go.mod","language_id":"go.mod"}],"initialization_options":{"nested":{"list":[true,null,"x"]}},"configuration":{"gopls":{"staticcheck":true}}}],"debounce":"750ms","force_chat":["codex","custom"]}`

func lspGeneral(t *testing.T) General {
	t.Helper()
	g := Defaults()
	if err := json.Unmarshal([]byte(`{"lsp":`+lspJSON+`}`), &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func lspWire(t *testing.T, g General) string {
	t.Helper()
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return string(fields["lsp"])
}

func TestLSPDefaultsAndRoundTrip(t *testing.T) {
	s, home, _ := openTest(t)
	wantDefault := `{"servers":[],"debounce":"500ms","force_chat":[]}`
	if got := lspWire(t, s.General()); got != wantDefault {
		t.Fatalf("default LSP = %s, want %s", got, wantDefault)
	}
	// Older files without the group retain safe, empty registrations.
	writeFile(t, filepath.Join(home, "settings.yaml"), "limits:\n  tasks: 5\n")
	s.Reload()
	if got := lspWire(t, s.General()); got != wantDefault {
		t.Fatalf("old file LSP = %s", got)
	}
	g := lspGeneral(t)
	if err := s.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(home, Options{})
	if err != nil || len(reopened.Errors()) != 0 {
		t.Fatalf("reopen: %v, errors %v", err, reopened.Errors())
	}
	var want, got any
	json.Unmarshal([]byte(lspJSON), &want)
	json.Unmarshal([]byte(lspWire(t, reopened.General())), &got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LSP round trip = %#v, want %#v", got, want)
	}
	if got := lspWire(t, LoadGeneral(filepath.Join(home, "settings.yaml"))); got != lspWire(t, reopened.General()) {
		t.Fatalf("LoadGeneral LSP = %s", got)
	}
}

func TestLSPValidation(t *testing.T) {
	// Each mutation represents a malformed registration that must never reach a runtime.
	cases := []struct{ field, patch string }{
		{"lsp.debounce", `{"debounce":"-1ms"}`},
		{"lsp.debounce", `{"debounce":"61s"}`},
		{"lsp.force_chat.0", `{"force_chat":["unknown"]}`},
		{"lsp.force_chat.1", `{"force_chat":["codex","codex"]}`},
		{"lsp.servers.0.name", `{"servers":[{"name":"Bad name"}]}`},
		{"lsp.servers.1.name", `{"servers":[{"name":"go","command":"gopls","files":[{"pattern":"*.go","language_id":"go"}]},{"name":"go"}]}`},
		{"lsp.servers.0.command", `{"servers":[{"name":"go","command":" "}]}`},
		{"lsp.servers.0.command", `{"servers":[{"name":"go","command":"gopls\u0000"}]}`},
		{"lsp.servers.0.args.0", `{"servers":[{"name":"go","command":"gopls","args":["\u0000"]}]}`},
		{"lsp.servers.0.env", `{"servers":[{"name":"go","command":"gopls","env":{"BAD=KEY":"secret"}}]}`},
		{"lsp.servers.0.env.MODE", `{"servers":[{"name":"go","command":"gopls","env":{"MODE":"secret\u0000"}}]}`},
		{"lsp.servers.0.files", `{"servers":[{"name":"go","command":"gopls"}]}`},
		{"lsp.servers.0.files.0.language_id", `{"servers":[{"name":"go","command":"gopls","files":[{"pattern":"*.go","language_id":" "}]}]}`},
	}
	for _, pattern := range []string{"", "[", "**/*.go", "/abs.go", "../*.go", "src/../*.go", "src//*.go", "./*.go", "src/", `src\*.go`, "C:/*.go"} {
		p, _ := json.Marshal(pattern)
		cases = append(cases, struct{ field, patch string }{"lsp.servers.0.files.0.pattern", `{"servers":[{"name":"go","command":"gopls","files":[{"pattern":` + string(p) + `,"language_id":"go"}]}]}`})
	}
	for _, tc := range cases {
		t.Run(tc.patch, func(t *testing.T) {
			g := lspGeneral(t)
			// Lists replace whole registrations; encoding/json otherwise reuses
			// an existing element and preserves fields absent from the input.
			var patch map[string]json.RawMessage
			json.Unmarshal([]byte(tc.patch), &patch)
			if _, ok := patch["servers"]; ok {
				g.LSP.Servers = nil
			}
			// Replace only named group members, as a draft editor would.
			if err := json.Unmarshal([]byte(`{"lsp":`+tc.patch+`}`), &g); err != nil {
				t.Fatal(err)
			}
			var fe *FieldError
			if err := g.Validate(); !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("Validate = %v, want field %s", err, tc.field)
			} else if strings.Contains(err.Error(), "secret") {
				t.Fatalf("validation exposed environment value: %v", err)
			}
		})
	}
}

func TestLSPHandEditsAndImportValidation(t *testing.T) {
	s, home, log := openTest(t)
	path := filepath.Join(home, "settings.yaml")
	valid := "lsp:\n  servers:\n    - name: go\n      enabled: true\n      command: gopls\n      files:\n        - pattern: '*.go'\n          language_id: go\n      configuration: {nested: {list: [true, null]}}\n"
	writeFile(t, path, valid)
	s.Reload()
	if len(s.Errors()) != 0 {
		t.Fatal(s.Errors())
	}
	before := lspWire(t, s.General())
	if !strings.Contains(before, `"name":"go"`) {
		t.Fatalf("registration missing: %s", before)
	}
	changes := log.take()
	if len(changes) != 1 || !changes[0].FromDisk || changes[0].Err != nil {
		t.Fatalf("valid edit changes = %+v", changes)
	}
	for _, bad := range []string{
		strings.Replace(valid, "command: gopls", "command: ''", 1),
		valid + "  cwd: /elsewhere\n",
		strings.Replace(valid, "language_id: go", "unknown: go", 1),
		strings.Replace(valid, "{nested: {list: [true, null]}}", "{date: 2026-01-01}", 1),
		strings.Replace(valid, "{nested: {list: [true, null]}}", "{number: .nan}", 1),
		strings.Replace(valid, "{nested: {list: [true, null]}}", "{nested: {1: value}}", 1),
	} {
		if errs := s.CheckFiles(t.TempDir(), map[string][]byte{"settings.yaml": []byte(bad)}); len(errs) != 1 || errs[0].File != "settings.yaml" {
			t.Fatalf("import check = %+v for %s", errs, bad)
		}
		writeFile(t, path, bad)
		s.Reload()
		if len(s.Errors()) != 1 || lspWire(t, s.General()) != before {
			t.Fatalf("invalid edit replaced last valid state: %+v", s.Errors())
		}
		changes := log.take()
		if len(changes) != 1 || !changes[0].FromDisk || changes[0].Err == nil {
			t.Fatalf("invalid edit changes = %+v", changes)
		}
		// Import checks intentionally ignore already-broken current files;
		// start each next import check from a valid on-disk file.
		writeFile(t, path, valid)
		s.Reload()
		log.take()
	}
	writeFile(t, path, valid)
	s.Reload()
	if len(s.Errors()) != 0 || lspWire(t, s.General()) != before {
		t.Fatalf("recovery failed: %+v", s.Errors())
	}
	if errs := s.CheckFiles(t.TempDir(), map[string][]byte{"settings.yaml": []byte(valid)}); len(errs) != 0 {
		t.Fatalf("valid import check: %+v", errs)
	}
}

// Invalid JSON-only values must be refused on programmatic writes too.
func TestLSPRejectsInvalidConfiguration(t *testing.T) {
	for _, field := range []string{"initialization_options", "configuration"} {
		for _, value := range []any{math.NaN(), make(chan int), map[any]any{1: "x"}, strings.Repeat("x", 64<<10)} {
			g := lspGeneral(t)
			config := map[string]any{"value": value}
			if field == "configuration" {
				g.LSP.Servers[0].Configuration = config
			} else {
				g.LSP.Servers[0].InitializationOptions = config
			}
			var fe *FieldError
			if err := g.Validate(); !errors.As(err, &fe) || fe.Field != "lsp.servers.0."+field {
				t.Fatalf("%s: %v, want field error", field, err)
			}
		}
	}
}

func TestLSPCopyIsolation(t *testing.T) {
	s, _, _ := openTest(t)
	g := lspGeneral(t)
	want := lspWire(t, g)
	mutate := func(g *General) {
		g.LSP.ForceChat[0] = api.CLIClaude
		m := &g.LSP.Servers[0]
		m.Args[0] = "changed"
		m.Env["MODE"] = "changed"
		m.Files[0].LanguageID = "changed"
		m.InitializationOptions["nested"].(map[string]any)["list"].([]any)[0] = false
		m.Configuration["gopls"].(map[string]any)["staticcheck"] = false
		m.Name = "changed"
	}
	if err := s.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	mutate(&g)
	read := s.General()
	mutate(&read)
	if got := lspWire(t, s.General()); got != want {
		t.Fatalf("write input or returned copy mutated store: %s", got)
	}
	updated, err := s.UpdateGeneral("", func(g *General) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	mutate(&updated)
	stop := errors.New("abort edit")
	if _, err := s.UpdateGeneral("", func(g *General) error { mutate(g); return stop }); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if got := lspWire(t, s.General()); got != want {
		t.Fatalf("UpdateGeneral copy mutated store: %s", got)
	}
}

func TestLSPBoundsAndEmptyNormalization(t *testing.T) {
	g := lspGeneral(t)
	for _, debounce := range []api.Duration{0, api.Duration(60_000_000_000)} {
		g.LSP.Debounce = debounce
		if err := g.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, pattern := range []string{"*.go", "src/*.go", "file?.[ch]", ".config", "go.mod"} {
		g.LSP.Servers[0].Files[0].Pattern = pattern
		if err := g.Validate(); err != nil {
			t.Fatalf("valid pattern %s: %v", pattern, err)
		}
	}
	for _, tc := range []struct {
		field  string
		mutate func(*General)
	}{
		{"lsp.servers", func(g *General) { g.LSP.Servers = make([]api.LSPServer, 33) }},
		{"lsp.servers.0.files", func(g *General) { g.LSP.Servers[0].Files = make([]api.LSPFileSelector, 101) }},
		{"lsp.servers.0.args", func(g *General) { g.LSP.Servers[0].Args = make([]string, 101) }},
		{"lsp.servers.0.args.0", func(g *General) { g.LSP.Servers[0].Args = []string{strings.Repeat("a", 4097)} }},
		{"lsp.servers.0.command", func(g *General) { g.LSP.Servers[0].Command = strings.Repeat("a", 4097) }},
		{"lsp.servers.0.env", func(g *General) {
			for i := range 201 {
				g.LSP.Servers[0].Env[strings.Repeat("a", i+1)] = ""
			}
		}},
		{"lsp.servers.0.env.MODE", func(g *General) { g.LSP.Servers[0].Env["MODE"] = strings.Repeat("x", (32<<10)+1) }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			g := lspGeneral(t)
			tc.mutate(&g)
			var fe *FieldError
			if err := g.Validate(); !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("got %v, want %s", err, tc.field)
			}
		})
	}
	s, _, _ := openTest(t)
	g = Defaults()
	g.LSP.Servers = []api.LSPServer{{Name: "go", Command: "gopls", Files: []api.LSPFileSelector{{Pattern: "*.go", LanguageID: "go"}}}}
	if err := s.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	m := s.General().LSP.Servers[0]
	if m.Enabled || m.Args == nil || m.Env == nil || m.InitializationOptions == nil || m.Configuration == nil {
		t.Fatalf("omitted values not normalized: %+v", m)
	}
}
