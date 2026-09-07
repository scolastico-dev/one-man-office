package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderGlobalExtensionsBeforeOffice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	office := t.TempDir()
	for path, content := range map[string]string{filepath.Join(home, "extensions", "developer.md"): "GLOBAL RULE", filepath.Join(office, ExtensionsDir, "developer.md"): "LOCAL RULE"} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := Render(office, "developer", Data{Name: "dev", Role: "developer", Goal: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "GLOBAL RULE\n\nLOCAL RULE") {
		t.Fatalf("wrong extension precedence: %s", out)
	}
}

func TestRenderLoadsRoleExtensionFile(t *testing.T) {
	office := t.TempDir()
	if err := os.MkdirAll(filepath.Join(office, ExtensionsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(office, ExtensionsDir, "developer.md"), []byte("FILE EXTENSION RULE"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Render(office, "developer", Data{Name: "dev", Role: "developer", Goal: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PROMPT EXTENSIONS:\nFILE EXTENSION RULE") {
		t.Fatalf("extension missing from prompt:\n%s", out)
	}
}

func TestRenderLoadsExtensionDirectoryInLexicalOrder(t *testing.T) {
	office := t.TempDir()
	dir := filepath.Join(office, ExtensionsDir, "product_manager")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"20-second.md": "SECOND FRAGMENT",
		"01-first.md":  "FIRST FRAGMENT",
		"ignored.txt":  "DO NOT LOAD",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := Render(office, "product_manager", Data{Name: "pm", Role: "product_manager", Goal: "g"})
	if err != nil {
		t.Fatal(err)
	}
	first, second := strings.Index(out, "FIRST FRAGMENT"), strings.Index(out, "SECOND FRAGMENT")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("fragments not loaded lexically:\n%s", out)
	}
	if strings.Contains(out, "DO NOT LOAD") {
		t.Fatalf("non-markdown extension loaded:\n%s", out)
	}
}

func TestRenderRejectsAmbiguousExtensionPreset(t *testing.T) {
	office := t.TempDir()
	root := filepath.Join(office, ExtensionsDir)
	if err := os.MkdirAll(filepath.Join(root, "reviewer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "reviewer.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(office, "reviewer", Data{}); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("ambiguous preset error = %v", err)
	}
}
