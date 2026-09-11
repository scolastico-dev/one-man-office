package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestLifecycleHooksUseTenSecondDefaultAndRemainImmutable(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, Dir, "lifecycle")
	writePlugin(t, dir, Manifest{Name: "lifecycle", Hooks: []Hook{
		{Event: EventLoad, Lua: "hook.lua"},
		{Event: EventStartup, Lua: "hook.lua"},
		{Event: EventUnload, Timeout: "7s", Lua: "hook.lua"},
		{Event: EventJobCreate, Lua: "hook.lua"},
	}}, `event.mutable = true`)

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	for _, hook := range manager.hooks {
		switch hook.hook.Event {
		case EventLoad, EventStartup:
			if hook.timeout != 10*time.Second {
				t.Fatalf("%s timeout = %s, want 10s", hook.hook.Event, hook.timeout)
			}
		case EventUnload:
			if hook.timeout != 7*time.Second {
				t.Fatalf("unload timeout = %s, want 7s", hook.timeout)
			}
		case EventJobCreate:
			if hook.timeout != 30*time.Second {
				t.Fatalf("ordinary timeout = %s, want 30s", hook.timeout)
			}
		}
	}
	event, err := manager.emitLifecycle(context.Background(), EventStartup, map[string]any{"plugin": "lifecycle"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if event.Mutable {
		t.Fatal("lifecycle event became mutable")
	}
}

func TestLoadLifecycleHookRunsAfterManagerLoad(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "installed-loader"), Manifest{Name: "loader", Hooks: []Hook{{Event: EventLoad, Lua: "hook.lua"}}}, `omo.local_set("loaded", event.data.plugin); omo.local_set("scope", event.data.scope)`)

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	assertStored(t, manager, "local", "loader", "loaded", `"installed-loader"`)
	assertStored(t, manager, "local", "loader", "scope", `"office"`)
}

func TestLifecycleHooksTargetPluginsAndReverseShutdownOrder(t *testing.T) {
	office, database := newPluginOffice(t)
	for _, name := range []string{"a", "b"} {
		script := fmt.Sprintf(`omo.global_set("order", (omo.global_get("order") or "") .. "-%s")`, name)
		writePlugin(t, filepath.Join(office, Dir, name), Manifest{Name: name, Hooks: []Hook{
			{Event: EventStartup, Lua: "hook.lua"},
			{Event: EventShutdown, Lua: "hook.lua"},
		}}, script)
	}
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, err := manager.emitLifecycle(context.Background(), EventStartup, map[string]any{}, false); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "global", "", "order", `"-a-b"`)
	if _, err := manager.emitLifecycle(context.Background(), EventShutdown, map[string]any{}, true); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "global", "", "order", `"-a-b-b-a"`)
}

func TestNonLoadLifecyclePayloadsDoNotInjectPluginFields(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "payload"), Manifest{Name: "payload", Hooks: []Hook{{Event: EventStartup, Lua: "hook.lua"}}}, `omo.local_set("plugin", event.data.plugin or "missing"); omo.local_set("scope", event.data.scope or "missing")`)
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, err := manager.EmitLifecycle(context.Background(), Event{Name: EventStartup, Data: map[string]any{"office_path": office, "office_started_at_unix": int64(1)}}); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "local", "payload", "plugin", `"missing"`)
	assertStored(t, manager, "local", "payload", "scope", `"missing"`)
}

