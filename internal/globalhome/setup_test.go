package globalhome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
)

func TestSaveSetupTemplateWritesOnlyReusableModelChoices(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	home, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]config.Profile{"codex": {Provider: "codex", Cmd: "codex", Args: []string{"--yolo"}}}
	roles := map[string]config.RoleModels{}
	for _, role := range config.AllRoles {
		roles[role] = config.RoleModels{Models: []string{"codex"}, Assignment: config.AssignmentRoundRobin}
	}
	templateDir := filepath.Join(home.Dir, "template", ".omo")
	if err := os.MkdirAll(templateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(templateDir, "omo.yaml")
	if err := os.WriteFile(templatePath, []byte("# keep this setting\nlimits:\n  max_developers: 99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := home.SaveSetupTemplate(models, roles); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "repos:") || !strings.Contains(string(raw), "models:") || !strings.Contains(string(raw), "roles:") || !strings.Contains(string(raw), "# keep this setting") || !strings.Contains(string(raw), "max_developers: 99") {
		t.Fatalf("setup template contains the wrong scope:\n%s", raw)
	}
	loadedModels, loadedRoles, found, err := home.LoadSetupTemplate()
	if err != nil {
		t.Fatal(err)
	}
	if !found || loadedModels["codex"].Cmd != "codex" || loadedRoles["developer"].First() != "codex" {
		t.Fatalf("loaded setup choices = models:%#v roles:%#v found:%v", loadedModels, loadedRoles, found)
	}
}

func TestSetSetupNeverAskPreservesOtherGlobalSettings(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	home, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home.Dir, "config.yaml")
	raw := "# keep\ntrusted_offices: []\ntemplate:\n  enabled: false\n  auto_sync: true\n  setup_never_ask: false\nplugins:\n  update_on_start: false\n  installed: {}\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	home, err = Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := home.SetSetupNeverAsk(true); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "# keep") || !strings.Contains(string(written), "auto_sync: true") || !strings.Contains(string(written), "update_on_start: false") || !strings.Contains(string(written), "setup_never_ask: true") {
		t.Fatalf("global settings were not preserved:\n%s", written)
	}
}
