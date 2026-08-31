package db

import (
	"database/sql"
	"fmt"
	"time"
)

// OfficeActivityDays returns the last recorded event timestamp for each UTC
// calendar day. One representative timestamp per day lets storage retention
// count days the office actually ran without charging shutdown gaps.
func OfficeActivityDays(d *sql.DB) ([]time.Time, error) {
	rows, err := d.Query(`SELECT MAX(created_at) FROM events GROUP BY date(created_at) ORDER BY MAX(created_at)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var days []time.Time
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		stamp, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("parse event timestamp %q: %w", raw, err)
		}
		days = append(days, stamp)
	}
	return days, rows.Err()
}

type CleanupPolicy struct {
	ReadMessagesAfter time.Duration
	TerminalJobsAfter time.Duration
	StorageActiveDays int
	MaxEntries        EntryCaps
}

type EntryCaps struct {
	Agents              int
	Jobs                int
	Messages            int
	Events              int
	Incidents           int
	OverallStatistics   int
	ShutdownContexts    int
	ModelUsageSnapshots int
}

type CleanupResult struct {
	Messages   int64
	Jobs       int64
	CappedRows int64
}

// Cleanup removes history that is no longer live orchestration state. Time
// retention and row caps both preserve unread mail, open incidents, living
// agents, non-terminal jobs, parent jobs with retained children, jobs still
// attached to living agents, and event-day anchors needed by storage cleanup.
func Cleanup(d *sql.DB, policy CleanupPolicy, now time.Time) (CleanupResult, error) {
	tx, err := d.Begin()
	if err != nil {
		return CleanupResult{}, err
	}
	defer tx.Rollback()
	var out CleanupResult
	if policy.ReadMessagesAfter > 0 {
		cutoff := now.Add(-policy.ReadMessagesAfter).UTC().Format("2006-01-02 15:04:05")
		result, err := tx.Exec(`DELETE FROM messages WHERE read_at IS NOT NULL AND read_at < ?`, cutoff)
		if err != nil {
			return CleanupResult{}, err
		}
		out.Messages, _ = result.RowsAffected()
	}
	if policy.TerminalJobsAfter > 0 {
		cutoff := now.Add(-policy.TerminalJobsAfter).UTC().Format("2006-01-02 15:04:05")
		result, err := tx.Exec(`
DELETE FROM jobs
WHERE state IN ('done','failed','cancelled')
  AND updated_at < ?
  AND NOT EXISTS (SELECT 1 FROM jobs child WHERE child.parent_job = jobs.id)
  AND NOT EXISTS (
    SELECT 1 FROM agents
    WHERE agents.job_id = jobs.id
      AND agents.state IN ('spawning','working','waiting')
  )`, cutoff)
		if err != nil {
			return CleanupResult{}, err
		}
		out.Jobs, _ = result.RowsAffected()
	}
	caps := policy.MaxEntries
	for _, spec := range []struct {
		table, key, order, where string
		max                      int
	}{
		{"agents", "name", "created_at, name", `state NOT IN ('spawning','working','waiting')`, caps.Agents},
		{"messages", "id", "created_at, id", `read_at IS NOT NULL`, caps.Messages},
		{"incidents", "id", "COALESCE(resolved_at, created_at), id", `state != 'open'`, caps.Incidents},
		{"overall_statistics", "model", "updated_at, model", "", caps.OverallStatistics},
		{"shutdown_contexts", "id", "created_at, id", "", caps.ShutdownContexts},
		{"model_usage_snapshots", "profile", "fetched_at, profile", "", caps.ModelUsageSnapshots},
	} {
		removed, err := trimOldest(tx, spec.table, spec.key, spec.order, spec.where, spec.max)
		if err != nil {
			return CleanupResult{}, err
		}
		out.CappedRows += removed
	}
	if caps.Jobs > 0 {
		for {
			removed, err := trimOldest(tx, "jobs", "id", "updated_at, id", `
state IN ('done','failed','cancelled')
AND NOT EXISTS (SELECT 1 FROM jobs child WHERE child.parent_job = jobs.id)
AND NOT EXISTS (
  SELECT 1 FROM agents
  WHERE agents.job_id = jobs.id
    AND agents.state IN ('spawning','working','waiting')
)`, caps.Jobs)
			if err != nil {
				return CleanupResult{}, err
			}
			out.CappedRows += removed
			if removed == 0 {
				break
			}
			var count int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&count); err != nil {
				return CleanupResult{}, err
			}
			if count <= caps.Jobs {
				break
			}
		}
	}
	removed, err := trimEvents(tx, caps.Events, policy.StorageActiveDays)
	if err != nil {
		return CleanupResult{}, err
	}
	out.CappedRows += removed
	if err := tx.Commit(); err != nil {
		return CleanupResult{}, err
	}
	return out, nil
}

func trimOldest(tx *sql.Tx, table, key, order, where string, maxEntries int) (int64, error) {
	if maxEntries <= 0 {
		return 0, nil
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		return 0, err
	}
	excess := count - maxEntries
	if excess <= 0 {
		return 0, nil
	}
	predicate := ""
	if where != "" {
		predicate = " WHERE " + where
	}
	query := `DELETE FROM ` + table + ` WHERE ` + key + ` IN (` +
		`SELECT ` + key + ` FROM ` + table + predicate + ` ORDER BY ` + order + ` LIMIT ?)`
	result, err := tx.Exec(query, excess)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func trimEvents(tx *sql.Tx, maxEntries, storageActiveDays int) (int64, error) {
	if maxEntries <= 0 {
		return 0, nil
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		return 0, err
	}
	excess := count - maxEntries
	if excess <= 0 {
		return 0, nil
	}
	result, err := tx.Exec(`
DELETE FROM events
WHERE id IN (
  SELECT id FROM events
  WHERE id NOT IN (
    SELECT MAX(id) FROM events
    GROUP BY date(created_at)
    ORDER BY date(created_at) DESC
    LIMIT ?
  )
  ORDER BY id
  LIMIT ?
)`, storageActiveDays, excess)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
