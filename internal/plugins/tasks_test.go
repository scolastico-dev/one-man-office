package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/luabox"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func taskRequest(t *testing.T, name, mode string, args ...string) TaskStart {
	t.Helper()
	cmd := helperArgs(t, mode, args...)
	return TaskStart{Name: name, Command: cmd[0], Args: cmd[1:]}
}

func taskPlugin(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	for _, name := range []string{"p", "q"} {
		e.plugin(name, manifestOf(), nil)
	}
	if errs := e.load(enabled("p", nil), enabled("q", nil)); len(errs) != 0 {
		t.Fatal(errs)
	}
	return e
}

// Catches accidentally waiting for the child or binding it to the caller context,
// unscoped lookups, and duplicate reservations that launch more than one process.
func TestTasksAsyncIsolationAndConcurrentNames(t *testing.T) {
	e := taskPlugin(t)
	start := time.Now()
	a, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "editor", "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("StartTask waited for the child")
	}
	b, err := e.m.StartTask(t.Context(), "q", taskRequest(t, "editor", "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("task identities collided")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			_, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "race", "sleep"))
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrTaskRunning) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("successful starts = %d", wins.Load())
	}
	if err := e.m.StopTask("q", "race"); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("wrong-owner stop: %v", err)
	}
	if err := e.m.StopTask("p", "editor"); err != nil {
		t.Fatal(err)
	}
	qs, _ := e.m.Tasks("q")
	if len(qs) != 1 || qs[0].State != "running" {
		t.Fatalf("other plugin changed: %+v", qs)
	}
	if err := e.m.StopTask("p", "editor"); err != nil {
		t.Fatal("stop is not idempotent:", err)
	}
	c, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "editor", "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == a.ID {
		t.Fatal("reused task identity")
	}
	if err := e.m.StopTaskID("p", a.ID, TaskStopped); err != nil {
		t.Fatal(err)
	}
	ps, _ := e.m.Tasks("p")
	for _, p := range ps {
		if p.ID == c.ID && p.State != "running" {
			t.Fatal("stale ID stopped replacement")
		}
	}
}

// Catches retention of failed starts, unredacted output, and losing exit errors.
func TestTasksFailureOutputAndExitNotification(t *testing.T) {
	e := taskPlugin(t)
	e.m.o.TaskExited = func(p string, task api.PluginTask) {
		if p != "p" || task.Reason != "exited" {
			t.Errorf("exit: %s %+v", p, task)
		}
	}
	_, err := e.m.StartTask(t.Context(), "p", TaskStart{Name: "bad", Command: "/no/such/plugin-program"})
	if err == nil {
		t.Fatal("missing command started")
	}
	tasks, _ := e.m.Tasks("p")
	if len(tasks) != 0 {
		t.Fatalf("failed start retained: %+v", tasks)
	}
	req := taskRequest(t, "output", "fail", "sensitive-value\x1b[31m")
	req.Env = map[string]string{"TOKEN": "sensitive-value"}
	task, err := e.m.StartTask(t.Context(), "p", req)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "failed task output", func() bool {
		ts, _ := e.m.Tasks("p")
		for _, x := range ts {
			if x.ID == task.ID {
				return x.State == "exited" && x.Error != "" && len(x.Output) > 0
			}
		}
		return false
	})
	tasks, _ = e.m.Tasks("p")
	raw, _ := json.Marshal(tasks)
	if strings.Contains(string(raw), "sensitive-value") || !strings.Contains(string(raw), "[redacted]") || !strings.Contains(string(raw), `\\u{1B}`) {
		t.Fatalf("unsafe output: %s", raw)
	}
}

func TestTasksValidateTicketAndCompletionRace(t *testing.T) {
	e := taskPlugin(t)
	var calls atomic.Int32
	e.m.o.TaskTicketLive = func(_ context.Context, n int) bool { return n == 7 && calls.Add(1) == 1 }
	req := taskRequest(t, "ticket", "sleep")
	req.Ticket = 7
	if _, err := e.m.StartTask(t.Context(), "p", req); !errors.Is(err, ErrTaskTicket) {
		t.Fatalf("completion race: %v", err)
	}
	tasks, _ := e.m.Tasks("p")
	for _, x := range tasks {
		if x.State == "running" {
			t.Fatal("race left a running process")
		}
	}
	req.Ticket = 8
	if _, err := e.m.StartTask(t.Context(), "p", req); !errors.Is(err, ErrTaskTicket) {
		t.Fatalf("invalid ticket: %v", err)
	}
}

