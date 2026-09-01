// Package db opens the office SQLite database and owns its schema plus the
// small agents/events tables. Jobs and messages have their own packages.
package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Queryer is satisfied by *sql.DB and *sql.Tx.
type Queryer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// OpenReadOnly opens an existing office database without schema creation or
// migrations. SQLite itself enforces the observer's no-write contract.
func OpenReadOnly(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, fmt.Errorf("open read-only database: %w", err)
	}
	return d, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS agents (
  name       TEXT PRIMARY KEY,
  role       TEXT NOT NULL,
  profile    TEXT NOT NULL,
  job_id     INTEGER NOT NULL DEFAULT 0,
  goal       TEXT NOT NULL DEFAULT '',
  workdir    TEXT NOT NULL DEFAULT '',
  current_step TEXT NOT NULL DEFAULT '',
  step_updated_at TEXT,
  state      TEXT NOT NULL DEFAULT 'spawning',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  ended_at   TEXT
);
CREATE TABLE IF NOT EXISTS jobs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  title      TEXT NOT NULL,
  goal       TEXT NOT NULL,
  role       TEXT NOT NULL,
  model      TEXT NOT NULL DEFAULT '',
  repo       TEXT NOT NULL DEFAULT '',
  worktree   TEXT NOT NULL DEFAULT '',
  branch     TEXT NOT NULL DEFAULT '',
  parent_job INTEGER NOT NULL DEFAULT 0,
  state      TEXT NOT NULL DEFAULT 'queued',
  assignee   TEXT NOT NULL DEFAULT '',
  result     TEXT NOT NULL DEFAULT '',
  note       TEXT NOT NULL DEFAULT '',
  retries    INTEGER NOT NULL DEFAULT 0,
  review_rejections INTEGER NOT NULL DEFAULT 0,
  review_override   INTEGER NOT NULL DEFAULT 0,
  developer_models TEXT NOT NULL DEFAULT '[]',
  force_developer_model TEXT NOT NULL DEFAULT '',
  force_model INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  from_agent TEXT NOT NULL,
  to_target  TEXT NOT NULL,
  subject    TEXT NOT NULL,
  body       TEXT NOT NULL,
  priority   TEXT NOT NULL DEFAULT 'normal',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  read_at    TEXT
);
CREATE TABLE IF NOT EXISTS events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL,
  agent      TEXT NOT NULL DEFAULT '',
  job_id     INTEGER NOT NULL DEFAULT 0,
  detail     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS incidents (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  agent       TEXT NOT NULL,
  class       TEXT NOT NULL,
  detail      TEXT NOT NULL DEFAULT '',
  state       TEXT NOT NULL DEFAULT 'open',
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  resolved_at TEXT
);
CREATE TABLE IF NOT EXISTS overall_statistics (
  model        TEXT PRIMARY KEY,
  agents_started INTEGER NOT NULL DEFAULT 0,
  active_ns    INTEGER NOT NULL DEFAULT 0,
  idle_ns      INTEGER NOT NULL DEFAULT 0,
  ceo_active_ns INTEGER NOT NULL DEFAULT 0,
  ceo_idle_ns  INTEGER NOT NULL DEFAULT 0,
  updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS shutdown_contexts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  role       TEXT NOT NULL,
  job_id     INTEGER NOT NULL DEFAULT 0,
  agent      TEXT NOT NULL UNIQUE,
  context    TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS model_usage_snapshots (
  profile      TEXT PRIMARY KEY,
  provider     TEXT NOT NULL,
  used_percent REAL NOT NULL,
  reset_at     TEXT NOT NULL DEFAULT '',
  session_used_percent REAL,
  session_reset_at TEXT NOT NULL DEFAULT '',
  fetched_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS plugin_storage (
  scope      TEXT NOT NULL CHECK(scope IN ('global', 'local')),
  plugin     TEXT NOT NULL DEFAULT '',
  key        TEXT NOT NULL,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY(scope, plugin, key)
);
CREATE TABLE IF NOT EXISTS plugin_runtime (
  name          TEXT PRIMARY KEY,
  version       TEXT NOT NULL DEFAULT '',
  description   TEXT NOT NULL DEFAULT '',
  state         TEXT NOT NULL,
  hook_count    INTEGER NOT NULL DEFAULT 0,
  last_event    TEXT NOT NULL DEFAULT '',
  last_run_at   TEXT NOT NULL DEFAULT '',
  last_log      TEXT NOT NULL DEFAULT '',
  last_log_at   TEXT NOT NULL DEFAULT '',
  updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
`

// Open opens (creating if needed) the office database in WAL mode and runs
// the migration. A single connection serializes all writes.
func Open(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	if _, err := d.Exec(schema); err != nil {
		d.Close()
		return nil, err
	}
	// Additive migrations for offices created by earlier omo versions.
	for _, stmt := range []string{
		`ALTER TABLE jobs ADD COLUMN review_rejections INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs ADD COLUMN review_override INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE agents ADD COLUMN current_step TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN step_updated_at TEXT`,
		`ALTER TABLE agents ADD COLUMN workdir TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE jobs ADD COLUMN developer_models TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE jobs ADD COLUMN force_developer_model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE jobs ADD COLUMN force_model INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE model_usage_snapshots ADD COLUMN session_used_percent REAL`,
		`ALTER TABLE model_usage_snapshots ADD COLUMN session_reset_at TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := d.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			d.Close()
			return nil, err
		}
	}
	// Usage snapshots used to be keyed by model profile. They are provider-level
	// values, so discard legacy duplicates rather than surfacing stale model rows.
	if _, err := d.Exec(`DELETE FROM model_usage_snapshots WHERE profile <> provider`); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
