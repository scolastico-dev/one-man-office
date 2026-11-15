//go:build !windows

package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hostileDotGit replaces the .git file of the worktree wt by a fresh
// repository with one commit whose config defines a clean filter, an
// fsmonitor hook and an external diff, each writing marker when it runs, and
// a .gitattributes that selects the filter. It then changes the committed
// file so that a diff against the working tree has to read it.
func hostileDotGit(t *testing.T, wt, marker string) {
	t.Helper()
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	tg(t, wt, "init", "-q", "-b", "main")
	write(t, filepath.Join(wt, "f.txt"), "hostile\n")
	tg(t, wt, "add", "f.txt")
	tg(t, wt, "commit", "-q", "-m", "hostile")
	cmd := "touch '" + marker + "'; cat"
	tg(t, wt, "config", "filter.x.clean", cmd)
	tg(t, wt, "config", "filter.x.smudge", cmd)
	tg(t, wt, "config", "filter.x.required", "true")
	tg(t, wt, "config", "core.fsmonitor", "touch '"+marker+"'; false #")
	tg(t, wt, "config", "diff.external", "touch '"+marker+"'; true #")
	write(t, filepath.Join(wt, ".gitattributes"), "* filter=x\n")
	write(t, filepath.Join(wt, "f.txt"), "hostile, changed\n")
}

func noMarker(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a command from the repository planted in the worktree ran")
	}
}

func TestWorktreeGitIgnoresReplacedDotGit(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	wt := branchWorktree(t, repo, "feat")
	marker := filepath.Join(t.TempDir(), "ran")
	hostileDotGit(t, wt, marker)
	ctx := context.Background()

	res, err := g.WorktreeChanges(ctx, repo, wt, DefaultDiffLimits)
	noMarker(t, marker)
	if err != nil {
		t.Fatal(err)
	}
	// What is shown is the worktree against the branch's own HEAD.
	if f := byPath(res)["f.txt"]; f.Status != "modified" || !strings.Contains(string(f.Patch), "-f1") {
		t.Errorf("f.txt = %+v", f)
	}
	if _, err := g.WorktreeFileDiff(ctx, repo, wt, "f.txt"); err != nil {
		t.Error(err)
	}
	noMarker(t, marker)
	if _, err := g.Status(repo, wt); err != nil {
		t.Error(err)
	}
	noMarker(t, marker)
	if _, err := g.RemoveWorktreeIfClean(repo, wt, "feat"); err != nil {
		t.Error(err)
	}
	noMarker(t, marker)
}

