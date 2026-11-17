package gitops

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// feature commits on branch feature: f.txt changed and new.txt added.
func feature(t *testing.T, repo string) string {
	t.Helper()
	return commitOn(t, repo, "feature", "feature", map[string]string{"f.txt": "f2\n", "new.txt": "n\n"})
}

func prepare(t *testing.T, g *Git, repo, target, source string, style MergeStyle, msg string) MergeResult {
	t.Helper()
	res, err := g.PrepareMerge(repo, target, source, style, msg)
	if err != nil {
		t.Fatalf("PrepareMerge: %v", err)
	}
	return res
}

func TestPrepareMergeBuildsMergeCommitWithoutCheckout(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	src := feature(t, repo)
	commitOn(t, repo, "main", "main moves", map[string]string{"other.txt": "o2\n"})
	beforeRefs, before := refs(t, repo), snapshot(t, repo)
	mainID := rev(t, repo, "main")

	res := prepare(t, g, repo, "main", "feature", StyleMerge, "Merge #3: x")
	if res.Commit == "" || res.AlreadyMerged || len(res.Conflicts) > 0 {
		t.Fatalf("result = %+v", res)
	}
	parents := strings.Fields(tg(t, repo, "show", "-s", "--format=%P", res.Commit))
	if !reflect.DeepEqual(parents, []string{mainID, src}) {
		t.Fatalf("parents = %v, want [%s %s]", parents, mainID, src)
	}
	if msg := tg(t, repo, "show", "-s", "--format=%B", res.Commit); strings.TrimSpace(msg) != "Merge #3: x" {
		t.Fatalf("message = %q", msg)
	}
	if got := tg(t, repo, "show", res.Commit+":other.txt"); got != "o2\n" {
		t.Fatalf("other.txt = %q", got)
	}
	if got := tg(t, repo, "show", res.Commit+":new.txt"); got != "n\n" {
		t.Fatalf("new.txt = %q", got)
	}
	if after := refs(t, repo); after != beforeRefs {
		t.Fatalf("refs changed:\n%s\n%s", beforeRefs, after)
	}
	sameSnapshot(t, "repo folder", before, snapshot(t, repo))
	noLeftovers(t, repo)
}

func TestPrepareMergeSquash(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	mainID := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleSquash, "x (#3)\n\n- #4 a\n")
	parents := strings.Fields(tg(t, repo, "show", "-s", "--format=%P", res.Commit))
	if !reflect.DeepEqual(parents, []string{mainID}) {
		t.Fatalf("parents = %v, want [%s]", parents, mainID)
	}
	if got := tg(t, repo, "show", res.Commit+":f.txt"); got != "f2\n" {
		t.Fatalf("f.txt = %q", got)
	}
	if msg := tg(t, repo, "show", "-s", "--format=%B", res.Commit); strings.TrimSpace(msg) != "x (#3)\n\n- #4 a" {
		t.Fatalf("message = %q", msg)
	}
}

func TestPrepareMergeReportsConflictFiles(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	commitOn(t, repo, "feature", "feature", map[string]string{"README.md": "feature\n", "new.txt": "n\n"})
	commitOn(t, repo, "main", "main", map[string]string{"README.md": "main\n"})
	beforeRefs, before := refs(t, repo), snapshot(t, repo)

	for _, style := range []MergeStyle{StyleMerge, StyleSquash} {
		res := prepare(t, g, repo, "main", "feature", style, "m")
		if !reflect.DeepEqual(res.Conflicts, []string{"README.md"}) || res.Commit != "" || res.AlreadyMerged {
			t.Fatalf("%s: result = %+v", style, res)
		}
	}
	if after := refs(t, repo); after != beforeRefs {
		t.Fatalf("refs changed:\n%s\n%s", beforeRefs, after)
	}
	sameSnapshot(t, "repo folder", before, snapshot(t, repo))
	noLeftovers(t, repo)
}

