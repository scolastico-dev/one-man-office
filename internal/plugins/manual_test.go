package plugins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManualRunsOnlySelectedActionAndGatesArgumentsPerHook(t *testing.T) {
	office, database := newPluginOffice(t)
	for _, name := range []string{"selected", "other"} {
		writePlugin(t, filepath.Join(office, Dir, name), Manifest{Name: name, Hooks: []Hook{
			{Event: "manual", Name: "run", Description: "Build the report", ManualArgs: true, Lua: "hook.lua"},
			{Event: "manual", Name: "reset", Description: "Reset the report", Lua: "second.lua"},
			{Event: EventAgentStart, Lua: "hook.lua"},
		}}, `assert(event.event == "manual")
assert(event.data.caller == "user")
assert(event.data.action == "run")
assert(event.data.args[1] == "two words" and event.data.args[2] == "--flag")
assert(event.data.at_unix > 0)
omo.local_set("runs", (omo.local_get("runs") or 0) + 1)`)
		if err := os.WriteFile(filepath.Join(office, Dir, name, "second.lua"), []byte(`assert(#event.data.args == 0); omo.local_set("reset", true)`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TriggerManual("selected", "run", "user", []string{"two words", "--flag"}); err != nil {
		t.Fatal(err)
	}
	assertStored(t, m, "local", "selected", "runs", "1")
	if err := m.TriggerManual("selected", "reset", "user", []string{"no"}); err == nil {
		t.Fatal("reset accepted disabled arguments")
	}
	if err := m.TriggerManual("selected", "missing", "user", nil); err == nil {
		t.Fatal("accepted unknown action")
	}
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
	var reset int
	if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE key='reset'`).Scan(&reset); err != nil {
		t.Fatal(err)
	}
	if reset != 0 {
		t.Fatal("unselected action ran")
	}
	if err := m.TriggerManual("selected", "reset", "user", nil); err != nil {
		t.Fatal(err)
	}
	assertStored(t, m, "local", "selected", "reset", "true")
	actions := m.ManualActions("selected")
	if len(actions) != 2 || actions[0].Name != "run" || actions[0].Description != "Build the report" || !actions[0].ManualArgs || actions[1].ManualArgs {
		t.Fatalf("actions = %+v", actions)
	}
}

func TestManualRejectsOverlappingRunsOfSamePlugin(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "blocking"), Manifest{Name: "blocking", Hooks: []Hook{
		{Event: "manual", Name: "run", Description: "Run action", Lua: "hook.lua"},
		{Event: "manual", Name: "again", Description: "Another action", Lua: "hook.lua"},
	}}, `omo.local_set("entered", true); while not omo.global_get("release") do end`)
	writePlugin(t, filepath.Join(office, Dir, "independent"), Manifest{Name: "independent", Hooks: []Hook{{Event: "manual", Name: "run", Description: "Run action", Lua: "hook.lua"}}}, `omo.local_set("ran", true)`)
	m, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	done := make(chan error, 1)
	go func() { done <- m.TriggerManual("blocking", "run", "user", nil) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE plugin='blocking' AND key='entered'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hook never entered")
		}
		time.Sleep(time.Millisecond)
	}
	second := make(chan error, 1)
	go func() { second <- m.TriggerManual("blocking", "again", "user", nil) }()
	select {
	case err := <-second:
		if err == nil || !strings.Contains(err.Error(), "already running") {
			t.Errorf("overlap error = %v", err)
		}
	case <-time.After(time.Second):
		t.Error("overlapping trigger was not rejected promptly")
	}
	if err := m.TriggerManual("independent", "run", "user", nil); err != nil {
		t.Error(err)
	}
	if _, err := database.Exec(`INSERT INTO plugin_storage(scope, plugin, key, value) VALUES('global', '', 'release', 'true')`); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var requests int
	if err := database.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_requested'`).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("accepted %d requests, want two different plugins", requests)
	}
	if err := m.TriggerManual("blocking", "run", "user", nil); err != nil {
		t.Fatalf("finished plugin remained busy: %v", err)
	}
}

func TestManualRejectsInvalidTargetsArgumentsAndBroadcast(t *testing.T) {
	office, database := newPluginOffice(t)
	for _, name := range []string{"manual", "disabled", "automatic"} {
		event := "manual"
		if name == "automatic" {
			event = EventAgentStart
		}
		writePlugin(t, filepath.Join(office, Dir, name), Manifest{Name: name, Hooks: []Hook{{Event: event, Name: "run", Description: "Run action", Lua: "hook.lua"}}}, `omo.local_set("ran", true)`)
	}
	m, err := LoadConfigured(office, database, map[string]Settings{"disabled": {Enabled: false}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing", "disabled", "automatic"} {
		if err := m.TriggerManual(name, "run", "user", nil); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	if err := m.TriggerManual("manual", "run", "user", []string{""}); err == nil {
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
	if err := m.TriggerManual("manual", "run", "user", nil); err != nil {
		t.Fatal(err)
	}
	assertStored(t, m, "local", "manual", "ran", "true")
}

func TestManualRequiresDurableRequestAndReportsHookErrors(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "broken"), Manifest{Name: "broken", Hooks: []Hook{{Event: "manual", Name: "run", Description: "Run action", Lua: "hook.lua"}}}, `omo.local_set("ran", true); error("manual failure")`)
	m, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER reject_manual BEFORE INSERT ON events WHEN NEW.kind='plugin_manual_requested' BEGIN SELECT RAISE(FAIL, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := m.TriggerManual("broken", "run", "user", nil); err == nil || !strings.Contains(err.Error(), "audit unavailable") {
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
	if err := m.TriggerManual("broken", "run", "user", nil); err == nil || !strings.Contains(err.Error(), "manual failure") {
		t.Fatalf("hook error: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_failed'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("missing failure audit")
	}
}

func TestManualManifestRequiresUniqueNamedDescribedActions(t *testing.T) {
	for _, manifest := range []string{
		`{"hooks":[{"event":"manual","description":"Run it","lua":"hook.lua"}]}`,
		`{"hooks":[{"event":"manual","name":"run","lua":"hook.lua"}]}`,
		`{"hooks":[{"event":"manual","name":"run","description":" ","lua":"hook.lua"}]}`,
		`{"hooks":[{"event":"manual","name":"two words","description":"Run it","lua":"hook.lua"}]}`,
		`{"hooks":[{"event":"manual","name":"run","description":"Run it","lua":"hook.lua"},{"event":"manual","name":"run","description":"Again","lua":"hook.lua"}]}`,
		`{"manual_args":true,"hooks":[{"event":"manual","name":"run","description":"Run it","lua":"hook.lua"}]}`,
		`{"hooks":[{"event":"agent_start","manual_args":true,"lua":"hook.lua"}]}`,
	} {
		t.Run(manifest, func(t *testing.T) {
			office, database := newPluginOffice(t)
			dir := filepath.Join(office, Dir, "invalid")
			writePlugin(t, dir, Manifest{}, "")
			if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(office, database); err == nil {
				t.Fatal("accepted invalid manual action manifest")
			}
		})
	}
}
