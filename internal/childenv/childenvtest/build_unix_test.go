//go:build !windows

package childenvtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGoBuildDoesNotRunVCSHooks(t *testing.T) {
	repo := t.TempDir()
	marker := filepath.Join(t.TempDir(), "startup-ran")
	startup := filepath.Join(t.TempDir(), "startup")
	hook := filepath.Join(t.TempDir(), "fsmonitor")
	config := filepath.Join(t.TempDir(), "gitconfig")
	for path, body := range map[string]string{
		filepath.Join(repo, "go.mod"):  "module fixture.invalid/build\n\ngo 1.26.2\n",
		filepath.Join(repo, "main.go"): "package main\nfunc main() {}\n",
		startup:                        "printf ran > \"$TEST_BUILD_MARKER\"\nexit 93\n",
		hook:                           "#!/bin/sh\n. \"$TEST_BUILD_STARTUP\"\n",
		config:                         "[core]\n fsmonitor = \"" + filepath.ToSlash(hook) + "\"\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."},
		{"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = ShellEnv(os.Environ(), t.TempDir())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v\n%s", err, out)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("TEST_BUILD_MARKER", marker)
	t.Setenv("TEST_BUILD_STARTUP", startup)
	GoBuild(t, repo, filepath.Join(t.TempDir(), "fixture"))
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("fixture build ran its VCS shell hook/startup: %v", err)
	}
}
