package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBranchName(t *testing.T) {
	none := func(string) bool { return false }
	cases := []struct {
		n     int64
		title string
		want  string
	}{
		{12, "Add OAuth login!", "omo/12-add-oauth-login"},
		{12, "", "omo/12"},
		{12, "!!!", "omo/12"},
		{12, "   ", "omo/12"},
		{7, "Fix the parser's ../.. handling", "omo/7-fix-the-parser-s-handling"},
		{7, "--leading dashes--", "omo/7-leading-dashes"},
		{7, "release/v1.2.lock", "omo/7-release-v1-2-lock"},
		{7, "Größe ändern 🚀 日本語", "omo/7-gr-e-ndern"},
		{7, "日本語のタイトル", "omo/7"},
		{7, "MiXeD CaSe 42", "omo/7-mixed-case-42"},
		{7, "a@{b}~^:?*[\\", "omo/7-a-b"},
	}
	for _, c := range cases {
		if got := BranchName(c.n, c.title, none); got != c.want {
			t.Errorf("BranchName(%d, %q) = %q, want %q", c.n, c.title, got, c.want)
		}
	}

	// A 60-character title is cut at the last dash within 40 characters.
	long := "implement the extremely long feature name that goes on and on"
	got := BranchName(3, long, none)
	slug := strings.TrimPrefix(got, "omo/3-")
	if len(slug) > 40 || strings.HasSuffix(slug, "-") || !strings.HasPrefix(long, strings.ReplaceAll(slug, "-", " ")) {
		t.Fatalf("long title gave %q", got)
	}
	if want := "omo/3-implement-the-extremely-long-feature"; got != want {
		t.Fatalf("long title gave %q, want %q", got, want)
	}
	// Without any dash within the limit the slug is cut hard.
	hard := BranchName(3, strings.Repeat("x", 60), none)
	if hard != "omo/3-"+strings.Repeat("x", 40) {
		t.Fatalf("hard cut gave %q", hard)
	}
	// A dash exactly at the limit keeps all 40 characters.
	exact := strings.Repeat("a", 40) + " b"
	if got := BranchName(3, exact, none); got != "omo/3-"+strings.Repeat("a", 40) {
		t.Fatalf("cut at the limit gave %q", got)
	}

	// Taken names get -2, -3, ...
	taken := map[string]bool{"omo/12-x": true, "omo/12-x-2": true}
	if got := BranchName(12, "x", func(b string) bool { return taken[b] }); got != "omo/12-x-3" {
		t.Fatalf("taken gave %q", got)
	}
	if got := BranchName(12, "x", func(b string) bool { return b == "omo/12-x" }); got != "omo/12-x-2" {
		t.Fatalf("taken once gave %q", got)
	}
	if got := BranchName(12, "", func(b string) bool { return b == "omo/12" }); got != "omo/12-2" {
		t.Fatalf("taken empty slug gave %q", got)
	}
	// Stable: the same input gives the same name.
	if BranchName(5, "Same title", none) != BranchName(5, "Same title", none) {
		t.Fatal("not stable")
	}
	// nil exists means nothing is taken.
	if got := BranchName(5, "x", nil); got != "omo/5-x" {
		t.Fatalf("nil exists gave %q", got)
	}
}

// branchTitles are adversarial titles for the ref-name checks.
var branchTitles = []string{
	"", ".", "..", "...", "-", "--x", ".hidden", "x.lock", "x.lock.lock", "a..b", "a/b/c", "/leading", "trailing/",
	"@", "@{-1}", "a@{b", "HEAD", "refs/heads/main", "~^:?*[\\", "tab\there", "new\nline", "nul\x00byte",
	"\x7f del", "emoji 🚀🔥", "日本語", "Ελληνικά τίτλος", "עברית", "العربية", "x​zero width", "ǅ title-case",
	"Kelvin K sign", "İstanbul", "ß straße", strings.Repeat("a-", 40), strings.Repeat("ab ", 30), strings.Repeat("é", 100),
	"a" + strings.Repeat(" ", 50) + "b", "1234567890", " spaces around ", "end with dot.", "under_score", "x//y",
}

func TestBranchNamesAreValidRefs(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Log("git not found; checking by rule only")
	}
	dir := t.TempDir()
	seen := map[string]int64{}
	for i, title := range branchTitles {
		n := int64(i + 1)
		name := BranchName(n, title, nil)
		if !utf8.ValidString(name) || !validRefByRule(name) {
			t.Errorf("BranchName(%d, %q) = %q breaks the ref rules", n, title, name)
		}
		if !strings.HasPrefix(name, "omo/") || strings.Count(name, "/") != 1 {
			t.Errorf("BranchName(%d, %q) = %q is not one component below omo/", n, title, name)
		}
		if other, ok := seen[name]; ok {
			t.Errorf("tickets %d and %d share branch %q", other, n, name)
		}
		seen[name] = n
		if git != "" {
			cmd := exec.Command(git, "check-ref-format", "--branch", name)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("git check-ref-format --branch %q: %v %s", name, err, out)
			}
			cmd = exec.Command(git, "check-ref-format", "refs/heads/"+name)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("git check-ref-format refs/heads/%q: %v %s", name, err, out)
			}
		}
	}
}

// validRefByRule checks the rules of git check-ref-format that a branch
// name below omo/ could break.
func validRefByRule(name string) bool {
	if name == "" || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") ||
		strings.Contains(name, "//") || strings.Contains(name, "@{") || name == "@" {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") || strings.HasPrefix(part, "-") {
			return false
		}
	}
	return true
}

func TestWorktreeDir(t *testing.T) {
	root := filepath.Join("home", "worktrees")
	if got, want := WorktreeDir(root, "api", 12), filepath.Join(root, "api", "12"); got != want {
		t.Fatalf("WorktreeDir = %q, want %q", got, want)
	}
}
