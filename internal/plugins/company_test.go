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
