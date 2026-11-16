package gitops

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func addWorktree(t *testing.T, repo, branch string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt")
	tg(t, repo, "worktree", "add", "-q", "-b", branch, wt, "main")
	return wt
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func TestStatusListsUntrackedAndModifiedButNotIgnored(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	wt := addWorktree(t, repo, "omo/1-x")
	write(t, filepath.Join(wt, "build", "x"), "ignored\n")
	write(t, filepath.Join(wt, "new.txt"), "untracked\n")
	write(t, filepath.Join(wt, "deep", "dir", "n.txt"), "untracked in a new folder\n")
	write(t, filepath.Join(wt, "f.txt"), "edited\n")
	write(t, filepath.Join(wt, "with space.txt"), "untracked\n")
	tg(t, wt, "mv", "other.txt", "renamed.txt")
	got, err := g.Status(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"deep/dir/n.txt", "f.txt", "new.txt", "renamed.txt", "with space.txt"}
	if !reflect.DeepEqual(sorted(got), want) {
		t.Fatalf("Status = %q, want %q", got, want)
	}
}

func TestStatusCleanAndIgnoredOnly(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	wt := addWorktree(t, repo, "omo/1-x")
	got, err := g.Status(repo, wt)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("clean: Status = %#v, %v", got, err)
	}
	write(t, filepath.Join(wt, "build", "x"), "ignored\n")
	if got, err := g.Status(repo, wt); err != nil || len(got) != 0 {
		t.Fatalf("ignored only: Status = %#v, %v", got, err)
	}
	gitDir := strings.TrimSpace(tg(t, wt, "rev-parse", "--absolute-git-dir"))
	if _, err := os.Stat(filepath.Join(gitDir, "index.lock")); err == nil {
		t.Fatal("index.lock left behind")
	}
}

func TestRemoveWorktreeIfCleanKeepsDirty(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	for name, dirty := range map[string]func(wt string){
		"untracked": func(wt string) { write(t, filepath.Join(wt, "new.txt"), "keep\n") },
		"modified":  func(wt string) { write(t, filepath.Join(wt, "f.txt"), "keep\n") },
	} {
		t.Run(name, func(t *testing.T) {
			wt := addWorktree(t, repo, "omo/"+name)
			dirty(wt)
			before := snapshot(t, wt)
			files, err := g.RemoveWorktreeIfClean(repo, wt, "omo/"+name)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"untracked": "new.txt", "modified": "f.txt"}[name]
			if !reflect.DeepEqual(files, []string{want}) {
				t.Fatalf("dirty = %q, want [%s]", files, want)
			}
			sameSnapshot(t, "worktree", before, snapshot(t, wt))
			if !strings.Contains(tg(t, repo, "worktree", "list", "--porcelain"), wt) {
				t.Fatal("registration removed")
			}
		})
	}
}

func TestRemoveWorktreeIfCleanRemovesIgnoredFiles(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	wt := addWorktree(t, repo, "omo/1-x")
	write(t, filepath.Join(wt, "build", "x"), "ignored\n")
	files, err := g.RemoveWorktreeIfClean(repo, wt, "omo/1-x")
	if err != nil || len(files) != 0 {
		t.Fatalf("dirty = %q, err = %v", files, err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("folder still exists")
	}
	if strings.Contains(tg(t, repo, "worktree", "list", "--porcelain"), wt) {
		t.Fatal("registration left behind")
	}
	if exists, _ := g.BranchExists(repo, "omo/1-x"); !exists {
		t.Fatal("branch removed with the worktree")
	}
}

func TestRemoveWorktreeOfMissingFolderDropsOnlyItsRegistration(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	gone := addWorktree(t, repo, "omo/gone")
	other := addWorktree(t, repo, "omo/other-gone")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}
	if files, err := g.RemoveWorktreeIfClean(repo, gone, "omo/gone"); err != nil || len(files) != 0 {
		t.Fatalf("dirty = %q, err = %v", files, err)
	}
	list := tg(t, repo, "worktree", "list", "--porcelain")
	if strings.Contains(list, gone) {
		t.Fatal("registration of the missing folder left behind")
	}
	if !strings.Contains(list, other) {
		t.Fatal("another missing worktree was pruned too")
	}
	// Unknown folders and repeated calls are no-ops.
	if files, err := g.RemoveWorktreeIfClean(repo, gone, "omo/gone"); err != nil || len(files) != 0 {
		t.Fatalf("repeat: dirty = %q, err = %v", files, err)
	}
	if err := g.ForceRemoveWorktree(repo, gone); err != nil {
		t.Fatalf("force on unknown folder: %v", err)
	}
}

