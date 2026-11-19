package skillsets

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/testpath"
)

// gitEnv is the environment of the test's git commands and of the Git under
// test: no user or system configuration, a fixed identity, no signing.
func gitEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=omo-test", "GIT_AUTHOR_EMAIL=omo@test",
		"GIT_COMMITTER_NAME=omo-test", "GIT_COMMITTER_EMAIL=omo@test",
	)
	if len(extra) == 0 {
		extra = []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=false"}
	}
	return append(env, extra...)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// upstream is a local bare repository with a working clone to commit from.
type upstream struct {
	t    *testing.T
	bare string
	work string
}

func newUpstream(t *testing.T, files map[string]string) *upstream {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	u := &upstream{t: t, bare: filepath.Join(t.TempDir(), "skills.git"), work: t.TempDir()}
	git(t, u.work, "init", "-q", "-b", "main")
	u.commit(files)
	git(t, u.work, "clone", "-q", "--bare", u.work, u.bare)
	return u
}

// commit commits files in the working clone and pushes them to the bare
// repository once it exists; it returns the commit.
func (u *upstream) commit(files map[string]string) string {
	u.t.Helper()
	for p, c := range files {
		writeFile(u.t, filepath.Join(u.work, p), c)
	}
	git(u.t, u.work, "add", "-A")
	git(u.t, u.work, "commit", "-q", "-m", "change")
	if _, err := os.Stat(u.bare); err == nil {
		git(u.t, u.work, "push", "-q", u.bare, "main")
	}
	return git(u.t, u.work, "rev-parse", "HEAD")
}

func (u *upstream) head() string { return git(u.t, u.bare, "rev-parse", "main") }

// env is a skills folder, a settings store and a Git for a manager.
type env struct {
	t     *testing.T
	root  string
	dir   string
	store *settings.Store
	git   *gitops.Git

	mu     sync.Mutex
	events []api.SkillSetState
}

func newEnv(t *testing.T, extraGitEnv ...string) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, root: root, dir: filepath.Join(root, "skills"), git: gitops.New()}
	for _, d := range []string{e.dir, filepath.Join(root, "worktrees")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	e.git.SetEnvironment(gitEnv(t, extraGitEnv...))
	e.git.SetAgentFolders(filepath.Join(root, "worktrees"))
	var err error
	if e.store, err = settings.Open(root, settings.Options{}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) manager() *Manager {
	m := New(Options{Dir: e.dir, Git: e.git, Store: e.store, Poll: 20 * time.Millisecond, OnChange: func(st api.SkillSetState) {
		e.mu.Lock()
		e.events = append(e.events, st)
		e.mu.Unlock()
	}})
	e.t.Cleanup(m.Stop)
	return m
}

func (e *env) put(name, repo, ref, update string, text map[string]string) {
	e.t.Helper()
	if err := e.store.PutSkillSetIf("", api.SkillSet{Name: name, Repository: repo, Ref: ref, Update: update, Enabled: true, Instructions: text}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) eventCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.events)
}

// waitChange observes publication independently of the manager's state:
// finish makes state visible before calling OnChange.
func (e *env) waitChange(name, state string) {
	e.t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		e.mu.Lock()
		found := false
		for _, event := range e.events {
			if event.Name == name && event.State == state {
				found = true
				break
			}
		}
		e.mu.Unlock()
		if found {
			return
		}
		select {
		case <-tick.C:
		}
	}
}