func TestPrepareMergeAlreadyMerged(t *testing.T) {
	for _, style := range []MergeStyle{StyleMerge, StyleSquash} {
		t.Run(string(style), func(t *testing.T) {
			repo := newTestRepo(t)
			g := testGit(t)
			feature(t, repo)
			from := rev(t, repo, "main")
			res := prepare(t, g, repo, "main", "feature", style, "m")
			if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
				t.Fatal(err)
			}
			again := prepare(t, g, repo, "main", "feature", style, "m")
			if !again.AlreadyMerged || again.Commit != "" {
				t.Fatalf("after advance: %+v", again)
			}
		})
	}
}

func TestPrepareMergeUsesEnvironmentIdentity(t *testing.T) {
	repo := newTestRepo(t)
	feature(t, repo)
	g := testGit(t)
	env := isolatedEnv(t)
	env = append(env,
		"GIT_AUTHOR_NAME=Configured Agent", "GIT_AUTHOR_EMAIL=agent@example.com",
		"GIT_COMMITTER_NAME=Committer", "GIT_COMMITTER_EMAIL=committer@example.com")
	g.SetEnvironment(env)
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	got := strings.TrimSpace(tg(t, repo, "show", "-s", "--format=%an <%ae>|%cn <%ce>", res.Commit))
	if want := "Configured Agent <agent@example.com>|Committer <committer@example.com>"; got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}
	if strings.Contains(tg(t, repo, "cat-file", "commit", res.Commit), "gpgsig") {
		t.Fatal("commit signed although the environment switches signing off")
	}
}

func TestPrepareMergeSignsWhenTheEnvironmentSaysSo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake signing program is a shell script")
	}
	repo := newTestRepo(t)
	feature(t, repo)
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-gpg")
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" > '" + argsFile + "'\ncat >/dev/null\n" +
		"echo '[GNUPG:] SIG_CREATED ' >&2\n" +
		"printf -- '-----BEGIN PGP SIGNATURE-----\\nfake\\n-----END PGP SIGNATURE-----\\n'\n"
	write(t, fake, script)
	if err := os.Chmod(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	g := testGit(t)
	g.SetEnvironment(isolatedEnv(t, "GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=true",
		"GIT_CONFIG_KEY_1=gpg.program", "GIT_CONFIG_VALUE_1="+fake,
		"GIT_CONFIG_KEY_2=user.signingkey", "GIT_CONFIG_VALUE_2=KEY123"))
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	if !strings.Contains(tg(t, repo, "cat-file", "commit", res.Commit), "gpgsig") {
		t.Fatal("commit not signed although the environment asks for it")
	}
	if !strings.Contains(read(t, argsFile), "KEY123") {
		t.Fatalf("signing key not used: %q", read(t, argsFile))
	}
}

func TestAdvanceUpdatesCheckedOutFolder(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
		t.Fatal(err)
	}
	if got := rev(t, repo, "HEAD"); got != res.Commit {
		t.Fatalf("HEAD = %s, want %s", got, res.Commit)
	}
	if got := read(t, filepath.Join(repo, "new.txt")); got != "n\n" {
		t.Fatalf("new.txt = %q", got)
	}
	if st := tg(t, repo, "status", "--porcelain"); st != "" {
		t.Fatalf("folder not clean after advance:\n%s", st)
	}
	noLeftovers(t, repo)
}

func TestAdvanceIntoDirtyCheckoutChangesNothing(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	write(t, filepath.Join(repo, "f.txt"), "my own edit\r\nwith bytes \x00\xff\n")
	write(t, filepath.Join(repo, "other.txt"), "unrelated\n")
	before, beforeRefs := snapshot(t, repo), refs(t, repo)

	err := g.AdvanceBranch(repo, "main", from, res.Commit)
	var blocked *BlockedError
	if !errors.Is(err, ErrCheckoutBlocked) || !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want ErrCheckoutBlocked", err)
	}
	if !reflect.DeepEqual(blocked.Files, []string{"f.txt"}) || blocked.Worktree != repo {
		t.Fatalf("blocked = %+v", blocked)
	}
	if !strings.Contains(err.Error(), "f.txt") {
		t.Fatalf("error does not name the file: %v", err)
	}
	sameSnapshot(t, "user folder", before, snapshot(t, repo))
	if after := refs(t, repo); after != beforeRefs {
		t.Fatalf("refs changed:\n%s\n%s", beforeRefs, after)
	}
	noLeftovers(t, repo)
}

