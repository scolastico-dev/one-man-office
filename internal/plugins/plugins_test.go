package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/scolastico-dev/one-man-office/internal/db"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
)

// TestMain doubles as the helper program of command hooks: the test binary
// run as "<binary> plugin-helper <mode> …".
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "plugin-helper" {
		os.Exit(helper(os.Args[2], os.Args[3:]))
	}
	os.Exit(childenvtest.Run(m))
}

func helper(mode string, args []string) int {
	switch mode {
	case "record": // record <file> …: argv, environment and standard input as JSON
		stdin, _ := io.ReadAll(os.Stdin)
		raw, _ := json.Marshal(map[string]any{"args": os.Args[1:], "env": os.Environ(), "stdin": string(stdin)})
		if err := os.WriteFile(args[0], raw, 0o600); err != nil {
			return 2
		}
	case "append": // append <file>: standard input as one line
		stdin, _ := io.ReadAll(os.Stdin)
		f, err := os.OpenFile(args[0], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return 2
		}
		defer f.Close()
		_, _ = f.Write(append(bytes.TrimSpace(stdin), '\n'))
	case "wait": // wait <file>: until the file exists
		for i := 0; i < 6000; i++ {
			if _, err := os.Stat(args[0]); err == nil {
				return 0
			}
			time.Sleep(10 * time.Millisecond)
		}
		return 4
	case "print": // print <text>: the text on standard output
		fmt.Print(args[0])
	case "fail": // fail <text>: the text on standard error, status 3
		fmt.Fprintln(os.Stderr, args[0])
		return 3
	case "config-to-stderr": // the plugin's configuration on standard error, status 3
		fmt.Fprintln(os.Stderr, "config was "+os.Getenv("OMO_PLUGIN_CONFIG"))
		return 3
	case "lines":
		for range 100 {
			fmt.Println(strings.Repeat("x", 1024))
		}
	case "sleep":
		if len(args) > 0 {
			if err := os.WriteFile(args[0], nil, 0o600); err != nil {
				return 2
			}
		}
		time.Sleep(time.Minute)
	case "spawn", "spawn-exit": // spawn <pidfile>: start a sleeping child, write its pid, sleep
		exe, _ := os.Executable()
		child := newHelperCommand(exe, "sleep")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			return 2
		}
		_ = os.WriteFile(args[0], []byte(fmt.Sprint(child.Process.Pid)), 0o600)
		if mode == "spawn-exit" {
			return 0
		}
		time.Sleep(time.Minute)
	}
	return 0
}

// newHelperCommand runs the helper in mode with args.
func newHelperCommand(exe string, args ...string) *exec.Cmd {
	return exec.Command(exe, append([]string{"plugin-helper"}, args...)...)
}

// waitFinished joins the observable completion of n hooks, including state
// persistence and the Changed callback, instead of sampling an idle state.
func (e *env) waitFinished(name string, n int) {
	e.t.Helper()
	eventually(e.t, name+" hook completions", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		finished := 0
		for _, change := range e.changes {
			if change.Name == name && change.LastRunAt != nil && change.State != StateRunning {
				finished++
			}
		}
		return finished >= n
	})
}

// helperArgs is the command of a hook that runs the helper in mode.
func helperArgs(t *testing.T, mode string, args ...string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return append([]string{exe, "plugin-helper", mode}, args...)
}

// env is a plugin root, a database and a manager over them.
type env struct {
	t    *testing.T
	root string
	db   interface{ Close() error }
	m    *Manager

	mu      sync.Mutex
	changes []api.PluginInfo
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := filepath.Join(t.TempDir(), "plugins")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, root: root, db: d}
	e.m = New(Options{Root: root, DB: d, Socket: func() string { return "/run/omo-test.sock" }, Changed: func(i api.PluginInfo) {
		e.mu.Lock()
		e.changes = append(e.changes, i)
		e.mu.Unlock()
	}})
	t.Cleanup(func() {
		e.m.Close()
		_ = d.Close()
	})
	return e
}

