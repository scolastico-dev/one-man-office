package office

import (
	"os"
	"path/filepath"
	"testing"
)

func mkRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Topology 1: omo is run inside a single repository.
func TestDiscoverSingleRepoOffice(t *testing.T) {
	dir := t.TempDir()
	mkRepo(t, dir)
	repos, layout := DiscoverRepos(dir)
	if layout != LayoutSingleRepo {
		t.Fatalf("layout = %v, want single-repo", layout)
	}
	if len(repos) != 1 {
		t.Fatalf("repos = %v, want exactly the office repo", repos)
	}
	if repos[filepath.Base(dir)] != dir {
		t.Fatalf("repo not keyed by its directory name: %v", repos)
	}
}

// Topology 2: omo is run in a parent directory of several repositories.
func TestDiscoverLandscape(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"api", "ui", "worker"} {
		mkRepo(t, filepath.Join(dir, name))
	}
	// Noise that must not be picked up.
	os.MkdirAll(filepath.Join(dir, "notes"), 0o755)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644)

	repos, layout := DiscoverRepos(dir)
	if layout != LayoutLandscape {
		t.Fatalf("layout = %v, want landscape", layout)
	}
	if len(repos) != 3 {
		t.Fatalf("repos = %v, want api/ui/worker", repos)
	}
	for _, name := range []string{"api", "ui", "worker"} {
		if repos[name] != filepath.Join(dir, name) {
			t.Errorf("missing or wrong path for %s: %v", name, repos)
		}
	}
}

// A repo containing further repos is still the office repo: omo must not
// descend into a monorepo's vendored checkouts.
func TestDiscoverPrefersOfficeRepoOverChildren(t *testing.T) {
	dir := t.TempDir()
	mkRepo(t, dir)
	mkRepo(t, filepath.Join(dir, "vendored"))
	repos, layout := DiscoverRepos(dir)
	if layout != LayoutSingleRepo || len(repos) != 1 {
		t.Fatalf("layout=%v repos=%v, want just the office repo", layout, repos)
	}
}

func TestDiscoverEmptyDir(t *testing.T) {
	repos, layout := DiscoverRepos(t.TempDir())
	if layout != LayoutUnknown || len(repos) != 0 {
		t.Fatalf("layout=%v repos=%v, want nothing discovered", layout, repos)
	}
}

func TestDiscoverSkipsDotDirsAndWorktrees(t *testing.T) {
	dir := t.TempDir()
	mkRepo(t, filepath.Join(dir, "api"))
	mkRepo(t, filepath.Join(dir, ".hidden"))
	mkRepo(t, filepath.Join(dir, ".omo", "worktrees", "api-1"))
	repos, _ := DiscoverRepos(dir)
	if len(repos) != 1 || repos["api"] == "" {
		t.Fatalf("repos = %v, want only api", repos)
	}
}

// A repo whose .git is a file (a worktree or submodule) still counts.
func TestDiscoverHandlesGitFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "linked")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: /elsewhere\n"), 0o644)
	repos, _ := DiscoverRepos(dir)
	if repos["linked"] == "" {
		t.Fatalf("repos = %v, want linked", repos)
	}
}