func TestForceRemoveWorktreeDiscardsChangesKeepsBranch(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	wt := addWorktree(t, repo, "omo/1-x")
	write(t, filepath.Join(wt, "new.txt"), "discard\n")
	write(t, filepath.Join(wt, "f.txt"), "discard\n")
	if err := g.ForceRemoveWorktree(repo, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("folder still exists")
	}
	if strings.Contains(tg(t, repo, "worktree", "list", "--porcelain"), wt) {
		t.Fatal("registration left behind")
	}
	if exists, _ := g.BranchExists(repo, "omo/1-x"); !exists {
		t.Fatal("branch removed")
	}
}

func TestForceRemoveWorktreeRefusesTheMainFolder(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	write(t, filepath.Join(repo, "f.txt"), "user edit\n")
	if err := g.ForceRemoveWorktree(repo, repo); err == nil {
		t.Fatal("main folder removed")
	}
	if read(t, filepath.Join(repo, "f.txt")) != "user edit\n" {
		t.Fatal("user edit lost")
	}
}

func TestDeleteBranch(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	if err := g.DeleteBranch(repo, "omo/missing", "main"); err != nil {
		t.Fatalf("missing branch: %v", err)
	}
	tip := commitOn(t, repo, "omo/unmerged", "x", map[string]string{"x.txt": "x\n"})
	if err := g.DeleteBranch(repo, "omo/unmerged", "main"); !errors.Is(err, ErrBranchMoved) {
		t.Fatalf("err = %v, want ErrBranchMoved for a branch not at the expected tip", err)
	}
	if exists, _ := g.BranchExists(repo, "omo/unmerged"); !exists {
		t.Fatal("branch at another tip deleted")
	}
	if err := g.DeleteBranch(repo, "omo/unmerged", tip); err != nil {
		t.Fatal(err)
	}
	if exists, _ := g.BranchExists(repo, "omo/unmerged"); exists {
		t.Fatal("branch still exists")
	}
	if err := g.DeleteBranch(repo, "main", ""); err == nil {
		t.Fatal("delete without an expected tip accepted")
	}
	wt := addWorktree(t, repo, "omo/checked-out")
	if err := g.DeleteBranch(repo, "omo/checked-out", "main"); err == nil {
		t.Fatal("branch checked out in a worktree deleted")
	}
	if _, err := os.Stat(filepath.Join(wt, "f.txt")); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteBranch(repo, "-D", "main"); err == nil {
		t.Fatal("option-like branch name accepted")
	}
}

// Commits added between the caller's check and the delete survive.
func TestDeleteBranchKeepsCommitsAddedAfterTheCheck(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	tip := commitOn(t, repo, "omo/1-x", "x", map[string]string{"x.txt": "x\n"})
	var late string
	g.pause = func(point string) {
		if point == "delete-branch" {
			late = commitOn(t, repo, "omo/1-x", "late", map[string]string{"y.txt": "y\n"})
		}
	}
	err := g.DeleteBranch(repo, "omo/1-x", tip)
	if !errors.Is(err, ErrBranchMoved) {
		t.Fatalf("err = %v, want ErrBranchMoved", err)
	}
	if got := rev(t, repo, "refs/heads/omo/1-x"); got != late {
		t.Fatalf("branch at %s, want the late commit %s", got, late)
	}
}

// A checkout of the branch that slips in before the delete gets it back.
func TestDeleteBranchRestoresBranchCheckedOutMeanwhile(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	tip := commitOn(t, repo, "omo/1-x", "x", map[string]string{"x.txt": "x\n"})
	wt := filepath.Join(t.TempDir(), "wt")
	g.pause = func(point string) {
		if point == "delete-branch" {
			tg(t, repo, "worktree", "add", "-q", wt, "omo/1-x")
		}
	}
	if err := g.DeleteBranch(repo, "omo/1-x", tip); err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("err = %v, want the branch reported as restored", err)
	}
	if got := rev(t, wt, "HEAD"); got != tip {
		t.Fatalf("HEAD in the new checkout = %s, want %s", got, tip)
	}
}