func TestLifecyclePayloadsContainOnlyContractFields(t *testing.T) {
	office, database := newPluginOffice(t)
	script := `local keys = {}
for key, _ in pairs(event.data) do table.insert(keys, key) end
table.sort(keys)
omo.local_set("keys_" .. event.event, table.concat(keys, ","))`
	writePlugin(t, filepath.Join(office, Dir, "payloads"), Manifest{Name: "payloads", Hooks: []Hook{
		{Event: EventLoad, Lua: "hook.lua"},
		{Event: EventUnload, Lua: "hook.lua"},
		{Event: EventStartup, Lua: "hook.lua"},
		{Event: EventShutdown, Lua: "hook.lua"},
		{Event: EventCompanyShutdown, Lua: "hook.lua"},
	}}, script)
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, err := manager.EmitLifecycle(context.Background(), Event{Name: EventStartup, Data: map[string]any{
		"office_path": office, "office_started_at_unix": int64(1),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EmitLifecycle(context.Background(), Event{Name: EventShutdown, Data: map[string]any{
		"office_path": office, "reason": "test", "safe": true,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EmitLifecycle(context.Background(), Event{Name: EventCompanyShutdown, Data: map[string]any{
		"home_path": office,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	for event, want := range map[string]string{
		EventLoad:            "plugin,scope",
		EventUnload:          "plugin,scope",
		EventStartup:         "office_path,office_started_at_unix",
		EventShutdown:        "office_path,reason,safe",
		EventCompanyShutdown: "home_path",
	} {
		assertStored(t, manager, "local", "payloads", "keys_"+event, `"`+want+`"`)
	}
}

func TestCloseRunsUnloadInReverseContinuesAfterFailureAndIsIdempotent(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "a-fails"), Manifest{Name: "a-fails", Hooks: []Hook{{Event: EventUnload, Lua: "hook.lua"}}}, `error("unload failed")`)
	writePlugin(t, filepath.Join(office, Dir, "b-runs"), Manifest{Name: "b-runs", Hooks: []Hook{{Event: EventUnload, Lua: "hook.lua"}}}, `omo.global_set("order", (omo.global_get("order") or "") .. "-b")`)
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "global", "", "order", `"-b"`)
}

func TestAliasedLifecycleFailureLogsUnderManifestName(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "installed"), Manifest{Name: "manifest", Hooks: []Hook{{Event: EventUnload, Lua: "hook.lua"}}}, `error("aliased failure")`)
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	logs, err := db.PluginLogs(database, "manifest")
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, log := range logs {
		joined += log.Message + "\n"
	}
	if len(logs) == 0 || !strings.Contains(joined, "aliased failure") {
		t.Fatalf("manifest logs = %+v", logs)
	}
}

func TestLifecycleCommandFailuresPersistStderr(t *testing.T) {
	office, database := newPluginOffice(t)
	command := []string{os.Args[0], "-test.run=^TestLifecycleFailingCommandChildProcess$"}
	writePlugin(t, filepath.Join(office, Dir, "command-failure"), Manifest{Name: "command-failure", Hooks: []Hook{
		{Event: EventLoad, Command: command},
		{Event: EventUnload, Command: command},
	}}, "")
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	logs, err := db.PluginLogs(database, "command-failure")
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, log := range logs {
		joined += log.Message + "\n"
	}
	if !strings.Contains(joined, "lifecycle stderr") {
		t.Fatalf("command stderr logs = %+v", logs)
	}
}

func TestLifecycleFailingCommandChildProcess(t *testing.T) {
	if os.Getenv("OMO_PLUGIN_EVENT") == "" {
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, "lifecycle stderr")
	os.Exit(1)
}

func TestUnloadSeesSharedSnapshotBeforeDeletion(t *testing.T) {
	office, database := newPluginOffice(t)
	shared := t.TempDir()
	writePlugin(t, filepath.Join(shared, "shared"), Manifest{Name: "shared", Hooks: []Hook{{Event: EventUnload, Lua: "hook.lua"}}}, `local _, err = omo.exec("test", "-f", "plugin.json"); omo.global_set("snapshot", err == "")`)
	manager, err := LoadSources(office, database, Source{Root: shared, Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := manager.snapshotDir
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "global", "", "snapshot", `true`)
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("snapshot after unload = %v, want removed", err)
	}
}

func TestLifecycleCommandStdoutIsIgnoredForImmutableEvents(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "command"), Manifest{Name: "command", Hooks: []Hook{{Event: EventStartup, Command: []string{os.Args[0], "-test.run=^TestLifecycleCommandChildProcess$"}}}}, "")
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	event, err := manager.emitLifecycle(context.Background(), EventStartup, map[string]any{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := event.Data["bad"]; ok {
		t.Fatalf("immutable command output changed event: %#v", event.Data)
	}
}

func TestLifecycleOSExecuteHonorsExplicitTimeout(t *testing.T) {
	office, database := newPluginOffice(t)
	t.Setenv("OMO_LIFECYCLE_OS_EXEC_CHILD", "1")
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("OMO_LIFECYCLE_OS_EXEC_PID", pidPath)
	command := lifecycleShellQuote(os.Args[0]) + " -test.run=^TestLifecycleOSExecuteChildProcess$"
	writePlugin(t, filepath.Join(office, Dir, "os-execute"), Manifest{Name: "os-execute", Hooks: []Hook{{
		Event: EventStartup, Timeout: "50ms", Lua: "hook.lua",
	}}}, `os.execute(os.getenv("OMO_LIFECYCLE_OS_EXEC_COMMAND"))`)
	t.Setenv("OMO_LIFECYCLE_OS_EXEC_COMMAND", command)
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	started := time.Now()
	_, err = manager.EmitLifecycle(context.Background(), Event{Name: EventStartup, Data: map[string]any{}})
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("os.execute lifecycle hook took %s after timeout", elapsed)
	}
	if err == nil {
		t.Fatal("timed out os.execute lifecycle hook returned nil error")
	}
	rawPID, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read os.execute child pid: %v", err)
	}
	pid, err := strconv.Atoi(string(rawPID))
	if err != nil {
		t.Fatalf("parse os.execute child pid %q: %v", rawPID, err)
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycleProcessStillRunning(child, pid) {
		t.Fatal("os.execute child process survived lifecycle timeout")
	}
}

func lifecycleProcessStillRunning(process *os.Process, pid int) bool {
	if runtime.GOOS == "linux" {
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if err != nil {
			return false
		}
		// A killed orphan may remain as a zombie until its reaper collects it;
		// that process has exited even though kill(2) still accepts its PID.
		if end := strings.LastIndexByte(string(raw), ')'); end >= 0 {
			fields := strings.Fields(string(raw)[end+1:])
			return len(fields) == 0 || fields[0] != "Z"
		}
	}
	return process.Kill() == nil
}

func TestLifecycleOSExecuteChildProcess(t *testing.T) {
	if os.Getenv("OMO_LIFECYCLE_OS_EXEC_CHILD") != "1" {
		return
	}
	if path := os.Getenv("OMO_LIFECYCLE_OS_EXEC_PID"); path != "" {
		if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(5 * time.Second)
}

func lifecycleShellQuote(value string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestLifecycleCommandChildProcess(t *testing.T) {
	if os.Getenv("OMO_PLUGIN_EVENT") != EventStartup {
		return
	}
	if _, err := fmt.Fprint(os.Stdout, `{"bad":true}`); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestLifecycleTimeoutBoundsClose(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "slow"), Manifest{Name: "slow", Hooks: []Hook{{Event: EventUnload, Timeout: "10ms", Lua: "hook.lua"}}}, `while true do end`)
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Close() took %s after lifecycle timeout", elapsed)
	}
}
