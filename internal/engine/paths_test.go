package engine

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/branchname"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
)

// numbered is the template of the older numbered names.
const numbered = "omo/{number}-{slug}"

// branchName is BranchName with the numbered template and an exists that
// cannot fail.
func branchName(t *testing.T, n int64, title string, taken func(string) bool) string {
	t.Helper()
	return branchNameWith(t, numbered, n, title, taken)
}

func branchNameWith(t *testing.T, tmpl string, n int64, title string, taken func(string) bool) string {
	t.Helper()
	var exists func(string) (bool, error)
	if taken != nil {
		exists = func(b string) (bool, error) { return taken(b), nil }
	}
	name, err := BranchName(tmpl, branchname.Vars{Number: n, Title: title, Type: "task", Project: "api", Host: "box", User: "jb"}, exists)
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func TestBranchName(t *testing.T) {
	none := func(string) bool { return false }
	cases := []struct {
		n     int64
		title string
		want  string
	}{
		{12, "Add OAuth login!", "omo/12-add-oauth-login"},
		{12, "", "omo/12-ticket"},
		{12, "!!!", "omo/12-ticket"},
		{12, "   ", "omo/12-ticket"},
		{7, "Fix the parser's ../.. handling", "omo/7-fix-the-parser-s-handling"},
		{7, "--leading dashes--", "omo/7-leading-dashes"},
		{7, "release/v1.2.lock", "omo/7-release-v1-2-lock"},
		{7, "Größe ändern 🚀 日本語", "omo/7-gr-e-ndern"},
		{7, "日本語のタイトル", "omo/7-ticket"},
		{7, "MiXeD CaSe 42", "omo/7-mixed-case-42"},
		{7, "a@{b}~^:?*[\\", "omo/7-a-b"},
	}
	for _, c := range cases {
		if got := branchName(t, c.n, c.title, none); got != c.want {
			t.Errorf("BranchName(%d, %q) = %q, want %q", c.n, c.title, got, c.want)
		}
	}

	// A 60-character title is cut at the last dash within 40 characters.
	long := "implement the extremely long feature name that goes on and on"
	got := branchName(t, 3, long, none)
	slug := strings.TrimPrefix(got, "omo/3-")
	if len(slug) > 40 || strings.HasSuffix(slug, "-") || !strings.HasPrefix(long, strings.ReplaceAll(slug, "-", " ")) {
		t.Fatalf("long title gave %q", got)
	}
	if want := "omo/3-implement-the-extremely-long-feature"; got != want {
		t.Fatalf("long title gave %q, want %q", got, want)
	}
	// Without any dash within the limit the slug is cut hard.
	hard := branchName(t, 3, strings.Repeat("x", 60), none)
	if hard != "omo/3-"+strings.Repeat("x", 40) {
		t.Fatalf("hard cut gave %q", hard)
	}
	// A dash exactly at the limit keeps all 40 characters.
	exact := strings.Repeat("a", 40) + " b"
	if got := branchName(t, 3, exact, none); got != "omo/3-"+strings.Repeat("a", 40) {
		t.Fatalf("cut at the limit gave %q", got)
	}

	// Taken names get -2, -3, ...
	taken := map[string]bool{"omo/12-x": true, "omo/12-x-2": true}
	if got := branchName(t, 12, "x", func(b string) bool { return taken[b] }); got != "omo/12-x-3" {
		t.Fatalf("taken gave %q", got)
	}
	if got := branchName(t, 12, "x", func(b string) bool { return b == "omo/12-x" }); got != "omo/12-x-2" {
		t.Fatalf("taken once gave %q", got)
	}
	if got := branchName(t, 12, "", func(b string) bool { return b == "omo/12-ticket" }); got != "omo/12-ticket-2" {
		t.Fatalf("taken empty slug gave %q", got)
	}
	// Stable: the same input gives the same name.
	if branchName(t, 5, "Same title", none) != branchName(t, 5, "Same title", none) {
		t.Fatal("not stable")
	}
	// nil exists means nothing is taken.
	if got := branchName(t, 5, "x", nil); got != "omo/5-x" {
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

func TestBranchNameFailsInsteadOfLooping(t *testing.T) {
	calls := 0
	_, err := BranchName(numbered, branchname.Vars{Number: 4, Title: "x"}, func(string) (bool, error) { calls++; return true, nil })
	if err == nil || calls != maxBranchSuffix {
		t.Fatalf("every name taken: err %v after %d checks, want an error after %d", err, calls, maxBranchSuffix)
	}
	if !strings.Contains(err.Error(), "omo/4-x-100") {
		t.Fatalf("error does not name the range: %v", err)
	}
	calls = 0
	_, err = BranchName(numbered, branchname.Vars{Number: 4, Title: "x"}, func(string) (bool, error) { calls++; return false, errors.New("disk on fire") })
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("a failing check gave %v after %d checks; want its error after 1", err, calls)
	}
	// The last suffix is still tried.
	name, err := BranchName(numbered, branchname.Vars{Number: 4, Title: "x"}, func(b string) (bool, error) { return b != "omo/4-x-100", nil })
	if err != nil || name != "omo/4-x-100" {
		t.Fatalf("last free suffix gave %q, %v", name, err)
	}
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
		name := branchName(t, n, title, nil)
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
		if plain := branchNameWith(t, branchname.Default, n, title, nil); !validRefByRule(plain) || !strings.HasPrefix(plain, "omo/") || strings.Count(plain, "/") != 1 {
			t.Errorf("plain BranchName(%q) = %q breaks the ref rules", title, plain)
		}
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

func TestBranchNameWithoutTicketNumber(t *testing.T) {
	none := func(string) bool { return false }
	for _, c := range []struct{ title, want string }{
		{"Add OAuth login!", "omo/add-oauth-login"},
		{"Fix the parser's ../.. handling", "omo/fix-the-parser-s-handling"},
		{"日本語のタイトル", "omo/ticket"},
		{"", "omo/ticket"},
		{"!!!", "omo/ticket"},
		{"MiXeD CaSe 42", "omo/mixed-case-42"},
		{"implement the extremely long feature name that goes on and on", "omo/implement-the-extremely-long-feature"},
	} {
		if got := branchNameWith(t, branchname.Default, 12, c.title, none); got != c.want {
			t.Errorf("plain BranchName(%q) = %q, want %q", c.title, got, c.want)
		}
		// The ticket's number is not part of the name.
		if got := branchNameWith(t, branchname.Default, 4711, c.title, none); strings.Contains(got, "4711") {
			t.Errorf("%q holds the ticket number", got)
		}
	}
	// A taken name, an empty slug included, gets -2, -3, ...
	taken := map[string]bool{"omo/fix-login": true, "omo/fix-login-2": true, "omo/ticket": true}
	for title, want := range map[string]string{"Fix login": "omo/fix-login-3", "Fix Login!": "omo/fix-login-3", "": "omo/ticket-2", "Other": "omo/other"} {
		if got := branchNameWith(t, branchname.Default, 1, title, func(b string) bool { return taken[b] }); got != want {
			t.Errorf("plain BranchName(%q) with names taken = %q, want %q", title, got, want)
		}
	}
	// With the number the form of old is unchanged.
	if got := branchNameWith(t, numbered, 12, "Add OAuth login!", none); got != "omo/12-add-oauth-login" {
		t.Errorf("numbered name %q", got)
	}
}

// Every placeholder, and names without omo/: the template is the whole name.
func TestBranchNameFromTemplate(t *testing.T) {
	v := branchname.Vars{Number: 42, Title: "Fix the Login!", Type: "story", Project: "web.app", Host: "Build-Box", User: "Jane Doe"}
	for tmpl, want := range map[string]string{
		"omo/{slug}":                     "omo/fix-the-login",
		"{slug}":                         "fix-the-login",
		"feature/{number}-{slug}":        "feature/42-fix-the-login",
		"{user}/{type}/{slug}":           "jane-doe/story/fix-the-login",
		"{host}-{project}/{number}":      "build-box-web-app/42",
		"JIRA-{number}_{slug}":           "JIRA-42_fix-the-login",
		"{project}/{type}/{number}.work": "web-app/story/42.work",
	} {
		got, err := BranchName(tmpl, v, func(string) (bool, error) { return false, nil })
		if err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", tmpl, got, err, want)
		}
	}
	// The suffix goes at the end of the whole name.
	got, err := BranchName("{user}/{slug}", v, func(b string) (bool, error) { return b == "jane-doe/fix-the-login", nil })
	if err != nil || got != "jane-doe/fix-the-login-2" {
		t.Errorf("taken: %q, %v", got, err)
	}
	// A template that cannot be used is an error, never a name.
	for _, tmpl := range []string{"", "main", "omo/{nope}", "{slug} x", "-{slug}", "refs/heads/{slug}"} {
		if got, err := BranchName(tmpl, v, nil); err == nil {
			t.Errorf("%q gave %q", tmpl, got)
		}
	}
	// A conflict with a folder of the name ends the search at once.
	calls := 0
	_, err = BranchName("{user}/{slug}", v, func(b string) (bool, error) {
		calls++
		return false, &gitops.PathConflictError{Name: b, Existing: "jane-doe"}
	})
	var conflict *gitops.PathConflictError
	if !errors.As(err, &conflict) || calls != 1 || !strings.Contains(err.Error(), "jane-doe") {
		t.Fatalf("conflict: %v after %d checks", err, calls)
	}
}
