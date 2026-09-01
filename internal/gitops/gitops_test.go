package gitops

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	os.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	os.Setenv("GIT_AUTHOR_NAME", "omo-test")
	os.Setenv("GIT_AUTHOR_EMAIL", "omo@test")
	os.Setenv("GIT_COMMITTER_NAME", "omo-test")
	os.Setenv("GIT_COMMITTER_EMAIL", "omo@test")
	os.Exit(m.Run())
}

func TestWorktreeAddRemove(t *testing.T) {
	repo := initRepo(t)
	g := New()
	wt := filepath.Join(t.TempDir(), "wt-1")
	if err := g.AddWorktree(repo, wt, "omo/job-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "README.md")); err != nil {
		t.Fatalf("worktree not populated: %v", err)
	}
	if err := g.RemoveWorktree(repo, wt, "omo/job-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree dir still exists")
	}
	if err := g.RemoveWorktree(repo, wt, "omo/job-1"); err != nil {
		t.Fatalf("repeated cleanup must be idempotent: %v", err)
	}
}

func TestMergeBranchFastPath(t *testing.T) {
	repo := initRepo(t)
	g := New()
	wt := filepath.Join(t.TempDir(), "wt-2")
	g.AddWorktree(repo, wt, "omo/job-2")
	os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("f\n"), 0o644)
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-m", "feat")
	if err := g.MergeBranch(repo, "omo/job-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatal("merge did not land feature.txt on main")
	}
}

func TestMergeConflictAborts(t *testing.T) {
	repo := initRepo(t)
	g := New()
	wt := filepath.Join(t.TempDir(), "wt-3")
	g.AddWorktree(repo, wt, "omo/job-3")
	// Conflicting edits to README.md on both branches.
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("branch\n"), 0o644)
	git(t, wt, "commit", "-am", "branch edit")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("main\n"), 0o644)
	git(t, repo, "commit", "-am", "main edit")
	err := g.MergeBranch(repo, "omo/job-3")
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("err = %v, want ErrMergeConflict", err)
	}
	// Repo must not be mid-merge.
	status := git(t, repo, "status", "--porcelain")
	if strings.Contains(status, "UU") {
		t.Fatalf("repo left mid-merge:\n%s", status)
	}
}

func TestDiff(t *testing.T) {
	repo := initRepo(t)
	g := New()
	wt := filepath.Join(t.TempDir(), "wt-4")
	g.AddWorktree(repo, wt, "omo/job-4")
	os.WriteFile(filepath.Join(wt, "new.go"), []byte("package x\n"), 0o644)
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-m", "add new.go")
	diff, err := g.Diff(repo, "omo/job-4")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "new.go") {
		t.Fatalf("diff missing new.go:\n%s", diff)
	}
}