func TestAdvanceBlockedByUntrackedOrIgnoredFileInTheWay(t *testing.T) {
	for name, path := range map[string]string{"untracked": "new.txt", "ignored": "build/out"} {
		t.Run(name, func(t *testing.T) {
			repo := newTestRepo(t)
			g := testGit(t)
			commitOn(t, repo, "feature", "feature", map[string]string{path: "theirs\n"})
			from := rev(t, repo, "main")
			res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
			write(t, filepath.Join(repo, path), "mine\n")
			before := snapshot(t, repo)
			err := g.AdvanceBranch(repo, "main", from, res.Commit)
			var blocked *BlockedError
			if !errors.As(err, &blocked) || !reflect.DeepEqual(blocked.Files, []string{path}) {
				t.Fatalf("err = %v (%+v), want blocked by %s", err, blocked, path)
			}
			sameSnapshot(t, "user folder", before, snapshot(t, repo))
			if rev(t, repo, "main") != from {
				t.Fatal("main moved")
			}
			noLeftovers(t, repo)
		})
	}
}

func TestAdvanceKeepsUnrelatedLocalChanges(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	write(t, filepath.Join(repo, "other.txt"), "unrelated edit\x00\xfe\r\n")
	write(t, filepath.Join(repo, "README.md"), "staged edit\n")
	tg(t, repo, "add", "README.md")
	write(t, filepath.Join(repo, "scratch", "notes.txt"), "untracked\n")
	write(t, filepath.Join(repo, "build", "cache"), "ignored\n")
	before := snapshot(t, repo)

	if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
		t.Fatal(err)
	}
	if rev(t, repo, "HEAD") != res.Commit {
		t.Fatal("HEAD not advanced")
	}
	after := snapshot(t, repo)
	for _, f := range []string{"file:other.txt", "file:README.md", "file:scratch/notes.txt", "file:build/cache"} {
		if after[f] != before[f] {
			t.Errorf("%s = %q, want %q", f, after[f], before[f])
		}
	}
	if !strings.Contains(tg(t, repo, "diff", "--cached", "--name-only"), "README.md") {
		t.Error("staged change lost")
	}
	if after["file:new.txt"] != "n\n" || after["file:f.txt"] != "f2\n" {
		t.Errorf("merge result not checked out: %q %q", after["file:new.txt"], after["file:f.txt"])
	}
}

func TestAdvanceNeverTouchesUsersOwnMerge(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	commitOn(t, repo, "users", "users", map[string]string{"README.md": "users\n"})
	write(t, filepath.Join(repo, "README.md"), "main\n")
	tg(t, repo, "commit", "-q", "-am", "main edit")
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	tgFails(t, repo, "merge", "users") // the user's own merge stops on a conflict
	gitDir := filepath.Join(repo, ".git")
	mergeHead := read(t, filepath.Join(gitDir, "MERGE_HEAD"))
	before := snapshot(t, repo)
	if !strings.Contains(before["file:README.md"], "<<<<<<<") {
		t.Fatalf("setup: no conflict markers: %q", before["file:README.md"])
	}

	err := g.AdvanceBranch(repo, "main", from, res.Commit)
	if !errors.Is(err, ErrCheckoutBlocked) || !strings.Contains(err.Error(), "merge is in progress") {
		t.Fatalf("err = %v, want blocked by the merge in progress", err)
	}
	if got := read(t, filepath.Join(gitDir, "MERGE_HEAD")); got != mergeHead {
		t.Fatalf("MERGE_HEAD changed: %q", got)
	}
	sameSnapshot(t, "user folder", before, snapshot(t, repo))
	if rev(t, repo, "main") != from {
		t.Fatal("main moved")
	}
}