// plugin writes a plugin folder with manifest and files.
func (e *env) plugin(name string, manifest any, files map[string]string) {
	e.t.Helper()
	dir := filepath.Join(e.root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		e.t.Fatal(err)
	}
	raw, ok := manifest.(string)
	if !ok {
		b, err := json.Marshal(manifest)
		if err != nil {
			e.t.Fatal(err)
		}
		raw = string(b)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(raw), 0o600); err != nil {
		e.t.Fatal(err)
	}
	for rel, text := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(text), 0o600); err != nil {
			e.t.Fatal(err)
		}
	}
}

// load loads settings and fails on any error of the names in ok.
func (e *env) load(settings ...api.PluginSettings) map[string]error {
	e.t.Helper()
	return e.m.Load(settings)
}

func enabled(name string, config map[string]any) api.PluginSettings {
	return api.PluginSettings{Name: name, Source: "/src/" + name, Enabled: true, Config: config}
}

func luaHook(event, file string) map[string]any {
	return map[string]any{"event": event, "lua": file}
}

func manifestOf(hooks ...map[string]any) map[string]any {
	list := make([]any, len(hooks))
	for i, h := range hooks {
		list[i] = h
	}
	return map[string]any{"name": "p", "version": "1.0.0", "hooks": list}
}

// info is the info of plugin name.
func (e *env) info(name string) api.PluginInfo {
	e.t.Helper()
	for _, i := range e.m.Infos() {
		if i.Name == name {
			return i
		}
	}
	e.t.Fatalf("no info for %s", name)
	return api.PluginInfo{}
}

// logText is the plugin's whole log as one text.
func (e *env) logText(name string) string {
	var b strings.Builder
	for _, l := range e.m.LogLines(name, LogLines) {
		b.WriteString(l.Message)
		b.WriteString("\n")
	}
	return b.String()
}

