package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// numbered is n lines "line 1" … "line n", with the lines in changed
// replaced by "<line> changed".
func numbered(n int, changed ...int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "line %d", i)
		for _, c := range changed {
			if c == i {
				sb.WriteString(" changed")
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

var hunkHeader = regexp.MustCompile(`(?m)^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// hunks returns the headers of the hunks of a patch, as "-a,b +c,d".
func hunks(patch []byte) []string {
	var out []string
	for _, m := range hunkHeader.FindAllStringSubmatch(string(patch), -1) {
		out = append(out, fmt.Sprintf("-%s,%s +%s,%s", m[1], m[2], m[3], m[4]))
	}
	return out
}

func TestBranchFileContextWidensTheHunks(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	commitOn(t, repo, "main", "a long file", map[string]string{"long.txt": numbered(100), "gone.txt": numbered(30), "moved.txt": numbered(40)})
	wt := branchWorktree(t, repo, "feat")
	write(t, filepath.Join(wt, "long.txt"), numbered(100, 10, 90))
	tg(t, wt, "rm", "-q", "gone.txt")
	tg(t, wt, "mv", "moved.txt", "renamed.txt")
	write(t, filepath.Join(wt, "renamed.txt"), numbered(40, 20))
	commitAll(t, wt, "changes")
	done := readOnly(t, repo, wt)
	defer done()
	ctx := context.Background()

	for _, c := range []struct {
		lines int
		want  []string
	}{
		{3, []string{"-7,7 +7,7", "-87,7 +87,7"}},
		{0, []string{"-10, +10,", "-90, +90,"}},
		{-5, []string{"-10, +10,", "-90, +90,"}},
		{40, []string{"-1,100 +1,100"}},
		{30, []string{"-1,40 +1,40", "-60,41 +60,41"}},
		{MaxDiffContext, []string{"-1,100 +1,100"}},
		{MaxDiffContext * 4, []string{"-1,100 +1,100"}},
	} {
		f, err := g.BranchFileContext(ctx, repo, "refs/heads/main", "feat", "long.txt", c.lines)
		if err != nil || f.Truncated || f.Status != "modified" || f.Additions != 2 || f.Deletions != 2 {
			t.Fatalf("%d lines: %+v %v", c.lines, f, err)
		}
		if got := hunks(f.Patch); strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%d lines: hunks %v, want %v", c.lines, got, c.want)
		}
	}
	// The default is the diff's own.
	plain, err := g.BranchFileDiff(ctx, repo, "refs/heads/main", "feat", "long.txt")
	three, _ := g.BranchFileContext(ctx, repo, "refs/heads/main", "feat", "long.txt", 3)
	if err != nil || string(plain.Patch) != string(three.Patch) {
		t.Errorf("BranchFileDiff differs from three lines of context: %v", err)
	}

	// A renamed file: both names, its whole new and old text.
	r, err := g.BranchFileContext(ctx, repo, "refs/heads/main", "feat", "renamed.txt", MaxDiffContext)
	if err != nil || r.OldPath != "moved.txt" || !strings.Contains(string(r.Patch), "rename from moved.txt") || strings.Join(hunks(r.Patch), "") != "-1,40 +1,40" {
		t.Errorf("renamed = %+v %v", r, err)
	}
	// A deleted file already shows all of it.
	d, err := g.BranchFileContext(ctx, repo, "refs/heads/main", "feat", "gone.txt", MaxDiffContext)
	if err != nil || d.Status != "deleted" || strings.Join(hunks(d.Patch), "") != "-1,30 +0,0" {
		t.Errorf("deleted = %+v %v", d, err)
	}
	// Only a path of git's own list.
	for _, p := range []string{"README.md", "moved.txt", "../x", ":(glob)*", "*", "", "long.txt\x00", "--help", "/etc/passwd"} {
		if _, err := g.BranchFileContext(ctx, repo, "refs/heads/main", "feat", p, MaxDiffContext); !errors.Is(err, ErrNotInDiff) {
			t.Errorf("%q: err = %v, want ErrNotInDiff", p, err)
		}
	}
	// Values that look like options are no branch.
	if _, err := g.BranchFileContext(ctx, repo, "--output=/tmp/x", "feat", "long.txt", 3); !errors.Is(err, ErrNoBranch) {
		t.Errorf("an option as base: %v", err)
	}
}

func TestBranchFileContextRefusesWhatDoesNotFit(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	// About 1.9 MiB of 48-byte lines, one line changed in the middle.
	line := func(i int) string { return fmt.Sprintf("line %07d of a file larger than the limit\n", i) }
	var before, after strings.Builder
	for i := 0; i < 40000; i++ {
		before.WriteString(line(i))
		if i == 20000 {
			after.WriteString("changed\n")
		} else {
			after.WriteString(line(i))
		}
	}
	commitOn(t, repo, "main", "big", map[string]string{"big.txt": before.String()})
	wt := branchWorktree(t, repo, "feat")
	write(t, filepath.Join(wt, "big.txt"), after.String())
	// A line over the line cap, inside the context.
	write(t, filepath.Join(wt, "f.txt"), "f1\n"+strings.Repeat("x", MaxPatchLine*2)+"\nnew\n")
	commitAll(t, wt, "changes")
	ctx := context.Background()

	small, err := g.BranchFileContext(ctx, repo, "refs/heads/main", "feat", "big.txt", 200)
	if err != nil || small.Truncated || len(hunks(small.Patch)) != 1 {
		t.Fatalf("200 lines of a big file: %d bytes %v", len(small.Patch), err)
	}
	if _, err := g.BranchFileContext(ctx, repo, "refs/heads/main", "feat", "big.txt", MaxDiffContext); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("the whole big file: err = %v, want ErrTooLarge", err)
	}
	// The plain single-file read still cuts instead.
	if f, err := g.BranchFileDiff(ctx, repo, "refs/heads/main", "feat", "big.txt"); err != nil || f.Truncated {
		t.Fatalf("plain read of a small change: %+v %v", f.Truncated, err)
	}
	f, err := g.BranchFileContext(ctx, repo, "refs/heads/main", "feat", "f.txt", MaxDiffContext)
	if err != nil || !f.Truncated {
		t.Fatalf("long line: truncated=%v err=%v", f.Truncated, err)
	}
	for _, l := range strings.Split(string(f.Patch), "\n") {
		if len(l) > MaxPatchLine {
			t.Fatalf("a line of %d bytes", len(l))
		}
	}
}

func TestWorktreeFileContext(t *testing.T) {
	g := testGit(t)
	repo := newTestRepo(t)
	commitOn(t, repo, "main", "a long file", map[string]string{"long.txt": numbered(60)})
	wt := branchWorktree(t, repo, "feat")
	write(t, filepath.Join(wt, "long.txt"), numbered(60, 30))
	write(t, filepath.Join(wt, "untracked.txt"), "SECRET\n")
	done := readOnly(t, repo, wt)
	ctx := context.Background()
	f, err := g.WorktreeFileContext(ctx, repo, wt, "long.txt", MaxDiffContext)
	if err != nil || strings.Join(hunks(f.Patch), "") != "-1,60 +1,60" {
		t.Fatalf("whole = %+v %v", f, err)
	}
	f, err = g.WorktreeFileContext(ctx, repo, wt, "long.txt", 5)
	if err != nil || strings.Join(hunks(f.Patch), "") != "-25,11 +25,11" {
		t.Fatalf("five lines = %v %v", hunks(f.Patch), err)
	}
	// An untracked file is never read.
	u, err := g.WorktreeFileContext(ctx, repo, wt, "untracked.txt", MaxDiffContext)
	if err != nil || u.Patch != nil || u.Status != "untracked" {
		t.Fatalf("untracked = %+v %v", u, err)
	}
	for _, p := range []string{"README.md", "../x", ":(glob)*"} {
		if _, err := g.WorktreeFileContext(ctx, repo, wt, p, MaxDiffContext); !errors.Is(err, ErrNotInDiff) {
			t.Errorf("%q: err = %v, want ErrNotInDiff", p, err)
		}
	}
	done()
}

func TestFileContextShowsALinkAsALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges on Windows")
	}
	g := testGit(t)
	repo := newTestRepo(t)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	write(t, secret, "SECRET CONTENT\n")
	wt := branchWorktree(t, repo, "feat")
	if err := os.Symlink("f.txt", filepath.Join(wt, "link")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, wt, "a link")
	// Committed: the link's target name is its content.
	f, err := g.BranchFileContext(context.Background(), repo, "refs/heads/main", "feat", "link", MaxDiffContext)
	if err != nil || !strings.Contains(string(f.Patch), "+f.txt") || strings.Contains(string(f.Patch), "f1") {
		t.Fatalf("committed link = %q %v", f.Patch, err)
	}
	// Uncommitted: the agent points the link and a tracked file at a secret.
	if err := os.Remove(filepath.Join(wt, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(wt, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(wt, "other.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(wt, "other.txt")); err != nil {
		t.Fatal(err)
	}
	u, err := g.WorktreeFileContext(context.Background(), repo, wt, "link", MaxDiffContext)
	if err != nil || strings.Contains(string(u.Patch), "SECRET") || !strings.Contains(string(u.Patch), "+"+secret) {
		t.Errorf("link = %q %v", u.Patch, err)
	}
	// A file that became a link is a type change: git writes it as two
	// sections, which the single-file read does not take apart; nothing of
	// the target is read either way.
	tc, err := g.WorktreeFileContext(context.Background(), repo, wt, "other.txt", MaxDiffContext)
	if err != nil || tc.Status != "type_changed" || strings.Contains(string(tc.Patch), "SECRET") {
		t.Errorf("type change = %+v %v", tc, err)
	}
}