// waitState waits until the set's state is want and returns it.
func waitState(t *testing.T, m *Manager, name, want string) api.SkillSetState {
	t.Helper()
	for {
		st := m.State(name)
		if st.State == want {
			return st
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// waitCommit waits until the set's checkout is at commit.
func waitCommit(t *testing.T, m *Manager, name, commit string) {
	t.Helper()
	for {
		st := m.State(name)
		if st.State == StateOK && st.Commit == commit {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// onlySets fails when the skills folder holds anything but the named
// checkouts.
func onlySets(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for _, e := range entries {
		if !want[e.Name()] {
			t.Errorf("left in the skills folder: %s", e.Name())
		}
	}
}

func TestSkillSetFetchedAtStart(t *testing.T) {
	u := newUpstream(t, map[string]string{"SKILLS.md": "one\n"})
	e := newEnv(t)
	e.put("sp", u.bare, "", api.SkillSetAtStart, map[string]string{"developer": "Read SKILLS.md first."})
	m := e.manager()
	m.Start(context.Background())
	st := waitState(t, m, "sp", StateOK)
	if st.Commit != u.head() || st.Path != filepath.Join(e.dir, "sp") || st.UpdatedAt == nil || st.Message != "" {
		t.Fatalf("state = %+v, want commit %s", st, u.head())
	}
	if readFile(t, filepath.Join(e.dir, "sp", "SKILLS.md")) != "one\n" {
		t.Fatal("the checkout lacks the repository's file")
	}
	e.waitChange("sp", StateOK)
	e.mu.Lock()
	first := e.events[0]
	last := e.events[len(e.events)-1]
	e.mu.Unlock()
	if first.State != StateUpdating || last.State != StateOK {
		t.Fatalf("events %+v … %+v", first, last)
	}
	if got := m.States(); len(got) != 1 || got[0].Name != "sp" || got[0].State != StateOK {
		t.Fatalf("States = %+v", got)
	}
	onlySets(t, e.dir, "sp")
}

func TestSkillSetPinnedNotUpdated(t *testing.T) {
	u := newUpstream(t, map[string]string{"a.md": "one\n"})
	first := u.head()
	e := newEnv(t)
	e.put("pinned", u.bare, "main", api.SkillSetPinned, nil)
	e.put("zz-fresh", u.bare, "main", api.SkillSetAtStart, nil)
	m := e.manager()
	m.Start(context.Background())
	waitState(t, m, "pinned", StateOK)
	waitState(t, m, "zz-fresh", StateOK)
	m.Stop()

	second := u.commit(map[string]string{"a.md": "two\n"})
	// A restart: a new manager on the same folder.
	m2 := e.manager()
	m2.Start(context.Background())
	waitCommit(t, m2, "zz-fresh", second)
	// The last startup set completed, so every earlier set was considered.
	// Join any fetch wrongly queued for the pinned set without cancelling it.
	m2.Reconcile()
	m2.mu.Lock()
	var pending []*job
	for _, j := range m2.jobs {
		pending = append(pending, j)
	}
	m2.mu.Unlock()
	for _, j := range pending {
		<-j.done
	}
	if st := m2.State("pinned"); st.State != StateOK || st.Commit != first {
		t.Fatalf("pinned set after restart = %+v, want %s", st, first)
	}
	if st := m2.Update(context.Background(), "pinned"); st.State != StateOK || st.Commit != second {
		t.Fatalf("Update = %+v, want %s", st, second)
	}
	if readFile(t, filepath.Join(e.dir, "pinned", "a.md")) != "two\n" {
		t.Fatal("the pinned checkout was not updated by Update")
	}
}

func TestSkillSetUpdateSwaps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an open file cannot be renamed away on Windows")
	}
	u := newUpstream(t, map[string]string{"a.md": "old\n"})
	e := newEnv(t)
	e.put("s", u.bare, "", api.SkillSetPinned, nil)
	m := e.manager()
	m.Start(context.Background())
	waitState(t, m, "s", StateOK)
	f, err := os.Open(filepath.Join(e.dir, "s", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	second := u.commit(map[string]string{"a.md": "new\n", "b.md": "b\n"})
	if st := m.Update(context.Background(), "s"); st.Commit != second {
		t.Fatalf("Update = %+v", st)
	}
	buf := make([]byte, 16)
	n, _ := f.Read(buf)
	if string(buf[:n]) != "old\n" {
		t.Fatalf("a reader of the old checkout saw %q", buf[:n])
	}
	if readFile(t, filepath.Join(e.dir, "s", "a.md")) != "new\n" || readFile(t, filepath.Join(e.dir, "s", "b.md")) != "b\n" {
		t.Fatal("the new checkout is not complete")
	}
	onlySets(t, e.dir, "s")
}

func TestSkillSetFailureHidesCredentials(t *testing.T) {
	e := newEnv(t)
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	e.git.SetEnvironment(append(gitEnv(t), "GIT_SSL_CAINFO="+ca))
	e.put("s", strings.Replace(srv.URL, "https://", "https://user:SECRET@", 1)+"/x.git", "", api.SkillSetPinned, nil)
	m := e.manager()
	st := m.Update(context.Background(), "s")
	if requests.Load() == 0 {
		t.Fatal("the fetch did not reach the failing server")
	}
	if st.State != StateFailed || st.Message == "" || st.Commit != "" {
		t.Fatalf("state = %+v", st)
	}
	if strings.Contains(st.Message, "SECRET") || strings.Contains(st.Message, "user:") || len([]rune(st.Message)) > maxMessage {
		t.Fatalf("message = %q", st.Message)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range e.events {
		if strings.Contains(ev.Message, "SECRET") {
			t.Fatalf("event repeats the password: %+v", ev)
		}
	}
}

func TestSkillSetFailureKeepsLastCheckout(t *testing.T) {
	u := newUpstream(t, map[string]string{"a.md": "one\n"})
	e := newEnv(t)
	e.put("s", u.bare, "", api.SkillSetPinned, nil)
	m := e.manager()
	good := m.Update(context.Background(), "s")
	if good.State != StateOK {
		t.Fatalf("first update = %+v", good)
	}
	// The repository goes away: the update of the same source fails.
	if err := os.Rename(u.bare, u.bare+".gone"); err != nil {
		t.Fatal(err)
	}
	st := m.Update(context.Background(), "s")
	if st.State != StateFailed || st.Commit != good.Commit || st.Message == "" {
		t.Fatalf("failed update = %+v, want the old commit %s kept", st, good.Commit)
	}
	if readFile(t, filepath.Join(e.dir, "s", "a.md")) != "one\n" {
		t.Fatal("the last good checkout is gone")
	}
	onlySets(t, e.dir, "s")
}

func TestSkillSetHostileRepositoryHasNoEffect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh hooks and links")
	}
	outside := t.TempDir()
	marker := filepath.Join(outside, "ran")
	secret := filepath.Join(outside, "secret.txt")
	writeFile(t, secret, "secret\n")
	u := newUpstream(t, map[string]string{
		".gitattributes": "* filter=evil\n",
		"SKILLS.md":      "hello\n",
	})
	// A link out of the checkout, and hooks in the upstream repositories.
	if err := os.Symlink(secret, filepath.Join(u.work, "link")); err != nil {
		t.Fatal(err)
	}
	u.commit(nil)
	hook := "#!/bin/sh\ntouch " + marker + "\n"
	for _, dir := range []string{filepath.Join(u.bare, "hooks"), filepath.Join(u.work, ".git", "hooks")} {
		for _, h := range []string{"post-checkout", "post-merge", "pre-upload-pack", "post-upload-pack", "reference-transaction"} {
			writeFile(t, filepath.Join(dir, h), hook)
			if err := os.Chmod(filepath.Join(dir, h), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The user's configuration names the filter the repository asks for.
	e := newEnv(t, "GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=filter.evil.smudge", "GIT_CONFIG_VALUE_1=touch "+marker,
		"GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_2="+filepath.Join(u.bare, "hooks"))
	e.put("s", u.bare, "", api.SkillSetAtStart, map[string]string{"developer": "x"})
	m := e.manager()
	if st := m.Update(context.Background(), "s"); st.State != StateOK {
		t.Fatalf("update = %+v", st)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a hook or filter of the repository ran")
	}
	fi, err := os.Lstat(filepath.Join(e.dir, "s", "link"))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() {
		t.Fatalf("the link arrived as %v, want a plain file", fi.Mode())
	}
	if readFile(t, filepath.Join(e.dir, "s", "link")) == "secret\n" {
		t.Fatal("the checkout reads a file outside of it")
	}
	if readFile(t, filepath.Join(e.dir, "s", "SKILLS.md")) != "hello\n" {
		t.Fatal("the content was changed on checkout")
	}
	onlySets(t, e.dir, "s")
}

func TestSkillSetRemovedDeletesCheckout(t *testing.T) {
	u := newUpstream(t, map[string]string{"a.md": "one\n"})
	e := newEnv(t)
	e.put("a", u.bare, "", api.SkillSetPinned, nil)
	e.put("b", u.bare, "", api.SkillSetPinned, nil)
	m := e.manager()
	m.Update(context.Background(), "a")
	m.Update(context.Background(), "b")
	// Through the API: the store first, then Removed.
	if err := e.store.DeleteSkillSetIf("", "a"); err != nil {
		t.Fatal(err)
	}
	m.Removed("a")
	if _, err := os.Lstat(filepath.Join(e.dir, "a")); err == nil {
		t.Fatal("the checkout of a removed set is still there")
	}
	// By hand: the file goes, the next comparison removes the checkout.
	if err := os.Remove(filepath.Join(e.root, "skillsets", "b.yaml")); err != nil {
		t.Fatal(err)
	}
	e.store.Reload()
	m.Reconcile()
	if _, err := os.Lstat(filepath.Join(e.dir, "b")); err == nil {
		t.Fatal("the checkout of a set removed on disk is still there")
	}
	if got := m.States(); len(got) != 0 {
		t.Fatalf("States = %+v", got)
	}
	onlySets(t, e.dir)
}

func TestSkillSetBrokenFileKeepsCheckout(t *testing.T) {
	u := newUpstream(t, map[string]string{"a.md": "one\n"})
	e := newEnv(t)
	e.put("a", u.bare, "", api.SkillSetPinned, nil)
	m := e.manager()
	m.Update(context.Background(), "a")
	writeFile(t, filepath.Join(e.root, "skillsets", "a.yaml"), "repository: [\n")
	e.store.Reload()
	m.Reconcile()
	m.Stop()
	// A restart with the broken file keeps the checkout too.
	m2 := e.manager()
	m2.Start(context.Background())
	// Stop joins startup cleanup, so the checkout assertion observes its result.
	m2.Stop()
	if _, ok := m2.Checkout("a"); !ok {
		t.Fatal("a hand-broken settings file cost the set its checkout")
	}
}

func TestSkillSetSourceChange(t *testing.T) {
	u := newUpstream(t, map[string]string{"a.md": "one\n"})
	first := u.head()
	git(t, u.work, "checkout", "-q", "-b", "next")
	second := u.commit(map[string]string{"a.md": "two\n"})
	git(t, u.work, "push", "-q", u.bare, "next")
	e := newEnv(t)
	e.put("auto", u.bare, "main", api.SkillSetAtStart, nil)
	e.put("pin", u.bare, "main", api.SkillSetPinned, nil)
	m := e.manager()
	m.Start(context.Background())
	waitState(t, m, "auto", StateOK)
	waitState(t, m, "pin", StateOK)
	if st := m.State("pin"); st.Commit != first {
		t.Fatalf("pinned set = %+v, want %s", st, first)
	}
	e.put("auto", u.bare, "next", api.SkillSetAtStart, nil)
	e.put("pin", u.bare, "next", api.SkillSetPinned, nil)
	m.Reconcile()
	// A changed source is fetched whatever the update: pinned means not
	// following the branch, not keeping another source's checkout.
	waitCommit(t, m, "auto", second)
	waitCommit(t, m, "pin", second)
}

// A fetch replaces a checkout while omo reads it (the manager's own
// comparison, states, prompts): every replacement succeeds. Windows refuses
// to rename a folder while a file in it is open, so omo's readers and the
// swap must take turns.
func TestSkillSetReplacedWhileRead(t *testing.T) {
	u := newUpstream(t, map[string]string{"one/SKILL.md": "---\nname: one\n---\n"})
	e := newEnv(t)
	e.put("s", u.bare, "main", api.SkillSetPinned, map[string]string{"developer": "Use them."})
	m := e.manager()
	if st := m.Update(context.Background(), "s"); st.State != StateOK {
		t.Fatalf("first fetch: %+v", st)
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				m.Reconcile()
				m.States()
				Text("developer", e.store.SkillSets(), m.Checkout)
			}
		})
	}
	for i := range 5 {
		want := u.commit(map[string]string{"one/SKILL.md": fmt.Sprintf("---\nname: one\ndescription: %d\n---\n", i)})
		if st := m.Update(context.Background(), "s"); st.State != StateOK || st.Commit != want {
			close(stop)
			readers.Wait()
			t.Fatalf("fetch %d while the checkout is read: %+v, want commit %s", i, st, want)
		}
	}
	close(stop)
	readers.Wait()
	onlySets(t, e.dir, "s")
}

// Until a changed source is fetched, agents get nothing of the old
// checkout, and the state does not show it as the set's: a failing fetch
// of the new source leaves the set without skills.
func TestSkillSetOldCheckoutIsNotGivenAfterASourceChange(t *testing.T) {
	u := newUpstream(t, map[string]string{"SKILLS.md": "one\n"})
	e := newEnv(t)
	e.put("s", u.bare, "main", api.SkillSetPinned, map[string]string{"developer": "Use them."})
	m := e.manager()
	m.Start(context.Background())
	waitState(t, m, "s", StateOK)
	text := func() string { return Text("developer", e.store.SkillSets(), CheckoutIn(e.dir)) }
	if !strings.Contains(text(), "Use them.") {
		t.Fatalf("text before the change: %q", text())
	}
	e.store.PutSkillSetIf(e.store.Revision(settings.SectionSkillSets, "s"), api.SkillSet{Name: "s", Repository: u.bare, Ref: "no-such-branch",
		Update: api.SkillSetPinned, Enabled: true, Instructions: map[string]string{"developer": "Use them."}})
	if got := text(); got != "" {
		t.Fatalf("text right after the change: %q", got)
	}
	m.Reconcile()
	st := waitState(t, m, "s", StateFailed)
	if st.Commit != "" {
		t.Fatalf("the state shows the old checkout as the set's: %+v", st)
	}
	if got := text(); got != "" {
		t.Fatalf("text after the failed fetch: %q", got)
	}
	if _, ok := m.Checkout("s"); ok {
		t.Fatal("the old checkout counts as the set's")
	}
}

func TestSkillSetStartCleansLeftovers(t *testing.T) {
	e := newEnv(t)
	for _, d := range []string{".s.new-123", ".s.old-456/old", "orphan", "s"} {
		if err := os.MkdirAll(filepath.Join(e.dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(e.dir, "NOTES"), "kept: not a set name\n")
	e.put("s", testpath.Abs("/nonexistent/repo.git"), "", api.SkillSetPinned, nil)
	m := e.manager()
	m.Start(context.Background())
	for {
		entries, _ := os.ReadDir(e.dir)
		if len(entries) == 2 {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}
	onlySets(t, e.dir, "s", "NOTES")
}

func TestSkillSetStopDuringFetch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a sh ssh command")
	}
	// An ssh that never answers: the fetch hangs until it is cancelled.
	started := filepath.Join(t.TempDir(), "ssh-started")
	ssh := filepath.Join(t.TempDir(), "ssh")
	writeFile(t, ssh, "#!/bin/sh\nprintf ready > "+shellQuote(started)+"\nexec sleep 60\n")
	if err := os.Chmod(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=false", "GIT_SSH_COMMAND="+shellQuote(ssh))
	e.put("s", "ssh://git@127.0.0.1/x.git", "", api.SkillSetAtStart, nil)
	m := e.manager()
	m.Start(context.Background())
	waitState(t, m, "s", StateUpdating)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}
	before := e.eventCount()
	done := make(chan struct{})
	go func() { m.Stop(); close(done) }()
	<-done
	if e.eventCount() != before {
		t.Fatal("an event was sent after Stop")
	}
	if m.Update(context.Background(), "s").State == StateOK {
		t.Fatal("an update ran after Stop")
	}
	onlySets(t, e.dir)
}

func TestSkillSetOneFetchPerSet(t *testing.T) {
	u := newUpstream(t, map[string]string{"a.md": "one\n"})
	e := newEnv(t)
	e.put("s", u.bare, "", api.SkillSetPinned, nil)
	m := e.manager()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st := m.Update(context.Background(), "s"); st.State != StateOK {
				t.Errorf("Update = %+v", st)
			}
		}()
	}
	wg.Wait()
	onlySets(t, e.dir, "s")
}

func TestMessage(t *testing.T) {
	long := strings.Repeat("x", 2000)
	if got := Message(errString("fetching https://u:pw@h/x failed: ‮denied\n" + long)); strings.Contains(got, "pw") ||
		strings.ContainsRune(got, '‮') || strings.Contains(got, "\n") || len([]rune(got)) > maxMessage {
		t.Fatalf("Message = %q", got)
	}
	if Message(context.Canceled) != "the update was cancelled" || Message(nil) != "" {
		t.Fatal("fixed messages")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// The state names the set's paths its checkout does not have, at once
// after a fetch, after a change of the paths and after a new commit.
func TestStateNamesMissingPaths(t *testing.T) {
	u := newUpstream(t, map[string]string{"skills/a/SKILL.md": "a\n", "docs/b.md": "b\n"})
	e := newEnv(t)
	k := api.SkillSet{Name: "s", Repository: u.bare, Ref: "main", Update: api.SkillSetAtStart, Enabled: true, Paths: []string{"skills/a", "skills/c", "docs/b.md"}}
	if err := e.store.PutSkillSetIf("", k); err != nil {
		t.Fatal(err)
	}
	m := e.manager()
	if st := m.State("s"); st.MissingPaths == nil || len(st.MissingPaths) != 0 {
		t.Fatalf("without checkout: %+v", st)
	}
	m.Start(context.Background())
	st := waitState(t, m, "s", StateOK)
	if strings.Join(st.MissingPaths, ",") != "skills/c" {
		t.Fatalf("state = %+v", st)
	}
	e.waitChange("s", StateOK)
	e.mu.Lock()
	last := e.events[len(e.events)-1]
	e.mu.Unlock()
	if strings.Join(last.MissingPaths, ",") != "skills/c" {
		t.Fatalf("event = %+v", last)
	}
	k.Paths = []string{"docs/b.md", "skills"}
	if err := e.store.PutSkillSetIf("", k); err != nil {
		t.Fatal(err)
	}
	if st := m.State("s"); len(st.MissingPaths) != 0 {
		t.Fatalf("after the change: %+v", st)
	}
	u.commit(map[string]string{"docs/b.md": ""})
	git(t, u.work, "rm", "-q", "docs/b.md")
	git(t, u.work, "commit", "-q", "-m", "remove")
	git(t, u.work, "push", "-q", u.bare, "main")
	m.Update(context.Background(), "s")
	if st := m.State("s"); strings.Join(st.MissingPaths, ",") != "docs/b.md" {
		t.Fatalf("after the update: %+v", st)
	}
	for _, st := range m.States() {
		if st.MissingPaths == nil {
			t.Fatalf("States has nil missing paths: %+v", st)
		}
	}
}

// shellQuote protects temporary paths passed through git's SSH shell.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// A request arriving while the preceding worker retires must either join
// another fetch or start a new worker; it cannot disappear with the old job.
func TestSkillSetUpdateDuringWorkerRetirementIsNotLost(t *testing.T) {
	u := newUpstream(t, map[string]string{"a.md": "one\n"})
	e := newEnv(t)
	e.put("s", u.bare, "main", api.SkillSetPinned, nil)
	m := e.manager()
	finished, releaseFinish := make(chan struct{}), make(chan struct{})
	retiring, releaseRetire := make(chan struct{}), make(chan struct{})
	var finishOnce, releaseFinishOnce, releaseRetireOnce sync.Once
	releaseFirst := func() { releaseFinishOnce.Do(func() { close(releaseFinish) }) }
	releaseLast := func() { releaseRetireOnce.Do(func() { close(releaseRetire) }) }
	t.Cleanup(releaseFirst)
	t.Cleanup(releaseLast)
	m.o.OnChange = func(st api.SkillSetState) {
		if st.State == StateOK {
			finishOnce.Do(func() { close(finished); <-releaseFinish })
		}
	}
	first := m.enqueue("s")
	<-finished
	secondCommit := u.commit(map[string]string{"a.md": "two\n"})
	cancel := first.cancel
	first.cancel = func() { close(retiring); <-releaseRetire; cancel() }
	releaseFirst()
	<-retiring
	second := m.enqueue("s")
	releaseLast()
	<-second.done
	if st := m.State("s"); st.Commit != secondCommit {
		t.Fatalf("update during worker retirement was lost: commit = %s, want %s", st.Commit, secondCommit)
	}
}
