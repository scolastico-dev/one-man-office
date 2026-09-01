package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneStorageUsesDistinctActiveOfficeDaysPerFile(t *testing.T) {
	o := newOffice(t, nil)
	storage := filepath.Join(o.Dir, ".omo", "storage")
	if err := os.MkdirAll(storage, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(storage, "old.txt")
	recent := filepath.Join(storage, "recent.txt")
	for _, path := range []string{old, recent} {
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	recentTime := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(recent, recentTime, recentTime); err != nil {
		t.Fatal(err)
	}
	for _, timestamp := range []string{
		"2026-01-02 09:00:00",
		"2026-01-02 18:00:00", // the same calendar day counts only once
		"2026-01-20 10:00:00",
		"2026-03-15 09:00:00", // shutdown gaps do not add active days
	} {
		if _, err := o.DB.Exec(`INSERT INTO events (kind, detail, created_at) VALUES ('test_activity', '', ?)`, timestamp); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := o.Sup.PruneStorage(3)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old file retained: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent file removed: %v", err)
	}
}

func TestPruneStorageZeroDisablesCleanup(t *testing.T) {
	o := newOffice(t, nil)
	path := filepath.Join(o.Dir, ".omo", "storage", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := o.Sup.PruneStorage(0)
	if err != nil || removed != 0 {
		t.Fatalf("disabled cleanup = %d, %v", removed, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("disabled cleanup removed file: %v", err)
	}
}
