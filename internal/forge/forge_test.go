package forge

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseRemotes(t *testing.T) {
	for _, c := range []struct {
		url  string
		want Remote
	}{
		{"https://github.com/you/app.git", Remote{Host: "github.com", Repo: "you/app", Web: "https://github.com/you/app", Kind: GitHub}},
		{"https://you:SECRET@github.com/you/app", Remote{Host: "github.com", Repo: "you/app", Web: "https://github.com/you/app", Kind: GitHub}},
		{"ssh://git@github.com:22/you/app.git", Remote{Host: "github.com", Repo: "you/app", Web: "https://github.com/you/app", Kind: GitHub}},
		{"git@github.com:you/app.git", Remote{Host: "github.com", Repo: "you/app", Web: "https://github.com/you/app", Kind: GitHub}},
		{"git@GitHub.com:you/app.git/", Remote{Host: "github.com", Repo: "you/app", Web: "https://github.com/you/app", Kind: GitHub}},
		{"https://gitlab.example.com/group/sub/app.git", Remote{Host: "gitlab.example.com", Repo: "group/sub/app", Web: "https://gitlab.example.com/group/sub/app", Kind: GitLab}},
		{"git@gitlab.com:group/app.git", Remote{Host: "gitlab.com", Repo: "group/app", Web: "https://gitlab.com/group/app", Kind: GitLab}},
		{"https://code.example.org:3000/you/app", Remote{Host: "code.example.org", Repo: "you/app", Web: "https://code.example.org:3000/you/app", Kind: Gitea}},
		{"/srv/git/app.git", Remote{}},
	} {
		got, err := Parse(c.url)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.url, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %+v, want %+v", c.url, got, c.want)
		}
		if strings.Contains(got.Web, "SECRET") || strings.Contains(got.Web, "@") {
			t.Errorf("Parse(%q): web address %q keeps credentials", c.url, got.Web)
		}
	}
	for _, bad := range []string{
		"", "relative/path", "http://github.com/you/app", "git://github.com/you/app", "ext::sh -c x",
		"https://github.com/app", "https://github.com/you/../app", "https://github.com/you/a$pp",
		"https://evil_host/you/app", "git@-oProxy:you/app", "https://github.com/you/app‮",
		"https://[::1]/you/app", "https://github.com/you/app?x=1", "https://github.com/you/app#x",
	} {
		if r, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) = %+v, want an error", bad, r)
		}
	}
}

func lookAll(name string) (string, error) { return "/usr/bin/" + name, nil }

func lookNone(string) (string, error) { return "", exec.ErrNotFound }

