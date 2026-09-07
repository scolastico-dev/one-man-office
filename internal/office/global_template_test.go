package office

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupGlobalTemplateOnlyOnCreation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	for path, value := range map[string]string{".omo/prompts/developer.md": "GLOBAL OVERRIDE", ".omo/omo.yaml": "# custom office\nrepos: {}\n", "nested/readme.txt": "extra file"} {
		dest := filepath.Join(home, "template", path)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, []byte(value), 0755); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
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
	assertFile(".omo/omo.yaml", "# custom office\nrepos: {}\n")
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
	assertFile(".omo/omo.yaml", "# custom office\nrepos: {}\n")
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