func TestAdvanceWithBaseNotCheckedOutLeavesFolderAlone(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	tg(t, repo, "switch", "-q", "-c", "other")
	write(t, filepath.Join(repo, "f.txt"), "edit on other\n") // overlaps the merge
	write(t, filepath.Join(repo, "new.txt"), "untracked on other\n")
	before := snapshot(t, repo)

	if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
		t.Fatal(err)
	}
	if rev(t, repo, "refs/heads/main") != res.Commit {
		t.Fatal("main not advanced")
	}
	sameSnapshot(t, "user folder", before, snapshot(t, repo))
	if wt, found, _ := g.WorktreeOf(repo, "main"); found {
		t.Fatalf("main checked out in %s", wt)
	}
	noLeftovers(t, repo)
}

func TestAdvanceWithDetachedHeadLeavesFolderAlone(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	tg(t, repo, "switch", "-q", "--detach", "main")
	write(t, filepath.Join(repo, "f.txt"), "detached edit\n")
	before := snapshot(t, repo)
	if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
		t.Fatal(err)
	}
	if rev(t, repo, "refs/heads/main") != res.Commit {
		t.Fatal("main not advanced")
	}
	sameSnapshot(t, "user folder", before, snapshot(t, repo))
}

func TestAdvanceInSecondWorktree(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	tg(t, repo, "switch", "-q", "-c", "other")
	write(t, filepath.Join(repo, "f.txt"), "user folder edit\n")
	second := filepath.Join(t.TempDir(), "second")
	tg(t, repo, "worktree", "add", "-q", second, "main")
	write(t, filepath.Join(second, "other.txt"), "unrelated in second\n")
	beforeUser := snapshot(t, repo)

	if wt, found, err := g.WorktreeOf(repo, "main"); err != nil || !found || wt != second {
		t.Fatalf("WorktreeOf = %q %v %v, want %q", wt, found, err, second)
	}
	if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
		t.Fatal(err)
	}
	if rev(t, second, "HEAD") != res.Commit || read(t, filepath.Join(second, "new.txt")) != "n\n" {
		t.Fatal("second worktree not fast-forwarded")
	}
	if read(t, filepath.Join(second, "other.txt")) != "unrelated in second\n" {
		t.Fatal("local change in the second worktree lost")
	}
	sameSnapshot(t, "user folder", beforeUser, snapshot(t, repo))

	// Overlapping change in the second worktree: refused there, nothing moves.
	from2 := res.Commit
	commitOn(t, repo, "feature", "feature 2", map[string]string{"other.txt": "o3\n"})
	res2 := prepare(t, g, repo, "main", "feature", StyleMerge, "m2")
	before2 := snapshot(t, second)
	err := g.AdvanceBranch(repo, "main", from2, res2.Commit)
	var blocked *BlockedError
	if !errors.As(err, &blocked) || blocked.Worktree != second || !reflect.DeepEqual(blocked.Files, []string{"other.txt"}) {
		t.Fatalf("err = %v, want blocked in %s by other.txt", err, second)
	}
	sameSnapshot(t, "second worktree", before2, snapshot(t, second))
}

func TestAdvanceDetectsMovedBranch(t *testing.T) {
	for _, checkedOut := range []bool{false, true} {
		name := "ref"
		if checkedOut {
			name = "checked out"
		}
		t.Run(name, func(t *testing.T) {
			repo := newTestRepo(t)
			g := testGit(t)
			feature(t, repo)
			if !checkedOut {
				tg(t, repo, "switch", "-q", "-c", "other")
			}
			from := rev(t, repo, "main")
			res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
			// Someone else commits to main between building and advancing.
			var moved string
			if checkedOut {
				write(t, filepath.Join(repo, "README.md"), "someone\n")
				tg(t, repo, "commit", "-q", "-am", "someone else")
				moved = rev(t, repo, "HEAD")
			} else {
				moved = commitOn(t, repo, "main", "someone else", map[string]string{"README.md": "someone\n"})
			}
			before := snapshot(t, repo)
			err := g.AdvanceBranch(repo, "main", from, res.Commit)
			if !errors.Is(err, ErrBranchMoved) {
				t.Fatalf("err = %v, want ErrBranchMoved", err)
			}
			if rev(t, repo, "refs/heads/main") != moved {
				t.Fatal("main changed")
			}
			sameSnapshot(t, "folder", before, snapshot(t, repo))
			noLeftovers(t, repo)
			// Rebuilding on the new main succeeds and keeps both changes.
			res2 := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
			if err := g.AdvanceBranch(repo, "main", moved, res2.Commit); err != nil {
				t.Fatal(err)
			}
			if tg(t, repo, "show", "main:README.md") != "someone\n" || tg(t, repo, "show", "main:new.txt") != "n\n" {
				t.Fatal("a change was lost")
			}
		})
	}
}

