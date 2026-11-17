package oldhome

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/filelock"
	"github.com/scolastico-dev/one-man-office/internal/gittest"
)

func TestMain(m *testing.M) {
	gittest.Isolate()
	os.Exit(m.Run())
}

// old is a machine as omo 0.11 left it: a home, one office with a
// repository and two worktrees (one with uncommitted work), one trusted
// office that no longer exists, and the dashboard's login autostart entry.
type old struct {
	home, office, repo, gone, clean, dirty, autostart string
}

// The global config.yaml omo 0.11 wrote, with one office trusted.
const globalConfig = `trusted_offices:
  - %OFFICE%
  - %GONE%
template:
  enabled: false
  auto_sync: false
  setup_never_ask: false
plugins:
  update_on_start: true
  installed: {}
`

// An office configuration as omo 0.11 wrote it (shortened).
const officeConfig = `repos: # local paths
  api:
    path: api
models:
  sonnet:
    provider: claude
    cmd: claude
    args: ["--model", "sonnet", "--dangerously-skip-permissions"]
startup:
  check_self_update: true # check the latest GitHub release on boot
  check_templates: true
  check_timeout: 5s
`

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.invalid", "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func buildOld(t *testing.T) old {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "user"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "user", ".config"))
	o := old{
		home:   filepath.Join(root, "user", ".local", "omo"),
		office: filepath.Join(root, "work", "acme"),
		gone:   filepath.Join(root, "work", "moved"),
	}
	o.repo = filepath.Join(o.office, "api")
	write(t, filepath.Join(o.home, "config.yaml"), strings.NewReplacer("%OFFICE%", o.office, "%GONE%", o.gone).Replace(globalConfig))
	write(t, filepath.Join(o.home, "config.lock"), "")
	write(t, filepath.Join(o.home, "known_plugins.json"), "[]\n")
	write(t, filepath.Join(o.home, "known_plugins.example.json"), "[]\n")
	write(t, filepath.Join(o.home, "plugins", "filebrowser", "plugin.json"), "{}\n")
	write(t, filepath.Join(o.home, "superpowers", "README.md"), "skills\n")
	write(t, filepath.Join(o.home, "plugins.db"), "")
	write(t, filepath.Join(o.home, "company", "company.log"), "")
	write(t, filepath.Join(o.home, "company", "run.lock"), "")
	config := filepath.Join(o.home, "company", "autostart.json")
	write(t, config, `{"args":["company"],"directory":"/","omo_home":"`+o.home+`","path":"/usr/bin"}`)
	if runtime.GOOS == "linux" {
		o.autostart = filepath.Join(root, "user", ".config", "autostart", entryName(config)+".desktop")
		write(t, o.autostart, "[Desktop Entry]\nExec=omo company autostart-run "+config+"\n")
	}

	write(t, filepath.Join(o.repo, "README.md"), "# api\n")
	run(t, o.repo, "init", "-q", "-b", "main")
	run(t, o.repo, "add", ".")
	run(t, o.repo, "commit", "-qm", "init")
	write(t, filepath.Join(o.office, ".omo", "omo.yaml"), officeConfig)
	write(t, filepath.Join(o.office, ".omo", "omo.db"), "")
	write(t, filepath.Join(o.office, ".omo", "logs", "2026-01-01_10-00-developer-ada.log"), "log\n")
	o.clean = filepath.Join(o.office, ".omo", "worktrees", "api-1")
	o.dirty = filepath.Join(o.office, ".omo", "worktrees", "api-2")
	run(t, o.repo, "worktree", "add", "-q", "-b", "omo/job-1", o.clean)
	run(t, o.repo, "worktree", "add", "-q", "-b", "omo/job-2", o.dirty)
	write(t, filepath.Join(o.dirty, "work.txt"), "not committed\n")
	return o
}

