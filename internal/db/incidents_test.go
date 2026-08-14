package db

import (
	"path/filepath"
	"testing"
)

func TestCloseOpenIncidentsForRestartLeavesResolvedHistoryIntact(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO incidents (agent, class, detail) VALUES ('a', 'stuck', 'open')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO incidents (agent, class, detail, state, resolved_at)
		VALUES ('b', 'other', 'done', 'resolved', '2026-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	closed, err := CloseOpenIncidentsForRestart(d)
	if err != nil || closed != 1 {
		t.Fatalf("closed = %d, err = %v", closed, err)
	}
	incidents, err := AllIncidents(d)
	if err != nil {
		t.Fatal(err)
	}
	if incidents[0].ResolvedAt.String != "2026-01-01 00:00:00" {
		t.Fatalf("existing resolution timestamp changed: %+v", incidents[0])
	}
	if incidents[1].State != "resolved" || !incidents[1].ResolvedAt.Valid {
		t.Fatalf("open incident not closed: %+v", incidents[1])
	}
}