func TestWorktreeOf(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	if wt, found, err := g.WorktreeOf(repo, "main"); err != nil || !found || wt != repo {
		t.Fatalf("main: %q %v %v", wt, found, err)
	}
	linked := addWorktree(t, repo, "omo/1-x")
	if wt, found, err := g.WorktreeOf(repo, "omo/1-x"); err != nil || !found || wt != linked {
		t.Fatalf("linked: %q %v %v", wt, found, err)
	}
	tg(t, repo, "branch", "free", "main")
	if _, found, err := g.WorktreeOf(repo, "free"); err != nil || found {
		t.Fatalf("free: %v %v", found, err)
	}
	// Called from a linked worktree, the answer is the same.
	if wt, found, err := g.WorktreeOf(linked, "main"); err != nil || !found || wt != repo {
		t.Fatalf("from linked: %q %v %v", wt, found, err)
	}
}

func TestAddWorktreeForBranch(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	tg(t, repo, "branch", "omo/2-y", "main")
	dir := filepath.Join(t.TempDir(), "p", "2")
	if err := g.AddWorktreeForBranch(repo, dir, "omo/2-y"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(tg(t, dir, "branch", "--show-current")); got != "omo/2-y" {
		t.Fatalf("branch = %q", got)
	}
	if got := strings.TrimSpace(tg(t, repo, "branch", "--show-current")); got != "main" {
		t.Fatalf("repo folder switched to %q", got)
	}
	before := tg(t, repo, "worktree", "list", "--porcelain")
	if err := g.AddWorktreeForBranch(repo, filepath.Join(t.TempDir(), "m"), "omo/none"); err == nil {
		t.Fatal("missing branch accepted")
	}
	if err := g.AddWorktreeForBranch(repo, filepath.Join(t.TempDir(), "d"), "omo/2-y"); err == nil {
		t.Fatal("branch checked out twice")
	}
	if after := tg(t, repo, "worktree", "list", "--porcelain"); after != before {
		t.Fatalf("failed adds left registrations:\n%s", after)
	}
	if exists, _ := g.BranchExists(repo, "omo/none"); exists {
		t.Fatal("failed add created a branch")
	}
}

func TestBranchExistsRevParseIsAncestor(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	base := rev(t, repo, "main")
	tip := commitOn(t, repo, "feature", "x", map[string]string{"x.txt": "x\n"})
	if ok, err := g.BranchExists(repo, "feature"); err != nil || !ok {
		t.Fatalf("feature: %v %v", ok, err)
	}
	if ok, err := g.BranchExists(repo, "nope"); err != nil || ok {
		t.Fatalf("nope: %v %v", ok, err)
	}
	if id, err := g.RevParse(repo, "feature"); err != nil || id != tip {
		t.Fatalf("RevParse = %q %v", id, err)
	}
	if _, err := g.RevParse(repo, "nope"); err == nil {
		t.Fatal("RevParse of a missing ref succeeded")
	}
	if ok, err := g.IsAncestor(repo, base, "feature"); err != nil || !ok {
		t.Fatalf("base in feature: %v %v", ok, err)
	}
	if ok, err := g.IsAncestor(repo, "feature", "main"); err != nil || ok {
		t.Fatalf("feature in main: %v %v", ok, err)
	}
}

func TestRemoveWorktreeIfCleanKeepsCommitsOnlyHeadHas(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	wt := addWorktree(t, repo, "omo/1-x")
	tg(t, wt, "checkout", "-q", "--detach")
	// A detached HEAD at a commit some branch has is no loss.
	if files, err := g.RemoveWorktreeIfClean(repo, wt, "omo/1-x"); err != nil || len(files) != 0 {
		t.Fatalf("detached at a branch commit: dirty = %q, err = %v", files, err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree with nothing to lose kept")
	}

	wt = addWorktree(t, repo, "omo/2-y")
	tg(t, wt, "checkout", "-q", "--detach")
	write(t, filepath.Join(wt, "x.txt"), "agent work\n")
	tg(t, wt, "add", "x.txt")
	tg(t, wt, "commit", "-q", "-m", "agent work")
	lost := rev(t, wt, "HEAD")
	files, err := g.RemoveWorktreeIfClean(repo, wt, "omo/2-y")
	var unref *UnreferencedCommitsError
	if !errors.Is(err, ErrUnreferencedCommits) || !errors.As(err, &unref) || len(files) != 0 {
		t.Fatalf("err = %v, dirty = %q, want ErrUnreferencedCommits", err, files)
	}
	if !reflect.DeepEqual(unref.Commits, []string{lost}) || !strings.Contains(err.Error(), lost[:12]) {
		t.Fatalf("commits = %q, message %v; want %s named", unref.Commits, err, lost)
	}
	if _, err := os.Stat(filepath.Join(wt, "x.txt")); err != nil {
		t.Fatal("worktree removed")
	}

	// Once a tag holds the commit, removing the worktree loses nothing.
	tg(t, repo, "tag", "keep", lost)
	if files, err := g.RemoveWorktreeIfClean(repo, wt, "omo/2-y"); err != nil || len(files) != 0 {
		t.Fatalf("tagged: dirty = %q, err = %v", files, err)
	}
}

func TestRemoveWorktreeIfCleanRefusesOperationInProgress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the sequence editor is a sed command")
	}
	for name, start := range map[string]func(t *testing.T, wt string){
		"stopped rebase": func(t *testing.T, wt string) {
			write(t, filepath.Join(wt, "a.txt"), "a\n")
			tg(t, wt, "add", "a.txt")
			tg(t, wt, "commit", "-q", "-m", "one")
			// Stops at the commit with a clean status and a detached HEAD.
			tg(t, wt, "-c", "sequence.editor=sed -i s/^pick/edit/", "rebase", "-q", "-i", "HEAD~1")
		},
		"bisect": func(t *testing.T, wt string) {
			tg(t, wt, "bisect", "start")
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newTestRepo(t)
			g := testGit(t)
			wt := filepath.Join(t.TempDir(), "wt")
			tg(t, repo, "worktree", "add", "-q", "-b", "omo/op", wt, "main")
			start(t, wt)
			if st := tg(t, wt, "status", "--porcelain"); st != "" {
				t.Fatalf("setup: status not clean: %q", st)
			}
			files, err := g.RemoveWorktreeIfClean(repo, wt, "omo/op")
			if !errors.Is(err, ErrOperationInProgress) || len(files) != 0 {
				t.Fatalf("err = %v, dirty = %q, want ErrOperationInProgress", err, files)
			}
			if _, err := os.Stat(wt); err != nil {
				t.Fatal("worktree removed")
			}
		})
	}
}

