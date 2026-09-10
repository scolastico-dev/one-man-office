package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestCEOActivityEstimateUsesTranscriptChanges(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo-ada", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(o.Dir, ".omo", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "2026-01-01_12-00-ceo-ada.log")
	if err := os.WriteFile(logPath, []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	o.Sup.sampleCEOActivity(start)
	o.Sup.sampleCEOActivity(start.Add(2 * time.Second))
	if err := os.WriteFile(logPath, []byte("ready\nthinking\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.Sup.sampleCEOActivity(start.Add(3 * time.Second))

	o.Sup.mu.Lock()
	active, idle := o.Sup.ceoActivityActive, o.Sup.ceoActivityIdle
	o.Sup.mu.Unlock()
	if active != time.Second || idle != 2*time.Second {
		t.Fatalf("CEO estimate = active %s idle %s, want active 1s idle 2s", active, idle)
	}
}

func TestPluginSnapshotReportsCEOTranscriptActivityAtUnix(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo-ada", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "ceo-ada", "working"); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(o.Dir, ".omo", "logs")
	logPath := filepath.Join(logDir, "2026-01-01_12-00-ceo-ada.log")
	if err := os.WriteFile(logPath, []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	o.Sup.sampleCEOActivity(start)
	if err := os.WriteFile(logPath, []byte("ready\noutput\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	observed := start.Add(3 * time.Second)
	o.Sup.sampleCEOActivity(observed)

	if got := o.Sup.PluginSnapshot()["ceo_activity_at_unix"]; got != observed.Unix() {
		t.Fatalf("CEO activity timestamp = %#v, want %d", got, observed.Unix())
	}
}

func TestPluginSnapshotReportsHumanInputOnlyForCurrentCEO(t *testing.T) {
	o := newOffice(t, nil)
	for _, agent := range []db.Agent{
		{Name: "ceo-ada", Role: "ceo", Profile: "ceo"},
		{Name: "developer-bea", Role: "developer", Profile: "developer"},
	} {
		if err := db.InsertAgent(o.DB, agent); err != nil {
			t.Fatal(err)
		}
		if err := db.SetAgentState(o.DB, agent.Name, "working"); err != nil {
			t.Fatal(err)
		}
	}
	o.Sup.RecordUserInput("developer-bea")
	if got := o.Sup.PluginSnapshot()["ceo_activity_at_unix"]; got != int64(0) {
		t.Fatalf("developer input changed CEO activity timestamp to %#v", got)
	}
	before := time.Now().Unix()
	o.Sup.RecordUserInput("ceo-ada")
	after := time.Now().Unix()
	got, ok := o.Sup.PluginSnapshot()["ceo_activity_at_unix"].(int64)
	if !ok || got < before || got > after {
		t.Fatalf("CEO input timestamp = %#v, want Unix time in [%d, %d]", o.Sup.PluginSnapshot()["ceo_activity_at_unix"], before, after)
	}
}

func TestPluginSnapshotResetsCEOActivityForReplacement(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo-ada", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "ceo-ada", "working"); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(o.Dir, ".omo", "logs")
	logPath := filepath.Join(logDir, "2026-01-01_12-00-ceo-ada.log")
	if err := os.WriteFile(logPath, []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	o.Sup.sampleCEOActivity(start)
	if err := os.WriteFile(logPath, []byte("ready\noutput\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.Sup.sampleCEOActivity(start.Add(time.Second))
	if got := o.Sup.PluginSnapshot()["ceo_activity_at_unix"]; got != start.Add(time.Second).Unix() {
		t.Fatalf("old CEO activity timestamp = %#v", got)
	}
	if err := db.SetAgentState(o.DB, "ceo-ada", "dead"); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo-bea", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "ceo-bea", "working"); err != nil {
		t.Fatal(err)
	}
	o.Sup.sampleCEOActivity(start.Add(2 * time.Second))
	if got := o.Sup.PluginSnapshot()["ceo_activity_at_unix"]; got != int64(0) {
		t.Fatalf("replacement CEO inherited activity timestamp %#v", got)
	}
}

func TestPluginSnapshotReportsZeroCEOActivityWithoutActivity(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo-ada", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "ceo-ada", "working"); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	o.Sup.sampleCEOActivity(start)
	if got := o.Sup.PluginSnapshot()["ceo_activity_at_unix"]; got != int64(0) {
		t.Fatalf("inactive CEO activity timestamp = %#v, want 0", got)
	}
}
