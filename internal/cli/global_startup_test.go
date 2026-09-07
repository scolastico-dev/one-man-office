package cli

import (
	"context"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalPluginStartupChecksIndependentOfOfficeSetting(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			t.Setenv("OMO_HOME", t.TempDir())
			h, err := globalhome.Open()
			if err != nil {
				t.Fatal(err)
			}
			setting := "false"
			if enabled {
				setting = "true"
			}
			if err := os.WriteFile(filepath.Join(h.Dir, "config.yaml"), []byte("plugins:\n  update_on_start: "+setting+"\n  installed:\n    global:\n      source: https://example.com/plugin.git\n      enabled: true\n"), 0600); err != nil {
				t.Fatal(err)
			}
			old := globalPluginSyncAll
			t.Cleanup(func() { globalPluginSyncAll = old })
			calls := 0
			globalPluginSyncAll = func(_ context.Context, root, configPath string, settings config.Plugins) ([]pluginmanager.Result, []error) {
				calls++
				if root != filepath.Join(h.Dir, "plugins") || configPath != filepath.Join(h.Dir, "config.yaml") || settings.Installed["global"].Source != "https://example.com/plugin.git" {
					t.Fatalf("wrong update target: %s %s %+v", root, configPath, settings)
				}
				return nil, nil
			}
			cmd, _, _ := startupCommand("")
			if _, err := runStartupChecks(cmd, t.TempDir(), &config.Config{}, "test", false); err != nil {
				t.Fatal(err)
			}
			want := 0
			if enabled {
				want = 1
			}
			if calls != want {
				t.Fatalf("global updater calls=%d", calls)
			}
		})
	}
}
