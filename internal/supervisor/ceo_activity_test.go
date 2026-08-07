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
