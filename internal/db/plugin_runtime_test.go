package db

import (
	"testing"
	"time"
)

func TestSyncPluginRuntimesPreservesLogsAndRemovesMissingPlugins(t *testing.T) {
	d := open(t)
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	if err := SyncPluginRuntimes(d, []PluginRuntime{
		{Name: "nudge", Version: "1.0.0", Description: "reminders", State: "ready", HookCount: 3},
		{Name: "removed", State: "ready", HookCount: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := SetPluginRuntimeLog(d, "nudge", "sent stale_work reminder", now); err != nil {
		t.Fatal(err)
	}
	if err := SyncPluginRuntimes(d, []PluginRuntime{
		{Name: "nudge", Version: "1.1.0", Description: "workflow reminders", State: "ready", HookCount: 4},
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := PluginRuntimes(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("plugin runtimes = %+v", rows)
	}
	got := rows[0]
	if got.Name != "nudge" || got.Version != "1.1.0" || got.Description != "workflow reminders" || got.HookCount != 4 {
		t.Fatalf("nudge runtime metadata = %+v", got)
	}
	if got.LastLog != "sent stale_work reminder" || !got.LastLogAt.Equal(now) {
		t.Fatalf("nudge runtime log = %+v", got)
	}
}

func TestPluginRuntimeStateTracksLastHook(t *testing.T) {
	d := open(t)
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	if err := SyncPluginRuntimes(d, []PluginRuntime{{Name: "nudge", State: "ready", HookCount: 3}}); err != nil {
		t.Fatal(err)
	}
	if err := SetPluginRuntimeState(d, "nudge", "running", "cron", now); err != nil {
		t.Fatal(err)
	}
	rows, err := PluginRuntimes(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != "running" || rows[0].LastEvent != "cron" || !rows[0].LastRunAt.Equal(now) {
		t.Fatalf("plugin runtime = %+v", rows)
	}
}
