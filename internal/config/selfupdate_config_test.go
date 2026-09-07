package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSetCheckSelfUpdatePreservesFormattingAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.yaml")
	raw := "# office settings\nstartup:\n  # leave this comment alone\n  check_self_update: true # explicit user choice\n  check_templates: true\n\n# keep the boundary below\nmodels: {}\n"
	if err := os.WriteFile(path, []byte(raw), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := SetCheckSelfUpdate(path, false); err != nil {
		t.Fatal(err)
	}

	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	for _, want := range []string{
		"# office settings",
		"# leave this comment alone",
		"check_self_update: false # explicit user choice",
		"check_templates: true\n\n# keep the boundary below\nmodels:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("updated config missing %q:\n%s", want, text)
		}
	}
	var decoded struct {
		Startup struct {
			CheckSelfUpdate bool `yaml:"check_self_update"`
		} `yaml:"startup"`
	}
	if err := yaml.Unmarshal(updated, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Startup.CheckSelfUpdate {
		t.Fatal("automatic self-update remained enabled")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("config mode changed: %v %v", info, err)
	}
}
