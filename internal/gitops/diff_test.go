package gitops

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// branchWorktree cuts branch from main in a worktree outside repo and
// returns its folder.
func branchWorktree(t *testing.T, repo, branch string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt")
	tg(t, repo, "worktree", "add", "-q", "-b", branch, wt, "main")
	return wt
}

func commitAll(t *testing.T, wt, msg string) string {
	t.Helper()
	tg(t, wt, "add", "-A", "--", ".")
	tg(t, wt, "commit", "-q", "-m", msg)
	return rev(t, wt, "HEAD")
}

func byPath(res DiffResult) map[string]FileChange {
	m := map[string]FileChange{}
	for _, f := range res.Files {
		m[f.Path] = f
	}
	return m
}

func gitFile(t *testing.T, dir, name string) []byte {
	t.Helper()
	p := strings.TrimSpace(tg(t, dir, "rev-parse", "--absolute-git-dir"))
	b, _ := os.ReadFile(filepath.Join(p, name))
	return b
}

// readOnly captures what a diff must not change and compares after.
func readOnly(t *testing.T, repo string, dirs ...string) func() {
	t.Helper()
	type state struct {
		snap  map[string]string
		index []byte
		orig  []byte
	}
	all := append([]string{repo}, dirs...)
	before := map[string]state{}
	for _, d := range all {
		before[d] = state{snapshot(t, d), gitFile(t, d, "index"), gitFile(t, d, "ORIG_HEAD")}
	}
	refsBefore := refs(t, repo)
	return func() {
		t.Helper()
		for _, d := range all {
			// Read the git files first: the snapshot's own status call may
			// refresh the index.
			if !bytes.Equal(before[d].index, gitFile(t, d, "index")) {
				t.Errorf("%s: the index file changed", d)
			}
			if !bytes.Equal(before[d].orig, gitFile(t, d, "ORIG_HEAD")) {
				t.Errorf("%s: ORIG_HEAD changed", d)
			}
			sameSnapshot(t, d, before[d].snap, snapshot(t, d))
		}
		if refs(t, repo) != refsBefore {
			t.Error("refs changed")
		}
		noLeftovers(t, repo)
	}
}

func TestBranchDiffListsChangesSinceMergeBase(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	wt := branchWorktree(t, repo, "feat")
	write(t, filepath.Join(wt, "mine.txt"), "mine\n")
	tip := commitAll(t, wt, "mine")
	cut := rev(t, repo, "main")
	commitOn(t, repo, "main", "later", map[string]string{"later.txt": "later\n"})
	// Reads only; the check includes the user's folder and the worktree.
	done := readOnly(t, repo, wt)
	res, err := g.BranchDiff(context.Background(), repo, "main", "feat", DefaultDiffLimits)
	done()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "mine.txt" || res.Files[0].Status != "added" {
		t.Fatalf("files = %+v", res.Files)
	}
	f := res.Files[0]
	if f.Additions != 1 || f.Deletions != 0 || !strings.Contains(string(f.Patch), "+mine\n") || f.Truncated {
		t.Errorf("file = %+v", f)
	}
	if res.MergeBase != cut || res.Head != tip || res.MoreFiles {
		t.Errorf("merge base %s head %s more %v, want %s %s false", res.MergeBase, res.Head, res.MoreFiles, cut, tip)
	}
}

