package office

import (
	"context"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"os"
	"path/filepath"
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