func TestAdvanceRefusesBranchBeingRebased(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	commitOn(t, repo, "upstream", "upstream", map[string]string{"README.md": "upstream\n"})
	tg(t, repo, "switch", "-q", "-c", "other")
	second := filepath.Join(t.TempDir(), "second")
	tg(t, repo, "worktree", "add", "-q", second, "main")
	write(t, filepath.Join(second, "README.md"), "main\n")
	tg(t, second, "commit", "-q", "-am", "main edit")
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	tgFails(t, second, "rebase", "upstream") // stops on a conflict, HEAD detached
	if wt, found, err := g.WorktreeOf(repo, "main"); err != nil || !found || wt != second {
		t.Fatalf("WorktreeOf = %q %v %v, want the rebasing worktree", wt, found, err)
	}
	before := snapshot(t, second)
	err := g.AdvanceBranch(repo, "main", from, res.Commit)
	if !errors.Is(err, ErrCheckoutBlocked) || !strings.Contains(err.Error(), "rebase") {
		t.Fatalf("err = %v, want blocked by the rebase", err)
	}
	if rev(t, repo, "refs/heads/main") != from {
		t.Fatal("main moved during the rebase")
	}
	sameSnapshot(t, "rebasing worktree", before, snapshot(t, second))
}

func TestAdvanceAlreadyAtTargetAndNonDescendant(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
		t.Fatal(err)
	}
	if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
		t.Fatalf("repeated advance: %v", err)
	}
	if err := g.AdvanceBranch(repo, "main", res.Commit, from); err == nil || errors.Is(err, ErrBranchMoved) {
		t.Fatalf("backwards advance: err = %v", err)
	}
	if rev(t, repo, "main") != res.Commit {
		t.Fatal("main moved backwards")
	}
	if err := g.AdvanceBranch(repo, "--force", from, res.Commit); err == nil {
		t.Fatal("option-like branch name accepted")
	}
}

func TestAdvanceRunsNoHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hooks are shell scripts")
	}
	repo := newTestRepo(t)
	g := testGit(t)
	marker := filepath.Join(t.TempDir(), "hooks-ran")
	for _, h := range []string{"post-merge", "reference-transaction", "post-checkout", "pre-commit", "commit-msg"} {
		p := filepath.Join(repo, ".git", "hooks", h)
		write(t, p, "#!/bin/sh\necho "+h+" >> '"+marker+"'\n")
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	feature(t, repo)
	os.Remove(marker) // setup commits ran them
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil { // fast-forward in the folder
		t.Fatal(err)
	}
	tg(t, repo, "switch", "-q", "-c", "other")
	os.Remove(marker)
	commitOn(t, repo, "feature", "f2", map[string]string{"x.txt": "x\n"})
	os.Remove(marker)
	res2 := prepare(t, g, repo, "main", "feature", StyleMerge, "m2")
	if err := g.AdvanceBranch(repo, "main", res.Commit, res2.Commit); err != nil { // ref update
		t.Fatal(err)
	}
	if err := g.DeleteBranch(repo, "feature", rev(t, repo, "feature")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("hooks ran: %s", b)
	}
}

