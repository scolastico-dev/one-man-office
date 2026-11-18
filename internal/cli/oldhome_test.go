package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/filelock"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/oldhome"
	"github.com/scolastico-dev/one-man-office/internal/selfupdate"
)

// oldMachine is what omo 0.11 left: a home with config.yaml trusting one
// office, the office's .omo folder (configuration, database, a worktree
// folder) and a file of the user's in the office.
type oldMachine struct {
	home, office, officeConfig string
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func buildOldMachine(t *testing.T, worktree string) oldMachine {
	t.Helper()
	home := isolate(t)
	m := oldMachine{home: home, office: filepath.Join(t.TempDir(), "acme")}
	m.officeConfig = filepath.Join(m.office, ".omo", "omo.yaml")
	writeFile(t, filepath.Join(home, "config.yaml"), "trusted_offices:\n  - "+m.office+"\ntemplate:\n  enabled: false\nplugins:\n  update_on_start: true\n  installed: {}\n")
	writeFile(t, filepath.Join(home, "known_plugins.json"), "[]\n")
	writeFile(t, filepath.Join(home, "plugins", "filebrowser", "plugin.json"), "{}\n")
	writeFile(t, filepath.Join(home, "company", "company.log"), "")
	writeFile(t, m.officeConfig, "repos:\n  api:\n    path: api\nstartup:\n  check_self_update: true\n")
	writeFile(t, filepath.Join(m.office, ".omo", "omo.db"), "")
	writeFile(t, filepath.Join(m.office, "notes.md"), "mine\n")
	if worktree != "" {
		// A folder git does not know: it counts as uncommitted work.
		writeFile(t, filepath.Join(m.office, ".omo", "worktrees", worktree, "work.txt"), "x\n")
	}
	return m
}

// answering makes the input a terminal that answers with text.
func answering(t *testing.T, text string) io.Reader {
	t.Helper()
	old := inputIsTerminal
	inputIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { inputIsTerminal = old })
	return strings.NewReader(text)
}

func noTerminal(t *testing.T) {
	t.Helper()
	old := inputIsTerminal
	inputIsTerminal = func(io.Reader) bool { return false }
	t.Cleanup(func() { inputIsTerminal = old })
}

// executeWithInput runs omo with in as its input. Without arguments omo
// starts its server, which these tests must never get to on a home without
// a port setting (it would take omo's default port): the home must be old.
func executeWithInput(t *testing.T, in io.Reader, args ...string) (string, error) {
	t.Helper()
	if home, _ := globalhome.Dir(); len(args) == 0 && !oldhome.IsOld(home) {
		if _, err := os.Stat(filepath.Join(home, "photo.jpg")); err != nil {
			t.Fatal("a start on a home that is neither old nor foreign would take the default port")
		}
	}
	ctx := context.Background()
	cmd := Root("test")
	var out syncBuffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(in)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	return out.String(), err
}

func TestOldInstallationWithoutTerminalChangesNothing(t *testing.T) {
	m := buildOldMachine(t, "api-1")
	noTerminal(t)
	before, office := snapshotTree(t, m.home), snapshotTree(t, m.office)
	out, err := executeWithInput(t, strings.NewReader(""))
	if !errors.Is(err, errOldInstallation) {
		t.Fatalf("err = %v\n%s", err, out)
	}
	for _, want := range []string{
		"omo found the files of omo 0.11 or older in " + m.home,
		"are not carried over",
		"office           " + m.office,
		"CANNOT BE CHECKED",
		"started without a terminal",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if !reflect.DeepEqual(before, snapshotTree(t, m.home)) || !reflect.DeepEqual(office, snapshotTree(t, m.office)) {
		t.Fatal("a start without a terminal changed the old installation")
	}
}

func TestOldInstallationQuitChangesNothing(t *testing.T) {
	for name, answer := range map[string]string{"3": "3\n", "empty": "\n", "end of input": "", "continue then no": "1\nn\n"} {
		t.Run(name, func(t *testing.T) {
			m := buildOldMachine(t, "")
			before, office := snapshotTree(t, m.home), snapshotTree(t, m.office)
			out, err := executeWithInput(t, answering(t, answer))
			if err != nil {
				t.Fatalf("err = %v\n%s", err, out)
			}
			if !strings.Contains(out, "Nothing was changed.") {
				t.Fatalf("output:\n%s", out)
			}
			if !reflect.DeepEqual(before, snapshotTree(t, m.home)) || !reflect.DeepEqual(office, snapshotTree(t, m.office)) {
				t.Fatal("quitting changed the old installation")
			}
		})
	}
}

func TestOldInstallationWithUncommittedWorkNeedsTheWord(t *testing.T) {
	m := buildOldMachine(t, "api-1")
	before := snapshotTree(t, m.home)
	out, err := executeWithInput(t, answering(t, "1\ny\n"))
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 worktree marked above holds work nobody committed") || !strings.Contains(out, "Type delete") || !strings.Contains(out, "Nothing was changed.") {
		t.Fatalf("output:\n%s", out)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, m.home)) {
		t.Fatal("a y in place of the word deleted the old installation")
	}
}