func TestCommandQuoting(t *testing.T) {
	gh := Remote{Host: "github.com", Repo: "you/app", Web: "https://github.com/you/app", Kind: GitHub}
	c, ok := For(gh, "omo/12-login", "main", lookAll)
	if !ok || c.CLI != "gh" {
		t.Fatalf("For = %+v, %v", c, ok)
	}
	want := `gh pr create --repo='you/app' --head='omo/12-login' --base='main' --title="$(cat pr-title.txt)" --body-file='pr-body.md'`
	if c.Line != want {
		t.Fatalf("line\n%s\nwant\n%s", c.Line, want)
	}
	want = `gh pr edit 'omo/12-login' --repo='you/app' --title="$(cat pr-title.txt)" --body-file='pr-body.md'`
	if c.Edit != want {
		t.Fatalf("edit\n%s\nwant\n%s", c.Edit, want)
	}
	gl := Remote{Host: "gitlab.com", Repo: "group/sub/app", Web: "https://gitlab.com/group/sub/app", Kind: GitLab}
	c, ok = For(gl, "omo/1-x", "release/2", lookAll)
	want = `glab mr create --repo='group/sub/app' --source-branch='omo/1-x' --target-branch='release/2' --title="$(cat pr-title.txt)" --description="$(cat pr-body.md)" --yes`
	if !ok || c.Line != want {
		t.Fatalf("gitlab line\n%s\nwant\n%s", c.Line, want)
	}
	want = `glab mr update 'omo/1-x' --repo='group/sub/app' --title="$(cat pr-title.txt)" --description="$(cat pr-body.md)"`
	if c.Edit != want {
		t.Fatalf("gitlab edit\n%s\nwant\n%s", c.Edit, want)
	}
	own := Remote{Host: "gitlab.example.com", Repo: "group/app", Web: "https://gitlab.example.com/group/app", Kind: GitLab}
	if c, ok = For(own, "omo/1-x", "main", lookAll); !ok || !strings.Contains(c.Line, `--repo='https://gitlab.example.com/group/app'`) {
		t.Fatalf("own gitlab line %q", c.Line)
	}
	tea := Remote{Host: "code.example.org", Repo: "you/app", Web: "https://code.example.org/you/app", Kind: Gitea}
	c, ok = For(tea, "omo/1-x", "main", lookAll)
	want = `tea pulls create --repo='you/app' --head='omo/1-x' --base='main' --title="$(cat pr-title.txt)" --description="$(cat pr-body.md)"`
	if !ok || c.Line != want {
		t.Fatalf("gitea line\n%s\nwant\n%s", c.Line, want)
	}
	want = `tea pulls edit --repo='you/app' --title="$(cat pr-title.txt)" --description="$(cat pr-body.md)" INDEX`
	if c.Edit != want {
		t.Fatalf("gitea edit\n%s\nwant\n%s", c.Edit, want)
	}

	for _, base := range []string{"it's", "a\nb", "x\x1by", "", "-x​"} {
		if c, ok := For(gh, "omo/1-x", base, lookAll); ok {
			t.Errorf("base %q gave %q", base, c.Line)
		}
	}
	for _, branch := range []string{"omo/it's", "", "omo/a b", "-x"} {
		if c, ok := For(gh, branch, "main", lookAll); ok {
			t.Errorf("branch %q gave %q", branch, c.Line)
		}
	}
	if _, ok := For(gh, "omo/1-x", "main", lookNone); ok {
		t.Error("a command without its tool on PATH")
	}
	if _, ok := For(Remote{}, "omo/1-x", "main", lookAll); ok {
		t.Error("a command for a local remote")
	}
	if _, ok := For(Remote{Host: "github.com", Repo: "you/a'b", Kind: GitHub}, "omo/1-x", "main", lookAll); ok {
		t.Error("a command for a repository path with a quote")
	}
}