func TestVersionParses(t *testing.T) {
	for in, want := range map[string][2]int{
		"git version 2.56.0\n":                     {2, 56},
		"git version 2.39.2.windows.1\n":           {2, 39},
		"git version 2.39.3 (Apple Git-145)\n":     {2, 39},
		"git version 3.0.0-rc1\n":                  {3, 0},
		"git version 2.37.1.vfs.0.0 (something)\n": {2, 37},
	} {
		major, minor, err := parseVersion(in)
		if err != nil || major != want[0] || minor != want[1] {
			t.Errorf("parseVersion(%q) = %d.%d %v, want %d.%d", in, major, minor, err, want[0], want[1])
		}
	}
	if _, _, err := parseVersion("nonsense"); err == nil {
		t.Error("nonsense parsed")
	}
	requireGit(t)
	if major, minor, err := New().Version(); err != nil || CheckVersion(major, minor) != nil {
		t.Fatalf("installed git: %d.%d %v", major, minor, err)
	}
}

func TestCheckVersion(t *testing.T) {
	for _, v := range [][2]int{{2, 38}, {2, 50}, {3, 0}} {
		if err := CheckVersion(v[0], v[1]); err != nil {
			t.Errorf("%v: %v", v, err)
		}
	}
	err := CheckVersion(2, 37)
	if err == nil || err.Error() != "omo needs git 2.38 or newer (found 2.37)" {
		t.Fatalf("err = %v", err)
	}
	if CheckVersion(1, 99) == nil {
		t.Fatal("1.99 accepted")
	}
}

// The user's own merge settings in the repository's config must not let the
// fast-forward stash, squash or otherwise change the folder beyond a clean
// fast-forward.
func TestAdvanceIgnoresMergeSettingsThatTouchTheFolder(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"autostash", "merge.autoStash", "true"},
		{"branch merge options", "branch.main.mergeOptions", "--squash --autostash --no-ff"},
		{"submodule recursion", "submodule.recurse", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepo(t)
			g := testGit(t)
			feature(t, repo)
			tg(t, repo, "config", tc.key, tc.value)
			from := rev(t, repo, "main")
			res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")

			// An edit the merge also changes: refused, nothing changed.
			write(t, filepath.Join(repo, "f.txt"), "my own edit\n")
			before, beforeRefs := snapshot(t, repo), refs(t, repo)
			err := g.AdvanceBranch(repo, "main", from, res.Commit)
			if !errors.Is(err, ErrCheckoutBlocked) {
				t.Fatalf("overlapping edit: err = %v, want ErrCheckoutBlocked", err)
			}
			sameSnapshot(t, "user folder", before, snapshot(t, repo))
			if after := refs(t, repo); after != beforeRefs {
				t.Fatalf("refs changed:\n%s\n%s", beforeRefs, after)
			}
			noLeftovers(t, repo)

			// An unrelated edit: the fast-forward goes through around it.
			tg(t, repo, "checkout", "--", "f.txt")
			write(t, filepath.Join(repo, "other.txt"), "unrelated edit\n")
			if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
				t.Fatalf("unrelated edit: %v", err)
			}
			if rev(t, repo, "HEAD") != res.Commit || rev(t, repo, "refs/heads/main") != res.Commit {
				t.Fatal("main not fast-forwarded")
			}
			if got := tg(t, repo, "status", "--porcelain=v1", "--untracked-files=all"); got != " M other.txt\n" {
				t.Fatalf("status after advance = %q", got)
			}
			if read(t, filepath.Join(repo, "other.txt")) != "unrelated edit\n" || read(t, filepath.Join(repo, "f.txt")) != "f2\n" {
				t.Fatal("folder content wrong after advance")
			}
			if s := tg(t, repo, "stash", "list"); s != "" {
				t.Fatalf("stash created: %q", s)
			}
			noLeftovers(t, repo)
		})
	}
}

// The user switches their folder to another branch at the commit being
// merged from, after omo checked HEAD and before the fast-forward ran.
func TestAdvanceDetectsBranchSwitchBeforeFastForward(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	g.pause = func(point string) {
		if point == "fast-forward" {
			tg(t, repo, "switch", "-q", "-c", "other")
		}
	}
	err := g.AdvanceBranch(repo, "main", from, res.Commit)
	var switched *SwitchedError
	if !errors.Is(err, ErrCheckoutSwitched) || !errors.As(err, &switched) {
		t.Fatalf("err = %v, want ErrCheckoutSwitched", err)
	}
	if switched.Head != "other" || switched.Branch != "main" || switched.At != from || switched.Worktree != repo {
		t.Fatalf("switched = %+v", switched)
	}
	if !strings.Contains(err.Error(), "other") || !strings.Contains(err.Error(), "main did not move") {
		t.Fatalf("message does not say what happened: %v", err)
	}
	if rev(t, repo, "refs/heads/main") != from {
		t.Fatal("main moved")
	}
	noLeftovers(t, repo)
}

