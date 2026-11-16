package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func pushover() PluginSettings {
	return PluginSettings{
		Name:    "pushover",
		Source:  "https://github.com/scolastico-dev/one-man-office.git",
		Subpath: "plugins/pushover",
		Ref:     "release",
		Enabled: true,
		Config:  map[string]any{"user": "u", "priority": 1, "nested": map[string]any{"list": []any{"a", 2}}},
	}
}

func TestPluginSettingsValidation(t *testing.T) {
	s, home, _ := openTest(t)
	cases := map[string]func(*PluginSettings){
		"name":      func(p *PluginSettings) { p.Name = ".cache" },
		"name ":     func(p *PluginSettings) { p.Name = ".data" },
		"name  ":    func(p *PluginSettings) { p.Name = "a/b" },
		"source":    func(p *PluginSettings) { p.Source = "" },
		"source ":   func(p *PluginSettings) { p.Source = "fd::17" },
		"source  ":  func(p *PluginSettings) { p.Source = "http://example.com/p.git" },
		"source   ": func(p *PluginSettings) { p.Source = "file:///srv/plugin" },
		"subpath":   func(p *PluginSettings) { p.Subpath = "../x" },
		"subpath ":  func(p *PluginSettings) { p.Subpath = "/abs" },
		"subpath  ": func(p *PluginSettings) { p.Subpath = "a/./b" },
		"subpath   ": func(p *PluginSettings) {
			p.Subpath = "a/b/"
		},
		"subpath    ": func(p *PluginSettings) { p.Subpath = `a\..\b` },
		"ref":         func(p *PluginSettings) { p.Ref = "-x" },
		"config":      func(p *PluginSettings) { p.Config = map[string]any{"big": strings.Repeat("x", 64<<10)} },
		"config ":     func(p *PluginSettings) { p.Config = map[string]any{"f": func() {}} },
	}
	for name, mutate := range cases {
		p := pushover()
		mutate(&p)
		err := s.PutPluginIf("", p)
		wantCode(t, err, api.CodeValidation)
		if got := fieldOf(err); got != strings.TrimSpace(name) {
			t.Errorf("%q: field %q, err %v", name, got, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "plugins")); len(entries) != 0 {
		t.Fatalf("refused plugins were written: %v", entries)
	}
	// Accepted: git addresses, a clean subpath, upper case names.
	for _, p := range []PluginSettings{
		pushover(),
		{Name: "My.Plugin", Source: "https://example.com/p.git", Subpath: "plugins/mine", Ref: "v1.2"},
		{Name: "local", Source: "/srv/plugins/local"},
	} {
		if err := s.PutPluginIf(NoRevision, p); err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
	}
	got, ok := s.Plugin("pushover")
	if !ok || got.Revision == NoRevision || got.Revision != s.Revision(SectionPlugins, "pushover") {
		t.Fatalf("plugin = %+v", got)
	}
	// The configuration reads back as JSON-like values.
	if got.Config["user"] != "u" || got.Config["priority"] != 1 {
		t.Fatalf("config = %#v", got.Config)
	}
	// The store's copy is not shared with callers.
	got.Config["nested"].(map[string]any)["list"].([]any)[0] = "changed"
	if again, _ := s.Plugin("pushover"); again.Config["nested"].(map[string]any)["list"].([]any)[0] != "a" {
		t.Fatal("caller changed the store")
	}
	if p, _ := s.Plugin("local"); p.Config == nil {
		t.Fatal("nil config")
	}
	if list := s.Plugins(); len(list) != 3 || list[0].Name != "My.Plugin" {
		t.Fatalf("plugins = %+v", list)
	}
	wantCode(t, s.PutPluginIf(NoRevision, pushover()), api.CodeStaleSettings)

	// A file whose config is not an object is broken.
	writeFile(t, filepath.Join(home, "plugins", "list.yaml"), "source: /srv/plugins/pushover\nconfig: [1, 2]\n")
	writeFile(t, filepath.Join(home, "plugins", "text.yaml"), "source: /srv/plugins/pushover\nconfig: hello\n")
	writeFile(t, filepath.Join(home, "plugins", "keys.yaml"), "source: /srv/plugins/pushover\nconfig:\n  ? [a]\n  : x\n")
	s.Reload()
	errs := s.Errors()
	if len(errs) != 3 {
		t.Fatalf("errors = %+v", errs)
	}
	for _, name := range []string{"list", "text", "keys"} {
		if _, ok := s.Plugin(name); ok {
			t.Errorf("%s loaded", name)
		}
	}
}

func TestPluginFolderSkipsTrees(t *testing.T) {
	s, home, log := openTest(t)
	if err := s.PutPluginIf("", pushover()); err != nil {
		t.Fatal(err)
	}
	log.take()
	dir := filepath.Join(home, "plugins")
	writeFile(t, filepath.Join(dir, "pushover", "plugin.json"), `{"name":"pushover"}`)
	writeFile(t, filepath.Join(dir, "pushover", "hook.yaml"), "not: [settings")
	writeFile(t, filepath.Join(dir, ".cache", "x", "plugin.json"), "{}")
	writeFile(t, filepath.Join(dir, ".data", "pushover", "state.yaml"), "x: [")
	// A plugin may be named like a file: its tree ends in .yaml.
	writeFile(t, filepath.Join(dir, "odd.yaml", "plugin.json"), "{}")
	if ch := s.Reload(); len(ch) != 0 {
		t.Fatalf("changes = %+v", ch)
	}
	if errs := s.Errors(); len(errs) != 0 {
		t.Fatalf("errors = %+v", errs)
	}
	if list := s.Plugins(); len(list) != 1 || list[0].Name != "pushover" {
		t.Fatalf("plugins = %+v", list)
	}
	// Other sections still report a folder named like an item file.
	if err := os.Mkdir(filepath.Join(home, "skillsets", "x.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Reload()
	if errs := s.Errors(); len(errs) != 1 || !strings.HasSuffix(errs[0].File, "x.yaml") {
		t.Fatalf("errors = %+v", errs)
	}
	// Deleting removes only the settings file.
	if err := s.DeletePluginIf("", "pushover"); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(dir, "pushover", "plugin.json")) || fileExists(filepath.Join(dir, "pushover.yaml")) {
		t.Fatal("delete touched the tree or kept the file")
	}
	wantCode(t, s.DeletePluginIf("", "pushover"), api.CodeNotFound)
	wantCode(t, s.DeletePluginIf("", ".cache"), api.CodeNotFound)
	if got := s.Revision(SectionPlugins, ".data"); got != NoRevision {
		t.Fatalf("revision of .data = %q", got)
	}
}

func TestPluginConfigRoundTrips(t *testing.T) {
	s, home, _ := openTest(t)
	p := pushover()
	if err := s.PutPluginIf("", p); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(home, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Plugin("pushover")
	if !ok {
		t.Fatalf("not loaded: %+v", s2.Errors())
	}
	got.Revision = ""
	if !reflect.DeepEqual(got, p) {
		t.Fatalf("reloaded = %#v, want %#v", got, p)
	}
}

func TestPluginConfigHoldsOnlyJSONValues(t *testing.T) {
	s, home, _ := openTest(t)
	for name, text := range map[string]string{
		"date":  "config:\n  when: 2024-01-01\n",
		"inf":   "config:\n  n: .inf\n",
		"int":   "config:\n  a:\n    1: x\n",
		"deep":  "config:\n  a: " + strings.Repeat("[", 70) + strings.Repeat("]", 70) + "\n",
		"float": "config:\n  n: 1.5\n  ok: true\n  none: null\n  when: \"2024-01-01\"\n",
	} {
		writeFile(t, filepath.Join(home, "plugins", name+".yaml"), "source: /srv/plugins/pushover\n"+text)
	}
	s.Reload()
	for _, name := range []string{"date", "inf", "int", "deep"} {
		if msg := errorOf(s, filepath.Join(home, "plugins", name+".yaml")); !strings.Contains(msg, "config") {
			t.Errorf("%s: error %q", name, msg)
		}
	}
	if _, ok := s.Plugin("float"); !ok {
		t.Fatalf("plain values refused: %+v", s.Errors())
	}
}