func TestWorktreeGitIgnoresOuterRepository(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	outer := t.TempDir()
	tg(t, outer, "init", "-q", "-b", "main")
	write(t, filepath.Join(outer, "outer-tracked.txt"), "outer\n")
	tg(t, outer, "add", ".")
	tg(t, outer, "commit", "-q", "-m", "outer")
	write(t, filepath.Join(outer, "outer-tracked.txt"), "outer, changed\n")
	write(t, filepath.Join(outer, "outer-secret.txt"), "secret\n")
	wt := filepath.Join(outer, "worktrees", "feat")
	tg(t, repo, "worktree", "add", "-q", "-b", "feat", wt, "main")
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	check := func(what string, paths []string) {
		t.Helper()
		for _, p := range paths {
			if strings.Contains(p, "outer-") {
				t.Errorf("%s reports %q of the outer repository", what, p)
			}
		}
	}
	res, err := g.WorktreeChanges(ctx, repo, wt, DefaultDiffLimits)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range res.Files {
		paths = append(paths, f.Path)
	}
	check("WorktreeChanges", paths)
	if res.Head != rev(t, repo, "feat") {
		t.Errorf("head %s, want the branch's", res.Head)
	}
	for _, p := range []string{"outer-tracked.txt", "outer-secret.txt", "../outer-tracked.txt", "../../outer-secret.txt"} {
		if f, err := g.WorktreeFileDiff(ctx, repo, wt, p); err == nil {
			t.Errorf("WorktreeFileDiff(%q) = %+v", p, f)
		}
	}
	files, err := g.Status(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	check("Status", files)
}

// A submodule entry in the worktree's index whose folder holds a hostile
// repository: git would look into it to see whether it is dirty.
func TestWorktreeGitDoesNotEnterSubmodules(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	wt := branchWorktree(t, repo, "feat")
	marker := filepath.Join(t.TempDir(), "ran")
	sub := filepath.Join(wt, "sub")
	tg(t, wt, "init", "-q", "-b", "main", "sub")
	write(t, filepath.Join(sub, "s.txt"), "s\n")
	tg(t, sub, "add", "s.txt")
	tg(t, sub, "commit", "-q", "-m", "s")
	tg(t, wt, "update-index", "--add", "--cacheinfo", "160000,"+rev(t, sub, "HEAD")+",sub")
	cmd := "touch '" + marker + "'; cat"
	tg(t, sub, "config", "filter.x.clean", cmd)
	tg(t, sub, "config", "core.fsmonitor", "touch '"+marker+"'; false #")
	write(t, filepath.Join(sub, ".gitattributes"), "* filter=x\n")
	// Same size: only reading the content through the filter tells.
	write(t, filepath.Join(sub, "s.txt"), "t\n")
	ctx := context.Background()
	if _, err := g.Status(repo, wt); err != nil {
		t.Fatal(err)
	}
	noMarker(t, marker)
	if _, err := g.WorktreeChanges(ctx, repo, wt, DefaultDiffLimits); err != nil {
		t.Fatal(err)
	}
	noMarker(t, marker)
}

// The worktree folder replaced by a link to a sibling worktree.
func TestWorktreeGitRefusesLinkedFolder(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	tg(t, repo, "worktree", "add", "-q", "-b", "a", a, "main")
	tg(t, repo, "worktree", "add", "-q", "-b", "b", b, "main")
	write(t, filepath.Join(b, "sibling-secret.txt"), "secret\n")
	moved := filepath.Join(t.TempDir(), "a-moved")
	if err := os.Rename(a, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := g.WorktreeChanges(ctx, repo, a, DefaultDiffLimits); !errors.Is(err, ErrNotAFolder) {
		t.Errorf("WorktreeChanges: err = %v, want ErrNotAFolder", err)
	}
	if _, err := g.WorktreeFileDiff(ctx, repo, a, "sibling-secret.txt"); !errors.Is(err, ErrNotAFolder) {
		t.Errorf("WorktreeFileDiff: err = %v, want ErrNotAFolder", err)
	}
	if _, err := g.Status(repo, a); !errors.Is(err, ErrNotAFolder) {
		t.Errorf("Status: err = %v, want ErrNotAFolder", err)
	}
	if _, err := g.RemoveWorktreeIfClean(repo, a, "a"); !errors.Is(err, ErrNotAFolder) {
		t.Errorf("RemoveWorktreeIfClean: err = %v, want ErrNotAFolder", err)
	}
	if err := g.ForceRemoveWorktree(repo, a); !errors.Is(err, ErrNotAFolder) {
		t.Errorf("ForceRemoveWorktree: err = %v, want ErrNotAFolder", err)
	}
	if _, err := os.Stat(filepath.Join(b, "sibling-secret.txt")); err != nil {
		t.Errorf("the sibling worktree was touched: %v", err)
	}
	// A folder the repository does not record at all.
	if _, err := g.Status(repo, t.TempDir()); !errors.Is(err, ErrNotRecorded) {
		t.Errorf("unrecorded folder: err = %v, want ErrNotRecorded", err)
	}
}

// A filter defined in the worktree's own config.worktree, which git reads
// once the repository enables per-worktree config.
func TestWorktreeGitRefusesCommandsInWorktreeConfig(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	tg(t, repo, "config", "extensions.worktreeConfig", "true")
	tg(t, repo, "config", "--worktree", "user.name", "main-worktree-setting")
	wt := branchWorktree(t, repo, "feat")
	marker := filepath.Join(t.TempDir(), "ran")
	ctx := context.Background()
	// What git copied from the main worktree and sparse checkout settings
	// are fine.
	tg(t, wt, "config", "--worktree", "user.name", "main-worktree-setting")
	tg(t, wt, "config", "--worktree", "core.sparseCheckout", "false")
	if _, err := g.Status(repo, wt); err != nil {
		t.Fatalf("honest worktree config: %v", err)
	}
	tg(t, wt, "config", "--worktree", "filter.x.clean", "touch '"+marker+"'; cat")
	write(t, filepath.Join(wt, ".gitattributes"), "* filter=x\n")
	write(t, filepath.Join(wt, "f.txt"), "changed\n")
	if _, err := g.Status(repo, wt); !errors.Is(err, ErrWorktreeConfig) {
		t.Errorf("Status: err = %v, want ErrWorktreeConfig", err)
	}
	if _, err := g.WorktreeChanges(ctx, repo, wt, DefaultDiffLimits); !errors.Is(err, ErrWorktreeConfig) {
		t.Errorf("WorktreeChanges: err = %v, want ErrWorktreeConfig", err)
	}
	noMarker(t, marker)
	// A setting the main worktree has with another value is refused too,
	// and so is an include.
	tg(t, wt, "config", "--worktree", "--unset", "filter.x.clean")
	tg(t, wt, "config", "--worktree", "user.name", "other")
	if _, err := g.Status(repo, wt); !errors.Is(err, ErrWorktreeConfig) {
		t.Errorf("changed value: err = %v, want ErrWorktreeConfig", err)
	}
	tg(t, wt, "config", "--worktree", "user.name", "main-worktree-setting")
	tg(t, wt, "config", "--worktree", "include.path", filepath.Join(t.TempDir(), "x"))
	if _, err := g.Status(repo, wt); !errors.Is(err, ErrWorktreeConfig) {
		t.Errorf("include: err = %v, want ErrWorktreeConfig", err)
	}
	noMarker(t, marker)
}

// With the agents' folder set, git may not look for a repository there:
// only resolved worktrees run there.
func TestAgentFolderRefusesDiscovery(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	root := t.TempDir()
	wt := filepath.Join(root, "p", "1")
	tg(t, repo, "worktree", "add", "-q", "-b", "feat", wt, "main")
	g.SetAgentFolders(root)
	if _, err := g.RevParse(wt, "HEAD"); !errors.Is(err, ErrAgentFolder) {
		t.Errorf("RevParse in the worktree: err = %v, want ErrAgentFolder", err)
	}
	if _, err := g.CurrentBranch(wt); !errors.Is(err, ErrAgentFolder) {
		t.Errorf("CurrentBranch in the worktree: err = %v, want ErrAgentFolder", err)
	}
	if _, _, err := g.runCapped(context.Background(), filepath.Join(wt, "sub"), 100, "status"); !errors.Is(err, ErrAgentFolder) {
		t.Errorf("runCapped below the worktree: err = %v, want ErrAgentFolder", err)
	}
	if _, err := g.Status(repo, wt); err != nil {
		t.Errorf("Status through the records: %v", err)
	}
	if _, err := g.WorktreeChanges(context.Background(), repo, wt, DefaultDiffLimits); err != nil {
		t.Errorf("WorktreeChanges through the records: %v", err)
	}
	if _, err := g.RevParse(repo, "HEAD"); err != nil {
		t.Errorf("RevParse in the repository: %v", err)
	}
}

// replaceIndex puts what make creates in place of the index of the worktree
// wt, in the git directory the repository records for it.
func replaceIndex(t *testing.T, wt string, make func(path string) error) {
	t.Helper()
	index := filepath.Join(strings.TrimSpace(tg(t, wt, "rev-parse", "--absolute-git-dir")), "index")
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := make(index); err != nil {
		t.Fatal(err)
	}
}

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

func TestWorktreeIndexMustBeARegularFile(t *testing.T) {
	cases := map[string]func(path string) error{
		"link to /dev/zero": func(p string) error { return os.Symlink("/dev/zero", p) },
		"named pipe":        func(p string) error { return syscall.Mkfifo(p, 0o600) },
		"oversized": func(p string) error {
			f, err := os.Create(p)
			if err != nil {
				return err
			}
			defer f.Close()
			return f.Truncate(MaxIndexSize + 1) // sparse: takes no disk space
		},
	}
	for name, make := range cases {
		t.Run(name, func(t *testing.T) {
			g := testGit(t)
			repo := newTestRepo(t)
			wt := branchWorktree(t, repo, "feat")
			replaceIndex(t, wt, make)
			ctx := context.Background()
			err := returnsWithin(t, 5*time.Second, "WorktreeChanges", func() error {
				_, err := g.WorktreeChanges(ctx, repo, wt, DefaultDiffLimits)
				return err
			})
			if !errors.Is(err, ErrUnsafeFile) {
				t.Errorf("WorktreeChanges: err = %v, want ErrUnsafeFile", err)
			}
			err = returnsWithin(t, 5*time.Second, "WorktreeFileDiff", func() error {
				_, err := g.WorktreeFileDiff(ctx, repo, wt, "f.txt")
				return err
			})
			if !errors.Is(err, ErrUnsafeFile) {
				t.Errorf("WorktreeFileDiff: err = %v, want ErrUnsafeFile", err)
			}
			err = returnsWithin(t, 5*time.Second, "Status", func() error {
				_, err := g.Status(repo, wt)
				return err
			})
			if !errors.Is(err, ErrUnsafeFile) {
				t.Errorf("Status: err = %v, want ErrUnsafeFile", err)
			}
		})
	}
}

// The copy of the index ends with the context.
func TestCopyRegularHonoursContext(t *testing.T) {
	src := filepath.Join(t.TempDir(), "index")
	write(t, src, strings.Repeat("x", 1<<20))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyRegular(ctx, src, filepath.Join(t.TempDir(), "copy"), MaxIndexSize); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
