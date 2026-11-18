package branchname

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
)

func TestCheck(t *testing.T) {
	for _, ok := range []string{
		Default, "omo/{number}-{slug}", "{slug}", "{number}", "{user}/{type}/{slug}", "feature/{number}-{slug}",
		"JIRA-{number}_{slug}", "{host}.{user}/{slug}", "x/{project}/{slug}", "Team/{slug}", "{slug}@home",
	} {
		if err := Check(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for tmpl, want := range map[string]string{
		"":                                  "must not be empty",
		"main":                              "must contain {slug} or {number}",
		"{user}/{type}":                     "must contain {slug} or {number}",
		"omo/{title}":                       "the unknown placeholder {title}; it may use {slug}, {number}, {type}, {project}, {host} and {user}",
		"omo/{a b}":                         "an unknown placeholder",
		"omo/{slug":                         "a { without a closing }",
		"omo/{{slug}}":                      "a { without a closing }",
		"omo/slug}":                         "a } without an opening {",
		"omo/{}":                            "an unknown placeholder",
		"omo {slug}":                        "contains a space",
		"omo..{slug}":                       "contains ..",
		"omo~{slug}":                        "one of ~ ^ : ? * [ \\",
		"omo^{slug}":                        "one of ~ ^ : ? * [ \\",
		"omo:{slug}":                        "one of ~ ^ : ? * [ \\",
		"omo?{slug}":                        "one of ~ ^ : ? * [ \\",
		"omo*{slug}":                        "one of ~ ^ : ? * [ \\",
		"omo[{slug}":                        "one of ~ ^ : ? * [ \\",
		"omo\\{slug}":                       "one of ~ ^ : ? * [ \\",
		"-{slug}":                           "starts with - or +",
		"+{slug}":                           "starts with - or +",
		"/{slug}":                           "starts or ends with /",
		"{slug}/":                           "starts or ends with /",
		"omo//{slug}":                       "contains //",
		"{slug}.lock":                       "ends with .lock",
		"{slug}.lock/x":                     "ends with .lock",
		"omo/.{slug}":                       "starts with .",
		"{slug}.":                           "ends with .",
		"refs/heads/{slug}":                 "starts with refs/",
		"omo/\t{slug}":                      "control or invisible character",
		"omo/​{slug}":                       "control or invisible character",
		"omo/‮{slug}":                       "control or invisible character",
		"größe/{slug}":                      "outside printable ASCII",
		"omo/{slug}-ü":                      "outside printable ASCII",
		"{slug}_head":                       "a name git keeps for itself",
		"{slug}_HEAD":                       "a name git keeps for itself",
		"{number}_Head":                     "a name git keeps for itself",
		strings.Repeat("a", 190) + "{slug}": "at most 200",
		"{slug}/{project}/{host}/{user}/{slug}-{type}/{number}": "at most 200",
	} {
		err := Check(tmpl)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", tmpl, err, want)
		}
	}
}

func TestExpand(t *testing.T) {
	v := Vars{Number: 42, Title: "Fix the Login!", Type: "story", Project: "web.app", Host: "Build-Box", User: "Jane Doe"}
	for tmpl, want := range map[string]string{
		"omo/{slug}":                "omo/fix-the-login",
		"omo/{number}-{slug}":       "omo/42-fix-the-login",
		"{user}/{type}/{slug}":      "jane-doe/story/fix-the-login",
		"{host}/{project}/{number}": "build-box/web-app/42",
		"Feature/{slug}":            "Feature/fix-the-login",
		"{slug}{number}":            "fix-the-login42",
	} {
		if got, err := Expand(tmpl, v); err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", tmpl, got, err, want)
		}
	}
	// Empty values have words of their own.
	if got, _ := Expand("{user}/{host}/{project}/{type}/{slug}", Vars{Number: 1, Title: "日本語"}); got != "user/host/project/ticket/ticket" {
		t.Errorf("empty values: %q", got)
	}
	// A value that breaks the name next to the literal text is caught.
	for tmpl, v := range map[string]Vars{
		"{user}/{slug}":   {Title: "x", User: "refs"},
		"v.{slug}":        {Title: "lock"},
		"{slug}/x.{type}": {Title: "a", Type: "lock"},
	} {
		if got, err := Expand(tmpl, v); err == nil {
			t.Errorf("%q with %+v gave %q", tmpl, v, got)
		}
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Add OAuth login!":                "add-oauth-login",
		"Fix the parser's ../.. handling": "fix-the-parser-s-handling",
		"--leading dashes--":              "leading-dashes",
		"release/v1.2.lock":               "release-v1-2-lock",
		"Größe ändern 🚀 日本語":              "gr-e-ndern",
		"日本語のタイトル":                        "",
		"a@{b}~^:?*[\\":                   "a-b",
		"implement the extremely long feature name that goes on and on": "implement-the-extremely-long-feature",
		strings.Repeat("x", 60):        strings.Repeat("x", 40),
		strings.Repeat("a", 40) + " b": strings.Repeat("a", 40),
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every name of a template that passes Check is one git accepts, for
// adversarial titles and machine names.
func TestExpandedNamesAreValidBranches(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	titles := []string{"", ".", "..", "-", ".hidden", "x.lock", "a..b", "a/b/c", "@{-1}", "HEAD", "refs/heads/main",
		"~^:?*[\\", "tab\there", "nul\x00byte", "emoji 🚀", "日本語", strings.Repeat("ab ", 40)}
	templates := []string{Default, "omo/{number}-{slug}", "{user}/{type}/{slug}", "{host}-{project}/{number}.{slug}", "X_{slug}@{number}"}
	dir := t.TempDir()
	for _, tmpl := range templates {
		for i, title := range titles {
			name, err := Expand(tmpl, Vars{Number: int64(i + 1), Title: title, Type: "task", Project: "a.b_c", Host: title, User: title})
			if err != nil {
				t.Errorf("%q, %q: %v", tmpl, title, err)
				continue
			}
			cmd := exec.Command(git, "check-ref-format", "--branch", name)
			cmd.Dir = dir
			cmd.Env = append(childenvtest.WithoutOmo(os.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%q, %q: git refuses %q: %v %s", tmpl, title, name, err, out)
			}
		}
	}
}

func TestMachine(t *testing.T) {
	host, user := Machine()
	if strings.Contains(host, ".") || strings.Contains(user, "\\") {
		t.Fatalf("host %q user %q", host, user)
	}
}

// Names that git or a case-insensitive file system would read as another
// ref are refused, whatever the case.
func TestValidRefusesNamesGitKeeps(t *testing.T) {
	for _, name := range []string{"HEAD", "head", "Head", "FETCH_HEAD", "orig_head", "x/MERGE_Head", "@", "omo/ü", "ﬁx"} {
		if err := Valid(name); err == nil {
			t.Errorf("%q passed", name)
		}
	}
	for _, name := range []string{"heads", "x/head/y", "header", "omo/head-start", "_heads"} {
		if err := Valid(name); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
	// A title "Head" with a template of only {slug} gives no usable name.
	if _, err := Expand("{slug}", Vars{Title: "Head"}); err == nil {
		t.Error("the name head passed")
	}
}

func TestFold(t *testing.T) {
	for _, c := range [][2]string{{"JIRA-123-fix", "jira-123-fix"}, {"Omo/X", "omo/x"}, {"ſ", "S"}, {"\u212a", "k"}} {
		if !Same(c[0], c[1]) {
			t.Errorf("%q and %q differ", c[0], c[1])
		}
	}
	if Same("a", "b") || Same("omo/x", "omo/x/y") {
		t.Error("different names are the same")
	}
	if !Below("Omo/X/y", "omo/x") || Below("omo/xy", "omo/x") || Below("omo/x", "omo/x") {
		t.Error("Below")
	}
	if !In([]string{"main", "Develop"}, "develop") || In([]string{"main"}, "mainline") {
		t.Error("In")
	}
}