// stored is the JSON text a plugin stored under key, "" when none.
func (e *env) stored(plugin, key string) string {
	e.t.Helper()
	v, _, err := db.PluginStoreGet(e.m.o.DB, plugin, key)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func ticketEvent(n int, title string) Event {
	return Event{Name: EventTicketCreated, Data: map[string]any{"ticket": TicketPayload(api.TicketSummary{Number: n, Title: title, Type: "task", Column: "ready"}, "http://127.0.0.1:1")}}
}

func TestManifestRefusesOldEvents(t *testing.T) {
	for _, ev := range []string{"prompt_render", "company_load", "agent_log_line", "job_create", "startup", "load", "cron", "chron", "unload", "shutdown", "company_startup", "company_shutdown", "agent_start"} {
		_, err := ParseManifest([]byte(`{"hooks":[{"event":"` + ev + `","lua":"x.lua"}]}`))
		if err == nil || !strings.Contains(err.Error(), `"`+ev+`" is no longer available`) {
			t.Errorf("event %s: %v", ev, err)
		}
	}
	for _, c := range []struct{ field, raw string }{
		{"requires", `{"requires":[{"name":"x","source":"https://example.invalid/x.git"}],"hooks":[]}`},
		{"roles", `{"hooks":[{"event":"manual","name":"a","description":"d","lua":"x.lua","roles":["pm"]}]}`},
		{"manual_args", `{"hooks":[{"event":"manual","name":"a","description":"d","lua":"x.lua","manual_args":true}]}`},
		{"javascript", `{"hooks":[{"event":"timer","interval":"1m","lua":"x.lua","javascript":"a.js"}]}`},
		{"files", `{"hooks":[{"event":"timer","interval":"1m","lua":"x.lua","files":["a.js"]}]}`},
	} {
		_, err := ParseManifest([]byte(c.raw))
		if err == nil || !strings.Contains(err.Error(), `"`+c.field+`" is no longer available`) {
			t.Errorf("field %s: %v", c.field, err)
		}
	}
	for _, c := range []struct{ name, raw, want string }{
		{"unknown event", "{\"hooks\":[{\"event\":\"ticket_\u202edeleted\",\"lua\":\"x.lua\"}]}", "unknown event \"ticket_\\u202edeleted\""},
		{"unknown field", `{"hooks":[],"bogus":1}`, `unknown field "bogus"`},
		{"both", `{"hooks":[{"event":"ticket_moved","lua":"x.lua","command":["x"]}]}`, "exactly one of lua and command"},
		{"neither", `{"hooks":[{"event":"ticket_moved"}]}`, "exactly one of lua and command"},
		{"escape", `{"hooks":[{"event":"ticket_moved","lua":"../x.lua"}]}`, "inside the plugin"},
		{"absolute", `{"hooks":[{"event":"ticket_moved","lua":"/etc/x.lua"}]}`, "inside the plugin"},
		{"manual without description", `{"hooks":[{"event":"manual","name":"a","lua":"x.lua"}]}`, "description"},
		{"bad scope", `{"hooks":[{"event":"manual","name":"a","description":"d","scope":"office","lua":"x.lua"}]}`, "scope is ticket or global"},
		{"duplicate action", `{"hooks":[{"event":"manual","name":"a","description":"d","lua":"x.lua"},{"event":"manual","name":"a","description":"d","lua":"y.lua"}]}`, "another manual hook"},
		{"name on event hook", `{"hooks":[{"event":"ticket_moved","name":"a","lua":"x.lua"}]}`, "manual hooks only"},
		{"interval on event hook", `{"hooks":[{"event":"ticket_moved","interval":"1m","lua":"x.lua"}]}`, "timer hooks only"},
		{"timeout too long", `{"hooks":[{"event":"ticket_moved","timeout":"6m","lua":"x.lua"}]}`, "timeout"},
		{"bad version", `{"version":"v1","hooks":[]}`, "semantic version"},
		{"bad default_config", `{"default_config":[1],"hooks":[]}`, "default_config must be a JSON object"},
		{"not an object", `[1]`, "not a JSON object"},
		{"trailing data", `{"hooks":[]} {}`, "data after"},
	} {
		_, err := ParseManifest([]byte(c.raw))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
	m, err := ParseManifest([]byte(`{"name":"ok","hooks":[{"event":"manual","name":"a","description":"d","lua":"x.lua"}]}`))
	if err != nil || m.Hooks[0].Scope != ScopeGlobal {
		t.Fatalf("a manual hook without scope: %+v, %v", m, err)
	}
}

func TestOldPluginFailsToLoadWithFixedMessage(t *testing.T) {
	e := newEnv(t)
	e.plugin("old", `{"name":"old","requires":[{"name":"x","source":"https://example.invalid/x.git"}],"hooks":[{"event":"prompt_render","lua":"x.lua"}]}`, map[string]string{"x.lua": ""})
	errs := e.load(enabled("old", nil))
	want := `"requires" is no longer available: plugins have no dependencies any more; install each plugin by itself`
	if errs["old"] == nil || errs["old"].Error() != want {
		t.Fatalf("load error = %v", errs["old"])
	}
	if i := e.info("old"); i.State != StateInvalid || i.Message != want {
		t.Fatalf("info = %+v", i)
	}
}

func TestReadManifestBounds(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadManifest(dir); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("missing manifest: %v", err)
	}
	big := `{"description":"` + strings.Repeat("x", MaxManifestBytes) + `","hooks":[]}`
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(dir); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("large manifest: %v", err)
	}
	if runtime.GOOS != "windows" {
		real := filepath.Join(t.TempDir(), "real.json")
		if err := os.WriteFile(real, []byte(`{"hooks":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		linked := t.TempDir()
		if err := os.Symlink(real, filepath.Join(linked, ManifestFile)); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadManifest(linked); err == nil || !strings.Contains(err.Error(), "link") {
			t.Fatalf("linked manifest: %v", err)
		}
	}
}

func TestLoadReportsEachPlugin(t *testing.T) {
	e := newEnv(t)
	e.plugin("good", manifestOf(luaHook(EventTicketMoved, "h.lua"), map[string]any{"event": "manual", "name": "hi", "description": "Say hi", "scope": "ticket", "lua": "h.lua"}), map[string]string{"h.lua": "return 'hi'"})
	e.plugin("broken", `{"hooks":[{"event":"ticket_moved","lua":"missing.lua"}]}`, nil)
	e.plugin("off", manifestOf(luaHook(EventTicketMoved, "h.lua")), map[string]string{"h.lua": ""})
	errs := e.load(enabled("good", nil), enabled("broken", nil), enabled("absent", nil),
		api.PluginSettings{Name: "off", Source: "/plugins/off.git"}, enabled("../escape", nil))
	if errs["good"] != nil || errs["off"] != nil {
		t.Fatalf("errors of loadable plugins: %v", errs)
	}
	if !errors.Is(errs["absent"], ErrNotInstalled) || errs["broken"] == nil || errs["../escape"] == nil {
		t.Fatalf("errors = %v", errs)
	}
	states := map[string]string{}
	for _, i := range e.m.Infos() {
		states[i.Name] = i.State
	}
	want := map[string]string{"good": StateReady, "broken": StateInvalid, "absent": StateNotInstalled, "off": StateDisabled, "../escape": StateInvalid}
	for name, s := range want {
		if states[name] != s {
			t.Errorf("state of %s = %q, want %q", name, states[name], s)
		}
	}
	good := e.info("good")
	if good.Version != "1.0.0" || len(good.Hooks) != 2 || good.Hooks[1].Scope != ScopeTicket || good.Hooks[1].Name != "hi" {
		t.Fatalf("info of good = %+v", good)
	}
	if off := e.info("off"); off.Commit != "" {
		t.Fatalf("commit of a plugin the runtime did not install = %q", off.Commit)
	}
}

func TestDispatchOrderedAndBounded(t *testing.T) {
	e := newEnv(t)
	release := filepath.Join(t.TempDir(), "release")
	// Unblock the workload before manager cleanup even when an assertion fails.
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e.plugin("slow", manifestOf(luaHook(EventTicketCreated, "h.lua")), map[string]string{"h.lua": `
if event.data.ticket.number == 1 then
  local _, err = omo.exec(config.helper, "plugin-helper", "wait", config.release)
  if err ~= "" then error(err) end
end
local seen = omo.store_get("seen") or {}
seen[#seen + 1] = event.data.ticket.number
assert(omo.store_set("seen", seen))
`})
	e.plugin("fast", manifestOf(luaHook(EventTicketMoved, "h.lua")), map[string]string{"h.lua": `
omo.store_set("moved", event.data.n)
`})
	// Far above what this test sends: it is about the queue, not the rate.
	e.m.perMinute = 10000
	if errs := e.load(enabled("slow", map[string]any{"helper": exe, "release": release}), enabled("fast", nil)); len(errs) != 0 {
		t.Fatal(errs)
	}
	// Event 1 runs and blocks; 2 to 257 wait in the queue; 258 to 300 are
	// dropped.
	e.m.Dispatch(ticketEvent(1, "t"))
	eventually(t, "the first event to run", func() bool { return e.info("slow").State == StateRunning })
	dispatched := make(chan struct{})
	go func() {
		for n := 2; n <= 300; n++ {
			e.m.Dispatch(ticketEvent(n, "t"))
		}
		close(dispatched)
	}()
	<-dispatched
	// Another plugin is not held by the blocked one.
	e.m.Dispatch(Event{Name: EventTicketMoved, Data: map[string]any{"n": 1}})
	eventually(t, "the other plugin", func() bool { return e.stored("fast", "moved") == "1" })
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	released := time.Now()
	defer func() {
		// Says what a run that timed out got to: events lost (the first one
		// is missing when its program failed) or events still arriving.
		if t.Failed() {
			seen := e.stored("slow", "seen")
			t.Logf("%v after the release: %d events seen (%.60s…), state %+v, log:\n%s", time.Since(released), strings.Count(seen, ",")+1, seen, e.info("slow"), e.logText("slow"))
		}
	}()
	eventually(t, "the slow plugin", func() bool { return strings.Count(e.stored("slow", "seen"), ",") == 256 })
	var seen []int
	if err := json.Unmarshal([]byte(e.stored("slow", "seen")), &seen); err != nil {
		t.Fatal(err)
	}
	for i, n := range seen {
		if n != i+1 {
			t.Fatalf("event %d delivered as %d: order not kept", i+1, n)
		}
	}
	if !strings.Contains(e.logText("slow"), "43 events dropped: the plugin's queue of 256 was full") {
		t.Fatalf("log = %s", e.logText("slow"))
	}
}

// fakeClock replaces the manager's timer source.
type fakeClock struct {
	mu    sync.Mutex
	waits []chan time.Time
	asked []time.Duration
}

func (c *fakeClock) after(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waits = append(c.waits, ch)
	c.asked = append(c.asked, d)
	return ch
}

// fire fires every pending wait and returns how many there were.
func (c *fakeClock) fire(at time.Time) int {
	c.mu.Lock()
	waits := c.waits
	c.waits = nil
	c.mu.Unlock()
	for _, ch := range waits {
		ch <- at
	}
	return len(waits)
}

func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waits)
}

func TestTimerHooks(t *testing.T) {
	if _, err := ParseManifest([]byte(`{"hooks":[{"event":"timer","interval":"30s","lua":"x.lua"}]}`)); err == nil || !strings.Contains(err.Error(), "at least 1m") {
		t.Fatalf("interval of 30s: %v", err)
	}
	if _, err := ParseManifest([]byte(`{"hooks":[{"event":"timer","lua":"x.lua"}]}`)); err == nil {
		t.Fatal("a timer without interval loaded")
	}
	e := newEnv(t)
	clock := &fakeClock{}
	e.m.after = clock.after
	e.plugin("tick", manifestOf(
		map[string]any{"event": "timer", "interval": "2m", "lua": "h.lua"},
		map[string]any{"event": "timer", "interval_config": "every", "lua": "h2.lua"},
	), map[string]string{
		"h.lua":  `omo.store_set("at", event.data.at); omo.store_set("n", (omo.store_get("n") or 0) + 1)`,
		"h2.lua": `omo.store_set("n2", (omo.store_get("n2") or 0) + 1)`,
	})
	e.plugin("short", manifestOf(map[string]any{"event": "timer", "interval_config": "every", "lua": "h.lua"}), map[string]string{"h.lua": ""})
	errs := e.load(enabled("tick", map[string]any{"every": "5m"}), enabled("short", map[string]any{"every": 10}))
	if errs["tick"] != nil {
		t.Fatal(errs["tick"])
	}
	if errs["short"] == nil || !strings.Contains(errs["short"].Error(), `value of "every" is not a duration of at least 1m`) {
		t.Fatalf("short interval_config: %v", errs["short"])
	}
	eventually(t, "two timers", func() bool { return clock.pending() == 2 })
	clock.mu.Lock()
	asked := append([]time.Duration(nil), clock.asked...)
	clock.mu.Unlock()
	if !(asked[0] == 2*time.Minute && asked[1] == 5*time.Minute) && !(asked[0] == 5*time.Minute && asked[1] == 2*time.Minute) {
		t.Fatalf("intervals = %v", asked)
	}
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	clock.fire(at)
	eventually(t, "the timer hooks", func() bool { return e.stored("tick", "n") == "1" && e.stored("tick", "n2") == "1" })
	if e.stored("tick", "at") != `"2026-11-15T12:00:00Z"` {
		t.Fatalf("at = %s", e.stored("tick", "at"))
	}
	if i := e.info("tick"); i.Hooks[1].Interval != "5m0s" {
		t.Fatalf("interval from the configuration = %q", i.Hooks[1].Interval)
	}
}

func TestReloadStopsOldTimers(t *testing.T) {
	e := newEnv(t)
	clock := &fakeClock{}
	e.m.after = clock.after
	e.plugin("tick", manifestOf(map[string]any{"event": "timer", "interval": "1m", "lua": "h.lua"}), map[string]string{
		"h.lua": `omo.store_set("n", (omo.store_get("n") or 0) + 1)`,
	})
	e.load(enabled("tick", nil))
	eventually(t, "the timer", func() bool { return clock.pending() == 1 })
	clock.fire(time.Now())
	eventually(t, "the first tick", func() bool { return e.stored("tick", "n") == "1" })
	eventually(t, "the next wait", func() bool { return clock.pending() == 1 })

	disabled := enabled("tick", nil)
	disabled.Enabled = false
	e.load(disabled)
	// The old timer's goroutine is gone: firing its wait runs nothing and
	// asks for no new wait.
	clock.fire(time.Now())
	if n := e.stored("tick", "n"); n != "1" {
		t.Fatalf("an old timer ran after the reload: n = %s", n)
	}
	if p := clock.pending(); p != 0 {
		t.Fatalf("an old timer still waits: %d", p)
	}
	e.load(enabled("tick", nil))
	eventually(t, "the new timer", func() bool { return clock.pending() == 1 })
}

func TestCloseStopsEverything(t *testing.T) {
	e := newEnv(t)
	started := filepath.Join(t.TempDir(), "started")
	e.plugin("block", manifestOf(map[string]any{"event": EventTicketCreated, "command": helperArgs(t, "sleep", started)}), nil)
	e.load(enabled("block", nil))
	e.m.Dispatch(ticketEvent(1, "x"))
	eventually(t, "the hook program to start", func() bool { _, err := os.Stat(started); return err == nil })
	e.m.mu.Lock()
	p := e.m.entries[0].p
	e.m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		e.m.Close()
		close(done)
	}()
	<-done
	e.m.Dispatch(ticketEvent(2, "x"))
	if _, err := e.m.Trigger(t.Context(), "block", "x", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Trigger after Close = %v", err)
	}
	if errs := e.m.Load([]api.PluginSettings{enabled("block", nil)}); !errors.Is(errs["block"], ErrClosed) {
		t.Fatalf("Load after Close = %v", errs)
	}
	if p.ctx.Err() == nil {
		t.Fatal("Close left the plugin context live")
	}
	joined := make(chan struct{})
	go func() { p.wg.Wait(); close(joined) }()
	<-joined
	// A run cut short by Close is not an error of the plugin.
	if i := e.info("block"); i.State == StateError {
		t.Fatalf("state after Close = %+v", i)
	}
}

func TestHookTimeout(t *testing.T) {
	e := newEnv(t)
	e.plugin("cmd", manifestOf(map[string]any{"event": EventTicketCreated, "command": helperArgs(t, "sleep"), "timeout": "1s"}), nil)
	e.plugin("lua", manifestOf(map[string]any{"event": EventTicketCreated, "lua": "h.lua", "timeout": "1s"}), map[string]string{"h.lua": "while true do end"})
	e.plugin("other", manifestOf(luaHook(EventTicketCreated, "h.lua")), map[string]string{"h.lua": `omo.store_set("n", event.data.ticket.number)`})
	e.load(enabled("cmd", nil), enabled("lua", nil), enabled("other", nil))
	e.m.Dispatch(ticketEvent(1, "x"))
	e.m.Dispatch(ticketEvent(2, "x"))
	eventually(t, "the other plugin", func() bool { return e.stored("other", "n") == "2" })
	for _, name := range []string{"cmd", "lua"} {
		eventually(t, name+" to time out", func() bool {
			i := e.info(name)
			return i.State == StateError && i.Message == "time limit reached"
		})
		eventually(t, name+"'s log", func() bool {
			return strings.Contains(e.logText(name), "error in the ticket_created hook: time limit reached")
		})
	}
}

func TestPluginStorageIsPerPlugin(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"a", "b"} {
		e.plugin(name, manifestOf(luaHook(EventTicketCreated, "h.lua")), map[string]string{"h.lua": `
local before = omo.store_get("k")
assert(omo.store_set("k", {who = config.who, before = before}))
local ok, err = omo.store_set("big", string.rep("x", 70000))
assert(ok == nil and err == "the value exceeds 65536 bytes", err)
assert(omo.store_set("gone", 1)); assert(omo.store_delete("gone"))
local keys = omo.store_keys()
assert(#keys == 1 and keys[1] == "k")
assert(not pcall(omo.store_set, "", 1))
`})
	}
	e.load(enabled("a", map[string]any{"who": "plugin a"}), enabled("b", map[string]any{"who": "plugin b"}))
	e.m.Dispatch(ticketEvent(1, "x"))
	eventually(t, "both plugins", func() bool { return e.stored("a", "k") != "" && e.stored("b", "k") != "" })
	if a, b := e.stored("a", "k"), e.stored("b", "k"); a != `{"who":"plugin a"}` || b != `{"who":"plugin b"}` {
		t.Fatalf("stored a = %s, b = %s", a, b)
	}
	e.waitFinished("a", 1)
	e.waitFinished("b", 1)
	if i := e.info("a"); i.State != StateReady {
		t.Fatalf("a = %+v, log %s", i, e.logText("a"))
	}
}

func TestChangedReportsStates(t *testing.T) {
	e := newEnv(t)
	e.plugin("p", manifestOf(luaHook(EventTicketMoved, "h.lua")), map[string]string{"h.lua": `error("boom")`})
	e.load(enabled("p", nil))
	e.m.Dispatch(Event{Name: EventTicketMoved, Data: map[string]any{}})
	e.waitFinished("p", 1)
	e.mu.Lock()
	defer e.mu.Unlock()
	var states []string
	for _, c := range e.changes {
		states = append(states, c.State)
	}
	got := strings.Join(states, ",")
	if !strings.Contains(got, "ready,running") || !strings.HasSuffix(got, "error") {
		t.Fatalf("changes = %s", got)
	}
	if last := e.changes[len(e.changes)-1]; last.Message != "line 1: boom" || last.LastEvent != EventTicketMoved || last.LastRunAt == nil {
		t.Fatalf("last change = %+v", last)
	}
}

// A plugin whose hook causes the event it reacts to (a comment_written
// hook that writes a comment) cannot spin: each kind of event runs a
// plugin's hooks at most perMinute times a minute; the rest is dropped
// with one log line per minute.
func TestHookRunsPerMinuteBounded(t *testing.T) {
	e := newEnv(t)
	var offset atomic.Int64
	e.m.now = func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	if e.m.perMinute != DefaultHookRunsPerMinute || DefaultHookRunsPerMinute != 60 {
		t.Fatalf("limit %d, default %d", e.m.perMinute, DefaultHookRunsPerMinute)
	}
	e.m.perMinute = 5
	e.plugin("loop", manifestOf(luaHook(EventCommentWritten, "h.lua"), luaHook(EventTicketCreated, "h.lua")), map[string]string{"h.lua": `
local key = event.event
omo.store_set(key, (omo.store_get(key) or 0) + 1)
`})
	if errs := e.load(enabled("loop", nil)); len(errs) != 0 {
		t.Fatal(errs)
	}
	comment := Event{Name: EventCommentWritten, Data: map[string]any{}}
	for range 12 {
		e.m.Dispatch(comment)
	}
	// Another kind of event has its own count.
	e.m.Dispatch(ticketEvent(1, "t"))
	eventually(t, "the other kind", func() bool { return e.stored("loop", EventTicketCreated) == "1" })
	eventually(t, "the drop to be logged", func() bool {
		return strings.Contains(e.logText("loop"), "comment_written events are dropped")
	})
	eventually(t, "the queue to drain", func() bool { return e.info("loop").State != StateRunning })
	if got := e.stored("loop", EventCommentWritten); got != "5" {
		t.Fatalf("comment_written ran %s times, want 5", got)
	}
	if n := strings.Count(e.logText("loop"), "events are dropped"); n != 1 {
		t.Fatalf("%d drop lines, want 1:\n%s", n, e.logText("loop"))
	}

	// A minute later the hooks run again.
	offset.Store(int64(61 * time.Second))
	e.m.Dispatch(comment)
	eventually(t, "the next minute", func() bool { return e.stored("loop", EventCommentWritten) == "6" })
}
