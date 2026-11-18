package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"github.com/scolastico-dev/one-man-office/internal/gittest"
)

// returnsWithin runs fn and fails the test when it does not return within d.
func returnsWithin(t *testing.T, d time.Duration, what string, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("%s did not return within %s", what, d)
		return nil
	}
}

// git runs a git command in dir with fully isolated config (no user/global
// config, no signing, deterministic identity).
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=omo-test", "GIT_AUTHOR_EMAIL=omo@test",
		"GIT_COMMITTER_NAME=omo-test", "GIT_COMMITTER_EMAIL=omo@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "init")
	return dir
}

func TestMain(m *testing.M) {
	// Isolate git for the code under test as well.
	gittest.Isolate()
	os.Setenv("GIT_AUTHOR_NAME", "omo-test")
	os.Setenv("GIT_AUTHOR_EMAIL", "omo@test")
	os.Setenv("GIT_COMMITTER_NAME", "omo-test")
	os.Setenv("GIT_COMMITTER_EMAIL", "omo@test")
	os.Exit(childenvtest.Run(m))
}

func TestWorktreeAddRemove(t *testing.T) {
	repo := initRepo(t)
	g := New()
	wt := filepath.Join(t.TempDir(), "wt-1")
	if err := addNewWorktree(g, repo, wt, "omo/job-1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "README.md")); err != nil {
		t.Fatalf("worktree not populated: %v", err)
	}
	if dirty, err := g.RemoveWorktreeIfClean(repo, wt, "omo/job-1"); err != nil || len(dirty) > 0 {
		t.Fatalf("remove: %v %v", dirty, err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree dir still exists")
	}
	if _, err := g.RemoveWorktreeIfClean(repo, wt, "omo/job-1"); err != nil {
		t.Fatalf("repeated cleanup must be idempotent: %v", err)
	}
}

func TestAddWorktreeFromExplicitBaseDoesNotMutateCheckout(t *testing.T) {
	repo := initRepo(t)
	base := filepath.Join(t.TempDir(), "base")
	if err := os.Mkdir(base, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "switch", "-c", "base")
	os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "base")
	git(t, repo, "switch", "main")
	wt := filepath.Join(t.TempDir(), "explicit")
	if err := addNewWorktree(New(), repo, wt, "omo/explicit", "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "base.txt")); err != nil {
		t.Fatalf("explicit base was not used: %v", err)
	}
	if got := strings.TrimSpace(git(t, repo, "branch", "--show-current")); got != "main" {
		t.Fatalf("checkout branch changed to %q", got)
	}
}

// Without SetEnvironment, git and the hooks it runs get omo's environment.
func TestDefaultEnvironmentReachesHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hooks are shell scripts")
	}
	requireGit(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("OMO_TEST_VISIBLE", "visible")
	repo := initRepo(t)
	marker := filepath.Join(t.TempDir(), "hook-env")
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	script := "#!/bin/sh\necho \"other=$OMO_TEST_VISIBLE\" >> '" + marker + "'\n"
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	g := New()
	if err := addNewWorktree(g, repo, filepath.Join(t.TempDir(), "a"), "omo/a", ""); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "branch", "omo/b")
	if err := g.AddWorktreeForBranch(repo, filepath.Join(t.TempDir(), "b"), "omo/b"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
	if want := "other=visible\nother=visible\n"; string(raw) != want {
		t.Fatalf("hook saw %q, want %q", raw, want)
	}
}
