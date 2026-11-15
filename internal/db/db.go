// Package db opens the omo SQLite database and owns its versioned schema and
// the append-only event log.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Queryer is satisfied by *sql.DB and *sql.Tx.
type Queryer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// OpenReadOnly opens an existing omo database without schema creation or
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

// SchemaVersion is the schema version a freshly opened database ends up at.
const SchemaVersion = 2

// Open opens (creating if needed) the omo database in WAL mode and applies
// pending migrations, each in its own transaction. A single connection
// serializes all writes.
func Open(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	if err := migrate(d); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func migrate(d *sql.DB) error {
	for v := 0; v < len(migrations); v++ {
		if err := applyMigration(d, v); err != nil {
			return err
		}
	}
	return nil
}

// connect opens the pooled connection. Switching a fresh file to WAL during
// connection setup can report SQLITE_BUSY without consulting busy_timeout when
// another opener does the same, so it is retried for a bounded time.
func connect(ctx context.Context, d *sql.DB) (*sql.Conn, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := d.Conn(ctx)
		if err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") || time.Now().After(deadline) {
			return conn, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// applyMigration runs migration v (taking the schema from version v to v+1) in
// a write-locked transaction. The version is re-read under the lock, so a
// migration another opener applied in the meantime is skipped.
func applyMigration(d *sql.DB, v int) error {
	ctx := context.Background()
	conn, err := connect(ctx, d)
	if err != nil {
		return fmt.Errorf("connect for schema version %d: %w", v+1, err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("lock database for schema version %d: %w", v+1, err)
	}
	committed := false
	defer func() {
		if !committed {
			conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	var current int
	if err := conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than supported version %d", current, len(migrations))
	}
	if current > v {
		return nil
	}
	if _, err := conn.ExecContext(ctx, migrations[v]); err != nil {
		return fmt.Errorf("migrate to schema version %d: %w", v+1, err)
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
		return fmt.Errorf("set schema version %d: %w", v+1, err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit schema version %d: %w", v+1, err)
	}
	committed = true
	return nil
}
