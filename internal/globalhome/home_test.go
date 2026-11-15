package globalhome

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenCreatesLayoutAndMarker(t *testing.T) {
	root := filepath.Join(t.TempDir(), "omo")
	t.Setenv("OMO_HOME", root)
	l, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if l.Root != root || l.Settings != filepath.Join(root, "settings.yaml") || l.DB != filepath.Join(root, "omo.db") ||
		l.ServerConfig != filepath.Join(root, "server.yaml") || l.Token != filepath.Join(root, "token") {
		t.Fatalf("layout = %+v", l)
	}
	for _, dir := range []string{l.Projects, l.Profiles, l.Roles, l.Prompts, l.Usage, l.SkillSets, l.Plugins, l.Run, l.Worktrees, l.CEO, l.Logs} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("%s: %v %v", dir, info, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "layout.json"))
	if err != nil || strings.TrimSpace(string(raw)) != `{"layout":2}` {
		t.Fatalf("layout.json = %q, %v", raw, err)
	}
	if runtime.GOOS != "windows" {
		for _, dir := range []string{l.Root, l.Run} {
			if info, _ := os.Stat(dir); info.Mode().Perm() != 0700 {
				t.Fatalf("%s mode = %v", dir, info.Mode().Perm())
			}
		}
	}
	if _, err := Open(); err != nil {
		t.Fatalf("second Open: %v", err)
	}
}

func TestOpenRefusesOldInstallation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("x: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Open()
	if !errors.Is(err, ErrOldInstallation) || !strings.Contains(err.Error(), "set OMO_HOME to another folder") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "layout.json")); statErr == nil {
		t.Fatal("marker written into an old installation")
	}
}

func TestOpenAcceptsEmptyExistingDir(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	if _, err := Open(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFileAtomicReplacesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	for _, content := range []string{"one", "two"} {
		if err := WriteFileAtomic(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if raw, _ := os.ReadFile(path); string(raw) != "two" {
		t.Fatalf("content = %q", raw)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftover temp files: %v", entries)
	}
}
