package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	bundledplugins "github.com/scolastico-dev/one-man-office/plugins"
)

func TestLuaHookMutatesEventAndPersistsStorage(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, ".omo", "plugins", "decorate")
	writePlugin(t, dir, Manifest{Name: "decorate", Hooks: []Hook{{Event: EventJobCreate, Lua: "hook.lua"}}},
		"local runs = omo.local_get(\"runs\") or 0\n"+
			"omo.local_set(\"runs\", runs + 1)\n"+
			"omo.global_set(\"last_plugin\", \"decorate\")\n"+
			"event.data.title = \"decorated \" .. event.data.title\n"+
			"event.data.goal = event.data.goal .. \" [plugin]\"\n")

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Emit(context.Background(), Event{
		Name: EventJobCreate, Mutable: true,
		Data: map[string]any{"title": "task", "goal": "ship it"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["title"] != "decorated task" || result.Data["goal"] != "ship it [plugin]" {
		t.Fatalf("unexpected mutation: %#v", result.Data)
	}
	assertStored(t, manager, "local", "decorate", "runs", "1")
	assertStored(t, manager, "global", "", "last_plugin", "\"decorate\"")
}

func TestLuaPluginCanLogAndRuntimeReturnsToReady(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, ".omo", "plugins", "logger")
	writePlugin(t, dir, Manifest{Name: "logger", Version: "1.2.3", Hooks: []Hook{{Event: EventAgentStart, Lua: "hook.lua"}}},
		`omo.log("observed " .. event.data.agent)`)

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart, Data: map[string]any{"agent": "developer-ada"}}); err != nil {
		t.Fatal(err)
	}
	runtimes, err := db.PluginRuntimes(database)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || runtimes[0].Name != "logger" || runtimes[0].State != "ready" || runtimes[0].LastEvent != EventAgentStart {
		t.Fatalf("plugin runtime = %+v", runtimes)
	}
	if runtimes[0].LastLog != "observed developer-ada" || runtimes[0].LastLogAt.IsZero() {
		t.Fatalf("plugin log = %+v", runtimes[0])
	}
}

func TestFailedPluginHookRecordsErrorStateAndLog(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, ".omo", "plugins", "broken")
	writePlugin(t, dir, Manifest{Name: "broken", Hooks: []Hook{{Event: EventAgentStart, Lua: "hook.lua"}}},
		`error("something broke")`)

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart}); err == nil {
		t.Fatal("failed hook returned no error")
	}
	runtimes, err := db.PluginRuntimes(database)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || runtimes[0].State != "error" || runtimes[0].LastEvent != EventAgentStart || !strings.Contains(runtimes[0].LastLog, "something broke") {
		t.Fatalf("failed plugin runtime = %+v", runtimes)
	}
}

func TestCommandPluginStderrBecomesLastLog(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, ".omo", "plugins", "command-logger")
	writePlugin(t, dir, Manifest{Name: "command-logger", Hooks: []Hook{{
		Event: EventAgentStart, Command: []string{"sh", "-c", "cat >/dev/null; printf 'command observed agent' >&2"},
	}}}, "")

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart}); err != nil {
		t.Fatal(err)
	}
	runtimes, err := db.PluginRuntimes(database)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || runtimes[0].LastLog != "command observed agent" {
		t.Fatalf("command plugin runtime = %+v", runtimes)
	}
}

