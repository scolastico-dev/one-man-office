// Package db opens the omo SQLite database and owns its versioned schema and
// the append-only event log.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
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
// migrations; SQLite itself refuses every write. Open uses it to read the
// schema version of a database before it changes anything.
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

// ErrNewerSchema is returned (wrapped) by Open for a database a newer omo
// wrote. Open changes nothing in it.
var ErrNewerSchema = errors.New("the database was made by a newer omo")

// SchemaVersion is the schema version a freshly opened database ends up at.
const SchemaVersion = 10

// Open opens (creating if needed) the omo database in WAL mode and applies
// pending migrations, each in its own transaction. A single connection
// serializes all writes.
func Open(path string) (*sql.DB, error) {
	// A database of a newer omo is refused before anything (switching it to
	// WAL included) touches it.
	if err := refuseNewer(path); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)" + syncPragma()
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	if err := migrate(d); err != nil {
		d.Close()
		if errors.Is(err, ErrNewerSchema) {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return nil, err
	}
	return d, nil
}

// syncPragma is "" for omo itself: SQLite then flushes every commit to the
// disk, so a commit survives a power failure. Inside a Go test binary the
// flush is turned off: a test's database lives in a temporary folder and
// never has to survive the machine, and the flush is what makes the suite
// slow on a real disk (about three times here, far more on a Windows CI
// runner, where it ran packages into their time limit). Nothing else about
// the database differs in tests. A started omo binary is never a test
// binary, so the smoke and browser tests run with the flush.
func syncPragma() string {
	if testing.Testing() {
		return "&_pragma=synchronous(OFF)"
	}
	return ""
}

func refuseNewer(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	d, err := OpenReadOnly(path)
	if err != nil {
		// Not readable this way (for example not yet a database): Open
		// reports what is wrong.
		return nil
	}
	defer d.Close()
	var current int
	if err := d.QueryRow(`PRAGMA user_version`).Scan(&current); err != nil {
		return nil
	}
	if current > len(migrations) {
		return fmt.Errorf("%s: %w (schema %d, this omo knows %d): update omo to use it (omo update); this omo leaves it as it is", path, ErrNewerSchema, current, len(migrations))
	}
	return nil
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
	// Version 9 rebuilds tickets to widen its review-stage constraint. Foreign
	// keys must be disabled outside the transaction so dependent rows survive
	// the table replacement, then checked before committing the replacement.
	if v == 8 {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return fmt.Errorf("prepare tickets rebuild: %w", err)
		}
		defer conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	}
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
		return fmt.Errorf("%w (schema %d, this omo knows %d): update omo to use it (omo update); this omo leaves it as it is", ErrNewerSchema, current, len(migrations))
	}
	if current > v {
		return nil
	}
	if _, err := conn.ExecContext(ctx, migrations[v]); err != nil {
		return fmt.Errorf("migrate to schema version %d: %w", v+1, err)
	}
	if v == 8 {
		rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
		if err != nil {
			return fmt.Errorf("check rebuilt tickets: %w", err)
		}
		invalid := rows.Next()
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return fmt.Errorf("check rebuilt tickets: %w", rowErr)
		}
		if invalid {
			return fmt.Errorf("check rebuilt tickets: foreign key violation")
		}
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
