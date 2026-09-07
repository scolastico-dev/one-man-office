package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PluginRuntime is the durable operator-facing status of one installed plugin.
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
		if _, err := tx.Exec(`DELETE FROM plugin_runtime`); err != nil {
			return err
		}
	} else {
		placeholders := make([]string, len(runtimes))
		args := make([]any, len(runtimes))
		for i, runtime := range runtimes {
			placeholders[i] = "?"
			args[i] = runtime.Name
		}
		if _, err := tx.Exec(`DELETE FROM plugin_runtime WHERE name NOT IN (`+strings.Join(placeholders, ",")+`)`, args...); err != nil {
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
