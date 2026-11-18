package claudetrust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/testpath"
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
	if err := EnsureIn(cfg, office); err != nil {
		t.Fatal(err)
	}
	p := project(t, cfg, office)
	if p["hasTrustDialogAccepted"] != true {
		t.Fatalf("dir not trusted: %v", p)
	}
	if n, _ := p["projectOnboardingSeenCount"].(float64); n < 1 {
		t.Fatalf("onboarding not marked seen: %v", p)
	}
}

func TestEnsureForEnvUsesSelectedClaudeConfigDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "account", "claude")
	if err := EnsureForEnv(map[string]string{"CLAUDE_CONFIG_DIR": root}, office); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".claude.json")
	if got := project(t, path, office); got["hasTrustDialogAccepted"] != true {
		t.Fatalf("custom account trust = %v", got)
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
	if err := EnsureIn(cfg, office); err != nil {
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
		office: map[string]any{"allowedTools": []any{"Read"}, "hasTrustDialogAccepted": false},
	}})
	os.WriteFile(cfg, raw, 0o600)
	if err := EnsureIn(cfg, office); err != nil {
		t.Fatal(err)
	}
	p := project(t, cfg, office)
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
	if err := EnsureIn(cfg, office); err != nil {
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
			EnsureIn(cfg, filepath.Join(testpath.Abs("/work"), string(rune('a'+i))))
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

func TestForgetForEnvRemovesOnlyThatFolder(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude.json")
	seed := `{"numStartups": 7, "projects": {"/keep/me": {"allowedTools": ["x"]}}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CLAUDE_CONFIG_DIR": root}
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, dir := range []string{a, b} {
		if err := EnsureForEnv(env, dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := ForgetForEnv(env, a); err != nil {
		t.Fatal(err)
	}
	m := read(t, path)
	projects, _ := m["projects"].(map[string]any)
	if _, ok := projects[a]; ok {
		t.Fatalf("forgotten folder still listed: %v", projects)
	}
	if got := project(t, path, b); got["hasTrustDialogAccepted"] != true {
		t.Fatalf("other trusted folder changed: %v", got)
	}
	if _, ok := projects["/keep/me"]; !ok {
		t.Fatalf("unrelated project entry removed: %v", projects)
	}
	if m["numStartups"] != float64(7) {
		t.Fatalf("unrelated key changed: %v", m)
	}
	// Forgetting again, or in an account without a config file, is not an error.
	if err := ForgetForEnv(env, a); err != nil {
		t.Fatalf("second forget: %v", err)
	}
	if err := ForgetForEnv(map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(root, "none")}, a); err != nil {
		t.Fatalf("forget without a config file: %v", err)
	}
}

// setHooks installs the test hooks for the duration of a test.
func setHooks(t *testing.T, before, after func(string)) {
	t.Helper()
	beforeReplace, afterReplace = before, after
	t.Cleanup(func() { beforeReplace, afterReplace = nil, nil })
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureStartsOverWhenTheFileChangesWhileItWrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := filepath.Join(os.Getenv("HOME"), ".claude.json")
	writeJSON(t, cfg, map[string]any{"numStartups": 1.0})
	// Claude Code writes its own change between omo's read and omo's
	// replace, once.
	writes := 0
	setHooks(t, func(path string) {
		if writes == 0 {
			writes++
			writeJSON(t, path, map[string]any{"numStartups": 2.0, "tipsHistory": map[string]any{"x": 1.0}})
		}
	}, nil)
	if err := EnsureIn(cfg, office); err != nil {
		t.Fatal(err)
	}
	m := read(t, cfg)
	if m["numStartups"] != 2.0 || m["tipsHistory"] == nil {
		t.Fatalf("Claude's concurrent change was lost: %v", m)
	}
	if p := project(t, cfg, office); p["hasTrustDialogAccepted"] != true {
		t.Fatalf("trust missing: %v", p)
	}
}

func TestEnsureRetriesWhenItsChangeIsOverwritten(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := filepath.Join(os.Getenv("HOME"), ".claude.json")
	writeJSON(t, cfg, map[string]any{"numStartups": 1.0})
	// Claude Code writes back its stale copy right after omo's replace.
	overwrites := 0
	setHooks(t, nil, func(path string) {
		if overwrites < 2 {
			overwrites++
			writeJSON(t, path, map[string]any{"numStartups": 1.0})
		}
	})
	if err := EnsureIn(cfg, office); err != nil {
		t.Fatal(err)
	}
	if overwrites != 2 {
		t.Fatalf("overwrites = %d", overwrites)
	}
	if p := project(t, cfg, office); p["hasTrustDialogAccepted"] != true {
		t.Fatalf("trust missing after retries: %v", p)
	}

	// The same holds for removing an entry.
	overwrites = 0
	setHooks(t, nil, func(path string) {
		if overwrites < 1 {
			overwrites++
			writeJSON(t, path, map[string]any{"projects": map[string]any{office: map[string]any{"hasTrustDialogAccepted": true}}})
		}
	})
	if err := ForgetIn(cfg, office); err != nil {
		t.Fatal(err)
	}
	if projects, _ := read(t, cfg)["projects"].(map[string]any); projects[office] != nil {
		t.Fatalf("entry back after ForgetIn: %v", projects)
	}
}

func TestEnsureGivesUpWhenTheFileKeepsChanging(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := filepath.Join(os.Getenv("HOME"), ".claude.json")
	writeJSON(t, cfg, map[string]any{"n": 0.0})
	n := 0
	setHooks(t, func(path string) {
		n++
		writeJSON(t, path, map[string]any{"n": float64(n)})
	}, nil)
	if err := EnsureIn(cfg, office); err == nil {
		t.Fatal("no error although every attempt met a changed file")
	}
	if n != updateAttempts {
		t.Fatalf("attempts = %d, want %d", n, updateAttempts)
	}
	if m := read(t, cfg); m["projects"] != nil {
		t.Fatalf("omo wrote over a file that changed: %v", m)
	}
}

func TestUnparsableConfigIsNeverWritten(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := filepath.Join(os.Getenv("HOME"), ".claude.json")
	broken := []byte(`{"projects": {`)
	if err := os.WriteFile(cfg, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureIn(cfg, office); err == nil {
		t.Fatal("EnsureIn accepted an unparsable file")
	}
	if err := ForgetIn(cfg, office); err == nil {
		t.Fatal("ForgetIn accepted an unparsable file")
	}
	if raw, _ := os.ReadFile(cfg); string(raw) != string(broken) {
		t.Fatalf("unparsable file was changed: %q", raw)
	}
	entries, _ := os.ReadDir(os.Getenv("HOME"))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestEnsureDoesNotRewriteATrustedFolder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := filepath.Join(os.Getenv("HOME"), ".claude.json")
	if err := EnsureIn(cfg, office); err != nil {
		t.Fatal(err)
	}
	setHooks(t, func(string) { t.Fatal("a trusted folder was written again") }, nil)
	if err := EnsureIn(cfg, office); err != nil {
		t.Fatal(err)
	}
}

// office is the project folder of these tests: any absolute path.
var office = testpath.Abs("/work/office")
