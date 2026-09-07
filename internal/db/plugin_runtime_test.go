package db

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestOpenAddsPluginLogHistoryWithoutDiscardingLegacyLatestLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE plugin_runtime (
name TEXT PRIMARY KEY, version TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '',
state TEXT NOT NULL, hook_count INTEGER NOT NULL DEFAULT 0, last_event TEXT NOT NULL DEFAULT '',
last_run_at TEXT NOT NULL DEFAULT '', last_log TEXT NOT NULL DEFAULT '', last_log_at TEXT NOT NULL DEFAULT '',
updated_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO plugin_runtime(name,state,last_log,last_log_at) VALUES('logger','ready','legacy line','2026-09-01T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	runtimes, err := PluginRuntimes(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || runtimes[0].LastLog != "legacy line" {
		t.Fatalf("legacy runtime after migration = %+v", runtimes)
	}
	if logs, err := PluginLogs(d, "logger"); err != nil || len(logs) != 0 {
		t.Fatalf("new log history after migration = %+v, %v", logs, err)
	}
}

func TestAppendPluginRuntimeLogRetainsNewestLines(t *testing.T) {
	d := open(t)
	first := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	if err := SyncPluginRuntimes(d, []PluginRuntime{{Name: "logger", State: "ready"}}); err != nil {
		t.Fatal(err)
	}
	if err := AppendPluginRuntimeLog(d, "logger", "one\ntwo", first, 3); err != nil {
		t.Fatal(err)
	}
	if err := AppendPluginRuntimeLog(d, "logger", "three\nfour", second, 3); err != nil {
		t.Fatal(err)
	}

	logs, err := PluginLogs(d, "logger")
	if err != nil {
		t.Fatal(err)
	}
	messages := make([]string, len(logs))
	for i, log := range logs {
		messages[i] = log.Message
	}
	if !slices.Equal(messages, []string{"two", "three", "four"}) {
		t.Fatalf("retained plugin log lines = %q", messages)
	}
	if !logs[0].CreatedAt.Equal(first) || !logs[2].CreatedAt.Equal(second) {
		t.Fatalf("plugin log timestamps = %+v", logs)
	}
	runtimes, err := PluginRuntimes(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(runtimes) != 1 || runtimes[0].LastLog != "four" || !runtimes[0].LastLogAt.Equal(second) {
		t.Fatalf("latest runtime log = %+v", runtimes)
	}
}

func TestSyncPluginRuntimesPreservesLogsAndRemovesMissingPlugins(t *testing.T) {
	d := open(t)
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	if err := SyncPluginRuntimes(d, []PluginRuntime{
		{Name: "nudge", Version: "1.0.0", Description: "reminders", State: "ready", HookCount: 3},
		{Name: "removed", State: "ready", HookCount: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := AppendPluginRuntimeLog(d, "nudge", "sent stale_work reminder", now, 500); err != nil {
		t.Fatal(err)
	}
	if err := AppendPluginRuntimeLog(d, "removed", "orphan candidate", now, 500); err != nil {
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
	if logs, err := PluginLogs(d, "removed"); err != nil || len(logs) != 0 {
		t.Fatalf("removed plugin retained log history: %+v, %v", logs, err)
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