// The branch gets checked out in a new worktree after omo found it checked
// out nowhere and before the ref update.
func TestAdvanceDetectsCheckoutBeforeRefUpdate(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	tg(t, repo, "switch", "-q", "-c", "other")
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	second := filepath.Join(t.TempDir(), "second")
	g.pause = func(point string) {
		if point == "update-ref" {
			tg(t, repo, "worktree", "add", "-q", second, "main")
		}
	}
	err := g.AdvanceBranch(repo, "main", from, res.Commit)
	var stale *StaleCheckoutError
	if !errors.Is(err, ErrStaleCheckout) || !errors.As(err, &stale) {
		t.Fatalf("err = %v, want ErrStaleCheckout", err)
	}
	if stale.Worktree != second || stale.Branch != "main" || stale.From != from || stale.To != res.Commit {
		t.Fatalf("stale = %+v", stale)
	}

	// A worktree that checks the branch out only after the update is fine.
	tg(t, repo, "worktree", "remove", "--force", second)
	tg(t, repo, "update-ref", "refs/heads/main", from)
	third := filepath.Join(t.TempDir(), "third")
	g.pause = nil
	if err := g.AdvanceBranch(repo, "main", from, res.Commit); err != nil {
		t.Fatal(err)
	}
	tg(t, repo, "worktree", "add", "-q", third, "main")
	if err := g.checkNotCheckedOutStale(repo, "main", from, res.Commit); err != nil {
		t.Fatalf("worktree created after the update reported stale: %v", err)
	}
}

func TestAdvanceNamesOddPathsInTheWayExactly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("quotes and newlines are not allowed in Windows file names")
	}
	repo := newTestRepo(t)
	g := testGit(t)
	quoted, newline := `"q"`, "a\nb"
	commitOn(t, repo, "feature", "feature", map[string]string{quoted: "theirs\n", newline: "theirs\n", "plain.txt": "theirs\n"})
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")

	// A file literally named with quotes is reported as it is named.
	write(t, filepath.Join(repo, quoted), "mine\n")
	err := g.AdvanceBranch(repo, "main", from, res.Commit)
	var blocked *BlockedError
	if !errors.As(err, &blocked) || !reflect.DeepEqual(blocked.Files, []string{quoted}) || blocked.Incomplete {
		t.Fatalf("err = %v (%+v), want blocked by %s", err, blocked, quoted)
	}

	// A path with a newline is reported whole: the files are found from
	// plumbing output before git is asked, so the list is complete.
	write(t, filepath.Join(repo, newline), "mine\n")
	err = g.AdvanceBranch(repo, "main", from, res.Commit)
	blocked = nil
	if !errors.As(err, &blocked) || !reflect.DeepEqual(blocked.Files, []string{quoted, newline}) || blocked.Incomplete {
		t.Fatalf("err = %v (%+v), want blocked by %q and %q", err, blocked, quoted, newline)
	}
	if !strings.Contains(err.Error(), `"a\nb"`) {
		t.Fatalf("message = %v", err)
	}
	if rev(t, repo, "refs/heads/main") != from {
		t.Fatal("main moved")
	}
}

func TestAdvanceReportsSignatureRefusal(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	tg(t, repo, "config", "merge.verifySignatures", "true")
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	before := snapshot(t, repo)
	err := g.AdvanceBranch(repo, "main", from, res.Commit)
	if !errors.Is(err, ErrSignatureNotVerified) || errors.Is(err, ErrCheckoutBlocked) {
		t.Fatalf("err = %v, want ErrSignatureNotVerified", err)
	}
	sameSnapshot(t, "user folder", before, snapshot(t, repo))
	if rev(t, repo, "refs/heads/main") != from {
		t.Fatal("main moved")
	}
}