func TestTasksBoundOutputAndHistory(t *testing.T) {
	// Dropping a raw prefix or truncating before redaction could leak a secret.
	red := luabox.NewRedactor()
	red.Add("boundary-sensitive-value")
	out := &taskOutput{red: red}
	out.Write([]byte("boundary-sensitive-"))
	out.Write([]byte("value\n"))
	if got := out.lines(); len(got) != 1 || got[0] != "[redacted]" {
		t.Fatalf("split redaction: %q", got)
	}
	out.Write([]byte(strings.Repeat("x", 2<<20) + "boundary-sensitive-value\n"))
	out.Write([]byte(strings.Repeat("z", 4000) + "\n"))
	for range 200 {
		out.Write([]byte("tail\n"))
	}
	lines := out.lines()
	if len(lines) != 100 {
		t.Fatalf("tail lines=%d", len(lines))
	}
	for _, line := range lines {
		if len([]rune(line)) > 1024 {
			t.Fatal("unbounded line")
		}
	}
	e := taskPlugin(t)
	for i := 0; i < 70; i++ {
		task, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "reuse", "print", "done"))
		if err != nil {
			t.Fatal(err)
		}
		if err := e.m.StopTaskID("p", task.ID, TaskStopped); err != nil {
			t.Fatal(err)
		}
	}
	tasks, _ := e.m.Tasks("p")
	if len(tasks) > 64 {
		t.Fatalf("unbounded history: %d", len(tasks))
	}
}

func TestTasksLuaSurvivesHookAndCanStopFromLaterHook(t *testing.T) {
	e := newEnv(t)
	args := helperArgs(t, "sleep")
	src := fmt.Sprintf(`local t, err = omo.task_start{name="editor",command=%q,args={"plugin-helper","sleep"}}
 assert(t,err); return t.id`, args[0])
	e.plugin("p", manifestOf(map[string]any{"event": "manual", "name": "start", "description": "Start", "lua": "start.lua"}, map[string]any{"event": "manual", "name": "stop", "description": "Stop", "lua": "stop.lua"}), map[string]string{"start.lua": src, "stop.lua": `local ts=omo.task_list(); assert(#ts==1); assert(ts[1].state=="running"); assert(omo.task_stop("editor")); return "stopped"`})
	e.load(enabled("p", nil))
	if _, err := e.m.Trigger(t.Context(), "p", "start", nil); err != nil {
		t.Fatal(err)
	}
	tasks, _ := e.m.Tasks("p")
	if len(tasks) != 1 || tasks[0].State != "running" {
		t.Fatalf("hook killed task: %+v", tasks)
	}
	if out, err := e.m.Trigger(t.Context(), "p", "stop", nil); err != nil || out != "stopped" {
		t.Fatalf("later hook: %q %v", out, err)
	}
}

func TestStartupAndReloadPreserveTaskLifecycles(t *testing.T) {
	e := newEnv(t)
	hooks := manifestOf(luaHook(EventOmoStarted, "start.lua"))
	e.plugin("p", hooks, map[string]string{"start.lua": `omo.store_set("starts",(omo.store_get("starts") or 0)+1)`})
	e.plugin("q", manifestOf(), nil)
	e.load(enabled("p", nil), enabled("q", nil))
	count := func() int {
		raw, _, _ := db.PluginStoreGet(e.m.o.DB, "p", "starts")
		var n int
		json.Unmarshal([]byte(raw), &n)
		return n
	}
	if n := count(); n != 0 {
		t.Fatal("startup before readiness", n)
	}
	e.m.Ready()
	e.m.Ready()
	eventually(t, "one startup", func() bool { return count() == 1 })
	a, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "keep", "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	e.load(enabled("p", nil), enabled("q", map[string]any{"value": "changed"}))
	e.load(enabled("p", map[string]any{"config": "refresh"}), enabled("q", nil))
	tasks, _ := e.m.Tasks("p")
	if len(tasks) != 1 || tasks[0].ID != a.ID || tasks[0].State != "running" {
		t.Fatalf("settings killed unrelated task: %+v", tasks)
	}
	// Runtime code changes stop old processes without inventing another startup.
	e.plugin("p", hooks, map[string]string{"start.lua": `omo.store_set("starts",99)`})
	e.load(enabled("p", map[string]any{"config": "refresh"}), enabled("q", nil))
	tasks, _ = e.m.Tasks("p")
	if tasks[0].Reason != "plugin_replaced" || tasks[0].State != "exited" {
		t.Fatalf("replacement: %+v", tasks)
	}
	if n := count(); n != 1 {
		t.Fatalf("startup repeated on refresh/replacement: %d", n)
	}
	e.load(api.PluginSettings{Name: "p", Enabled: false})
	e.plugin("p", hooks, map[string]string{"start.lua": `omo.store_set("starts",(omo.store_get("starts") or 0)+1)`})
	e.load(enabled("p", nil))
	eventually(t, "startup on enable", func() bool { return count() == 2 })
}

