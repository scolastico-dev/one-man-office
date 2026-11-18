package gittest

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// usesGit matches Go code that runs git or uses omo's git client.
var usesGit = regexp.MustCompile(`exec\.Command(Context)?\((ctx, )?"git"|internal/gitops"`)

// TestEveryGitTestPackageIsolatesGit fails for a Go package whose tests
// can run git (its code or its tests run git or use internal/gitops)
// without a TestMain that calls Isolate: such a test would read the
// developer's git configuration and fail, for example, while the
// developer's signing key is locked.
func TestEveryGitTestPackageIsolatesGit(t *testing.T) {
	root := moduleRoot(t)
	type pkg struct{ git, isolated, tests bool }
	pkgs := map[string]*pkg{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (name == "node_modules" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dir, _ := filepath.Rel(root, filepath.Dir(path))
		p := pkgs[dir]
		if p == nil {
			p = &pkg{}
			pkgs[dir] = p
		}
		text := string(raw)
		p.git = p.git || usesGit.MatchString(text)
		if strings.HasSuffix(path, "_test.go") {
			p.tests = true
			p.isolated = p.isolated || (strings.Contains(text, "func TestMain(") && strings.Contains(text, "gittest.Isolate()"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for dir, p := range pkgs {
		if p.git && p.tests && !p.isolated && dir != filepath.Join("internal", "gittest") {
			missing = append(missing, dir)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("these packages' tests can run git without isolating it; add a TestMain that calls gittest.Isolate():\n%s",
			strings.Join(missing, "\n"))
	}
}

// TestBrowserHarnessIsolatesGit is the same rule for the browser tests:
// every file under web/e2e that runs git gives it an environment without
// the developer's global configuration, and the omo server the harness
// starts reads neither the global nor the system configuration.
func TestBrowserHarnessIsolatesGit(t *testing.T) {
	root := filepath.Join(moduleRoot(t), "web", "e2e")
	runsGit := regexp.MustCompile(`(execFileSync|spawnSync|spawn|execSync)\(\s*'git'`)
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".ts") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if runsGit.Match(raw) && !strings.Contains(string(raw), "GIT_CONFIG_GLOBAL") {
			rel, _ := filepath.Rel(root, path)
			bad = append(bad, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("these browser test files run git with the developer's configuration:\n%s", strings.Join(bad, "\n"))
	}
	raw, err := os.ReadFile(filepath.Join(root, "support", "env.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GIT_CONFIG_GLOBAL: devNull", "GIT_CONFIG_NOSYSTEM: '1'"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the harness's server environment lacks %s", want)
		}
	}
}

// Isolate hides the global, system and environment configuration from git.
func TestIsolateHidesTheDevelopersConfiguration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[commit]\n\tgpgsign = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.signingkey")
	t.Setenv("GIT_CONFIG_VALUE_0", "locked")
	Isolate()
	for _, key := range []string{"commit.gpgsign", "user.signingkey"} {
		cmd := exec.Command("git", "config", "--get", key)
		cmd.Dir = t.TempDir()
		if out, err := cmd.Output(); err == nil {
			t.Errorf("git still sees %s = %s", key, strings.TrimSpace(string(out)))
		}
	}
}

// moduleRoot is the folder of go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}
