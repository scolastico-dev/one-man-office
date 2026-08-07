package claudetrust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func read(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	return m
}

func project(t *testing.T, path, dir string) map[string]any {
	t.Helper()
	m := read(t, path)
	projects, _ := m["projects"].(map[string]any)
	p, _ := projects[dir].(map[string]any)
	if p == nil {
		t.Fatalf("no project entry for %s in %v", dir, projects)
	}
	return p
}

func TestEnsureCreatesConfigAndTrustsDir(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	if err := EnsureIn(cfg, "/work/office"); err != nil {
		t.Fatal(err)
	}
	p := project(t, cfg, "/work/office")
	if p["hasTrustDialogAccepted"] != true {
		t.Fatalf("dir not trusted: %v", p)
	}
	if n, _ := p["projectOnboardingSeenCount"].(float64); n < 1 {
		t.Fatalf("onboarding not marked seen: %v", p)
	}
}

// The file belongs to the user and holds far more than trust flags: every
// unrelated key, and every other project, must survive untouched.
func TestEnsurePreservesEverythingElse(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	original := map[string]any{
		"numStartups": float64(42),
		"userID":      "abc123",
		"projects": map[string]any{
			"/other/project": map[string]any{
				"hasTrustDialogAccepted": false,
				"allowedTools":           []any{"Bash"},
			},
		},
	}
	raw, _ := json.Marshal(original)
	if err := os.WriteFile(cfg, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureIn(cfg, "/work/office"); err != nil {
		t.Fatal(err)
	}
	m := read(t, cfg)
	if m["numStartups"] != float64(42) || m["userID"] != "abc123" {
		t.Fatalf("unrelated keys lost: %v", m)
	}
	other := project(t, cfg, "/other/project")
	if other["hasTrustDialogAccepted"] != false {
		t.Fatal("another project's trust flag was changed")
	}
	if tools, _ := other["allowedTools"].([]any); len(tools) != 1 {
		t.Fatalf("another project's settings lost: %v", other)
	}
}

func TestEnsureKeepsExistingProjectKeys(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	raw, _ := json.Marshal(map[string]any{"projects": map[string]any{
		"/work/office": map[string]any{"allowedTools": []any{"Read"}, "hasTrustDialogAccepted": false},
	}})
	os.WriteFile(cfg, raw, 0o600)
	if err := EnsureIn(cfg, "/work/office"); err != nil {
		t.Fatal(err)
	}
	p := project(t, cfg, "/work/office")
	if p["hasTrustDialogAccepted"] != true {
		t.Fatal("trust not flipped to true")
	}
	if tools, _ := p["allowedTools"].([]any); len(tools) != 1 {
		t.Fatalf("existing project keys lost: %v", p)
	}
}

func TestEnsurePreservesFileMode(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	os.WriteFile(cfg, []byte(`{"projects":{}}`), 0o600)
	if err := EnsureIn(cfg, "/work/office"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600 (the file holds credentials)", fi.Mode().Perm())
	}
}

// omo spawns agents concurrently; the config is a single shared file.
func TestEnsureIsSafeConcurrently(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			EnsureIn(cfg, filepath.Join("/work", string(rune('a'+i))))
		}(i)
	}
	wg.Wait()
	m := read(t, cfg) // must still be parseable, not a torn write
	projects, _ := m["projects"].(map[string]any)
	if len(projects) != 20 {
		t.Fatalf("expected 20 projects, got %d", len(projects))
	}
}

func TestEnsureRefusesRelativePath(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), ".claude.json")
	if err := EnsureIn(cfg, "relative/dir"); err == nil {
		t.Fatal("a relative dir cannot match claude's absolute project keys")
	}
}