func TestTasksEnvironmentCannotForgeSession(t *testing.T) {
	e := taskPlugin(t)
	for _, key := range []string{"OMO_AGENT_ID", "omo_agent_id", "OMO_SOCKET", "OMO_PLUGIN_NAME", "INVALID=NAME"} {
		req := taskRequest(t, "env", "sleep")
		req.Env = map[string]string{key: "fake-session"}
		if _, err := e.m.StartTask(t.Context(), "p", req); err == nil {
			t.Fatalf("accepted environment key %q", key)
		}
	}
	req := taskRequest(t, "env", "record", filepath.Join(t.TempDir(), "env.json"))
	req.Env = map[string]string{"EXTRA": "task-value"}
	req.Cwd = t.TempDir()
	x, err := e.m.StartTask(t.Context(), "p", req)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "recorded environment", func() bool {
		ts, _ := e.m.Tasks("p")
		return len(ts) > 0 && ts[0].ID == x.ID && ts[0].State == "exited"
	})
	raw, err := os.ReadFile(req.Args[len(req.Args)-1])
	if err != nil {
		t.Fatal(err)
	}
	var data struct{ Env []string }
	json.Unmarshal(raw, &data)
	found := false
	for _, kv := range data.Env {
		key, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(key, "OMO_AGENT_ID") {
			t.Fatal("inherited session id")
		}
		if kv == "EXTRA=task-value" {
			found = true
		}
	}
	if !found {
		t.Fatal("override missing")
	}
}

func TestTasksStopOnDisableRemoveAndShutdown(t *testing.T) {
	for _, reason := range []string{"plugin_disabled", "plugin_removed", "shutdown"} {
		t.Run(reason, func(t *testing.T) {
			e := taskPlugin(t)
			exits := make(chan api.PluginTask, 1)
			e.m.o.TaskExited = func(_ string, task api.PluginTask) { exits <- task }
			_, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "life", "sleep"))
			if err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "plugin_disabled":
				e.load(api.PluginSettings{Name: "p"})
			case "plugin_removed":
				e.load()
			default:
				e.m.Close()
			}
			select {
			case task := <-exits:
				if task.Reason != reason || task.State != "exited" {
					t.Fatalf("exit: %+v", task)
				}
			default:
				t.Fatal("cleanup did not notify before returning")
			}
			// A fresh Manager has no restored records or processes.
			other := New(Options{Root: e.root, DB: e.m.o.DB})
			defer other.Close()
			other.Load([]api.PluginSettings{enabled("p", nil)})
			ts, _ := other.Tasks("p")
			if len(ts) != 0 {
				t.Fatalf("resurrected tasks: %+v", ts)
			}
		})
	}
}

func TestStartupDoesNotBlockOnFullHookQueue(t *testing.T) {
	e := newEnv(t)
	e.plugin("p", manifestOf(luaHook(EventOmoStarted, "start.lua"), luaHook(EventTicketCreated, "slow.lua")), map[string]string{"start.lua": `omo.store_set("started",true)`, "slow.lua": `omo.store_set("busy",true); omo.sleep(10)`})
	e.load(enabled("p", nil))
	e.m.Dispatch(Event{Name: EventTicketCreated})
	eventually(t, "slow hook running", func() bool { return e.stored("p", "busy") == "true" })
	for range QueueSize + 10 {
		e.m.Dispatch(Event{Name: EventTicketCreated})
	}
	done := make(chan struct{})
	go func() { e.m.Ready(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readiness blocked behind hook queue")
	}
}

func TestConcurrentCloseWaitsForTaskReapingAndCallbacks(t *testing.T) {
	e := taskPlugin(t)
	entered, release := make(chan struct{}), make(chan struct{})
	e.m.o.TaskExited = func(_ string, _ api.PluginTask) { close(entered); <-release }
	if _, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "close", "sleep")); err != nil {
		t.Fatal(err)
	}
	first := make(chan struct{})
	go func() { e.m.Close(); close(first) }()
	<-entered
	second := make(chan struct{})
	go func() { e.m.Close(); close(second) }()
	select {
	case <-second:
		t.Error("concurrent Close returned before process callback finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-first
	<-second
}

func TestTaskRunningLimit(t *testing.T) {
	e := taskPlugin(t)
	for i := 0; i < 32; i++ {
		if _, err := e.m.StartTask(t.Context(), "p", taskRequest(t, fmt.Sprint(i), "sleep")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "overflow", "sleep")); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("running limit: %v", err)
	}
	if _, err := e.m.StartTask(t.Context(), "q", taskRequest(t, "independent", "sleep")); err != nil {
		t.Fatalf("limit crossed plugin namespaces: %v", err)
	}
}

