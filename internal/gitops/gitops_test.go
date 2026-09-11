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
	if err := New().AddWorktreeFromBase(repo, wt, "omo/explicit", "base"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "base.txt")); err != nil {
		t.Fatalf("explicit base was not used: %v", err)
	}
	if got := strings.TrimSpace(git(t, repo, "branch", "--show-current")); got != "main" {
		t.Fatalf("checkout branch changed to %q", got)
	}
}

func TestTargetDirectoryMergeAndDiff(t *testing.T) {
	repo := initRepo(t)
	target := filepath.Join(t.TempDir(), "target")
	if err := New().AddWorktree(repo, target, "omo/pm-1"); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(t.TempDir(), "child")
	if err := New().AddWorktreeFromBase(repo, child, "omo/child-1", "omo/pm-1"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(child, "child.txt"), []byte("child\n"), 0o644)
	git(t, child, "add", ".")
	git(t, child, "commit", "-m", "child")
	diff, err := New().DiffAgainst(repo, target, "omo/child-1")
	if err != nil || !strings.Contains(diff, "child.txt") {
		t.Fatalf("target diff = %q, %v", diff, err)
	}
	if err := New().MergeBranchInto(repo, target, "omo/child-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "child.txt")); err != nil {
		t.Fatalf("merge did not land in target worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "child.txt")); !os.IsNotExist(err) {
		t.Fatalf("merge unexpectedly mutated checkout: %v", err)
	}
}

func TestTargetDirectoryMergeConflictAbortsTarget(t *testing.T) {
	repo := initRepo(t)
	target := filepath.Join(t.TempDir(), "target-conflict")
	child := filepath.Join(t.TempDir(), "child-conflict")
	if err := New().AddWorktree(repo, target, "omo/pm-conflict"); err != nil {
		t.Fatal(err)
	}
	if err := New().AddWorktreeFromBase(repo, child, "omo/child-conflict", "omo/pm-conflict"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(child, "README.md"), []byte("child\n"), 0o644)
	git(t, child, "commit", "-am", "child conflict")
	os.WriteFile(filepath.Join(target, "README.md"), []byte("target\n"), 0o644)
	git(t, target, "commit", "-am", "target conflict")
	err := New().MergeBranchInto(repo, target, "omo/child-conflict")
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("err = %v, want ErrMergeConflict", err)
	}
	if strings.Contains(git(t, target, "status", "--porcelain"), "UU") {
		t.Fatal("target worktree left mid-merge")
	}
}

func TestEnsureWorktreeRecoversLostRegistrationWithoutDiscardingChanges(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "lost-registration")
	branch := "omo/lost-registration"
	if err := New().AddWorktreeFromBase(repo, wt, branch, "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "untracked.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitdirRaw, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(string(gitdirRaw), "gitdir: "))
	lostAdmin := gitdir + ".lost"
	if err := os.Rename(gitdir, lostAdmin); err != nil {
		t.Fatal(err)
	}
	if err := New().EnsureWorktree(repo, wt, branch); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git(t, repo, "branch", "--show-current")); got != "main" {
		t.Fatalf("checkout branch changed to %q", got)
	}
	status := git(t, wt, "status", "--porcelain")
	if !strings.Contains(status, "README.md") || !strings.Contains(status, "untracked.txt") {
		t.Fatalf("existing changes not preserved after recovery: %s", status)
	}
	if got := git(t, wt, "branch", "--show-current"); strings.TrimSpace(got) != branch {
		t.Fatalf("recovered branch = %q, want %q", strings.TrimSpace(got), branch)
	}
	if !strings.Contains(git(t, repo, "worktree", "list", "--porcelain"), wt) {
		t.Fatal("recovered worktree was not registered")
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

func TestMergeBranchUsesConfiguredEnvironment(t *testing.T) {
	repo := initRepo(t)
	g := New()
	wt := filepath.Join(t.TempDir(), "wt-env")
	if err := g.AddWorktree(repo, wt, "omo/job-env"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt, "environment.txt"), []byte("configured\n"), 0o644)
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-m", "environment")

	env := append([]string(nil), os.Environ()...)
	env = append(env,
		"GIT_AUTHOR_NAME=Configured Agent",
		"GIT_AUTHOR_EMAIL=configured-agent@example.com",
		"GIT_COMMITTER_NAME=Configured Agent",
		"GIT_COMMITTER_EMAIL=configured-agent@example.com",
	)
	g.SetEnvironment(env)
	if err := g.MergeBranch(repo, "omo/job-env"); err != nil {
		t.Fatal(err)
	}
	identity := strings.TrimSpace(git(t, repo, "show", "-s", "--format=%an <%ae>|%cn <%ce>", "HEAD"))
	want := "Configured Agent <configured-agent@example.com>|Configured Agent <configured-agent@example.com>"
	if identity != want {
		t.Fatalf("merge identity = %q, want %q", identity, want)
	}
}
