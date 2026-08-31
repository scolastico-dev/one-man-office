package pluginmanager

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"gopkg.in/yaml.v3"
)

func TestNormalizeSourceAddsDotGit(t *testing.T) {
	tests := map[string]string{
		"https://github.com/acme/plugins":      "https://github.com/acme/plugins.git",
		"https://gitea.example/acme/plugins/":  "https://gitea.example/acme/plugins.git",
		"ssh://git@git.example/acme/plugins":   "ssh://git@git.example/acme/plugins.git",
		"git@git.example:acme/plugins":         "git@git.example:acme/plugins.git",
		"https://github.com/acme/plugins.git":  "https://github.com/acme/plugins.git",
		"https://gitea.example/acme/p.git?q=1": "https://gitea.example/acme/p.git?q=1",
	}
	for input, want := range tests {
		got, err := NormalizeSource(input)
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeSource(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSyncInstallsAndUpdatesRepositorySubpath(t *testing.T) {
	work, remoteURL := pluginRemote(t, "one")
	office := t.TempDir()
	entry := config.Plugin{Source: remoteURL, Subpath: "examples/nudge", Enabled: true}

	first, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed {
		t.Fatal("initial clone must be reported as changed")
	}
	active := filepath.Join(office, rootDir, "nudge")
	assertFile(t, filepath.Join(active, "hook.lua"), "one")
	if _, err := os.Stat(filepath.Join(active, "unrelated.txt")); !os.IsNotExist(err) {
		t.Fatal("repository files outside the selected subpath were activated")
	}

	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: update plugin")
	remotePath, _ := url.Parse(remoteURL)
	git(t, work, "push", remotePath.Path, "HEAD:main")

	second, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Changed {
		t.Fatal("new upstream commit was not detected")
	}
	assertFile(t, filepath.Join(active, "hook.lua"), "two")
	third, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if third.Changed {
		t.Fatal("unchanged repository was reported as updated")
	}
}

func TestConfigEditsPreservePluginWhileToggling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.yaml")
	if err := os.WriteFile(path, []byte("# office\nplugins:\n  update_on_start: true\n  installed: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	entry := config.Plugin{Source: "https://example.test/acme/nudge.git", Subpath: "plugins/nudge", Enabled: true}
	if err := UpsertConfig(path, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(path, "nudge", false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Plugins config.Plugins `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.Plugins.Installed["nudge"]
	if !ok || got.Source != entry.Source || got.Subpath != entry.Subpath || got.Enabled {
		t.Fatalf("plugin was deleted or changed while disabling: %+v", got)
	}
	if !strings.Contains(string(raw), "# office") {
		t.Fatal("top-level config comment was not preserved")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("config mode changed: %v %v", info, err)
	}
}

func TestSyncEnsuresBundledNudgeWithoutOverwritingIt(t *testing.T) {
	office := t.TempDir()
	entry := config.Plugin{Source: "builtin:nudge", Enabled: true}
	first, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil || !first.Changed || first.Revision != "bundled" {
		t.Fatalf("first bundled sync = %+v, %v", first, err)
	}
	script := filepath.Join(office, rootDir, "nudge", "nudge.lua")
	if err := os.WriteFile(script, []byte("-- local edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil || second.Changed {
		t.Fatalf("second bundled sync = %+v, %v", second, err)
	}
	assertFile(t, script, "-- local edit")
}

func pluginRemote(t *testing.T, content string) (string, string) {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "work")
	if err := os.MkdirAll(filepath.Join(work, "examples", "nudge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "plugin.json"), []byte("{\"name\":\"nudge\",\"hooks\":[]}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "unrelated.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "init", "-b", "main")
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: initial plugin")
	remote := filepath.Join(base, "plugins.git")
	git(t, base, "clone", "--bare", work, remote)
	return work, (&url.URL{Scheme: "file", Path: remote}).String()
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=omo-test", "GIT_AUTHOR_EMAIL=omo@test",
		"GIT_COMMITTER_NAME=omo-test", "GIT_COMMITTER_EMAIL=omo@test")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Fatalf("%s = %q, want %q", path, raw, want)
	}
}