func TestBranchDiffStatuses(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	write(t, filepath.Join(repo, "run.sh"), "#!/bin/sh\n")
	write(t, filepath.Join(repo, "deleted.txt"), "gone\n")
	write(t, filepath.Join(repo, "docs.txt"), "one\ntwo\nthree\nfour\nfive\nsix\n")
	tg(t, repo, "add", "-A", "--", ".")
	tg(t, repo, "commit", "-q", "-m", "more")
	wt := branchWorktree(t, repo, "feat")
	write(t, filepath.Join(wt, "added.txt"), "new\n")
	write(t, filepath.Join(wt, "f.txt"), "f1\nf2\n")
	if err := os.Remove(filepath.Join(wt, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	tg(t, wt, "mv", "docs.txt", "moved.txt")
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(wt, "run.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(wt, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	tg(t, sub, "init", "-q", "-b", "main")
	write(t, filepath.Join(sub, "x"), "x\n")
	tg(t, sub, "add", "x")
	tg(t, sub, "commit", "-q", "-m", "sub")
	if runtime.GOOS != "windows" {
		if err := os.Symlink("f.txt", filepath.Join(wt, "link")); err != nil {
			t.Fatal(err)
		}
	}
	commitAll(t, wt, "all kinds")
	res, err := g.BranchDiff(context.Background(), repo, "main", "feat", DefaultDiffLimits)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(res)
	want := map[string]string{"added.txt": "added", "f.txt": "modified", "deleted.txt": "deleted",
		"moved.txt": "renamed", "sub": "added"}
	if runtime.GOOS != "windows" {
		want["link"] = "added"
		want["run.sh"] = "modified"
	}
	if len(m) != len(want) {
		t.Fatalf("files = %+v", res.Files)
	}
	for p, s := range want {
		if m[p].Status != s {
			t.Errorf("%s: status %q, want %q", p, m[p].Status, s)
		}
		if m[p].Patch == nil || m[p].Truncated {
			t.Errorf("%s: patch %q truncated %v", p, m[p].Patch, m[p].Truncated)
		}
	}
	if mv := m["moved.txt"]; mv.OldPath != "docs.txt" || !strings.Contains(string(mv.Patch), "rename from docs.txt") {
		t.Errorf("rename = %+v", mv)
	}
	if runtime.GOOS != "windows" && !strings.Contains(string(m["run.sh"].Patch), "new mode 100755") {
		t.Errorf("mode change patch = %q", m["run.sh"].Patch)
	}
	if !strings.Contains(string(m["sub"].Patch), "Subproject commit") {
		t.Errorf("submodule patch = %q", m["sub"].Patch)
	}
	if runtime.GOOS != "windows" && !strings.Contains(string(m["link"].Patch), "+f.txt") {
		t.Errorf("symlink patch = %q", m["link"].Patch)
	}
	if !strings.Contains(string(m["deleted.txt"].Patch), "-gone") {
		t.Errorf("deleted patch = %q", m["deleted.txt"].Patch)
	}
}

func TestBranchDiffBinary(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	wt := branchWorktree(t, repo, "feat")
	write(t, filepath.Join(wt, "a.txt"), "first\n")
	write(t, filepath.Join(wt, "b.bin"), "bin\x00ary\x00\x01\x02")
	write(t, filepath.Join(wt, "c.txt"), "last\n")
	commitAll(t, wt, "bin")
	res, err := g.BranchDiff(context.Background(), repo, "main", "feat", DefaultDiffLimits)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(res)
	if b := m["b.bin"]; !b.Binary || b.Patch != nil || b.Truncated || b.Additions != 0 {
		t.Errorf("binary = %+v", b)
	}
	if !strings.Contains(string(m["a.txt"].Patch), "+first") || !strings.Contains(string(m["c.txt"].Patch), "+last") {
		t.Errorf("text files around the binary one lost their patches: %+v", res.Files)
	}
	fc, err := g.BranchFileDiff(context.Background(), repo, "main", "feat", "b.bin")
	if err != nil || !fc.Binary || fc.Patch != nil {
		t.Errorf("single binary = %+v, %v", fc, err)
	}
}

func TestBranchDiffOddNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("these names cannot exist on Windows")
	}
	g := testGit(t)
	repo := newTestRepo(t)
	wt := branchWorktree(t, repo, "feat")
	names := []string{"sp ace.txt", "tab\there.txt", "new\nline.txt", "quo\"te.txt", "back\\slash.txt",
		"café.txt", "rtl‮txt.exe", "-rf", "--help", "bad\xffbyte.txt", ":(glob)x"}
	for _, n := range names {
		write(t, filepath.Join(wt, n), "content of "+strings.ToValidUTF8(n, "?")+"\n")
	}
	commitAll(t, wt, "odd")
	res, err := g.BranchDiff(context.Background(), repo, "main", "feat", DefaultDiffLimits)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(res)
	for _, n := range names {
		f, ok := m[n]
		if !ok {
			t.Errorf("%q missing from %d files", n, len(res.Files))
			continue
		}
		if f.Status != "added" || !strings.Contains(string(f.Patch), "+content of ") || f.Truncated {
			t.Errorf("%q = %+v", n, f)
		}
		one, err := g.BranchFileDiff(context.Background(), repo, "main", "feat", n)
		if err != nil || !strings.Contains(string(one.Patch), "+content of ") {
			t.Errorf("%q single: %+v, %v", n, one, err)
		}
	}
	if len(res.Files) != len(names) {
		t.Errorf("%d files, want %d", len(res.Files), len(names))
	}
	// The same through the worktree path: staged changes with odd names.
	write(t, filepath.Join(wt, "-new\nname"), "x\n")
	tg(t, wt, "add", "-A", "--", ".")
	wres, err := g.WorktreeChanges(context.Background(), repo, wt, DefaultDiffLimits)
	if err != nil {
		t.Fatal(err)
	}
	if f := byPath(wres)["-new\nname"]; f.Status != "added" || !strings.Contains(string(f.Patch), "+x") {
		t.Errorf("worktree odd name = %+v", f)
	}
}

func TestBranchDiffCaps(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	wt := branchWorktree(t, repo, "feat")
	for _, n := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
		write(t, filepath.Join(wt, n), strings.Repeat("xxxxxxxxx\n", 20))
	}
	commitAll(t, wt, "five")
	ctx := context.Background()

	res, err := g.BranchDiff(ctx, repo, "main", "feat", DiffLimits{MaxFiles: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 3 || !res.MoreFiles || res.Files[0].Path != "a.txt" || res.Files[2].Path != "c.txt" {
		t.Errorf("max files: %+v more=%v", res.Files, res.MoreFiles)
	}
	for _, f := range res.Files {
		if !strings.Contains(string(f.Patch), "+xxxxxxxxx") {
			t.Errorf("%s lost its patch within the file cap", f.Path)
		}
	}

	// Files past the total budget are named, with no patch.
	res, err = g.BranchDiff(ctx, repo, "main", "feat", DiffLimits{MaxTotalPatch: 700})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 5 || res.MoreFiles {
		t.Fatalf("files = %d more=%v", len(res.Files), res.MoreFiles)
	}
	if res.Files[0].Patch == nil || res.Files[0].Truncated {
		t.Errorf("first = %+v", res.Files[0])
	}
	sum := 0
	for _, f := range res.Files {
		sum += len(f.Patch)
	}
	if sum > 700 {
		t.Errorf("patches hold %d bytes, budget 700", sum)
	}
	last := res.Files[4]
	if last.Patch != nil || !last.Truncated || last.Additions != 20 {
		t.Errorf("last = %+v", last)
	}

	// A file over its own limit keeps whole lines of its start.
	write(t, filepath.Join(wt, "big.txt"), strings.Repeat("0123456789012345678901234567890123456789\n", 500))
	write(t, filepath.Join(wt, "wide.txt"), strings.Repeat("w", 3*MaxPatchLine)+"\nshort\n")
	commitAll(t, wt, "big")
	res, err = g.BranchDiff(ctx, repo, "main", "feat", DiffLimits{MaxFilePatch: 1000})
	if err != nil {
		t.Fatal(err)
	}
	big := byPath(res)["big.txt"]
	if len(big.Patch) == 0 || len(big.Patch) > 1000 || !big.Truncated || big.Patch[len(big.Patch)-1] != '\n' || big.Additions != 500 {
		t.Errorf("big = %d bytes truncated=%v additions=%d", len(big.Patch), big.Truncated, big.Additions)
	}
	if a := byPath(res)["a.txt"]; a.Truncated || a.Patch == nil {
		t.Errorf("a = %+v", a)
	}
	res, err = g.BranchDiff(ctx, repo, "main", "feat", DefaultDiffLimits)
	if err != nil {
		t.Fatal(err)
	}
	wide := byPath(res)["wide.txt"]
	if !wide.Truncated {
		t.Error("a line over the line limit is not reported")
	}
	for _, line := range bytes.Split(wide.Patch, []byte("\n")) {
		if len(line) > MaxPatchLine {
			t.Errorf("a patch line has %d bytes", len(line))
		}
	}
	if !strings.Contains(string(wide.Patch), "+short") {
		t.Error("the lines after a long one are lost")
	}
}

func TestBranchDiffContentThatLooksLikeHeaders(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	wt := branchWorktree(t, repo, "feat")
	write(t, filepath.Join(wt, "a.txt"), "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\nindex 1..2 100644\n")
	write(t, filepath.Join(wt, "b.txt"), "after\n")
	commitAll(t, wt, "headers")
	res, err := g.BranchDiff(context.Background(), repo, "main", "feat", DefaultDiffLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files = %+v", res.Files)
	}
	a, b := res.Files[0], res.Files[1]
	if a.Truncated || !strings.Contains(string(a.Patch), "+diff --git a/x b/x\n") || !strings.Contains(string(a.Patch), "+index 1..2 100644") {
		t.Errorf("a = %q truncated=%v", a.Patch, a.Truncated)
	}
	if b.Truncated || !strings.Contains(string(b.Patch), "+after") || strings.Contains(string(b.Patch), "diff --git a/x") {
		t.Errorf("b = %q", b.Patch)
	}
}

func TestPatchOfWrongSectionsIsDropped(t *testing.T) {
	files := []FileChange{{Path: "a.txt", Status: "added"}, {Path: "b.txt", Status: "added"}}
	out := []byte("diff --git a/a.txt b/a.txt\n+a\n")
	attachPatches(files, 2, out, false, DefaultDiffLimits.fill())
	for _, f := range files {
		if f.Patch != nil || !f.Truncated {
			t.Errorf("%s = %+v, want no patch and Truncated", f.Path, f)
		}
	}
	// Same count, but the sections belong to other files.
	files = []FileChange{{Path: "a.txt", Status: "added"}, {Path: "b.txt", Status: "added"}}
	out = []byte("diff --git a/b.txt b/b.txt\n+b\ndiff --git a/a.txt b/a.txt\n+a\n")
	attachPatches(files, 2, out, false, DefaultDiffLimits.fill())
	for _, f := range files {
		if f.Patch != nil || !f.Truncated {
			t.Errorf("%s = %+v, want no patch and Truncated", f.Path, f)
		}
	}
}

func TestBranchDiffIgnoresExternalDiffAndTextconv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	g := testGit(t)
	repo := newTestRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "evil.sh")
	write(t, script, "#!/bin/sh\necho ran >> '"+marker+"'\ncat \"$1\" 2>/dev/null\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	tg(t, repo, "config", "diff.external", script)
	tg(t, repo, "config", "diff.evil.textconv", script)
	wt := branchWorktree(t, repo, "feat")
	write(t, filepath.Join(wt, ".gitattributes"), "*.txt diff=evil\n")
	write(t, filepath.Join(wt, "f.txt"), "f1\nchanged\n")
	commitAll(t, wt, "attrs")
	write(t, filepath.Join(wt, "f.txt"), "f1\nchanged\nagain\n")
	ctx := context.Background()
	res, err := g.BranchDiff(ctx, repo, "main", "feat", DefaultDiffLimits)
	if err != nil {
		t.Fatal(err)
	}
	if f := byPath(res)["f.txt"]; !strings.Contains(string(f.Patch), "+changed") || !strings.HasPrefix(string(f.Patch), "diff --git ") {
		t.Errorf("patch = %q", f.Patch)
	}
	if _, err := g.BranchFileDiff(ctx, repo, "main", "feat", "f.txt"); err != nil {
		t.Fatal(err)
	}
	wres, err := g.WorktreeChanges(ctx, repo, wt, DefaultDiffLimits)
	if err != nil {
		t.Fatal(err)
	}
	if f := byPath(wres)["f.txt"]; !strings.Contains(string(f.Patch), "+again") {
		t.Errorf("worktree patch = %q", f.Patch)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the external diff or the textconv driver ran")
	}
}

func TestBranchFileDiffOnlyListedPaths(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	wt := branchWorktree(t, repo, "feat")
	write(t, filepath.Join(wt, "f.txt"), "f1\nmore\n")
	tg(t, wt, "mv", "other.txt", "renamed.txt")
	write(t, filepath.Join(wt, "big.txt"), strings.Repeat("a line of moderate length for the size check\n", 13000))
	commitAll(t, wt, "changes")
	ctx := context.Background()

	for _, p := range []string{"README.md", "../x", ":(glob)*", "*", ":(top)f.txt", "", "f.txt\x00", "nope.txt", "other.txt", "/etc/passwd", "--help", "docs/../f.txt"} {
		if _, err := g.BranchFileDiff(ctx, repo, "main", "feat", p); !errors.Is(err, ErrNotInDiff) {
			t.Errorf("%q: err = %v, want ErrNotInDiff", p, err)
		}
	}
	f, err := g.BranchFileDiff(ctx, repo, "main", "feat", "f.txt")
	if err != nil || !strings.Contains(string(f.Patch), "+more") || f.Status != "modified" || f.Additions != 1 {
		t.Errorf("f.txt = %+v, %v", f, err)
	}
	r, err := g.BranchFileDiff(ctx, repo, "main", "feat", "renamed.txt")
	if err != nil || r.Status != "renamed" || r.OldPath != "other.txt" || !strings.Contains(string(r.Patch), "rename from other.txt") {
		t.Errorf("renamed.txt = %+v, %v", r, err)
	}
	big, err := g.BranchFileDiff(ctx, repo, "main", "feat", "big.txt")
	if err != nil || big.Truncated || len(big.Patch) < 500<<10 || len(big.Patch) > MaxSingleFilePatch {
		t.Errorf("big.txt = %d bytes truncated=%v err=%v", len(big.Patch), big.Truncated, err)
	}
	write(t, filepath.Join(wt, "huge.txt"), strings.Repeat("a line of moderate length for the size check\n", 40000))
	commitAll(t, wt, "huge")
	huge, err := g.BranchFileDiff(ctx, repo, "main", "feat", "huge.txt")
	if err != nil || !huge.Truncated || len(huge.Patch) == 0 || len(huge.Patch) > MaxSingleFilePatch || huge.Patch[len(huge.Patch)-1] != '\n' {
		t.Errorf("huge.txt = %d bytes truncated=%v err=%v", len(huge.Patch), huge.Truncated, err)
	}
}

func TestBranchDiffMissingBranch(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	tg(t, repo, "branch", "feat")
	ctx := context.Background()
	for _, c := range [][2]string{{"main", "gone"}, {"gone", "feat"}, {"main", "--output=/tmp/x"}, {"-x", "feat"}, {"main", ""}, {"main", "a..b"}} {
		if _, err := g.BranchDiff(ctx, repo, c[0], c[1], DefaultDiffLimits); !errors.Is(err, ErrNoBranch) {
			t.Errorf("%q %q: err = %v, want ErrNoBranch", c[0], c[1], err)
		}
		if _, err := g.BranchFileDiff(ctx, repo, c[0], c[1], "f.txt"); !errors.Is(err, ErrNoBranch) {
			t.Errorf("file %q %q: err = %v, want ErrNoBranch", c[0], c[1], err)
		}
	}
	if _, err := os.Stat("/tmp/x"); err == nil {
		t.Log("note: /tmp/x exists, from elsewhere")
	}
}

func TestWorktreeChanges(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	wt := branchWorktree(t, repo, "feat")
	write(t, filepath.Join(wt, "f.txt"), "f1\nstaged\n")
	tg(t, wt, "add", "f.txt")
	write(t, filepath.Join(wt, "other.txt"), "o1\nunstaged\n")
	write(t, filepath.Join(wt, "added.txt"), "added\n")
	tg(t, wt, "add", "added.txt")
	write(t, filepath.Join(wt, "untracked.txt"), "SECRET untracked content\n")
	write(t, filepath.Join(wt, "dir", "inner.txt"), "inner\n")
	write(t, filepath.Join(wt, "build", "out.o"), "ignored\n")
	done := readOnly(t, repo, wt)
	// Make the index stale without changing content: a refresh would write.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(wt, "README.md"), old, old); err != nil {
		t.Fatal(err)
	}
	res, err := g.WorktreeChanges(context.Background(), repo, wt, DefaultDiffLimits)
	done()
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(res)
	if len(m) != 5 {
		t.Fatalf("files = %+v", res.Files)
	}
	for p, st := range map[string]string{"f.txt": "modified", "other.txt": "modified", "added.txt": "added"} {
		if m[p].Status != st || !bytes.Contains(m[p].Patch, []byte("+")) {
			t.Errorf("%s = %+v", p, m[p])
		}
	}
	if !strings.Contains(string(m["f.txt"].Patch), "+staged") || !strings.Contains(string(m["other.txt"].Patch), "+unstaged") {
		t.Error("staged and unstaged changes are not both shown")
	}
	for _, p := range []string{"untracked.txt", "dir/inner.txt"} {
		if f := m[p]; f.Status != "untracked" || f.Patch != nil || f.Binary {
			t.Errorf("%s = %+v", p, f)
		}
	}
	if _, ok := m["build/out.o"]; ok {
		t.Error("an ignored file is listed")
	}
	// Untracked files come after the tracked ones, and count against the cap.
	small, err := g.WorktreeChanges(context.Background(), repo, wt, DiffLimits{MaxFiles: 4})
	if err != nil || len(small.Files) != 4 || !small.MoreFiles {
		t.Errorf("capped = %d files more=%v err=%v", len(small.Files), small.MoreFiles, err)
	}
	if res.Head != rev(t, wt, "HEAD") || res.MergeBase != "" {
		t.Errorf("head %q merge base %q", res.Head, res.MergeBase)
	}

	f, err := g.WorktreeFileDiff(context.Background(), repo, wt, "other.txt")
	if err != nil || !strings.Contains(string(f.Patch), "+unstaged") {
		t.Errorf("single = %+v, %v", f, err)
	}
	u, err := g.WorktreeFileDiff(context.Background(), repo, wt, "untracked.txt")
	if err != nil || u.Status != "untracked" || u.Patch != nil {
		t.Errorf("single untracked = %+v, %v", u, err)
	}
	for _, p := range []string{"README.md", "build/out.o", "../x", ":(glob)*"} {
		if _, err := g.WorktreeFileDiff(context.Background(), repo, wt, p); !errors.Is(err, ErrNotInDiff) {
			t.Errorf("%q: err = %v, want ErrNotInDiff", p, err)
		}
	}
}

