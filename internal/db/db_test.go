package db

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
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
	if v := userVersion(t, d); v != 10 || SchemaVersion != 10 {
		t.Fatalf("user_version = %d, SchemaVersion = %d, want 10", v, SchemaVersion)
	}
	for _, table := range []string{
		"tickets", "ticket_projects", "dependencies", "description_versions", "comments",
		"comment_mentions", "review_verdicts", "agent_sessions", "events", "server_state",
		"plugin_storage", "plugin_runtime", "plugin_logs", "images", "mcp_tokens", "mcp_upstream_auth", "proxy_forwards",
	} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d, err=%v)", table, n, err)
		}
	}
	for _, index := range []string{
		"agent_sessions_ticket", "tickets_col_rank", "tickets_story", "ticket_projects_project",
		"dependencies_reverse", "comments_ticket", "comments_open_questions", "events_ticket",
		"idx_plugin_logs_plugin_id", "images_ticket", "proxy_forwards_ticket",
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
	before, _ := os.ReadFile(path)
	_, err = Open(path)
	if !errors.Is(err, ErrNewerSchema) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "update omo") {
		t.Fatalf("Open of a newer database: %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("Open changed a database of a newer omo")
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

func TestMigrationFiveAddsTicketWorkdir(t *testing.T) {
	d := open(t)
	if !hasColumn(t, d, "tickets", "workdir") {
		t.Fatal("tickets.workdir missing")
	}
	if _, err := d.Exec(`INSERT INTO tickets (type, title, rank, supervisor, created_by_role, created_at, updated_at) VALUES ('task', 't', 1, 'user', 'user', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	var dir string
	if err := d.QueryRow(`SELECT workdir FROM tickets`).Scan(&dir); err != nil || dir != "" {
		t.Fatalf("workdir of a new ticket = %q, %v", dir, err)
	}
}

func TestMigrationSixAddsRecordedCommits(t *testing.T) {
	d := open(t)
	for _, c := range []string{"start_commit", "end_commit", "range_round"} {
		if !hasColumn(t, d, "ticket_projects", c) {
			t.Errorf("ticket_projects.%s missing", c)
		}
	}
	if _, err := d.Exec(`INSERT INTO tickets (type, title, rank, supervisor, created_by_role, created_at, updated_at) VALUES ('task', 't', 1, 'user', 'user', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO ticket_projects (ticket_id, project_id, position) VALUES (1, 'api', 0)`); err != nil {
		t.Fatal(err)
	}
	var start, end string
	var round int
	if err := d.QueryRow(`SELECT start_commit, end_commit, range_round FROM ticket_projects`).Scan(&start, &end, &round); err != nil || start != "" || end != "" || round != 0 {
		t.Fatalf("recorded commits of a new project = %q %q %d, %v", start, end, round, err)
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

// TestMigrationFourUpgradesVersionThree opens a database at schema 3 (what
// the installed omo has written) with data in it: the images table is added,
// nothing else changes, and the result equals a fresh database.
func TestMigrationFourUpgradesVersionThree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	old, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for v := 0; v < 3; v++ {
		if _, err := old.Exec(migrations[v]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.Exec(`PRAGMA user_version = 3`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO tickets (type, title, rank, supervisor, created_by_role, created_at, updated_at) VALUES ('task', 'keep me', 1, 'user', 'user', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO ticket_projects (ticket_id, project_id, position, export_state) VALUES (1, 'api', 0, 'left_out')`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v := userVersion(t, d); v != SchemaVersion {
		t.Fatalf("user_version = %d, want %d", v, SchemaVersion)
	}
	var title, state string
	if err := d.QueryRow(`SELECT t.title, p.export_state FROM tickets t JOIN ticket_projects p ON p.ticket_id = t.id`).Scan(&title, &state); err != nil {
		t.Fatal(err)
	}
	if title != "keep me" || state != "left_out" {
		t.Fatalf("data after upgrade: %q %q", title, state)
	}
	if _, err := d.Exec(`INSERT INTO images (id, ticket_id, author_role, name, content_type, size, created_at) VALUES ('a', 1, 'user', 'x.png', 'image/png', 3, 'x')`); err != nil {
		t.Fatalf("insert an image after the upgrade: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO images (id, ticket_id, author_role, name, content_type, size, created_at) VALUES ('b', 1, 'user', 'x.svg', 'image/svg+xml', 3, 'x')`); err == nil {
		t.Fatal("an SVG image record was accepted")
	}
	if fresh := schemaOf(t, open(t)); fresh != schemaOf(t, d) {
		t.Fatal("upgraded schema differs from fresh schema")
	}
}

// TestMigrationSevenUpgradesVersionSix opens a database at schema 6 with
// data in it: the MCP token and upstream sign-in tables are added, nothing
// else changes, and the result equals a fresh database.
func TestMigrationSevenUpgradesVersionSix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	old, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for v := 0; v < 6; v++ {
		if _, err := old.Exec(migrations[v]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.Exec(`PRAGMA user_version = 6`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO tickets (type, title, rank, supervisor, created_by_role, created_at, updated_at) VALUES ('task', 'keep me', 1, 'user', 'user', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO ticket_projects (ticket_id, project_id, position, start_commit) VALUES (1, 'api', 0, 'abc')`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO server_state (key, value) VALUES ('paused', 'true')`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v := userVersion(t, d); v != SchemaVersion {
		t.Fatalf("user_version = %d, want %d", v, SchemaVersion)
	}
	var title, commit, paused string
	if err := d.QueryRow(`SELECT t.title, p.start_commit FROM tickets t JOIN ticket_projects p ON p.ticket_id = t.id`).Scan(&title, &commit); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT value FROM server_state WHERE key = 'paused'`).Scan(&paused); err != nil {
		t.Fatal(err)
	}
	if title != "keep me" || commit != "abc" || paused != "true" {
		t.Fatalf("data after upgrade: %q %q %q", title, commit, paused)
	}
	if _, err := d.Exec(`INSERT INTO mcp_tokens (id, kind, name, token_hash, created_at) VALUES ('a', 'pat', 'laptop', 'h1', 'x')`); err != nil {
		t.Fatalf("insert a token after the upgrade: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO mcp_tokens (id, kind, name, token_hash, created_at) VALUES ('b', 'login', 'x', 'h2', 'x')`); err == nil {
		t.Fatal("a token of an unknown kind was accepted")
	}
	if _, err := d.Exec(`INSERT INTO mcp_tokens (id, kind, name, token_hash, created_at) VALUES ('c', 'pat', 'x', 'h1', 'x')`); err == nil {
		t.Fatal("a second token with the same hash was accepted")
	}
	if _, err := d.Exec(`INSERT INTO mcp_upstream_auth (server, issuer, client_id, redirect_uri, state, updated_at) VALUES ('github', 'https://as', 'id', 'http://localhost:8090/oauth/callback', 'connected', 'x')`); err != nil {
		t.Fatalf("insert an upstream sign-in after the upgrade: %v", err)
	}
	if fresh := schemaOf(t, open(t)); fresh != schemaOf(t, d) {
		t.Fatal("upgraded schema differs from fresh schema")
	}
}

func TestMigrationNinePreservesTicketsAndAddsExportWatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	old, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for v := 0; v < 8; v++ {
		if _, err := old.Exec(migrations[v]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.Exec(`PRAGMA user_version=8;
 INSERT INTO tickets (id,type,title,rank,supervisor,created_by_role,created_at,updated_at) VALUES (17,'task','preserved',1,'user','user','x','x');
 INSERT INTO ticket_projects (ticket_id,project_id,position) VALUES (17,'api',0);
 INSERT INTO tickets (id,type,title,rank,supervisor,created_by_role,created_at,updated_at,col,resolution,closed_at,export_url) VALUES (18,'task','old export',2,'user','user','x','x','done','exported','x','https://example.test/pr/1');
 INSERT INTO dependencies (ticket_id,depends_on_id,created_at) VALUES (17,18,'x');`); err != nil {
		t.Fatal(err)
	}
	old.Close()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, c := range []string{"export_remote", "export_branch", "export_base", "export_commit", "export_target_commit"} {
		if !hasColumn(t, d, "ticket_projects", c) {
			t.Errorf("missing %s", c)
		}
	}
	if _, err := d.Exec(`UPDATE tickets SET col='review',review_stage='awaiting_merge' WHERE id=17`); err != nil {
		t.Fatal(err)
	}
	var title, col, res, url string
	if err := d.QueryRow(`SELECT title,col,resolution,export_url FROM tickets WHERE id=18`).Scan(&title, &col, &res, &url); err != nil || title != "old export" || col != "done" || res != "exported" || url != "https://example.test/pr/1" {
		t.Fatalf("old ticket changed: %s %s %s %s %v", title, col, res, url, err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM dependencies WHERE ticket_id=17 AND depends_on_id=18`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("dependency lost: %d %v", n, err)
	}
	if _, err := d.Exec(`UPDATE tickets SET review_stage='invalid' WHERE id=17`); err == nil {
		t.Fatal("invalid review stage accepted")
	}
	if _, err := d.Exec(`INSERT INTO ticket_projects(ticket_id,project_id,position) VALUES(999,'missing',0)`); err == nil {
		t.Fatal("foreign keys disabled after migration")
	}
}
