package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
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

func TestOpenCreatesCurrentSchema(t *testing.T) {
	d := open(t)
	if v := userVersion(t, d); v != 3 || SchemaVersion != 3 {
		t.Fatalf("user_version = %d, SchemaVersion = %d, want 3", v, SchemaVersion)
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
	if v := userVersion(t, d); v != SchemaVersion {
		t.Fatalf("user_version = %d, want %d", v, SchemaVersion)
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

func TestOpenConcurrentFreshDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	const n = 8
	var wg sync.WaitGroup
	dbs := make([]*sql.DB, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			dbs[i], errs[i] = Open(path)
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("Open #%d: %v", i, errs[i])
		}
		defer dbs[i].Close()
		if v := userVersion(t, dbs[i]); v != SchemaVersion {
			t.Fatalf("Open #%d: user_version = %d, want %d", i, v, SchemaVersion)
		}
	}
}

func hasColumn(t *testing.T, d *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := d.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == column {
			return true
		}
	}
	return false
}

func TestMigrationTwoAddsEngineColumns(t *testing.T) {
	d := open(t)
	if v := userVersion(t, d); v != SchemaVersion {
		t.Fatalf("user_version = %d, want %d", v, SchemaVersion)
	}
	for _, c := range [][2]string{
		{"tickets", "busy"}, {"tickets", "failed_step"}, {"tickets", "start_blocked"},
		{"ticket_projects", "export_url"},
		{"agent_sessions", "purpose"}, {"agent_sessions", "review_round"},
	} {
		if !hasColumn(t, d, c[0], c[1]) {
			t.Errorf("%s.%s missing", c[0], c[1])
		}
	}
	if _, err := d.Exec(`INSERT INTO agent_sessions (id, role, name, model, state, started_at) VALUES ('c1','ceo','n','m','running','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	var purpose string
	var round int
	if err := d.QueryRow(`SELECT purpose, review_round FROM agent_sessions WHERE id='c1'`).Scan(&purpose, &round); err != nil {
		t.Fatal(err)
	}
	if purpose != "work" || round != 0 {
		t.Fatalf("defaults: purpose %q round %d", purpose, round)
	}
	if _, err := d.Exec(`UPDATE agent_sessions SET purpose='bogus'`); err == nil {
		t.Fatal("purpose check not enforced")
	}
}

// schemaOf returns the normalized schema (tables, columns and indexes).
func schemaOf(t *testing.T, d *sql.DB) string {
	t.Helper()
	rows, err := d.Query(`SELECT type, name, sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var typ, name, sqlText string
		if err := rows.Scan(&typ, &name, &sqlText); err != nil {
			t.Fatal(err)
		}
		b.WriteString(typ + " " + name + " " + sqlText + "\n")
	}
	for _, table := range []string{"tickets", "ticket_projects", "agent_sessions"} {
		cols, err := d.Query(`SELECT name, type, "notnull", dflt_value FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatal(err)
		}
		for cols.Next() {
			var n, ty string
			var nn int
			var def sql.NullString
			if err := cols.Scan(&n, &ty, &nn, &def); err != nil {
				t.Fatal(err)
			}
			b.WriteString(table + "." + n + " " + ty + " " + def.String + "\n")
		}
		cols.Close()
	}
	return b.String()
}

func TestMigrationTwoUpgradesVersionOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	old, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO tickets (type, title, rank, supervisor, created_by_role, created_at, updated_at) VALUES ('task', 'keep me', 1, 'user', 'user', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := userVersion(t, d); v != SchemaVersion {
		t.Fatalf("user_version = %d, want %d", v, SchemaVersion)
	}
	var title, failed, blocked string
	var busy int
	if err := d.QueryRow(`SELECT title, busy, failed_step, start_blocked FROM tickets WHERE id=1`).Scan(&title, &busy, &failed, &blocked); err != nil {
		t.Fatal(err)
	}
	if title != "keep me" || busy != 0 || failed != "" || blocked != "" {
		t.Fatalf("ticket after upgrade: %q %d %q %q", title, busy, failed, blocked)
	}
	upgraded := schemaOf(t, d)
	d.Close()

	// Opening again applies nothing and keeps the data.
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v := userVersion(t, d); v != SchemaVersion {
		t.Fatalf("user_version after reopen = %d", v)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("tickets after reopen: %d, %v", n, err)
	}
	if fresh := schemaOf(t, open(t)); fresh != upgraded {
		t.Fatalf("upgraded schema differs from fresh schema:\n%s\nvs\n%s", upgraded, fresh)
	}
}

func TestFailedStepCheck(t *testing.T) {
	d := open(t)
	if _, err := d.Exec(`INSERT INTO tickets (type, title, rank, supervisor, created_by_role, created_at, updated_at) VALUES ('task', 't', 1, 'user', 'user', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE tickets SET failed_step='bogus'`); err == nil {
		t.Fatal("failed_step check not enforced")
	}
	if _, err := d.Exec(`UPDATE tickets SET failed_step='merge', start_blocked='slot', busy=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE tickets SET start_blocked='bogus'`); err == nil {
		t.Fatal("start_blocked check not enforced")
	}
	if _, err := d.Exec(`UPDATE tickets SET busy=2`); err == nil {
		t.Fatal("busy check not enforced")
	}
}

func TestMigrationThreeAddsProjectExportState(t *testing.T) {
	d := open(t)
	for _, c := range [][2]string{{"ticket_projects", "export_state"}, {"ticket_projects", "export_error"}} {
		if !hasColumn(t, d, c[0], c[1]) {
			t.Errorf("%s.%s missing", c[0], c[1])
		}
	}
}
