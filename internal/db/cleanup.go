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
}

type CleanupResult struct {
	Messages int64
	Jobs     int64
}

// Cleanup removes history that is no longer live orchestration state. Unread
// mail, non-terminal jobs, parent jobs with retained children, and jobs still
// attached to living agents are always preserved.
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
	if err := tx.Commit(); err != nil {
		return CleanupResult{}, err
	}
	return out, nil
}