// fakeCLI writes a program name on dir that records each argument on its
// own line, NUL-separated per call, into log.
func fakeCLI(t *testing.T, dir, name, log string) {
	t.Helper()
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\000' \"$a\" >> '" + log + "'; done\nprintf '\\n' >> '" + log + "'\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestExportCommandArgumentsAreLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command line is for a POSIX shell")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	title := `--repo=evil/x $(touch pwned) "; touch pwned2; ` + "`touch pwned3`"
	body := "Body with $(touch pwned4) and 'quotes'\n--yes\n"
	for _, r := range []Remote{
		{Host: "github.com", Repo: "you/app", Web: "https://github.com/you/app", Kind: GitHub},
		{Host: "gitlab.com", Repo: "group/app", Web: "https://gitlab.com/group/app", Kind: GitLab},
		{Host: "code.example.org", Repo: "you/app", Web: "https://code.example.org/you/app", Kind: Gitea},
	} {
		t.Run(r.Kind, func(t *testing.T) {
			bin, work := t.TempDir(), t.TempDir()
			log := filepath.Join(t.TempDir(), "argv")
			for _, name := range []string{"gh", "glab", "tea"} {
				fakeCLI(t, bin, name, log)
			}
			if err := os.WriteFile(filepath.Join(work, TitleFile), []byte(title+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, BodyFile), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			look := func(name string) (string, error) { return filepath.Join(bin, name), nil }
			c, ok := For(r, "omo/7-sign-in", "main", look)
			if !ok {
				t.Fatal("no command")
			}
			for _, line := range []string{c.Line, strings.ReplaceAll(c.Edit, EditIndex, "4")} {
				checkLiteral(t, sh, line, work, bin, log, r, title, body)
			}
		})
	}
}

// checkLiteral runs line in work with the fake tools of bin, which log their
// arguments to log, and checks that the title and body arrived as one
// argument each and ran nothing.
func checkLiteral(t *testing.T, sh, line, work, bin, log string, r Remote, title, body string) {
	t.Helper()
	if err := os.Remove(log); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	cmd := exec.Command(sh, "-c", line)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", line, err, out)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\x00"), "\x00")
	titles, repos := 0, 0
	for _, a := range args {
		if a == "--title="+title {
			titles++
		}
		if strings.HasPrefix(a, "--repo=") {
			repos++
			if strings.Contains(a, "evil") {
				t.Errorf("repo argument %q", a)
			}
		}
	}
	if titles != 1 || repos != 1 {
		t.Fatalf("%s\nargv %q: want the title as one argument and one --repo", line, args)
	}
	if r.Kind != GitHub && !containsArg(args, "--description="+strings.TrimRight(body, "\n")) {
		t.Fatalf("argv %q: want the body as one argument", args)
	}
	if r.Kind == GitHub && !containsArg(args, "--body-file="+BodyFile) {
		t.Fatalf("argv %q: want --body-file=%s", args, BodyFile)
	}
	for _, f := range []string{"pwned", "pwned2", "pwned3", "pwned4"} {
		if _, err := os.Stat(filepath.Join(work, f)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the title or body ran a command (%s exists)", f)
		}
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestCheckAddress(t *testing.T) {
	gh := Remote{Host: "github.com", Repo: "you/app", Web: "https://github.com/you/app", Kind: GitHub}
	for _, ok := range []string{
		"https://github.com/you/app/pull/3", "https://GitHub.com/you/app/pull/3", "https://github.com/You/App/pull/3",
		" https://github.com/you/app/pull/3 ",
	} {
		if err := CheckAddress(gh, ok); err != nil {
			t.Errorf("CheckAddress(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://github.com/you/app/pull/3", "https://evil.example/you/app/pull/3", "https://github.com.evil.example/pull/3",
		"https://user@github.com/you/app/pull/3", "https://user:pw@github.com/you/app/pull/3",
		"github.com/you/app/pull/3", "javascript:alert(1)",
		// Another repository on the same host, or a page outside the
		// repository.
		"https://github.com/you/other/pull/3", "https://github.com/you/application/pull/3", "https://github.com/evil/app/pull/3",
		"https://github.com/you/app", "https://github.com/you/app/", "https://github.com/settings/tokens", "https://github.com/",
		// Paths that leave the repository once a browser reads them.
		"https://github.com/you/app/../../evil/x", "https://github.com/you/app/%2e%2e/%2E%2E/evil/x",
		"https://github.com/you/app/./../other", `https://github.com/you/app/..\..\evil`,
	} {
		if err := CheckAddress(gh, bad); err == nil {
			t.Errorf("CheckAddress(%q) passed", bad)
		}
	}
	// A GitLab group path counts as a whole.
	gl := Remote{Host: "gitlab.example.com", Repo: "group/sub/app", Web: "https://gitlab.example.com/group/sub/app", Kind: GitLab}
	if err := CheckAddress(gl, "https://gitlab.example.com/group/sub/app/-/merge_requests/1"); err != nil {
		t.Errorf("gitlab: %v", err)
	}
	if err := CheckAddress(gl, "https://gitlab.example.com/group/sub/-/merge_requests/1"); err == nil {
		t.Error("gitlab: the group's page passed")
	}
	// A local remote has no forge: no address fits it.
	for _, addr := range []string{"https://example.invalid/pr/1", "https://github.com/you/app/pull/3"} {
		if err := CheckAddress(Remote{}, addr); err == nil {
			t.Errorf("local remote: %q passed", addr)
		}
	}
}

// A folder on this machine is a remote without a forge in the platform's own
// spelling too: on Windows a path with a drive. Elsewhere a backslash is in
// no address omo accepts, and "c:/repos" names the host c.
func TestParseLocalFolderOfThePlatform(t *testing.T) {
	for _, raw := range []string{`C:\repos\origin.git`, `C:/repos/origin.git`} {
		got, err := Parse(raw)
		if runtime.GOOS == "windows" {
			if err != nil || got != (Remote{}) {
				t.Errorf("Parse(%q) = %+v, %v; want a local folder", raw, got, err)
			}
			continue
		}
		if err == nil && got == (Remote{}) {
			t.Errorf("Parse(%q) was read as a local folder off Windows", raw)
		}
	}
	if _, err := Parse(`C:\repos\my app.git`); err == nil {
		t.Error("a path with a space was accepted")
	}
}