func TestRepoLockIsSharedByEveryPathIntoARepository(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	linked := addWorktree(t, repo, "omo/1-x")
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	want := g.repoLock(repo)
	paths := []string{linked, sub, filepath.Join(repo, ".git")}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(repo, link); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, link)
	}
	for _, p := range paths {
		if g.repoLock(p) != want {
			t.Errorf("%s has its own lock", p)
		}
	}
	if g.repoLock(newTestRepo(t)) == want {
		t.Error("another repository shares the lock")
	}
	if g.repoLock(filepath.Join(t.TempDir(), "missing")) == want {
		t.Error("a path outside any repository shares the lock")
	}
}

func TestForceRemoveWorktreeRefusesLockedWorktree(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	wt := addWorktree(t, repo, "omo/1-x")
	write(t, filepath.Join(wt, "new.txt"), "keep\n")
	tg(t, repo, "worktree", "lock", "--reason", "on a removable disk", wt)
	err := g.ForceRemoveWorktree(repo, wt)
	if !errors.Is(err, ErrWorktreeLocked) || !strings.Contains(err.Error(), "on a removable disk") {
		t.Fatalf("err = %v, want ErrWorktreeLocked with the reason", err)
	}
	if read(t, filepath.Join(wt, "new.txt")) != "keep\n" {
		t.Fatal("locked worktree changed")
	}
	// A locked worktree whose folder is missing keeps its registration.
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if err := g.ForceRemoveWorktree(repo, wt); !errors.Is(err, ErrWorktreeLocked) {
		t.Fatalf("missing folder: err = %v, want ErrWorktreeLocked", err)
	}
	if !strings.Contains(tg(t, repo, "worktree", "list", "--porcelain"), wt) {
		t.Fatal("registration of the locked worktree removed")
	}
}

