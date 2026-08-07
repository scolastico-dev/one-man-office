package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

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

func TestAgentLifecycle(t *testing.T) {
	d := open(t)
	a := Agent{Name: "developer-jason", Role: "developer", Profile: "sonnet", JobID: 7, Goal: "build it"}
	if err := InsertAgent(d, a); err != nil {
		t.Fatal(err)
	}
	got, err := GetAgent(d, "developer-jason")
	if err != nil || got.State != "spawning" || got.JobID != 7 {
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
}
