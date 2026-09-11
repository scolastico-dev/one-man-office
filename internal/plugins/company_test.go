package plugins

import (
	"context"
	"os"
	"path/filepath"
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
		{dir: "app", manifest: Manifest{Name: "app", Requires: []Dependency{{Name: "base", Source: "https://example.test/base"}}, Hooks: []Hook{{Event: EventCompanyLoad, Javascript: "app.js"}}}},
		{dir: "base", manifest: Manifest{Name: "base", Hooks: []Hook{{Event: EventCompanyLoad, Javascript: "base.js"}}}},
	} {
		dir := filepath.Join(office, Dir, plugin.dir)
		writePlugin(t, dir, plugin.manifest, "")
		if err := os.WriteFile(filepath.Join(dir, plugin.manifest.Hooks[0].Javascript), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	extensions := manager.CompanyExtensions()
	if len(extensions) != 2 || extensions[0].Plugin != "base" || extensions[1].Plugin != "app" {
		t.Fatalf("extensions = %+v, want base then app", extensions)
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

func TestCompanyFileSupportsExactDirectoriesAndGlobPatterns(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, Dir, "dashboard")
	writePlugin(t, dir, Manifest{Name: "dashboard", Hooks: []Hook{{
		Event: EventCompanyLoad, Javascript: "main.js", Files: []string{
			"web", "styles/", "scripts/*.js", "scripts/?pp.js", "styles/[d-z]*.css", "deep/**/*.txt",
		},
	}}}, "")
	for path, content := range map[string]string{
		"main.js": "main", "web/index.html": "index", "web/nested/page.html": "page",
		"styles/site.css": "site", "styles/dark.css": "dark", "scripts/app.js": "app",
		"deep/a/b.txt": "nested", "deep/root.txt": "root",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, filepath.FromSlash(path))), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for _, path := range []string{"main.js", "web/index.html", "web/nested/page.html", "styles/site.css", "styles/dark.css", "scripts/app.js", "deep/a/b.txt", "deep/root.txt"} {
		if got, ok := manager.CompanyFile("dashboard", path); !ok || filepath.Base(got) != filepath.Base(filepath.FromSlash(path)) {
			t.Errorf("CompanyFile(%q) = %q, %v", path, got, ok)
		}
	}
	for _, path := range []string{"styles/no.txt", "scripts/other.ts", "deep/a/b.css"} {
		if _, ok := manager.CompanyFile("dashboard", path); ok {
			t.Errorf("CompanyFile(%q) unexpectedly authorized", path)
		}
	}
}

func TestCompanyFileGlobCanMatchFilesAddedAfterLoad(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, Dir, "dashboard")
	writePlugin(t, dir, Manifest{Name: "dashboard", Hooks: []Hook{{Event: EventCompanyLoad, Javascript: "main.js", Files: []string{"dynamic/**/*.txt"}}}}, "")
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("main"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, ok := manager.CompanyFile("dashboard", "dynamic/new.txt"); ok {
		t.Fatal("missing post-load file was authorized")
	}
	if err := os.MkdirAll(filepath.Join(dir, "dynamic", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dynamic", "nested", "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.CompanyFile("dashboard", "dynamic/nested/new.txt"); !ok {
		t.Fatal("post-load glob match was not authorized")
	}
}

func TestCompanyLoadRejectsInvalidExportPatternsAndSymlinkMatches(t *testing.T) {
	for name, files := range map[string][]string{
		"invalid-glob":     {"bad[.js"},
		"absolute":         {filepath.Join(string(filepath.Separator), "outside.js")},
		"windows-absolute": {`C:\outside.js`},
		"traversal":        {"web/../outside.js"},
	} {
		t.Run(name, func(t *testing.T) {
			office, database := newPluginOffice(t)
			dir := filepath.Join(office, Dir, name)
			writePlugin(t, dir, Manifest{Name: name, Hooks: []Hook{{Event: EventCompanyLoad, Javascript: "main.js", Files: files}}}, "")
			if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("main"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(office, database); err == nil {
				t.Fatal("unsafe export declaration was accepted")
			}
		})
	}
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, Dir, "symlink")
	writePlugin(t, dir, Manifest{Name: "symlink", Hooks: []Hook{{Event: EventCompanyLoad, Javascript: "main.js", Files: []string{"web/*"}}}}, "")
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(external, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(dir, "web", "outside.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Load(office, database); err == nil {
		t.Fatal("symlink export was accepted")
	}
}

func TestCompanyLoadRejectsSymlinkDirectoryBeforeGlobMatch(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, Dir, "escape")
	writePlugin(t, dir, Manifest{Name: "escape", Hooks: []Hook{{Event: EventCompanyLoad, Javascript: "main.js", Files: []string{"web/**/*.txt"}}}}, "")
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "outside.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(dir, "web", "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Load(office, database); err == nil {
		t.Fatal("symlink directory escape was accepted")
	}
}