func TestTaskRemovalForgetsHistory(t *testing.T) {
	e := taskPlugin(t)
	_, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "old", "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	e.load()
	e.load(enabled("p", nil))
	tasks, _ := e.m.Tasks("p")
	if len(tasks) != 0 {
		t.Fatalf("removed task history reappeared: %+v", tasks)
	}
}

func TestLuaTaskListHandlesFullOutputTails(t *testing.T) {
	e := newEnv(t)
	e.plugin("p", manifestOf(map[string]any{"event": "manual", "name": "list", "description": "List", "lua": "list.lua"}), map[string]string{"list.lua": `local tasks,err=omo.task_list(); assert(tasks,err); assert(#tasks==12); assert(#tasks[1].output==100); return "listed"`})
	e.load(enabled("p", nil))
	for i := 0; i < 12; i++ {
		if _, err := e.m.StartTask(t.Context(), "p", taskRequest(t, fmt.Sprint(i), "lines")); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "all output retained", func() bool {
		ts, _ := e.m.Tasks("p")
		if len(ts) != 12 {
			return false
		}
		for _, x := range ts {
			if x.State != "exited" {
				return false
			}
		}
		return true
	})
	if result, err := e.m.Trigger(t.Context(), "p", "list", nil); err != nil || result != "listed" {
		t.Fatalf("list: %q %v", result, err)
	}
}

func TestStartupMarkerSurvivesInvalidEnabledReplacement(t *testing.T) {
	e := newEnv(t)
	hooks := manifestOf(luaHook(EventOmoStarted, "start.lua"), luaHook(EventTicketCreated, "barrier.lua"))
	files := map[string]string{"start.lua": `omo.store_set("starts",(omo.store_get("starts") or 0)+1)`, "barrier.lua": `omo.store_set("barrier",true)`}
	e.plugin("p", hooks, files)
	e.load(enabled("p", nil))
	e.m.Ready()
	eventually(t, "initial startup", func() bool { return e.stored("p", "starts") == "1" })
	e.plugin("p", `{"hooks":[{"event":"invalid","lua":"start.lua"}]}`, nil)
	if errs := e.load(enabled("p", nil)); len(errs) == 0 {
		t.Fatal("invalid replacement loaded")
	}
	e.plugin("p", hooks, files)
	e.load(enabled("p", nil))
	e.m.Dispatch(Event{Name: EventTicketCreated})
	eventually(t, "restored plugin delivery", func() bool { return e.stored("p", "barrier") == "true" })
	if got := e.stored("p", "starts"); got != "1" {
		t.Fatalf("startup repeated in the same enabled lifecycle: %s", got)
	}
}

func TestTaskStartRejectsRuntimeCancelledDuringCreation(t *testing.T) {
	e := taskPlugin(t)
	// Load cancels an old runtime outside m.mu. The public server adapter's
	// context need not be that runtime's context, so check both at registration.
	e.m.o.Socket = func() string {
		for _, entry := range e.m.entries {
			if entry.settings.Name == "p" {
				entry.p.cancel()
			}
		}
		return ""
	}
	if _, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "obsolete", "sleep")); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("obsolete runtime start: %v", err)
	}
	tasks, _ := e.m.Tasks("p")
	if len(tasks) != 0 {
		t.Fatalf("obsolete child retained: %+v", tasks)
	}
}

func TestTaskMultilineEnvironmentSecretIsRedacted(t *testing.T) {
	e := taskPlugin(t)
	secret := "private-key-first-line\nprivate-key-second-line"
	req := taskRequest(t, "key", "print", secret)
	req.Env = map[string]string{"PRIVATE_KEY": secret}
	x, err := e.m.StartTask(t.Context(), "p", req)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "multiline task output", func() bool {
		ts, _ := e.m.Tasks("p")
		return len(ts) == 1 && ts[0].ID == x.ID && ts[0].State == "exited"
	})
	tasks, _ := e.m.Tasks("p")
	raw, _ := json.Marshal(tasks)
	if strings.Contains(string(raw), "private-key-first-line") || strings.Contains(string(raw), "private-key-second-line") {
		t.Fatalf("multiline secret leaked: %s", raw)
	}
}

