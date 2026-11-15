package pluginmanager

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	bundledplugins "github.com/scolastico-dev/one-man-office/plugins"
)

// gitEnv is the environment of the test's git commands and of the Git under
// test: no user or system configuration, a fixed identity, no signing.
func gitEnv(t *testing.T) []string {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=omo-test", "GIT_AUTHOR_EMAIL=omo@test",
		"GIT_COMMITTER_NAME=omo-test", "GIT_COMMITTER_EMAIL=omo@test",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=false",
	)
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

// upstream is a bare repository and a work folder that publishes to it.
type upstream struct {
	t          *testing.T
	bare, work string
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	u := &upstream{t: t, bare: filepath.Join(t.TempDir(), "plugins.git"), work: t.TempDir()}
	git(t, u.work, "init", "-q", "-b", "main")
	git(t, filepath.Dir(u.bare), "init", "-q", "--bare", "-b", "main", u.bare)
	return u
}

// publish writes files (a value "" deletes the file) and pushes a commit;
// it returns the commit id.
func (u *upstream) publish(files map[string]string) string {
	u.t.Helper()
	for rel, text := range files {
		p := filepath.Join(u.work, filepath.FromSlash(rel))
		if text == "" {
			_ = os.Remove(p)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			u.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			u.t.Fatal(err)
		}
	}
	git(u.t, u.work, "add", "-A")
	git(u.t, u.work, "commit", "-q", "--allow-empty", "-m", "change")
	git(u.t, u.work, "push", "-q", u.bare, "main")
	return git(u.t, u.work, "rev-parse", "HEAD")
}

// env is an omo home with a plugins folder and an agents' folder.
type env struct {
	t    *testing.T
	root string
	o    Options
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "plugins")
	work := filepath.Join(home, "worktrees")
	for _, d := range []string{root, work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	g := gitops.New()
	g.SetEnvironment(gitEnv(t))
	g.SetAgentFolders(work)
	return &env{t: t, root: root, o: Options{Root: root, Git: g}}
}

func (e *env) install(s api.PluginSettings) (api.PluginSettings, string, error) {
	e.t.Helper()
	return Install(context.Background(), e.o, s)
}

// entries lists the names in the plugins folder and in its .cache folder.
func (e *env) entries() []string {
	e.t.Helper()
	var out []string
	list, err := os.ReadDir(e.root)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, x := range list {
		out = append(out, x.Name())
	}
	if list, err := os.ReadDir(filepath.Join(e.root, ".cache")); err == nil {
		for _, x := range list {
			out = append(out, ".cache/"+x.Name())
		}
	}
	return out
}

// onlyEntries fails unless the plugins folder holds exactly want (and an
// empty .cache folder, which may exist).
func (e *env) onlyEntries(want ...string) {
	e.t.Helper()
	var got []string
	for _, n := range e.entries() {
		if n != ".cache" {
			got = append(got, n)
		}
	}
	if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
		e.t.Fatalf("plugins folder holds %q, want %q", got, want)
	}
}

func (e *env) read(rel string) string {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(rel)))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(raw)
}

const demoManifest = `{"name":"demo","version":"1.0.0","description":"demo plugin",
"default_config":{"greeting":"hello","count":3,"nested":{"x":1.5}},
"hooks":[{"event":"ticket_created","lua":"hook.lua"},{"event":"manual","name":"run","description":"Run it","command":["./run.sh"]}]}`

func demoFiles(version string) map[string]string {
	return map[string]string{
		"README.md":                    "top level, not part of the plugin\n",
		"plugins/demo/plugin.json":     demoManifest,
		"plugins/demo/hook.lua":        "-- " + version + "\nreturn {message = config.greeting}\n",
		"plugins/demo/run.sh":          "#!/bin/sh\necho " + version + "\n",
		"plugins/demo/lib/helpers.lua": "-- helpers\n",
	}
}