func TestWorktreeChangesCleanAndNoRepo(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	res, err := g.WorktreeChanges(context.Background(), repo, repo, DefaultDiffLimits)
	if err != nil || len(res.Files) != 0 || res.MoreFiles {
		t.Errorf("clean = %+v, %v", res, err)
	}
	if _, err := g.WorktreeChanges(context.Background(), repo, t.TempDir(), DefaultDiffLimits); err == nil {
		t.Error("a folder that is no repository gave no error")
	}
}

func TestRunCappedCutsAndStops(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	write(t, filepath.Join(repo, "big.txt"), strings.Repeat("0123456789\n", 100000))
	tg(t, repo, "add", "big.txt")
	tg(t, repo, "commit", "-q", "-m", "big")
	start := time.Now()
	out, cut, err := g.runCapped(context.Background(), repo, 100, "cat-file", "-p", "HEAD:big.txt")
	if err != nil || !cut || len(out) != 100 {
		t.Fatalf("out %d bytes cut=%v err=%v", len(out), cut, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
	out, cut, err = g.runCapped(context.Background(), repo, 1<<24, "cat-file", "-p", "HEAD:big.txt")
	if err != nil || cut || len(out) != 1100000 {
		t.Errorf("uncut: %d bytes cut=%v err=%v", len(out), cut, err)
	}
	ended, stop := context.WithCancel(context.Background())
	stop()
	if _, _, err := g.runCapped(ended, repo, 100, "status"); !errors.Is(err, context.Canceled) {
		t.Errorf("ended context: err = %v", err)
	}
}