func TestOldInstallationContinueEmptiesItAndTheStartGoesOn(t *testing.T) {
	m := buildOldMachine(t, "api-1")
	// The server itself is not started here: an emptied home has no port
	// setting, and a test must not use omo's default port.
	cmd := Root("test")
	var out syncBuffer
	cmd.SetOut(&out)
	cmd.SetIn(answering(t, "1\ndelete\n"))
	goOn, err := meetOldInstallation(context.Background(), cmd)
	if err != nil || !goOn {
		t.Fatalf("goOn = %v, err = %v\n%s", goOn, err, out.String())
	}
	for _, want := range []string{"removed " + filepath.Join(m.office, ".omo"), "emptied " + m.home, "The old installation is removed. Starting omo."} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(m.office, ".omo")); !os.IsNotExist(err) {
		t.Fatalf(".omo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.office, "notes.md")); err != nil {
		t.Fatal("a file of the user's was removed")
	}
	if entries, _ := os.ReadDir(m.home); len(entries) != 0 {
		t.Fatalf("home still holds %v", entries)
	}
	// What the start does next: the home opens as a fresh one.
	if _, err := globalhome.Open(); err != nil {
		t.Fatal(err)
	}
}

func TestOldInstallationContinueRefusedWhileTheOldDashboardRuns(t *testing.T) {
	m := buildOldMachine(t, "")
	lock := filepath.Join(m.home, "company", "run.lock")
	writeFile(t, lock, "")
	// The home is read before the lock is taken and after it is released:
	// a lock of Windows keeps every other handle from reading the locked
	// file, while a lock of Unix only keeps others from locking it. Taking
	// the lock writes nothing, so both readings cover the whole home.
	before := snapshotTree(t, m.home)
	held, err := filelock.Acquire(context.Background(), lock)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	out, err := executeWithInput(t, answering(t, "1\ny\n"))
	if !errors.Is(err, errOldInstallation) {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !strings.Contains(out, "Stop the old omo first (the company dashboard of "+m.home+")") {
		t.Fatalf("output:\n%s", out)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, m.home)) {
		t.Fatal("the home of a running old omo was changed")
	}
}

func TestOldInstallationGoBackInstallsTheLastTerminalReleaseAndStopsItsOffer(t *testing.T) {
	restoreUpdateHooks(t) // first: it sets a home of its own
	m := buildOldMachine(t, "")
	releaseByTag = func(_ context.Context, tag string) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: tag}, nil
	}
	installed, target := "", ""
	installRelease = func(_ context.Context, r selfupdate.Release, to string) error {
		installed, target = r.Tag, to
		return nil
	}
	exe := filepath.Join(t.TempDir(), "omo")
	writeFile(t, exe, "")
	currentExecutable = func() (string, error) { return exe, nil }
	// omo installs at the program's real path (links resolved); on Windows
	// that also turns the short names of the temporary folder
	// (RUNNER~1) into the long ones.
	realExe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, m.home)
	out, err := executeWithInput(t, answering(t, "2\n"))
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if installed != oldhome.LastRelease || target != realExe {
		t.Fatalf("installed %q at %q", installed, target)
	}
	raw, _ := os.ReadFile(m.officeConfig)
	if !strings.Contains(string(raw), "check_self_update: false") || !strings.Contains(string(raw), "path: api") {
		t.Fatalf("office config:\n%s", raw)
	}
	if !reflect.DeepEqual(before, snapshotTree(t, m.home)) {
		t.Fatal("going back changed the old home")
	}
	if !strings.Contains(out, "run omo self-update") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestOldInstallationGoBackFailingChangesNothing(t *testing.T) {
	restoreUpdateHooks(t) // first: it sets a home of its own
	m := buildOldMachine(t, "")
	releaseByTag = func(context.Context, string) (selfupdate.Release, error) {
		return selfupdate.Release{}, errors.New("offline")
	}
	installRelease = func(context.Context, selfupdate.Release, string) error {
		t.Fatal("nothing to install")
		return nil
	}
	office := snapshotTree(t, m.office)
	out, err := executeWithInput(t, answering(t, "2\n"))
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !reflect.DeepEqual(office, snapshotTree(t, m.office)) {
		t.Fatal("a failed go-back changed the office")
	}
}

func TestAFolderThatIsNoOmoHomeIsNeverOfferedForDeletion(t *testing.T) {
	home := isolate(t)
	writeFile(t, filepath.Join(home, "photo.jpg"), "x")
	out, err := executeWithInput(t, answering(t, "1\ny\n"))
	if err == nil || !strings.Contains(err.Error(), "is no omo home") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, "photo.jpg")); err != nil {
		t.Fatal(err)
	}
}

func TestOldCommandsExplainWhatHappened(t *testing.T) {
	m := buildOldMachine(t, "")
	for _, args := range [][]string{{"company", "autostart-run", filepath.Join(m.home, "company", "autostart.json")}, {"--mock"}, {"setup"}} {
		_, err := executeWithInput(t, strings.NewReader(""), args...)
		err = Explain(err)
		if err == nil || !strings.Contains(err.Error(), "This omo replaced omo 0.11 or older") {
			t.Fatalf("omo %v: %v", args, err)
		}
	}
	// On a home of this omo the error stays as it is.
	isolate(t)
	mustExecute(t, "config", "set", "port", "18999")
	_, err := executeWithInput(t, strings.NewReader(""), "setup")
	if err = Explain(err); err == nil || strings.Contains(err.Error(), "replaced") {
		t.Fatalf("err = %v", err)
	}
}