func TestInstallFromLocalRepository(t *testing.T) {
	u := newUpstream(t)
	files := demoFiles("v1")
	head := u.publish(files)
	if err := os.Chmod(filepath.Join(u.work, "plugins/demo/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, u.work, "update-index", "--chmod=+x", "plugins/demo/run.sh")
	head = u.publish(nil)
	e := newEnv(t)

	got, commit, err := e.install(api.PluginSettings{
		Name: "demo", Source: u.bare, Subpath: "plugins/demo/", Enabled: true,
		Config: map[string]any{"greeting": "mine", "extra": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if commit != head {
		t.Fatalf("commit = %s, want %s", commit, head)
	}
	if got.Subpath != "plugins/demo" || got.Name != "demo" || !got.Enabled || got.Source != u.bare {
		t.Fatalf("settings = %+v", got)
	}
	wantConfig := map[string]any{"greeting": "mine", "extra": true, "count": int64(3), "nested": map[string]any{"x": 1.5}}
	if !reflect.DeepEqual(got.Config, wantConfig) {
		t.Fatalf("config = %#v, want %#v", got.Config, wantConfig)
	}
	if e.read("demo/plugin.json") != demoManifest || !strings.HasPrefix(e.read("demo/hook.lua"), "-- v1") || e.read("demo/lib/helpers.lua") != "-- helpers\n" {
		t.Fatal("installed files differ from the subpath")
	}
	for _, rel := range []string{"demo/README.md", "demo/.git", "demo/plugins"} {
		if _, err := os.Lstat(filepath.Join(e.root, rel)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after install (err %v)", rel, err)
		}
	}
	for rel, perm := range map[string]os.FileMode{"demo/plugin.json": 0o600, "demo/run.sh": 0o700, "demo/lib": 0o700} {
		info, err := os.Lstat(filepath.Join(e.root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != perm {
			t.Errorf("%s: mode %v, want %v", rel, info.Mode().Perm(), perm)
		}
	}
	e.onlyEntries("demo")

	inst, err := Installed(e.o, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if inst.Commit != head || inst.Source != u.bare || inst.Subpath != "plugins/demo" || inst.InstalledAt.IsZero() {
		t.Fatalf("installed = %+v", inst)
	}
	if _, err := Installed(e.o, "other"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Installed(other) err = %v", err)
	}
}

func TestInstallPinsRefAndCommit(t *testing.T) {
	u := newUpstream(t)
	first := u.publish(demoFiles("v1"))
	git(t, u.work, "tag", "v1")
	git(t, u.work, "push", "-q", u.bare, "v1")
	u.publish(demoFiles("v2"))
	e := newEnv(t)

	_, commit, err := e.install(api.PluginSettings{Name: "demo", Source: u.bare, Subpath: "plugins/demo", Ref: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if commit != first || !strings.HasPrefix(e.read("demo/hook.lua"), "-- v1") {
		t.Fatalf("ref v1 installed commit %s and %q", commit, e.read("demo/hook.lua"))
	}
	if inst, _ := Installed(e.o, "demo"); inst.Ref != "v1" || inst.Commit != first {
		t.Fatalf("installed = %+v", inst)
	}
	_, commit, err = e.install(api.PluginSettings{Name: "demo", Source: u.bare, Subpath: "plugins/demo", Ref: first})
	if err != nil || commit != first {
		t.Fatalf("by commit id: %s, %v", commit, err)
	}
}

func TestInstallRefusesBadSource(t *testing.T) {
	u := newUpstream(t)
	files := demoFiles("v1")
	files["old/plugin.json"] = `{"name":"old","hooks":[{"event":"prompt_render","lua":"x.lua"}]}`
	files["old/x.lua"] = "return 1\n"
	files["deps/plugin.json"] = `{"name":"deps","requires":["other"],"hooks":[]}`
	files["badlua/plugin.json"] = `{"name":"badlua","hooks":[{"event":"timer","interval":"1m","lua":"x.lua"}]}`
	files["badlua/x.lua"] = "this is not lua (\n"
	files["nolua/plugin.json"] = `{"name":"nolua","hooks":[{"event":"timer","interval":"1m","lua":"missing.lua"}]}`
	files["nomanifest/readme"] = "nothing\n"
	files["afile"] = "plain\n"
	u.publish(files)
	marker := filepath.Join(t.TempDir(), "ran")
	e := newEnv(t)

	cases := []struct {
		name  string
		s     api.PluginSettings
		field string
	}{
		{"ext transport", api.PluginSettings{Source: "ext::sh -c touch% " + marker}, "source"},
		{"fd transport", api.PluginSettings{Source: "fd::3"}, "source"},
		{"file url", api.PluginSettings{Source: "file://" + u.bare}, "source"},
		{"plain http", api.PluginSettings{Source: "http://127.0.0.1/x.git"}, "source"},
		{"option", api.PluginSettings{Source: "-uhoh"}, "source"},
		{"relative", api.PluginSettings{Source: "repo/x"}, "source"},
		{"empty", api.PluginSettings{Source: ""}, "source"},
		{"unknown builtin", api.PluginSettings{Source: "builtin:bugreport"}, "source"},
		{"builtin escape", api.PluginSettings{Source: "builtin:../pushover"}, "source"},
		{"option ref", api.PluginSettings{Source: u.bare, Ref: "-x"}, "ref"},
		{"upload pack ref", api.PluginSettings{Source: u.bare, Ref: "--upload-pack=touch " + marker}, "ref"},
		{"dots ref", api.PluginSettings{Source: u.bare, Ref: "a..b"}, "ref"},
		{"parent subpath", api.PluginSettings{Source: u.bare, Subpath: "../x"}, "subpath"},
		{"inner parent subpath", api.PluginSettings{Source: u.bare, Subpath: "plugins/../../x"}, "subpath"},
		{"absolute subpath", api.PluginSettings{Source: u.bare, Subpath: "/etc"}, "subpath"},
		{"git subpath", api.PluginSettings{Source: u.bare, Subpath: ".git"}, "subpath"},
		{"git hooks subpath", api.PluginSettings{Source: u.bare, Subpath: "x/.GIT/hooks"}, "subpath"},
		{"backslash subpath", api.PluginSettings{Source: u.bare, Subpath: `..\x`}, "subpath"},
		{"missing subpath", api.PluginSettings{Source: u.bare, Subpath: "nothere"}, "subpath"},
		{"file subpath", api.PluginSettings{Source: u.bare, Subpath: "afile"}, "subpath"},
		{"builtin subpath", api.PluginSettings{Source: "builtin:pushover", Subpath: "x"}, "subpath"},
		{"builtin ref", api.PluginSettings{Source: "builtin:pushover", Ref: "main"}, "ref"},
		{"old event", api.PluginSettings{Source: u.bare, Subpath: "old"}, ""},
		{"requires", api.PluginSettings{Source: u.bare, Subpath: "deps"}, ""},
		{"lua does not parse", api.PluginSettings{Source: u.bare, Subpath: "badlua"}, ""},
		{"lua missing", api.PluginSettings{Source: u.bare, Subpath: "nolua"}, ""},
		{"no manifest", api.PluginSettings{Source: u.bare, Subpath: "nomanifest"}, ""},
		{"top level without manifest", api.PluginSettings{Source: u.bare}, ""},
		{"missing repository", api.PluginSettings{Source: filepath.Join(t.TempDir(), "none.git")}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.s.Name = "demo"
			_, _, err := e.install(c.s)
			if err == nil {
				t.Fatal("install succeeded")
			}
			var fe *FieldError
			if c.field != "" && (!errors.As(err, &fe) || fe.Field != c.field) {
				t.Fatalf("err = %v, want a refusal of %s", err, c.field)
			}
			if c.name == "old event" && !strings.Contains(err.Error(), "prompt_render") {
				t.Fatalf("err = %v, want it to name prompt_render", err)
			}
			e.onlyEntries()
		})
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a source or ref ran a command")
	}
	if _, _, err := e.install(api.PluginSettings{Name: "../x", Source: "builtin:pushover"}); err == nil {
		t.Fatal("a name with a path was installed")
	}
}

func TestInstallErrorsHideCredentials(t *testing.T) {
	e := newEnv(t)
	_, _, err := e.install(api.PluginSettings{Name: "demo", Source: "https://someone:s3cr3t-pass@127.0.0.1:1/x.git"})
	if err == nil {
		t.Fatal("install succeeded")
	}
	if strings.Contains(err.Error(), "s3cr3t-pass") {
		t.Fatalf("err = %v holds the password", err)
	}
	e.onlyEntries()
}

func TestInstallSwapsAtomically(t *testing.T) {
	u := newUpstream(t)
	u.publish(demoFiles("v1"))
	e := newEnv(t)
	s := api.PluginSettings{Name: "demo", Source: u.bare, Subpath: "plugins/demo"}
	if _, _, err := e.install(s); err != nil {
		t.Fatal(err)
	}
	v1, _ := Installed(e.o, "demo")

	// A broken update leaves the working install as it was.
	u.publish(map[string]string{"plugins/demo/plugin.json": `{"name":"demo","hooks":[{"event":"cron","lua":"hook.lua"}]}`, "plugins/demo/hook.lua": "-- v2\n"})
	if _, _, err := e.install(s); err == nil || !strings.Contains(err.Error(), "cron") {
		t.Fatalf("broken update: err = %v", err)
	}
	if !strings.HasPrefix(e.read("demo/hook.lua"), "-- v1") || e.read("demo/plugin.json") != demoManifest {
		t.Fatal("a failed update changed the installed tree")
	}
	if now, _ := Installed(e.o, "demo"); now.Commit != v1.Commit {
		t.Fatalf("record changed to %s", now.Commit)
	}
	e.onlyEntries("demo")

	// A good update replaces the tree, files of the old one go.
	files := demoFiles("v3")
	files["plugins/demo/lib/helpers.lua"] = ""
	head := u.publish(files)
	if _, commit, err := e.install(s); err != nil || commit != head {
		t.Fatalf("update: %s, %v", commit, err)
	}
	if !strings.HasPrefix(e.read("demo/hook.lua"), "-- v3") {
		t.Fatal("the update did not replace the tree")
	}
	if _, err := os.Lstat(filepath.Join(e.root, "demo/lib/helpers.lua")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file of the old tree survived the update")
	}
	e.onlyEntries("demo")
}

func TestInstallConcurrentlyKeepsOneWholeTree(t *testing.T) {
	u := newUpstream(t)
	u.publish(demoFiles("v1"))
	e := newEnv(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = Install(context.Background(), e.o, api.PluginSettings{Name: "demo", Source: u.bare, Subpath: "plugins/demo"})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if e.read("demo/plugin.json") != demoManifest {
		t.Fatal("the tree is not whole")
	}
	e.onlyEntries("demo")
}

func TestInstallCopiesNoLinks(t *testing.T) {
	u := newUpstream(t)
	files := demoFiles("v1")
	u.publish(files)
	// A link inside the plugin and a link as the subpath itself.
	if err := os.Symlink("/etc/hostname", filepath.Join(u.work, "plugins/demo/host")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("plugins/demo", filepath.Join(u.work, "linked")); err != nil {
		t.Fatal(err)
	}
	u.publish(nil)
	e := newEnv(t)

	if _, _, err := e.install(api.PluginSettings{Name: "demo", Source: u.bare, Subpath: "plugins/demo"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(e.root, "demo/host"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || e.read("demo/host") != "/etc/hostname" {
		t.Fatalf("a link arrived as %v with %q", info.Mode(), e.read("demo/host"))
	}
	_, _, err = e.install(api.PluginSettings{Name: "other", Source: u.bare, Subpath: "linked"})
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "subpath" {
		t.Fatalf("subpath through a link: err = %v", err)
	}

	// The copy itself refuses a link, should one be in a checkout.
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "plugin.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "passwd")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(src, dst); err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("copyTree with a link: err = %v", err)
	}
}

func TestInstallBoundsTheTree(t *testing.T) {
	u := newUpstream(t)
	files := demoFiles("v1")
	files["plugins/demo/big.bin"] = strings.Repeat("x", 4096)
	u.publish(files)
	e := newEnv(t)
	old := MaxTreeBytes
	MaxTreeBytes = 2048
	t.Cleanup(func() { MaxTreeBytes = old })
	_, _, err := e.install(api.PluginSettings{Name: "demo", Source: u.bare, Subpath: "plugins/demo"})
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
	e.onlyEntries()
}

func TestInstallRefusesNameInOtherCase(t *testing.T) {
	e := newEnv(t)
	if _, _, err := e.install(api.PluginSettings{Name: "pushover", Source: "builtin:pushover"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.install(api.PluginSettings{Name: "PushOver", Source: "builtin:pushover"})
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
	// A settings file alone counts as well.
	if err := os.WriteFile(filepath.Join(e.root, "Notes.yaml"), []byte("source: builtin:pushover\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.install(api.PluginSettings{Name: "notes", Source: "builtin:pushover"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
	// The same name again is an update.
	if _, _, err := e.install(api.PluginSettings{Name: "pushover", Source: "builtin:pushover"}); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinPushoverInstall(t *testing.T) {
	e := newEnv(t)
	e.o.Git = nil // a bundled plugin needs no git
	got, commit, err := e.install(api.PluginSettings{Name: "pushover", Source: "builtin:pushover", Enabled: true,
		Config: map[string]any{"user_key": "u-123456", "priority": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if commit != CommitBundled {
		t.Fatalf("commit = %q", commit)
	}
	want := map[string]any{"user_key": "u-123456", "app_token": "", "api_url": "https://api.pushover.net/1/messages.json", "priority": 1, "sound": "", "max_per_hour": int64(20)}
	if !reflect.DeepEqual(got.Config, want) {
		t.Fatalf("config = %#v, want %#v", got.Config, want)
	}
	names, _ := bundledplugins.Files(bundledplugins.Pushover)
	for _, rel := range names {
		want, _ := bundledplugins.ReadFile(bundledplugins.Pushover, rel)
		if e.read("pushover/"+rel) != string(want) {
			t.Errorf("%s differs from the bundled file", rel)
		}
	}
	inst, err := Installed(e.o, "pushover")
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := bundledplugins.Digest(bundledplugins.Pushover)
	if inst.Commit != CommitBundled || inst.Digest != digest || inst.Source != "builtin:pushover" {
		t.Fatalf("installed = %+v", inst)
	}

	// Update restores an edited copy.
	if err := os.WriteFile(filepath.Join(e.root, "pushover/notify.lua"), []byte("-- edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.install(api.PluginSettings{Name: "pushover", Source: "builtin:pushover"}); err != nil {
		t.Fatal(err)
	}
	if orig, _ := bundledplugins.ReadFile(bundledplugins.Pushover, "notify.lua"); e.read("pushover/notify.lua") != string(orig) {
		t.Fatal("update did not restore the bundled file")
	}
	e.onlyEntries("pushover")

	// A git source needs Git.
	if _, _, err := e.install(api.PluginSettings{Name: "x", Source: "/abs/repo"}); err == nil {
		t.Fatal("a git install without Git succeeded")
	}
}

func TestBundledListsOnlyPushover(t *testing.T) {
	got := Bundled()
	if len(got) != 1 || got[0].Name != "pushover" || got[0].Description == "" {
		t.Fatalf("Bundled() = %+v", got)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"name":"pushover"`) {
		t.Fatalf("json = %s", raw)
	}
}

func TestRemoveDeletesTreeCacheAndData(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"pushover", "pushover-2"} {
		if _, _, err := e.install(api.PluginSettings{Name: name, Source: "builtin:pushover"}); err != nil {
			t.Fatal(err)
		}
		for _, d := range []string{".data/" + name + "/sub", ".cache/" + name} {
			if err := os.MkdirAll(filepath.Join(e.root, d), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(e.root, "pushover.yaml"), []byte("source: builtin:pushover\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(e.root, ".stage-pushover-123"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Remove(e.o, "pushover"); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"pushover", ".cache/pushover", ".data/pushover", ".stage-pushover-123"} {
		if _, err := os.Lstat(filepath.Join(e.root, rel)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived Remove (err %v)", rel, err)
		}
	}
	// The settings file and the other plugin stay.
	for _, rel := range []string{"pushover.yaml", "pushover-2/plugin.json", ".data/pushover-2/sub", ".cache/pushover-2"} {
		if _, err := os.Lstat(filepath.Join(e.root, rel)); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	if err := Remove(e.o, "pushover"); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	if err := Remove(e.o, "../plugins"); err == nil {
		t.Fatal("Remove took a path")
	}
}

func TestSubpathRules(t *testing.T) {
	ok := map[string]string{"": "", ".": "", "a": "a", "a/b/": "a/b", "./a": "a", "a//b": "a/b", "a.b/c-d": "a.b/c-d"}
	for in, want := range ok {
		got, err := cleanSubpath(in)
		if err != nil || got != want {
			t.Errorf("cleanSubpath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"..", "../a", "a/..", "a/../b", "/a", `a\b`, "c:/x", "a/.git", ".Git/x", "a\x00b", "a\nb", strings.Repeat("a", 513)} {
		if got, err := cleanSubpath(in); err == nil {
			t.Errorf("cleanSubpath(%q) = %q, want a refusal", in, got)
		}
	}
}

// TestPackageStartsNoProcessAndReadsThroughHomefile is the guard of this
// package: it starts no process (git runs only through internal/gitops)
// and reads no file but through homefile (a checkout lies in the omo home,
// which agents can write to).
func TestPackageStartsNoProcessAndReadsThroughHomefile(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, x := range entries {
		name := x.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "os/exec" || p == "io/ioutil" || p == "syscall" {
				t.Errorf("%s imports %s", name, p)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" {
				switch sel.Sel.Name {
				case "ReadFile", "Open", "Readlink", "Stat":
					t.Errorf("%s: os.%s; read through homefile and check with Lstat", fset.Position(sel.Pos()), sel.Sel.Name)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no source file checked")
	}
}

func TestInstallKeepingRestoresOrDiscards(t *testing.T) {
	e := newEnv(t)
	e.o.Git = nil // bundled plugins need no git
	bundled := api.PluginSettings{Name: "p", Source: "builtin:pushover", Enabled: true}
	marker := filepath.Join(e.root, "p", "mine.txt")

	// Without an earlier tree, Restore removes the new one.
	_, _, prev, err := InstallKeeping(context.Background(), e.o, bundled)
	if err != nil {
		t.Fatal(err)
	}
	if err := prev.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(e.root, "p")); !os.IsNotExist(err) {
		t.Fatalf("the new tree stayed: %v", err)
	}

	// With one, Restore puts it back; Discard drops it.
	if _, _, err := e.install(bundled); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, prev, err = InstallKeeping(context.Background(), e.o, bundled)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("the new tree holds the earlier tree's file")
	}
	if err := prev.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(marker); err != nil {
		t.Fatalf("the earlier tree was not put back: %v", err)
	}
	_, _, prev, err = InstallKeeping(context.Background(), e.o, bundled)
	if err != nil {
		t.Fatal(err)
	}
	prev.Discard()
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("the earlier tree came back after Discard")
	}
	if left, _ := filepath.Glob(filepath.Join(e.root, ".old-*")); len(left) != 0 {
		t.Fatalf("left over: %v", left)
	}
}
