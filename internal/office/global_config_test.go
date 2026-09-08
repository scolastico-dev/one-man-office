package office

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
)

func TestGlobalTemplateOverridesOfficeInMemoryByDefault(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("OMO_HOME", homeDir)
	officeDir := t.TempDir()
	if _, err := Setup(officeDir); err != nil {
		t.Fatal(err)
	}
	home, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home.Dir, "config.yaml"), []byte("template:\n  enabled: true\n  auto_sync: false\nplugins:\n  update_on_start: true\n  installed: {}\ntrusted_offices: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home.Dir, "template", ".omo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home.Dir, "template", ".omo", "omo.yaml"), []byte("limits:\n  max_developers: 9\n"), 0600); err != nil {
		t.Fatal(err)
	}
	home, err = globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := loadOfficeConfig(officeDir, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limits.MaxDevelopers != 9 {
		t.Fatalf("global template was not applied: %+v", cfg.Limits)
	}
	local, err := os.ReadFile(filepath.Join(officeDir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(local), "max_developers: 9") {
		t.Fatal("in-memory global overlay modified the office config")
	}
}

func TestGlobalTemplateAutoSyncUpdatesOfficeConfig(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("OMO_HOME", homeDir)
	officeDir := t.TempDir()
	if _, err := Setup(officeDir); err != nil {
		t.Fatal(err)
	}
	home, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home.Dir, "config.yaml"), []byte("template:\n  enabled: true\n  auto_sync: true\nplugins:\n  update_on_start: true\n  installed: {}\ntrusted_offices: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home.Dir, "template", ".omo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home.Dir, "template", ".omo", "omo.yaml"), []byte("limits:\n  max_developers: 8\n"), 0600); err != nil {
		t.Fatal(err)
	}
	home, err = globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadOfficeConfig(officeDir, home, true); err != nil {
		t.Fatal(err)
	}
	local, err := os.ReadFile(filepath.Join(officeDir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(local), "max_developers: 8") {
		t.Fatalf("auto-sync did not update office config:\n%s", local)
	}
}
