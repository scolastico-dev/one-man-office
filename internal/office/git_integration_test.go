package office

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
)

func TestEnableGitIntegrationMakesOfficePortable(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	office := t.TempDir()
	if err := runGit(office, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(office); err != nil {
		t.Fatal(err)
	}
	if _, err := EnableGitIntegration(office); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(office, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "git_integration: true") {
		t.Fatalf("Git integration was not persisted:\n%s", raw)
	}
	if cfg, err := config.Load(filepath.Join(office, ConfigPath)); err != nil {
		t.Fatal(err)
	} else if cfg.GitIntegration != true {
		t.Fatal("loaded config did not enable Git integration")
	}
	ignore, err := os.ReadFile(filepath.Join(office, ".omo", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ignore), "!omo.yaml") || !strings.Contains(string(ignore), "omo.db") {
		t.Fatalf("Git integration ignore rules are incomplete:\n%s", ignore)
	}
	value, err := exec.Command("git", "-C", office, "config", "--local", "--get", "omo.gitIntegration").Output()
	if err != nil || strings.TrimSpace(string(value)) != "true" {
		t.Fatalf("local Git integration config = %q, err=%v", value, err)
	}
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