func TestTaskNameRefusesInheritedEnvironmentSecret(t *testing.T) {
	t.Setenv("PLUGIN_TEST_API_TOKEN", "abcdefghi")
	e := taskPlugin(t)
	if _, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "abcdefghi", "sleep")); err == nil {
		t.Fatal("inherited credential published as a task name")
	}
}

func TestTasksBoundPendingExitCallbacks(t *testing.T) {
	e := taskPlugin(t)
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{}, 64)
	e.m.o.TaskExited = func(_ string, _ api.PluginTask) { entered <- struct{}{}; <-release }
	for i := 0; i < 33; i++ {
		_, err := e.m.StartTask(t.Context(), "p", taskRequest(t, fmt.Sprint(i), "print", "done"))
		if i == 32 {
			if !errors.Is(err, ErrTaskLimit) {
				t.Fatalf("callback-pending runs escaped resource cap: %v", err)
			}
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		<-entered // The process exited, but its resource cleanup still runs.
	}
}

func TestTaskNameReuseDoesNotDependOnWallClock(t *testing.T) {
	e := taskPlugin(t)
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e.m.now = func() time.Time { return at }
	first, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "reuse", "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.StopTaskID("p", first.ID, TaskStopped); err != nil {
		t.Fatal(err)
	}
	// Wall-clock correction must not make a stale record become the latest run.
	at = at.Add(-time.Hour)
	second, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "reuse", "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.StopTask("p", "reuse"); err != nil {
		t.Fatal(err)
	}
	tasks, _ := e.m.Tasks("p")
	for _, task := range tasks {
		if task.ID == second.ID && task.State != "exited" {
			t.Fatal("name stop selected an older run after clock correction")
		}
	}
}

func TestTaskOutputInvalidatesOnlyVisibleChanges(t *testing.T) {
	changes := 0
	out := &taskOutput{red: luabox.NewRedactor(), changed: func() { changes++ }}
	out.Write([]byte("par"))
	out.Write([]byte("tial"))
	if changes != 0 {
		t.Fatal("partial output invalidated unchanged tail")
	}
	out.Write([]byte("\n"))
	if changes != 1 {
		t.Fatal("complete line did not invalidate tail")
	}
	for range 100 {
		out.Write([]byte("same\n"))
	}
	before := changes
	out.Write([]byte("same\n"))
	if changes != before {
		t.Fatal("unchanged repeated tail invalidated snapshot")
	}
}

func TestTaskOutputPreservesPathsAndRedactsOnlyCredentialEnvironment(t *testing.T) {
	t.Setenv("HOME", "/tmp/plugin-visible-home")
	t.Setenv("PATH", "/plugin-bin:/usr/bin")
	t.Setenv("PLUGIN_API_TOKEN", "inherited-private-value")
	e := taskPlugin(t)
	e.load(enabled("p", map[string]any{"secret": "config-private-value"}), enabled("q", nil))
	text := "failed /tmp/plugin-visible-home/work PATH=/plugin-bin:/usr/bin config-private-value task-private-value inherited-private-value"
	req := taskRequest(t, "diagnostic", "fail", text)
	req.Env = map[string]string{"SECRET": "task-private-value"}
	task, err := e.m.StartTask(t.Context(), "p", req)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "diagnostic output", func() bool {
		tasks, _ := e.m.Tasks("p")
		return len(tasks) == 1 && tasks[0].ID == task.ID && tasks[0].State == "exited"
	})
	tasks, _ := e.m.Tasks("p")
	raw, _ := json.Marshal(tasks)
	for _, path := range []string{"/tmp/plugin-visible-home/work", "/plugin-bin:/usr/bin"} {
		if !strings.Contains(string(raw), path) {
			t.Errorf("useful path over-redacted: %s", raw)
		}
	}
	for _, secret := range []string{"config-private-value", "task-private-value", "inherited-private-value"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("secret leaked: %s", raw)
		}
	}
}

func TestTaskDoesNotMutateCallingHookRedactor(t *testing.T) {
	e := taskPlugin(t)
	red := luabox.NewRedactor()
	red.Add("prior-http-secret")
	req := taskRequest(t, "independent", "sleep")
	req.Env = map[string]string{"SECRET": "task-private-value"}
	if _, err := e.m.startTask(t.Context(), "p", nil, req, red); err != nil {
		t.Fatal(err)
	}
	if got := red.Clean("task-private-value"); got != "task-private-value" {
		t.Fatalf("task contaminated calling hook redactor: %q", got)
	}
	if got := red.Clean("prior-http-secret"); got != "[redacted]" {
		t.Fatal("existing hook secret lost")
	}
}
