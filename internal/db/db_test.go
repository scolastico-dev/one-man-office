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

func userVersion(t *testing.T, d *sql.DB) int {
	t.Helper()
	var v int
	if err := d.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOpenCreatesSchemaVersion1(t *testing.T) {
	d := open(t)
	if v := userVersion(t, d); v != 1 || SchemaVersion != 1 {
		t.Fatalf("user_version = %d, SchemaVersion = %d, want 1", v, SchemaVersion)
	}
	for _, table := range []string{
		"tickets", "ticket_projects", "dependencies", "description_versions", "comments",
		"comment_mentions", "review_verdicts", "agent_sessions", "events", "server_state",
		"plugin_storage", "plugin_runtime", "plugin_logs",
	} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d, err=%v)", table, n, err)
		}
	}
	for _, index := range []string{
		"agent_sessions_ticket", "tickets_col_rank", "tickets_story", "ticket_projects_project",
		"dependencies_reverse", "comments_ticket", "comments_open_questions", "events_ticket",
		"idx_plugin_logs_plugin_id",
	} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s missing (n=%d, err=%v)", index, n, err)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v := userVersion(t, d); v != 1 {
		t.Fatalf("user_version = %d, want 1", v)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	d, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted a database from a newer schema version")
	}
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
	if _, err := AppendEvent(writable, "existing", "", 0, "visible"); err != nil {
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
	if _, err := AppendEvent(observer, "forbidden", "", 0, "write"); err == nil {
		t.Fatal("read-only database accepted a write")
	}
}

func TestTicketChecksRejectDoneWithoutResolution(t *testing.T) {
	d := open(t)
	_, err := d.Exec(`INSERT INTO tickets (type, title, col, resolution, rank, supervisor, created_by_role, created_at, updated_at, closed_at)
		VALUES ('task', 'x', 'done', '', 1, 'user', 'user', 't', 't', 't')`)
	if err == nil {
		t.Fatal("done ticket without resolution was accepted")
	}
	_, err = d.Exec(`INSERT INTO tickets (type, title, col, resolution, rank, supervisor, created_by_role, created_at, updated_at, closed_at)
		VALUES ('task', 'x', 'done', 'completed', 1, 'user', 'user', 't', 't', 't')`)
	if err != nil {
		t.Fatalf("valid done ticket rejected: %v", err)
	}
}
