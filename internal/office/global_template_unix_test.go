//go:build !windows

package office

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTemplateCopyFailureLeavesSetupRetryable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses filesystem write permissions")
	}
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	template := filepath.Join(home, "template")
	if err := os.MkdirAll(template, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "z.txt"} {
		if err := os.WriteFile(filepath.Join(template, name), []byte("overlay"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	office := t.TempDir()
	blocked := filepath.Join(office, "z.txt")
	if err := os.WriteFile(blocked, []byte("original"), 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0644) })
	if _, err := Setup(office); err == nil {
		t.Fatal("copy unexpectedly succeeded")
	}
	if _, err := os.Stat(filepath.Join(office, ConfigPath)); !os.IsNotExist(err) {
		t.Fatalf("failed copy left initialization marker: %v", err)
	}
	if err := os.Chmod(blocked, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(office); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "z.txt"} {
		raw, err := os.ReadFile(filepath.Join(office, name))
		if err != nil || string(raw) != "overlay" {
			t.Fatalf("retry skipped %s: %q %v", name, raw, err)
		}
	}
}
