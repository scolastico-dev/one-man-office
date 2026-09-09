package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
)

func TestPluginTriggerGlobalRunsWithoutOffice(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("OMO_HOME", homeDir)
	plugin := filepath.Join(homeDir, "plugins", "global-action")
	if err := os.MkdirAll(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "plugin.json"), []byte(`{"name":"global-action","hooks":[{"event":"manual","name":"run","description":"Run globally","lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "hook.lua"), []byte(`omo.local_set("ran", true)`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := Root("test")
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"plugin", "trigger", "--global", "global-action", "run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "global plugin global-action action run completed") {
		t.Fatalf("output = %q", output.String())
	}
	home, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(home.Dir, "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var value string
	if err := database.QueryRow(`SELECT value FROM plugin_storage WHERE scope='local' AND plugin='global-action' AND key='ran'`).Scan(&value); err != nil || value != "true" {
		t.Fatalf("global plugin state = %q, %v", value, err)
	}
}

func TestPluginGlobalFlagListsAndTogglesGlobalConfig(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	home, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	path := home.Dir + "/config.yaml"
	if err := pluginmanager.UpsertConfig(path, "shared", config.Plugin{Source: "https://example.com/shared.git", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) string {
		t.Helper()
		cmd := Root("test")
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("omo %s: %v", strings.Join(args, " "), err)
		}
		return out.String()
	}
	if got := run("plugin", "--global", "list"); !strings.Contains(got, "shared\tenabled\thttps://example.com/shared.git") {
		t.Fatalf("global list = %q", got)
	}
	if got := run("plugin", "disable", "--global", "shared"); !strings.Contains(got, "disabled plugin shared") {
		t.Fatalf("global disable = %q", got)
	}
	home, err = globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	if home.Config.Plugins.Installed["shared"].Enabled {
		t.Fatal("global plugin remained enabled")
	}
}
