package office

import (
	"context"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenLoadsGlobalPluginWithGlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(home, "plugins", "global")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{filepath.Join(pluginDir, "plugin.json"): `{"name":"global","hooks":[{"event":"job_create","lua":"hook.lua"}]}`, filepath.Join(pluginDir, "hook.lua"): `event.data.title = config.title`, filepath.Join(home, "config.yaml"): "trusted_offices: []\nplugins:\n  update_on_start: false\n  installed:\n    global:\n      enabled: true\n      config: {title: global-value}\n"}
	for path, data := range files {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	o, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	got, err := o.Sup.Plugins.Emit(context.Background(), plugins.Event{Name: plugins.EventJobCreate, Mutable: true, Data: map[string]any{"title": "original"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Data["title"] != "global-value" {
		t.Fatalf("global plugin not applied: %v", got.Data)
	}
}

func TestSetupPreservesPreexistingGlobalToolsPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	globalTools := filepath.Join(home, "plugins", "tools")
	if err := os.MkdirAll(globalTools, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalTools, "plugin.json"), []byte(`{"name":"tools","hooks":[{"event":"manual","name":"global-maintenance","description":"Global maintenance","lua":"tools.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalTools, "tools.lua"), []byte("-- global tools\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".omo", "plugins", "tools")); !os.IsNotExist(err) {
		t.Fatalf("setup installed a local tools plugin over the global owner: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "source: builtin:tools") {
		t.Fatalf("setup claimed the global tools plugin locally:\n%s", raw)
	}

	o, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	actions := o.Sup.Plugins.ManualActions("tools")
	if len(actions) != 1 || actions[0].Name != "global-maintenance" {
		t.Fatalf("global tools plugin was not selected: %+v", actions)
	}
}

func TestSetupPreservesGlobalToolsManifestAlias(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	globalTools := filepath.Join(home, "plugins", "maintenance")
	if err := os.MkdirAll(globalTools, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalTools, "plugin.json"), []byte(`{"name":"tools","hooks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".omo", "plugins", "tools")); !os.IsNotExist(err) {
		t.Fatalf("setup installed bundled tools over a global manifest alias: %v", err)
	}
	o, err := Open(dir, true)
	if err != nil {
		t.Fatalf("opening an office with a global tools manifest alias: %v", err)
	}
	defer o.Close()
}

func TestSetupPreservesConfiguredGlobalToolsPluginBeforeCheckout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("trusted_offices: []\nplugins:\n  update_on_start: false\n  installed:\n    tools:\n      source: https://example.test/tools.git\n      enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".omo", "plugins", "tools")); !os.IsNotExist(err) {
		t.Fatalf("setup installed a local tools plugin over configured global ownership: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "source: builtin:tools") {
		t.Fatalf("setup claimed configured global tools ownership locally:\n%s", raw)
	}
}
