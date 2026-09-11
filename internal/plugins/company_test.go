package plugins

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCompanyHookNamesAreAccepted(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, Dir, "dashboard")
	writePlugin(t, dir, Manifest{Name: "dashboard", Hooks: []Hook{
		{Event: "company_startup", Lua: "startup.lua"},
		{Event: "company_load", Javascript: "main.js"},
	}}, "")
	for _, name := range []string{"startup.lua", "main.js"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(office, database); err != nil {
		t.Fatalf("company hook names rejected: %v", err)
	}
}

func TestCompanyHooksRunAndExposeOnlyDeclaredFiles(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, Dir, "dashboard")
	writePlugin(t, dir, Manifest{Name: "dashboard", Hooks: []Hook{
		{Event: EventCompanyStartup, Lua: "startup.lua"},
		{Event: EventCompanyLoad, Javascript: "web/main.js", Files: []string{"web/theme.css"}},
	}}, "")
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"startup.lua":   `omo.local_set("started", true)`,
		"web/main.js":   `window.extensionLoaded = true;`,
		"web/theme.css": `.extension { color: blue }`,
		"secret.txt":    `not public`,
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Emit(context.Background(), Event{Name: EventCompanyStartup}); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "local", "dashboard", "started", "true")
	extensions := manager.CompanyExtensions()
	if len(extensions) != 1 || extensions[0].Plugin != "dashboard" || extensions[0].Javascript != "web/main.js" || len(extensions[0].Files) != 2 {
		t.Fatalf("extensions = %+v", extensions)
	}
	if path, ok := manager.CompanyFile("dashboard", "web/theme.css"); !ok || filepath.Base(path) != "theme.css" {
		t.Fatalf("declared file = %q, %v", path, ok)
	}
	if _, ok := manager.CompanyFile("dashboard", "secret.txt"); ok {
		t.Fatal("undeclared plugin file was exposed")
	}
}

func TestCompanyExtensionsSnapshotResolvedConfigs(t *testing.T) {
	office, database := newPluginOffice(t)
	sharedNested := map[string]any{"mode": "careful"}
	settings := map[string]Settings{
		"alpha": {Enabled: true, Config: map[string]any{
			"nested": sharedNested,
			"list":   []any{map[string]any{"value": "alpha"}},
		}},
		"beta": {Enabled: true, Config: map[string]any{
			"nested": sharedNested,
			"list":   []any{map[string]any{"value": "beta"}},
		}},
	}
	for name := range settings {
		dir := filepath.Join(office, Dir, name)
		writePlugin(t, dir, Manifest{Name: name, Hooks: []Hook{{Event: EventCompanyLoad, Javascript: "main.js"}}}, "")
		if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := LoadConfigured(office, database, settings)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	got := manager.CompanyExtensions()
	if len(got) != 2 || got[0].Plugin != "alpha" || got[1].Plugin != "beta" {
		t.Fatalf("extensions = %+v", got)
	}
	got[0].Config["nested"].(map[string]any)["mode"] = "mutated"
	gotList := got[0].Config["list"].([]any)
	gotList[0] = "mutated"

	if sharedNested["mode"] != "careful" || settings["alpha"].Config["list"].([]any)[0].(map[string]any)["value"] != "alpha" {
		t.Fatalf("manager config was mutated: %#v", settings)
	}
	if got[1].Config["nested"].(map[string]any)["mode"] != "careful" || got[1].Config["list"].([]any)[0].(map[string]any)["value"] != "beta" {
		t.Fatalf("plugin configs alias each other: %#v", got)
	}
}

func TestCompanyExtensionsFollowDependencyOrder(t *testing.T) {
	office, database := newPluginOffice(t)
	for _, plugin := range []struct {
		dir      string
		manifest Manifest
	}{
		{dir: "a-dependent", manifest: Manifest{Name: "dependent", Requires: []Dependency{{Name: "base", Source: "https://example.test/base", Version: "^1.2.0"}}, Hooks: []Hook{{Event: EventCompanyLoad, Javascript: "dependent.js"}}}},
		{dir: "z-base", manifest: Manifest{Name: "base", Version: "1.2.3", Hooks: []Hook{
			{Event: EventCompanyLoad, Javascript: "base-first.js"},
			{Event: EventCompanyLoad, Javascript: "base-second.js"},
		}}},
	} {
		dir := filepath.Join(office, Dir, plugin.dir)
		writePlugin(t, dir, plugin.manifest, "")
		for _, hook := range plugin.manifest.Hooks {
			if err := os.WriteFile(filepath.Join(dir, hook.Javascript), []byte(""), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	extensions := manager.CompanyExtensions()
	if got, want := manager.Ordered(), []string{"z-base", "a-dependent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("manager order = %v, want %v", got, want)
	}
	if len(extensions) != 3 || extensions[0].Plugin != "base" || extensions[0].Javascript != "base-first.js" || extensions[1].Plugin != "base" || extensions[1].Javascript != "base-second.js" || extensions[2].Plugin != "dependent" {
		t.Fatalf("extensions = %+v, want base hooks then dependent", extensions)
	}
}

func TestCompanyExtensionsFollowManagerOrderWhenHooksAreUnsorted(t *testing.T) {
	manager := &Manager{
		ordered: []string{"base", "dependent"},
		hooks: []loadedHook{
			{plugin: "dependent", hook: Hook{Event: EventCompanyLoad, Javascript: "dependent.js"}},
			{plugin: "base", hook: Hook{Event: EventCompanyLoad, Javascript: "base.js"}},
		},
	}

	got := manager.CompanyExtensions()
	if len(got) != 2 || got[0].Plugin != "base" || got[1].Plugin != "dependent" {
		t.Fatalf("extensions = %+v, want base then dependent", got)
	}
}

func TestCompanyLoadManifestRejectsUnsafeOrExecutableHooks(t *testing.T) {
	for name, hook := range map[string]Hook{
		"traversal":           {Event: EventCompanyLoad, Javascript: "../escape.js"},
		"command":             {Event: EventCompanyLoad, Javascript: "main.js", Command: []string{"echo"}},
		"missing-js":          {Event: EventCompanyLoad, Files: []string{"main.js"}},
		"old-prefix":          {Event: "on_company_load", Javascript: "main.js"},
		"web-on-office-event": {Event: EventAgentStart, Lua: "hook.lua", Javascript: "main.js"},
	} {
		t.Run(name, func(t *testing.T) {
			office, database := newPluginOffice(t)
			dir := filepath.Join(office, Dir, name)
			writePlugin(t, dir, Manifest{Name: name, Hooks: []Hook{hook}}, "")
			for _, path := range []string{"main.js", "hook.lua"} {
				if err := os.WriteFile(filepath.Join(dir, path), []byte(""), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Load(office, database); err == nil || !strings.Contains(err.Error(), "plugin "+name) {
				t.Fatalf("load error = %v", err)
			}
		})
	}
}
