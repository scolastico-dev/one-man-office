package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PluginRuntime is the durable operator-facing status of one installed plugin.
// Only the most recent log message is retained, keeping runtime telemetry bounded
// by the installed plugin count.
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

func SetPluginRuntimeLog(q Queryer, name, message string, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	_, err := q.Exec(`UPDATE plugin_runtime SET last_log=?, last_log_at=?, updated_at=datetime('now') WHERE name=?`,
		message, at.UTC().Format(time.RFC3339Nano), name)
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
