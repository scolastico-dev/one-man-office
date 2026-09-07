package globalhome

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTemplateRejectsSymlinksBeforeCopying(t *testing.T) {
	for _, source := range []bool{true, false} {
		t.Run(map[bool]string{true: "source", false: "destination"}[source], func(t *testing.T) {
			t.Setenv("OMO_HOME", t.TempDir())
			h, err := Open()
			if err != nil {
				t.Fatal(err)
			}
			dest := t.TempDir()
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(h.Dir, "template", "a.txt"), []byte("new"), 0644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(h.Dir, "template", "z")
			if !source {
				if err := os.MkdirAll(link, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(link, "value"), []byte("changed"), 0644); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(dest, "z")
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Skip(err)
			}
			if _, err := h.ApplyTemplate(dest); err == nil {
				t.Fatal("symlink accepted")
			}
			if _, err := os.Stat(filepath.Join(dest, "a.txt")); !os.IsNotExist(err) {
				t.Fatalf("partial copy before validation: %v", err)
			}
		})
	}
}
