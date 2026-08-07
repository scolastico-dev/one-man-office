package db

import (
	"database/sql"
	"time"
)

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
