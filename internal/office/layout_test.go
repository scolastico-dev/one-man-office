package office

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const layoutModels = `
models:
  any:
    cmd: claude
roles:
  ceo: any
  product_manager: any
  developer: any
  reviewer: any
  freelancer: any
  smokealarm: any
  firefighter: any
`

// Topology 1: the office is the repo. omo's own state must not pollute it.
func TestSingleRepoOfficeExcludesItsOwnState(t *testing.T) {
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644)
	gitInit(t, repo)

	if _, err := Setup(repo); err != nil {
		t.Fatal(err)
	}
	o, err := Open(repo, true)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	// Setup discovered the office's own repo.
	if len(o.Cfg.Repos) != 1 {
		t.Fatalf("expected the office repo to be configured, got %v", o.Cfg.Repos)
	}
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), ".omo") {
		t.Fatalf("omo's state is visible in the user's repo:\n%s", out)
	}
}

// Topology 2: a parent directory of several repos. Nothing is excluded
// there (the office is outside every repo), and all repos are offered.
func TestLandscapeOfficeConfiguresEveryRepo(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"api", "ui"} {
		p := filepath.Join(dir, name)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "README.md"), []byte("x\n"), 0o644)
		gitInit(t, p)
	}
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	o, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	if len(o.Cfg.Repos) != 2 || o.Cfg.Repos["api"] == "" || o.Cfg.Repos["ui"] == "" {
		t.Fatalf("landscape repos not configured: %v", o.Cfg.Repos)
	}
	// The CEO is told it spans two repositories.
	ctx := o.Sup.RepoContext()
	if !strings.Contains(ctx, "2 repositories") {
		t.Fatalf("CEO context does not describe the landscape:\n%s", ctx)
	}
}

// A developer job must get a worktree in the right repo in both layouts.
func TestWorktreeLandsInTheNamedRepo(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"api", "ui"} {
		p := filepath.Join(dir, name)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, name+".txt"), []byte(name+"\n"), 0o644)
		gitInit(t, p)
	}
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	o, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()

	wt := filepath.Join(dir, ".omo", "worktrees", "ui-1")
	if err := o.Sup.Git.AddWorktree(o.Cfg.Repos["ui"], wt, "omo/job-1"); err != nil {
		t.Fatal(err)
	}
	// The worktree must contain ui's file, not api's.
	if _, err := os.Stat(filepath.Join(wt, "ui.txt")); err != nil {
		t.Fatalf("worktree is not a checkout of ui: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "api.txt")); !os.IsNotExist(err) {
		t.Fatal("worktree contains another repo's files")
	}
}

// --mock must work in any office, including a landscape with no repo named
// "demo": the mock PM is handed a real repo key from the config.
func TestMockRunsInALandscapeWithArbitraryRepoNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"api", "ui"} {
		p := filepath.Join(dir, name)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "README.md"), []byte("x\n"), 0o644)
		gitInit(t, p)
	}
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	o, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if err := o.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 120*time.Second, "developer job merged in a landscape office", func() bool {
		return developerMergeFinished(o)
	})
	// It landed in the first repo (api), not somewhere invented.
	if _, err := os.Stat(filepath.Join(dir, "api", "hello.txt")); err != nil {
		t.Fatalf("merge did not land in the api repo: %v", err)
	}
}