func TestFindListsWhatTheOldOmoLeft(t *testing.T) {
	o := buildOld(t)
	in, err := Find(context.Background(), o.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Offices) != 1 || in.Offices[0].Dir != o.office || in.Offices[0].Config != filepath.Join(o.office, ".omo", "omo.yaml") {
		t.Fatalf("offices = %+v", in.Offices)
	}
	if !slices.Equal(in.Gone, []string{o.gone}) {
		t.Fatalf("gone = %v", in.Gone)
	}
	wt := in.Offices[0].Worktrees
	if len(wt) != 2 || wt[0].Path != o.clean || wt[0].Dirty || wt[1].Path != o.dirty || !wt[1].Dirty {
		t.Fatalf("worktrees = %+v", wt)
	}
	if d := in.Dirty(); len(d) != 1 || d[0].Path != o.dirty {
		t.Fatalf("dirty = %+v", d)
	}
	if len(in.Running()) != 0 {
		t.Fatalf("running = %v", in.Running())
	}
	if in.Autostart != o.autostart {
		t.Fatalf("autostart = %q, want %q", in.Autostart, o.autostart)
	}
	for _, name := range []string{"config.yaml", "company", "superpowers", "plugins"} {
		if !slices.Contains(in.Entries, name) {
			t.Fatalf("entries %v lack %s", in.Entries, name)
		}
	}
	// Find changed nothing.
	if _, err := os.Stat(filepath.Join(o.dirty, "work.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(o.home, "layout.json")); err == nil {
		t.Fatal("Find wrote into the old home")
	}
}

func TestRemoveDeletesOnlyWhatWasListedAndKeepsBranches(t *testing.T) {
	o := buildOld(t)
	// A file of the user's next to the office's .omo stays.
	write(t, filepath.Join(o.office, "notes.md"), "mine\n")
	in, err := Find(context.Background(), o.home)
	if err != nil {
		t.Fatal(err)
	}
	var said []string
	if err := in.Remove(context.Background(), func(s string) { said = append(said, s) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(o.office, ".omo")); !os.IsNotExist(err) {
		t.Fatalf(".omo still there: %v", err)
	}
	entries, err := os.ReadDir(o.home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("home not empty: %v %v", entries, err)
	}
	if o.autostart != "" {
		if _, err := os.Stat(o.autostart); !os.IsNotExist(err) {
			t.Fatalf("autostart entry still there: %v", err)
		}
	}
	for _, keep := range []string{filepath.Join(o.office, "notes.md"), filepath.Join(o.repo, "README.md")} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("%s was removed: %v", keep, err)
		}
	}
	if list := run(t, o.repo, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Fatalf("git still knows the removed worktrees:\n%s", list)
	}
	branches := run(t, o.repo, "branch", "--list", "--format=%(refname:short)")
	for _, b := range []string{"main", "omo/job-1", "omo/job-2"} {
		if !strings.Contains(branches, b) {
			t.Fatalf("branch %s is gone:\n%s", b, branches)
		}
	}
	if len(said) == 0 || !strings.Contains(strings.Join(said, "\n"), o.dirty) {
		t.Fatalf("said = %v", said)
	}
}

func TestRemoveRefusesWhileAnOldOfficeRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the old office's socket is a named pipe on Windows")
	}
	o := buildOld(t)
	dir, err := os.MkdirTemp("", "omo-old-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "omo.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	write(t, filepath.Join(o.office, ".omo", "omo.lock"), sock+"\n")
	in, err := Find(context.Background(), o.home)
	if err != nil {
		t.Fatal(err)
	}
	if got := in.Running(); !slices.Equal(got, []string{"the office " + o.office}) {
		t.Fatalf("running = %v", got)
	}
	if err := in.Remove(context.Background(), func(string) {}); err == nil || !strings.Contains(err.Error(), "stop it first") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(o.home, "config.yaml")); err != nil {
		t.Fatal("Remove changed the home of a running omo")
	}
	if _, err := os.Stat(filepath.Join(o.office, ".omo", "omo.yaml")); err != nil {
		t.Fatal("Remove changed a running office")
	}
}

func TestALockLeftByACrashIsNoRunningOffice(t *testing.T) {
	o := buildOld(t)
	write(t, filepath.Join(o.office, ".omo", "omo.lock"), filepath.Join(t.TempDir(), "gone.sock")+"\n")
	in, err := Find(context.Background(), o.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Running()) != 0 {
		t.Fatalf("running = %v", in.Running())
	}
}

func TestARunningDashboardIsFound(t *testing.T) {
	o := buildOld(t)
	l, err := filelock.Acquire(context.Background(), filepath.Join(o.home, "company", "run.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	in, err := Find(context.Background(), o.home)
	if err != nil {
		t.Fatal(err)
	}
	if got := in.Running(); !slices.Equal(got, []string{"the company dashboard of " + o.home}) {
		t.Fatalf("running = %v", got)
	}
}

func TestFindRefusesAFolderThatIsNoOmoHome(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "photo.jpg"), "x")
	if _, err := Find(context.Background(), home); !errors.Is(err, ErrNotOld) {
		t.Fatalf("err = %v", err)
	}
	if IsOld(home) {
		t.Fatal("IsOld")
	}
}

func TestAWorktreeGitCannotReadCountsAsDirty(t *testing.T) {
	o := buildOld(t)
	plain := filepath.Join(o.office, ".omo", "worktrees", "api-3")
	write(t, filepath.Join(plain, "file.txt"), "x")
	in, err := Find(context.Background(), o.home)
	if err != nil {
		t.Fatal(err)
	}
	var found *Worktree
	for i, w := range in.Offices[0].Worktrees {
		if w.Path == plain {
			found = &in.Offices[0].Worktrees[i]
		}
	}
	if found == nil || !found.Dirty || !found.Unknown {
		t.Fatalf("worktree = %+v", found)
	}
}

func TestDisableUpdateCheckChangesOnlyTheSwitch(t *testing.T) {
	o := buildOld(t)
	path := filepath.Join(o.office, ".omo", "omo.yaml")
	if err := DisableUpdateCheck(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	for _, want := range []string{"check_self_update: false # check the latest GitHub release on boot", "check_templates: true", "path: api", "repos: # local paths", `args: ["--model", "sonnet", "--dangerously-skip-permissions"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}

	bare := filepath.Join(t.TempDir(), "omo.yaml")
	write(t, bare, "repos: {}\n")
	if err := DisableUpdateCheck(bare); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(bare)
	if !strings.Contains(string(raw), "startup:\n  check_self_update: false") {
		t.Fatalf("got\n%s", raw)
	}
}
