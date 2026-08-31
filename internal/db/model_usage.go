package db

import (
	"database/sql"
	"time"
)

// ModelUsageSnapshot is the last successful usage check for a model
// profile. Rows are replaced in place so this table remains bounded by the
// configured profile count.
type ModelUsageSnapshot struct {
	Profile            string
	Provider           string
	UsedPercent        float64
	ResetAt            time.Time
	HasSession         bool
	SessionUsedPercent float64
	SessionResetAt     time.Time
	FetchedAt          time.Time
}

func UpsertModelUsageSnapshot(q Queryer, snapshot ModelUsageSnapshot) error {
	fetchedAt := snapshot.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	resetAt := ""
	if !snapshot.ResetAt.IsZero() {
		resetAt = snapshot.ResetAt.UTC().Format(time.RFC3339Nano)
	}
	sessionResetAt := ""
	if !snapshot.SessionResetAt.IsZero() {
		sessionResetAt = snapshot.SessionResetAt.UTC().Format(time.RFC3339Nano)
	}
	var sessionUsedPercent any
	if snapshot.HasSession {
		sessionUsedPercent = snapshot.SessionUsedPercent
	}
	_, err := q.Exec(`
INSERT INTO model_usage_snapshots (profile, provider, used_percent, reset_at, session_used_percent, session_reset_at, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(profile) DO UPDATE SET
  provider = excluded.provider,
  used_percent = excluded.used_percent,
  reset_at = excluded.reset_at,
  session_used_percent = excluded.session_used_percent,
  session_reset_at = excluded.session_reset_at,
  fetched_at = excluded.fetched_at`, snapshot.Profile, snapshot.Provider, snapshot.UsedPercent, resetAt, sessionUsedPercent, sessionResetAt, fetchedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func ModelUsageSnapshots(q Queryer) ([]ModelUsageSnapshot, error) {
	rows, err := q.Query(`SELECT profile, provider, used_percent, reset_at, session_used_percent, session_reset_at, fetched_at FROM model_usage_snapshots ORDER BY profile`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelUsageSnapshot
	for rows.Next() {
		var snapshot ModelUsageSnapshot
		var resetAt, sessionResetAt, fetchedAt string
		var sessionUsedPercent sql.NullFloat64
		if err := rows.Scan(&snapshot.Profile, &snapshot.Provider, &snapshot.UsedPercent, &resetAt, &sessionUsedPercent, &sessionResetAt, &fetchedAt); err != nil {
			return nil, err
		}
		if resetAt != "" {
			snapshot.ResetAt, err = time.Parse(time.RFC3339Nano, resetAt)
			if err != nil {
				return nil, err
			}
		}
		if sessionUsedPercent.Valid {
			snapshot.HasSession = true
			snapshot.SessionUsedPercent = sessionUsedPercent.Float64
			if sessionResetAt != "" {
				snapshot.SessionResetAt, err = time.Parse(time.RFC3339Nano, sessionResetAt)
				if err != nil {
					return nil, err
				}
			}
		}
		snapshot.FetchedAt, err = time.Parse(time.RFC3339Nano, fetchedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	return out, rows.Err()
}
