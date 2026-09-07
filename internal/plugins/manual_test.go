package plugins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManualRunsOnlySelectedPluginInHookOrder(t *testing.T) {
	office, database := newPluginOffice(t)
	for _, name := range []string{"selected", "other"} {
		writePlugin(t, filepath.Join(office, Dir, name), Manifest{Name: name, ManualArgs: true, Hooks: []Hook{
			{Event: "manual", Lua: "hook.lua"}, {Event: "manual", Lua: "second.lua"},
			{Event: EventAgentStart, Lua: "hook.lua"},
		}}, `assert(event.event == "manual")
assert(event.data.caller == "user")
assert(event.data.args[1] == "two words" and event.data.args[2] == "--flag")
assert(event.data.at_unix > 0)
omo.local_set("runs", (omo.local_get("runs") or 0) + 1)`)
		if err := os.WriteFile(filepath.Join(office, Dir, name, "second.lua"), []byte(`assert(omo.local_get("runs") == 1); omo.local_set("runs", 2)`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TriggerManual(context.Background(), "selected", "user", []string{"two words", "--flag"}); err != nil {
		t.Fatal(err)
	}
	assertStored(t, m, "local", "selected", "runs", "2")
	var other, requested, completed int
	if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE plugin='other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_requested' AND detail NOT LIKE '%two words%'`).Scan(&requested); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_completed'`).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if other != 0 || requested != 1 || completed != 1 {
		t.Fatalf("other=%d requested=%d completed=%d", other, requested, completed)
	}
}

func TestManualRejectsInvalidTargetsArgumentsAndBroadcast(t *testing.T) {
	office, database := newPluginOffice(t)
	for _, name := range []string{"manual", "disabled", "automatic"} {
		event := "manual"
		if name == "automatic" {
			event = EventAgentStart
		}
		writePlugin(t, filepath.Join(office, Dir, name), Manifest{Name: name, Hooks: []Hook{{Event: event, Lua: "hook.lua"}}}, `omo.local_set("ran", true)`)
	}
	m, err := LoadConfigured(office, database, map[string]Settings{"disabled": {Enabled: false}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing", "disabled", "automatic"} {
		if err := m.TriggerManual(context.Background(), name, "user", nil); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	if err := m.TriggerManual(context.Background(), "manual", "user", []string{""}); err == nil {
		t.Fatal("accepted disabled arguments")
	}
	if _, err := m.Emit(context.Background(), Event{Name: "manual"}); err == nil {
		t.Fatal("accepted manual broadcast")
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_storage`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected request ran hooks")
	}
	if err := m.TriggerManual(context.Background(), "manual", "user", nil); err != nil {
		t.Fatal(err)
	}
	assertStored(t, m, "local", "manual", "ran", "true")
}

func TestManualRequiresDurableRequestAndReportsHookErrors(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "broken"), Manifest{Name: "broken", Hooks: []Hook{{Event: "manual", Lua: "hook.lua"}}}, `omo.local_set("ran", true); error("manual failure")`)
	m, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER reject_manual BEFORE INSERT ON events WHEN NEW.kind='plugin_manual_requested' BEGIN SELECT RAISE(FAIL, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := m.TriggerManual(context.Background(), "broken", "user", nil); err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("request error: %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_storage`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("hook ran before durable request")
	}
	if _, err := database.Exec(`DROP TRIGGER reject_manual`); err != nil {
		t.Fatal(err)
	}
	if err := m.TriggerManual(context.Background(), "broken", "user", nil); err == nil || !strings.Contains(err.Error(), "manual failure") {
		t.Fatalf("hook error: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_failed'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("missing failure audit")
	}
}

func TestManualArgsRequiresManualSubscription(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "invalid"), Manifest{Name: "invalid", ManualArgs: true, Hooks: []Hook{{Event: EventAgentStart, Lua: "hook.lua"}}}, "")
	if _, err := Load(office, database); err == nil {
		t.Fatal("accepted manual_args without a manual hook")
	}
}
