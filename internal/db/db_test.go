package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestModelUsageSnapshotsKeepLatestCheckPerProfile(t *testing.T) {
	d := open(t)
	first := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	if err := UpsertModelUsageSnapshot(d, ModelUsageSnapshot{Profile: "codex-luna", Provider: "codex", UsedPercent: 42, FetchedAt: first}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertModelUsageSnapshot(d, ModelUsageSnapshot{Profile: "codex-luna", Provider: "codex", UsedPercent: 63.5, FetchedAt: second}); err != nil {
		t.Fatal(err)
	}
	if err := UpsertModelUsageSnapshot(d, ModelUsageSnapshot{Profile: "claude-opus", Provider: "claude", UsedPercent: 51, FetchedAt: first}); err != nil {
		t.Fatal(err)
	}

	rows, err := ModelUsageSnapshots(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Profile != "claude-opus" || rows[1].Profile != "codex-luna" {
		t.Fatalf("usage rows = %+v", rows)
	}
	if rows[1].UsedPercent != 63.5 || !rows[1].FetchedAt.Equal(second) {
		t.Fatalf("latest codex snapshot = %+v", rows[1])
	}
}

func open(t *testing.T) *sql.DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestOpenIsWAL(t *testing.T) {
	d := open(t)
	var mode string
	if err := d.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestOpenReadOnlyAllowsQueriesAndRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	writable, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writable.Close()
	if err := AppendEvent(writable, "existing", "", 0, "visible"); err != nil {
		t.Fatal(err)
	}
	if err := UpsertModelUsageSnapshot(writable, ModelUsageSnapshot{Profile: "codex-luna", Provider: "codex", UsedPercent: 60}); err != nil {
		t.Fatal(err)
	}
	observer, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	if events, err := AllEvents(observer); err != nil || len(events) != 1 {
		t.Fatalf("read events = %+v, %v", events, err)
	}
	if usage, err := ModelUsageSnapshots(observer); err != nil || len(usage) != 1 || usage[0].UsedPercent != 60 {
		t.Fatalf("read usage = %+v, %v", usage, err)
	}
	if err := AppendEvent(observer, "forbidden", "", 0, "write"); err == nil {
		t.Fatal("read-only database accepted a write")
	}
}

func TestAgentLifecycle(t *testing.T) {
	d := open(t)
	a := Agent{Name: "developer-jason", Role: "developer", Profile: "sonnet", JobID: 7, Goal: "build it", WorkDir: "/worktrees/job-7"}
	if err := InsertAgent(d, a); err != nil {
		t.Fatal(err)
	}
	got, err := GetAgent(d, "developer-jason")
	if err != nil || got.State != "spawning" || got.JobID != 7 || got.WorkDir != "/worktrees/job-7" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if err := SetAgentState(d, "developer-jason", "working"); err != nil {
		t.Fatal(err)
	}
	if err := SetAgentStep(d, "developer-jason", "running integration tests"); err != nil {
		t.Fatal(err)
	}
	got, _ = GetAgent(d, "developer-jason")
	if got.Step != "running integration tests" || !got.StepUpdatedAt.Valid {
		t.Fatalf("step not persisted: %+v", got)
	}
	if n, _ := CountLivingByRole(d, "developer"); n != 1 {
		t.Fatalf("living developers = %d, want 1", n)
	}
	if err := MarkAllAgentsDead(d); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountLivingByRole(d, "developer"); n != 0 {
		t.Fatalf("living after MarkAllAgentsDead = %d, want 0", n)
	}
}

func TestEvents(t *testing.T) {
	d := open(t)
	if err := AppendEvent(d, "agent_spawned", "pm-alex", 0, "role=product_manager"); err != nil {
		t.Fatal(err)
	}
	if err := AppendEvent(d, "job_state", "", 3, "queued→assigned"); err != nil {
		t.Fatal(err)
	}
	evs, err := EventsSince(d, 0)
	if err != nil || len(evs) != 2 {
		t.Fatalf("events = %v err %v", evs, err)
	}
	last, _ := LastEventID(d)
	evs, _ = EventsSince(d, last)
	if len(evs) != 0 {
		t.Fatalf("expected no events after last id, got %d", len(evs))
	}
	all, err := AllEvents(d)
	if err != nil || len(all) != 2 || all[0].ID < all[1].ID {
		t.Fatalf("AllEvents = %+v err %v", all, err)
	}
	if count, err := CountEvents(d); err != nil || count != 2 {
		t.Fatalf("CountEvents = %d, %v", count, err)
	}
	page, err := EventsPage(d, 1, 1)
	if err != nil || len(page) != 1 || page[0].ID != all[1].ID {
		t.Fatalf("EventsPage = %+v, %v", page, err)
	}
}

func TestAllIncidentsNewestFirst(t *testing.T) {
	d := open(t)
	if _, err := d.Exec(`INSERT INTO incidents (agent, class, detail) VALUES
		('developer-jason', 'stuck', 'no output'),
		('pm-alex', 'loop', 'same command')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE incidents SET state = 'resolved', resolved_at = datetime('now') WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	incidents, err := AllIncidents(d)
	if err != nil || len(incidents) != 2 {
		t.Fatalf("AllIncidents = %+v err %v", incidents, err)
	}
	if incidents[0].Agent != "pm-alex" || incidents[1].State != "resolved" || !incidents[1].ResolvedAt.Valid {
		t.Fatalf("unexpected incident history: %+v", incidents)
	}
}
