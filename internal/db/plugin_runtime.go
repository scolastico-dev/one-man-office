package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PluginRuntime is the durable status of one plugin of the plugin settings.
// The most recent message remains denormalized here for the overview, while
// plugin_logs stores a bounded line history for the detail view.
type PluginRuntime struct {
	Name        string
	Version     string
	Description string
	State       string
	HookCount   int
	LastEvent   string
	LastRunAt   time.Time
	LastLog     string
	LastLogAt   time.Time
}

type PluginLog struct {
	ID        int64
	Plugin    string
	Message   string
	CreatedAt time.Time
}

// SyncPluginRuntimes reconciles the installed plugin catalog while preserving
// the last hook and log output for plugins that remain installed.
func SyncPluginRuntimes(d *sql.DB, runtimes []PluginRuntime) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if len(runtimes) == 0 {
		if _, err := tx.Exec(`DELETE FROM plugin_runtime WHERE state <> 'removed'`); err != nil {
			return err
		}
	} else {
		placeholders := make([]string, len(runtimes))
		args := make([]any, len(runtimes))
		for i, runtime := range runtimes {
			placeholders[i] = "?"
			args[i] = runtime.Name
		}
		if _, err := tx.Exec(`DELETE FROM plugin_runtime WHERE state <> 'removed' AND name NOT IN (`+strings.Join(placeholders, ",")+`)`, args...); err != nil {
			return err
		}
	}
	for _, runtime := range runtimes {
		if strings.TrimSpace(runtime.Name) == "" {
			return fmt.Errorf("plugin runtime name is required")
		}
		if _, err := tx.Exec(`
INSERT INTO plugin_runtime(name, version, description, state, hook_count, updated_at)
VALUES(?,?,?,?,?,datetime('now'))
ON CONFLICT(name) DO UPDATE SET
  version=excluded.version,
  description=excluded.description,
  state=excluded.state,
  hook_count=excluded.hook_count,
  updated_at=excluded.updated_at`, runtime.Name, runtime.Version, runtime.Description, runtime.State, runtime.HookCount); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func SetPluginRuntimeState(q Queryer, name, state, event string, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	_, err := q.Exec(`UPDATE plugin_runtime SET state=?, last_event=?, last_run_at=?, updated_at=datetime('now') WHERE name=?`,
		state, event, at.UTC().Format(time.RFC3339Nano), name)
	return err
}

// AppendPluginRuntimeLog records every line in a message, preserves the whole
// message as the runtime's latest log, and prunes the plugin's oldest lines in
// the same transaction. maxLines must be positive so history is always bounded.
func AppendPluginRuntimeLog(d *sql.DB, name, message string, at time.Time, maxLines int) error {
	if maxLines < 1 {
		return fmt.Errorf("plugin log line limit must be positive")
	}
	if at.IsZero() {
		at = time.Now()
	}
	timestamp := at.UTC().Format(time.RFC3339Nano)
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lastLine := message
	if at := strings.LastIndex(lastLine, "\n"); at >= 0 {
		lastLine = lastLine[at+1:]
	}
	result, err := tx.Exec(`UPDATE plugin_runtime SET last_log=?, last_log_at=?, updated_at=datetime('now') WHERE name=?`, lastLine, timestamp, name)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return fmt.Errorf("plugin runtime %q does not exist", name)
	}
	lines := strings.Split(message, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	for _, line := range lines {
		if _, err := tx.Exec(`INSERT INTO plugin_logs(plugin, message, created_at) VALUES(?,?,?)`, name, line, timestamp); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
DELETE FROM plugin_logs
WHERE plugin=? AND id NOT IN (
  SELECT id FROM plugin_logs WHERE plugin=? ORDER BY id DESC LIMIT ?
)`, name, name, maxLines); err != nil {
		return err
	}
	return tx.Commit()
}

func PluginLogs(q Queryer, plugin string) ([]PluginLog, error) {
	rows, err := q.Query(`SELECT id, plugin, message, created_at FROM plugin_logs WHERE plugin=? ORDER BY id`, plugin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PluginLog
	for rows.Next() {
		var log PluginLog
		var createdAt string
		if err := rows.Scan(&log.ID, &log.Plugin, &log.Message, &createdAt); err != nil {
			return nil, err
		}
		log.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		out = append(out, log)
	}
	return out, rows.Err()
}

// TrimPluginLogs applies a new per-plugin line limit immediately, so reducing
// the configured limit does not wait for each plugin to emit again.
func TrimPluginLogs(q Queryer, maxLines int) error {
	if maxLines < 1 {
		return fmt.Errorf("plugin log line limit must be positive")
	}
	_, err := q.Exec(`
DELETE FROM plugin_logs WHERE id IN (
  SELECT id FROM (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY plugin ORDER BY id DESC) AS position
    FROM plugin_logs
  ) WHERE position > ?
)`, maxLines)
	return err
}

func PluginRuntimes(q Queryer) ([]PluginRuntime, error) {
	rows, err := q.Query(`SELECT name, version, description, state, hook_count, last_event, last_run_at, last_log, last_log_at FROM plugin_runtime ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PluginRuntime
	for rows.Next() {
		var runtime PluginRuntime
		var lastRunAt, lastLogAt string
		if err := rows.Scan(&runtime.Name, &runtime.Version, &runtime.Description, &runtime.State, &runtime.HookCount,
			&runtime.LastEvent, &lastRunAt, &runtime.LastLog, &lastLogAt); err != nil {
			return nil, err
		}
		if lastRunAt != "" {
			runtime.LastRunAt, err = time.Parse(time.RFC3339Nano, lastRunAt)
			if err != nil {
				return nil, err
			}
		}
		if lastLogAt != "" {
			runtime.LastLogAt, err = time.Parse(time.RFC3339Nano, lastLogAt)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, runtime)
	}
	return out, rows.Err()
}

// PluginLogTail is the newest limit lines of a plugin's log, oldest first.
func PluginLogTail(q Queryer, plugin string, limit int) ([]PluginLog, error) {
	if limit < 1 {
		return []PluginLog{}, nil
	}
	rows, err := q.Query(`SELECT id, plugin, message, created_at FROM (
  SELECT id, plugin, message, created_at FROM plugin_logs WHERE plugin=? ORDER BY id DESC LIMIT ?
) ORDER BY id`, plugin, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PluginLog{}
	for rows.Next() {
		var log PluginLog
		var createdAt string
		if err := rows.Scan(&log.ID, &log.Plugin, &log.Message, &createdAt); err != nil {
			return nil, err
		}
		if log.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, err
		}
		out = append(out, log)
	}
	return out, rows.Err()
}

// pluginScope is the storage scope of a plugin's own values. Every plugin
// has only its own storage; the scope column keeps the older value only
// because the table's check allows it.
const pluginScope = "local"

// ErrPluginStoreFull is the error of a write that would give a plugin more
// keys than allowed.
var ErrPluginStoreFull = errors.New("plugin storage is full")

// PluginStoreGet is the stored JSON text of a plugin's key; ok is false
// when it has none.
func PluginStoreGet(q Queryer, plugin, key string) (value string, ok bool, err error) {
	err = q.QueryRow(`SELECT value FROM plugin_storage WHERE scope=? AND plugin=? AND key=?`, pluginScope, plugin, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// PluginStoreSet stores value (JSON text) under a plugin's key. A new key
// is refused with ErrPluginStoreFull when the plugin already has maxKeys
// keys.
func PluginStoreSet(d *sql.DB, plugin, key, value string, maxKeys int) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE scope=? AND plugin=? AND key=?`, pluginScope, plugin, key).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE scope=? AND plugin=?`, pluginScope, plugin).Scan(&count); err != nil {
			return err
		}
		if count >= maxKeys {
			return ErrPluginStoreFull
		}
	}
	if _, err := tx.Exec(`INSERT INTO plugin_storage(scope, plugin, key, value, updated_at)
VALUES(?,?,?,?,datetime('now')) ON CONFLICT(scope, plugin, key)
DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, pluginScope, plugin, key, value); err != nil {
		return err
	}
	return tx.Commit()
}

// PluginStoreDelete removes a plugin's key.
func PluginStoreDelete(q Queryer, plugin, key string) error {
	_, err := q.Exec(`DELETE FROM plugin_storage WHERE scope=? AND plugin=? AND key=?`, pluginScope, plugin, key)
	return err
}

// PluginStoreKeys lists a plugin's keys in order.
func PluginStoreKeys(q Queryer, plugin string) ([]string, error) {
	rows, err := q.Query(`SELECT key FROM plugin_storage WHERE scope=? AND plugin=? ORDER BY key`, pluginScope, plugin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// DeletePluginData removes a plugin's stored values, runtime record and
// log.
func DeletePluginData(d *sql.DB, plugin string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM plugin_storage WHERE plugin=?`,
		`DELETE FROM plugin_logs WHERE plugin=?`,
		`DELETE FROM plugin_runtime WHERE name=?`,
	} {
		if _, err := tx.Exec(q, plugin); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AppendOrphanPluginLog adds one line to the log of a plugin that has no
// runtime record (it was removed). A stub record in the state "removed" keeps
// the line; syncs leave it alone and removing the plugin again or installing
// it anew replaces it.
func AppendOrphanPluginLog(d *sql.DB, name, message string, at time.Time, maxLines int) error {
	if at.IsZero() {
		at = time.Now()
	}
	timestamp := at.UTC().Format(time.RFC3339Nano)
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The log belongs to a runtime record; a stub keeps the line.
	if _, err := tx.Exec(`INSERT OR IGNORE INTO plugin_runtime(name, state) VALUES(?, 'removed')`, name); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO plugin_logs(plugin, message, created_at) VALUES(?,?,?)`, name, message, timestamp); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM plugin_logs WHERE plugin=? AND id NOT IN (SELECT id FROM plugin_logs WHERE plugin=? ORDER BY id DESC LIMIT ?)`, name, name, maxLines); err != nil {
		return err
	}
	return tx.Commit()
}
