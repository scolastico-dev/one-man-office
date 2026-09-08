package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
)

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