// Tickets started at the same moment never pick the same branch: the name is
// picked and the branch created under the repository's lock.
func TestAddWorktreeNewBranchPicksUniqueNamesConcurrently(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	tg(t, repo, "update-ref", "refs/remotes/origin/omo/fix-login-2", "main")
	const n = 8
	names := make(chan string, n)
	errs := make(chan error, n)
	dirs := t.TempDir()
	for i := range n {
		go func() {
			name, err := g.AddWorktreeNewBranch(repo, filepath.Join(dirs, strconv.Itoa(i)), "main", func(taken func(string) (bool, error)) (string, error) {
				for k := 1; ; k++ {
					name := "omo/fix-login"
					if k > 1 {
						name += "-" + strconv.Itoa(k)
					}
					if t, err := taken(name); err != nil || !t {
						return name, err
					}
				}
			})
			names <- name
			errs <- err
		}()
	}
	seen := map[string]bool{}
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		name := <-names
		if seen[name] {
			t.Fatalf("branch %s picked twice", name)
		}
		seen[name] = true
	}
	if seen["omo/fix-login-2"] || !seen["omo/fix-login"] || !seen["omo/fix-login-3"] {
		t.Fatalf("names %v: the remote-tracking name must be skipped", seen)
	}
}

// git keeps a ref and refs below it apart: a branch below the name takes
// it, a branch that is a folder of the name is a *PathConflictError, both
// for local and for remote-tracking branches.
func TestAddWorktreeNewBranchFolderConflicts(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	tg(t, repo, "branch", "jb/task/fix/old")
	tg(t, repo, "branch", "team")
	tg(t, repo, "update-ref", "refs/remotes/origin/far", "main")
	first := func(name string) func(func(string) (bool, error)) (string, error) {
		return func(taken func(string) (bool, error)) (string, error) {
			for k := 1; k < 5; k++ {
				n := name
				if k > 1 {
					n += "-" + strconv.Itoa(k)
				}
				if t, err := taken(n); err != nil || !t {
					return n, err
				}
			}
			return "", errors.New("all taken")
		}
	}
	got, err := g.AddWorktreeNewBranch(repo, filepath.Join(t.TempDir(), "a"), "main", first("jb/task/fix"))
	if err != nil || got != "jb/task/fix-2" {
		t.Fatalf("below: %q, %v", got, err)
	}
	for name, existing := range map[string]string{"team/x": "team", "far/y/z": "origin/far"} {
		_, err := g.AddWorktreeNewBranch(repo, filepath.Join(t.TempDir(), "b"), "main", first(name))
		var conflict *PathConflictError
		if !errors.As(err, &conflict) || conflict.Existing != existing || !strings.Contains(err.Error(), "the branch "+existing+" exists") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The name is checked again before git runs: a pick that ignores taken
// cannot make omo adopt an existing branch, and an invalid name never
// reaches git. A failure of git says whether it created the branch.
func TestAddWorktreeNewBranchChecksTheName(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	tg(t, repo, "branch", "feature")
	before := rev(t, repo, "feature")
	for _, name := range []string{"feature", "main", "refs/heads/x", "a..b", "x.lock", "-x", "HEAD"} {
		_, err := g.AddWorktreeNewBranch(repo, filepath.Join(t.TempDir(), "w"), "main", func(func(string) (bool, error)) (string, error) { return name, nil })
		var failed *NewBranchError
		if err == nil || errors.As(err, &failed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := rev(t, repo, "feature"); got != before {
		t.Fatal("feature moved")
	}
	// The folder exists already: git fails, and the error says whether
	// git left the new branch behind (git creates it before the folder).
	dir := t.TempDir()
	write(t, filepath.Join(dir, "x.txt"), "x")
	_, err := g.AddWorktreeNewBranch(repo, dir, "main", func(func(string) (bool, error)) (string, error) { return "fresh", nil })
	var failed *NewBranchError
	exists, _ := g.BranchExists(repo, "fresh")
	if !errors.As(err, &failed) || failed.Branch != "fresh" || failed.Created != exists {
		t.Fatalf("existing folder: %v, created %v, exists %v", err, failed != nil && failed.Created, exists)
	}
}

func TestProtectedBranches(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	tg(t, repo, "update-ref", "refs/remotes/origin/develop", "main")
	tg(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	tg(t, repo, "update-ref", "refs/remotes/up/trunk", "main")
	tg(t, repo, "symbolic-ref", "refs/remotes/up/HEAD", "refs/remotes/up/trunk")
	got, err := g.ProtectedBranches(repo)
	sort.Strings(got)
	if err != nil || !reflect.DeepEqual(got, []string{"develop", "main", "master", "trunk"}) {
		t.Fatalf("%v, %v", got, err)
	}
}
