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
	manager, err := LoadConfigured(office, database, map[string]bool{"disabled": false})
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
