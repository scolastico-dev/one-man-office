package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

func TestInactiveLogRetentionExemptsLivingSessions(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, ".omo", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{
		"2026-01-01_10-00-developer-old.log",
		"2026-01-01_11-00-developer-middle.log",
		"2026-01-01_12-00-developer-new.log",
		"2026-01-01_09-00-ceo-live.log",
	}
	for i, name := range names {
		path := filepath.Join(logDir, name)
		if err := os.WriteFile(path, []byte("log\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Unix(int64(i+1), 0)
		_ = os.Chtimes(path, when, when)
	}
	s := &Supervisor{OfficeDir: dir, Cfg: &config.Config{Logs: config.Logs{Keep: 2}}, sessions: map[string]*session.Session{"ceo-live": nil}}
	s.PruneInactiveLogs()
	if _, err := os.Stat(filepath.Join(logDir, names[0])); !os.IsNotExist(err) {
		t.Fatalf("old inactive log retained: %v", err)
	}
	for _, name := range names[1:] {
		if _, err := os.Stat(filepath.Join(logDir, name)); err != nil {
			t.Fatalf("expected %s retained: %v", name, err)
		}
	}
}
