package office

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
)

func TestSetupGlobalTemplateOnlyOnCreation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	for path, value := range map[string]string{".omo/prompts/developer.md": "GLOBAL OVERRIDE", ".omo/omo.yaml": "# custom office\nbranches:\n  naming: generated\n", "nested/readme.txt": "extra file"} {
		dest := filepath.Join(home, "template", path)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, []byte(value), 0755); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	assertFile := func(path, want string) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil || string(raw) != want {
			t.Fatalf("%s = %q, %v", path, raw, err)
		}
	}
	assertFile(".omo/prompts/developer.md", "GLOBAL OVERRIDE")
	cfg, err := config.Load(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Branches.Naming != "generated" {
		t.Fatalf("branch naming = %q, want generated", cfg.Branches.Naming)
	}
	if len(cfg.Models) == 0 || len(cfg.Roles) == 0 {
		t.Fatalf("partial template erased generated schema: models=%d roles=%d", len(cfg.Models), len(cfg.Roles))
	}
	if cfg.Repos["repo"].Path != repo {
		t.Fatalf("partial template changed detected repos: %+v", cfg.Repos)
	}
	assertFile("nested/readme.txt", "extra file")
	if err := os.WriteFile(filepath.Join(dir, "nested/readme.txt"), []byte("user edit"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	assertFile("nested/readme.txt", "user edit")
	if _, err := UpdateTemplates(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".omo/prompts/developer.md"))
	if err != nil || string(raw) == "GLOBAL OVERRIDE" {
		t.Fatalf("update applied global template: %q, %v", raw, err)
	}
	assertFile("nested/readme.txt", "user edit")
}

func TestSyncTemplateConfigAppliesOnlyPartialConfigOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	template := filepath.Join(home, "template", ConfigPath)
	if err := os.MkdirAll(filepath.Dir(template), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(template, []byte("branches:\n  naming: generated\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncTemplateConfig(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Branches.Naming != "generated" {
		t.Fatalf("branch naming = %q, want generated", cfg.Branches.Naming)
	}
	before, err := os.ReadFile(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SyncTemplateConfig(dir); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("sync was not idempotent")
	}
}

func TestSyncTemplateConfigRejectsInvalidOverrideWithoutMutatingOffice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ConfigPath)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(home, "template", ConfigPath)
	if err := os.MkdirAll(filepath.Dir(template), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(template, []byte("not_a_real_setting: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncTemplateConfig(dir); err == nil {
		t.Fatal("invalid override accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("invalid override mutated office config")
	}
}

func TestRejectedTemplateDoesNotInitializeOfficeAndCanRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	template := filepath.Join(home, "template")
	if err := os.MkdirAll(template, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(template, "first.txt"), []byte("overlay"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(template, "rejected")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skip(err)
	}
	office := t.TempDir()
	if _, err := Setup(office); err == nil {
		t.Fatal("symlink overlay accepted")
	}
	if _, err := os.Stat(filepath.Join(office, ".omo")); !os.IsNotExist(err) {
		t.Fatalf("rejected template mutated office: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(office); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(office, "first.txt"))
	if err != nil || string(raw) != "overlay" {
		t.Fatalf("retry skipped overlay: %q %v", raw, err)
	}
}