func TestLuaStorageKeysAreSortedFilterableAndDeletable(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, ".omo", "plugins", "storage")
	writePlugin(t, dir, Manifest{Name: "storage", Hooks: []Hook{{Event: EventAgentStart, Lua: "hook.lua"}}},
		"omo.local_set(\"activity:z\", 1)\n"+
			"omo.local_set(\"other\", 2)\n"+
			"omo.local_set(\"activity:a\", 3)\n"+
			"local local_keys = omo.local_keys(\"activity:\")\n"+
			"omo.local_set(\"listed\", table.concat(local_keys, \",\"))\n"+
			"omo.local_delete(local_keys[1])\n"+
			"omo.global_set(\"shared:z\", true)\n"+
			"omo.global_set(\"shared:a\", true)\n"+
			"omo.local_set(\"global_listed\", table.concat(omo.global_keys(\"shared:\"), \",\"))\n")

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart}); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "local", "storage", "listed", "\"activity:a,activity:z\"")
	assertStored(t, manager, "local", "storage", "global_listed", "\"shared:a,shared:z\"")
	var deleted int
	if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE scope='local' AND plugin='storage' AND key='activity:a'`).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatal("local_delete did not remove the enumerated key")
	}
}

func TestLuaExecAndCommandHook(t *testing.T) {
	office, database := newPluginOffice(t)
	luaDir := filepath.Join(office, ".omo", "plugins", "exec")
	writePlugin(t, luaDir, Manifest{Name: "exec", Hooks: []Hook{{Event: EventAgentStart, Lua: "hook.lua"}}},
		"local output, err = omo.exec(\"go\", \"version\")\n"+
			"if err ~= \"\" then error(err) end\n"+
			"omo.local_set(\"version\", output)\n")
	commandDir := filepath.Join(office, ".omo", "plugins", "command")
	writePlugin(t, commandDir, Manifest{Name: "command", Hooks: []Hook{{
		Event:   EventJobCreate,
		Command: []string{"sh", "-c", "cat >/dev/null; printf '{\"title\":\"from command\",\"goal\":\"kept\"}'"},
	}}}, "")

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart, Data: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := database.QueryRow("SELECT value FROM plugin_storage WHERE scope='local' AND plugin='exec' AND key='version'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "go version") {
		t.Fatalf("exec output not stored: %s", raw)
	}
	result, err := manager.Emit(context.Background(), Event{Name: EventJobCreate, Mutable: true, Data: map[string]any{"title": "old"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["title"] != "from command" {
		t.Fatalf("command mutation not applied: %#v", result.Data)
	}
}

func TestCronHookRuns(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, ".omo", "plugins", "clock")
	writePlugin(t, dir, Manifest{Name: "clock", Hooks: []Hook{{Event: EventCron, Interval: "10ms", Lua: "hook.lua"}}},
		"omo.local_set(\"ticked\", true)")
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var raw string
		if database.QueryRow("SELECT value FROM plugin_storage WHERE scope='local' AND plugin='clock' AND key='ticked'").Scan(&raw) == nil {
			if raw != "true" {
				t.Fatalf("unexpected cron value: %s", raw)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cron hook did not run")
}

func TestLoadRejectsEscapingLuaPath(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, ".omo", "plugins", "bad")
	writePlugin(t, dir, Manifest{Name: "bad", Hooks: []Hook{{Event: EventAgentStart, Lua: "../outside.lua"}}}, "")
	if _, err := Load(office, database); err == nil || !strings.Contains(err.Error(), "must stay inside") {
		t.Fatalf("expected contained-path error, got %v", err)
	}
}

func TestLoadConfiguredSkipsDisabledManagedPlugin(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, ".omo", "plugins", "disabled")
	writePlugin(t, dir, Manifest{Name: "disabled", Hooks: []Hook{{Event: EventAgentStart, Lua: "hook.lua"}}},
		"omo.local_set(\"ran\", true)")
	manager, err := LoadConfigured(office, database, map[string]Settings{"disabled": {Enabled: false}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart, Data: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM plugin_storage WHERE plugin='disabled'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("disabled plugin hook ran")
	}
	runtimes, err := db.PluginRuntimes(database)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || runtimes[0].Name != "disabled" || runtimes[0].State != "disabled" {
		t.Fatalf("disabled plugin runtime = %+v", runtimes)
	}
}

func TestConfiguredPluginExposesConfigToLuaAndCommandHooks(t *testing.T) {
	office, database := newPluginOffice(t)
	luaDir := filepath.Join(office, ".omo", "plugins", "lua-config")
	writePlugin(t, luaDir, Manifest{Name: "lua-config", Hooks: []Hook{{
		Event: EventCron, Interval: "1m", IntervalConfig: "check_interval", Lua: "hook.lua",
	}}}, "omo.local_set(\"mode\", config.nested.mode)\n"+
		"omo.local_set(\"delay\", omo.duration(config.delay))")

	record := filepath.Join(office, "command-config.json")
	t.Setenv("OMO_TEST_RECORD", record)
	commandDir := filepath.Join(office, ".omo", "plugins", "command-config")
	writePlugin(t, commandDir, Manifest{Name: "command-config", Hooks: []Hook{{
		Event:   EventAgentStart,
		Command: []string{"sh", "-c", "cat >/dev/null; printf '%s' \"$OMO_PLUGIN_CONFIG\" > \"$OMO_TEST_RECORD\""},
	}}}, "")

	settings := map[string]Settings{
		"lua-config": {Enabled: true, Config: map[string]any{
			"check_interval": "7s", "delay": "1500ms", "nested": map[string]any{"mode": "careful"},
		}},
		"command-config": {Enabled: true, Config: map[string]any{
			"threshold": 42, "nested": map[string]any{"mode": "careful"},
		}},
	}
	manager, err := LoadConfigured(office, database, settings)
	if err != nil {
		t.Fatal(err)
	}
	var configuredInterval time.Duration
	for _, hook := range manager.hooks {
		if hook.plugin == "lua-config" {
			configuredInterval = hook.interval
		}
	}
	if configuredInterval != 7*time.Second {
		t.Fatalf("configured cron interval = %s, want 7s", configuredInterval)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventCron}); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "local", "lua-config", "mode", `"careful"`)
	assertStored(t, manager, "local", "lua-config", "delay", "1.5")
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var commandConfig map[string]any
	if err := json.Unmarshal(raw, &commandConfig); err != nil {
		t.Fatalf("command config is not JSON: %v\n%s", err, raw)
	}
	if commandConfig["threshold"] != float64(42) || commandConfig["nested"].(map[string]any)["mode"] != "careful" {
		t.Fatalf("command config = %#v", commandConfig)
	}
}

func TestBundledNudgePluginRemindsStaleWorkingAgent(t *testing.T) {
	office, database := newPluginOffice(t)
	record := filepath.Join(office, "nudge-record")
	stub := buildRecordingOMO(t)
	t.Setenv("OMO_TEST_RECORD", record)
	t.Setenv("PATH", filepath.Dir(stub)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := bundledplugins.EnsureNudge(office); err != nil {
		t.Fatal(err)
	}
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart, Data: map[string]any{
		"agent": "developer-ada", "at_unix": int64(100),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventCron, Data: map[string]any{
		"at_unix": int64(1001),
		"agents": []any{map[string]any{
			"name": "developer-ada", "role": "developer", "state": "working",
			"job_id": int64(7), "job_state": "working", "unread_messages": 0,
			"created_at_unix": int64(100), "step_updated_at_unix": int64(0),
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	invocation := string(raw)
	for _, want := range []string{office, "send", "--to", "developer-ada", "--subject", "Workflow reminder", "omo step", "omo done"} {
		if !strings.Contains(invocation, want) {
			t.Errorf("recorded omo invocation missing %q:\n%s", want, invocation)
		}
	}
	runtimes, err := db.PluginRuntimes(database)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || !strings.Contains(runtimes[0].LastLog, "sent stale_work reminder to developer-ada") {
		t.Fatalf("nudge did not log its reminder: %+v", runtimes)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventCron, Data: map[string]any{
		"at_unix": int64(5000),
		"agents": []any{map[string]any{
			"name": "ceo-ada", "role": "ceo", "state": "working", "job_id": int64(0),
			"job_state": "", "unread_messages": 0, "created_at_unix": int64(100), "step_updated_at_unix": int64(0),
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != invocation {
		t.Fatalf("CEO received an invalid wait/done nudge:\n%s", after)
	}
}

func TestBundledNudgePluginOnlySendsInboxRemindersToCEO(t *testing.T) {
	office, database := newPluginOffice(t)
	record := filepath.Join(office, "nudge-record")
	stub := buildRecordingOMO(t)
	t.Setenv("OMO_TEST_RECORD", record)
	t.Setenv("PATH", filepath.Dir(stub)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := bundledplugins.EnsureNudge(office); err != nil {
		t.Fatal(err)
	}
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart, Data: map[string]any{
		"agent": "ceo-ada", "at_unix": int64(100),
	}}); err != nil {
		t.Fatal(err)
	}
	emitCEO := func(at int64, unread int, jobState string) {
		t.Helper()
		if _, err := manager.Emit(context.Background(), Event{Name: EventCron, Data: map[string]any{
			"at_unix": at,
			"agents": []any{map[string]any{
				"name": "ceo-ada", "role": "ceo", "state": "working", "job_id": int64(0),
				"job_state": jobState, "unread_messages": unread,
				"created_at_unix": int64(100), "step_updated_at_unix": int64(0),
			}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	emitCEO(1001, 1, "")
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--to\nceo-ada", "unread office mail"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("CEO inbox reminder missing %q:\n%s", want, raw)
		}
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	emitCEO(2001, 0, "done")
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("CEO received a non-mail workflow reminder: %v", err)
	}
}

func TestBundledNudgePluginUsesConfiguredTimings(t *testing.T) {
	office, database := newPluginOffice(t)
	record := filepath.Join(office, "nudge-record")
	stub := buildRecordingOMO(t)
	t.Setenv("OMO_TEST_RECORD", record)
	t.Setenv("PATH", filepath.Dir(stub)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := bundledplugins.EnsureNudge(office); err != nil {
		t.Fatal(err)
	}
	manager, err := LoadConfigured(office, database, map[string]Settings{
		"nudge": {Enabled: true, Config: map[string]any{
			"check_interval": "7s",
			"reminders": map[string]any{
				"stale_work": map[string]any{"after": "30m", "repeat": "45m"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if manager.hooks[2].interval != 7*time.Second {
		t.Fatalf("nudge check interval = %s, want 7s", manager.hooks[2].interval)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventAgentStart, Data: map[string]any{
		"agent": "developer-ada", "at_unix": int64(100),
	}}); err != nil {
		t.Fatal(err)
	}
	emitCron := func(at int64) {
		t.Helper()
		if _, err := manager.Emit(context.Background(), Event{Name: EventCron, Data: map[string]any{
			"at_unix": at,
			"agents": []any{map[string]any{
				"name": "developer-ada", "role": "developer", "state": "working",
				"job_id": int64(7), "job_state": "working", "unread_messages": 0,
				"created_at_unix": int64(100), "step_updated_at_unix": int64(0),
			}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	emitCron(1001)
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("nudge fired before configured threshold: %v", err)
	}
	emitCron(2001)
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("nudge did not fire after configured threshold: %v", err)
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	emitCron(3001)
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("nudge repeated before configured cooldown: %v", err)
	}
	emitCron(4802)
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("nudge did not repeat after configured cooldown: %v", err)
	}
}

func TestBundledNudgePluginCleansStorageForDeadAgents(t *testing.T) {
	office, database := newPluginOffice(t)
	if _, err := bundledplugins.EnsureNudge(office); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"activity:dead-ada", "last_nudge:dead-ada:inbox",
		"activity:developer-bea", "last_nudge:developer-bea:inbox", "custom",
	} {
		if _, err := database.Exec(`INSERT INTO plugin_storage(scope,plugin,key,value) VALUES('local','nudge',?, '1')`, key); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventCron, Data: map[string]any{
		"at_unix": int64(999), "snapshot_error": "database busy", "agents": []any{},
	}}); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE scope='local' AND plugin='nudge'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 5 {
		t.Fatalf("snapshot failure changed nudge storage: count=%d, want 5", before)
	}
	if _, err := manager.Emit(context.Background(), Event{Name: EventCron, Data: map[string]any{
		"at_unix": int64(1000),
		"agents": []any{map[string]any{
			"name": "developer-bea", "role": "developer", "state": "waiting",
			"job_id": int64(7), "job_state": "working", "unread_messages": 0,
			"created_at_unix": int64(100), "step_updated_at_unix": int64(0),
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query(`SELECT key FROM plugin_storage WHERE scope='local' AND plugin='nudge' ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"activity:developer-bea", "custom", "last_nudge:developer-bea:inbox"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("remaining nudge keys = %v, want %v", keys, want)
	}
}

func buildRecordingOMO(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	program := `package main
import (
	"os"
	"strings"
)
func main() {
	record := os.Getenv("OMO_TEST_RECORD")
	data := os.Getenv("OMO_OFFICE_DIR") + "\n" + strings.Join(os.Args[1:], "\n")
	if err := os.WriteFile(record, []byte(data), 0644); err != nil { panic(err) }
}
`
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "omo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(dir, name)
	if out, err := exec.Command("go", "build", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("build recording omo: %v\n%s", err, out)
	}
	return binary
}

func newPluginOffice(t *testing.T) (string, *sql.DB) {
	t.Helper()
	office := t.TempDir()
	database, err := db.Open(filepath.Join(office, "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return office, database
}

func writePlugin(t *testing.T, dir string, manifest Manifest, script string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if script != "" {
		if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func assertStored(t *testing.T, manager *Manager, scope, plugin, key, want string) {
	t.Helper()
	var got string
	if err := manager.DB.QueryRow("SELECT value FROM plugin_storage WHERE scope=? AND plugin=? AND key=?", scope, plugin, key).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("stored %s/%s/%s = %s, want %s", scope, plugin, key, got, want)
	}
}
