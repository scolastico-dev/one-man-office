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
	if strings.Contains(string(raw), filepath.ToSlash(office)) {
		t.Fatalf("absolute office path remained in portable config:\n%s", raw)
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
	for _, path := range []string{".omo/omo.db", ".omo/omo.db-wal", ".omo/omo.lock", ".omo/omo.sock", ".omo/logs/session.log", ".omo/storage/note", ".omo/worktrees/job", ".omo/plugins/.repos/cache", ".omo/plugins/.runtime-snapshot/file"} {
		if err := exec.Command("git", "-C", office, "check-ignore", "-q", "--", path).Run(); err != nil {
			t.Errorf("runtime path %s is visible to Git: %v", path, err)
		}
	}
	for _, path := range []string{".omo/omo.yaml", ".omo/prompts/developer.md", ".omo/plugins/tools/plugin.json", ".omo/specs/2026/09/1.yaml", ".omo/jobs/active/2026/09/1.yaml"} {
		if err := exec.Command("git", "-C", office, "check-ignore", "-q", "--", path).Run(); err == nil {
			t.Errorf("durable path %s remained ignored", path)
		}
	}
	value, err := exec.Command("git", "-C", office, "config", "--local", "--get", "omo.gitIntegration").Output()
	if err != nil || strings.TrimSpace(string(value)) != "true" {
		t.Fatalf("local Git integration config = %q, err=%v", value, err)
	}
}

func TestEnableGitIntegrationReportsUserIgnoreThatStillHidesOffice(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	if err := runGit(dir, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".omo/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := EnableGitIntegration(dir)
	if err == nil || !strings.Contains(err.Error(), "remains ignored") || !strings.Contains(err.Error(), ".gitignore") {
		t.Fatalf("user-owned ignore error = %v", err)
	}
}

func TestEnableGitIntegrationChecksContainingWorktree(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	root := t.TempDir()
	if err := runGit(root, "init"); err != nil {
		t.Fatal(err)
	}
	office := filepath.Join(root, "nested")
	if err := os.MkdirAll(office, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(office); err != nil {
		t.Fatal(err)
	}
	ignorePath := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(ignorePath, []byte("nested/.omo/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnableGitIntegration(office); err == nil || !strings.Contains(err.Error(), "nested/.omo/omo.yaml remains ignored") {
		t.Fatalf("nested user-owned ignore error = %v", err)
	}
	if err := os.WriteFile(ignorePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnableGitIntegration(office); err != nil {
		t.Fatal(err)
	}
	value, err := exec.Command("git", "-C", root, "config", "--local", "--get", "omo.gitIntegration").Output()
	if err != nil || strings.TrimSpace(string(value)) != "true" {
		t.Fatalf("containing worktree Git integration config = %q, err=%v", value, err)
	}
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
