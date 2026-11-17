package testpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWithoutDropsFoldersWithAgentPrograms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bits")
	}
	agents, plain := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(agents, "agy"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "git"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := strings.Join([]string{agents, plain}, string(os.PathListSeparator))
	if got := Reaches(in); len(got) != 1 || got[0] != "agy" {
		t.Fatalf("Reaches(in) = %v", got)
	}
	out := Without(in)
	if out != plain {
		t.Errorf("Without = %q, want %q", out, plain)
	}
	if got := Reaches(out); len(got) != 0 {
		t.Errorf("still reaches %v", got)
	}
}

// TestOnlyKnownTestsBuildAndStartOmo fails when a Go test builds a binary
// that is not listed here: a test that starts the built omo needs a PATH
// from this package (smoke_test.go), so a new one is added here on purpose.
func TestOnlyKnownTestsBuildAndStartOmo(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{
		"cmd/omo/smoke_test.go":              true, // starts omo: uses testpath
		"web/e2e/fakeagent/main_test.go":     true, // builds the scripted fake agent only
		"web/e2e/fakemcp/main_test.go":       true, // builds the scripted fake MCP server only
		"internal/testpath/testpath_test.go": true,
	}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != root && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, _ := os.ReadFile(path)
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.Contains(string(data), `"go", "build"`) && !known[rel] {
			t.Errorf("%s builds a binary: list it in this test, and give a started omo a PATH from testpath.Without", rel)
		}
		if rel == "cmd/omo/smoke_test.go" && !strings.Contains(string(data), "testpath.Without") {
			t.Errorf("%s starts omo without testpath.Without", rel)
		}
		return nil
	})
}
