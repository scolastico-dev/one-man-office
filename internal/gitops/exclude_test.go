package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// When the office lives inside the repo it works on, .omo (database, logs,
// worktrees) would otherwise show up as untracked — and a developer agent
// running `git add .` would commit omo's own state into the user's repo.
func TestExcludeHidesOfficeStateFromStatus(t *testing.T) {
	repo := initRepo(t)
	g := New()
	os.MkdirAll(filepath.Join(repo, ".omo", "logs"), 0o755)
	os.WriteFile(filepath.Join(repo, ".omo", "omo.db"), []byte("x"), 0o644)

	if out := git(t, repo, "status", "--porcelain"); !strings.Contains(out, ".omo") {
		t.Fatalf("precondition: .omo should be visible before excluding:\n%s", out)
	}
	if err := g.Exclude(repo, ".omo/"); err != nil {
		t.Fatal(err)
	}
	if out := git(t, repo, "status", "--porcelain"); strings.Contains(out, ".omo") {
		t.Fatalf(".omo still visible to git:\n%s", out)
	}
}

func TestExcludeIsIdempotent(t *testing.T) {
	repo := initRepo(t)
	g := New()
	for i := 0; i < 3; i++ {
		if err := g.Exclude(repo, ".omo/"); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), ".omo/"); n != 1 {
		t.Fatalf("pattern written %d times, want 1:\n%s", n, raw)
	}
}

// It must never touch a tracked .gitignore — that is the user's file.
func TestExcludeLeavesGitignoreAlone(t *testing.T) {
	repo := initRepo(t)
	ignore := filepath.Join(repo, ".gitignore")
	os.WriteFile(ignore, []byte("node_modules\n"), 0o644)
	git(t, repo, "add", ".gitignore")
	git(t, repo, "commit", "-m", "add gitignore")

	if err := New().Exclude(repo, ".omo/"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(ignore)
	if string(raw) != "node_modules\n" {
		t.Fatalf(".gitignore was modified: %q", raw)
	}
}

func TestExcludeOnNonRepoIsAnError(t *testing.T) {
	if err := New().Exclude(t.TempDir(), ".omo/"); err == nil {
		t.Fatal("expected an error for a directory that is not a repo")
	}
}
