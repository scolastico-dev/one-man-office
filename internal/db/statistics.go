package db

import (
	"database/sql"
	"time"
)

// ModelStatistics is the latest cumulative snapshot for one runner profile.
// The table is keyed by model so periodic snapshots update rows rather than
// appending an unbounded history.
type ModelStatistics struct {
	Model         string
	AgentsStarted int
	Active        time.Duration
	Idle          time.Duration
	CEOActive     time.Duration
	CEOIdle       time.Duration
	UpdatedAt     string
}

func UpsertOverallStatistics(d *sql.DB, rows []ModelStatistics) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, row := range rows {
		if _, err := tx.Exec(`
INSERT INTO overall_statistics (model, agents_started, active_ns, idle_ns, ceo_active_ns, ceo_idle_ns, updated_at)
VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
ON CONFLICT(model) DO UPDATE SET
  agents_started = excluded.agents_started,
  active_ns = excluded.active_ns,
  idle_ns = excluded.idle_ns,
  ceo_active_ns = excluded.ceo_active_ns,
  ceo_idle_ns = excluded.ceo_idle_ns,
  updated_at = excluded.updated_at`, row.Model, row.AgentsStarted, int64(row.Active), int64(row.Idle), int64(row.CEOActive), int64(row.CEOIdle)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func OverallStatistics(d *sql.DB) ([]ModelStatistics, error) {
	rows, err := d.Query(`SELECT model, agents_started, active_ns, idle_ns, ceo_active_ns, ceo_idle_ns, updated_at FROM overall_statistics ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelStatistics
	for rows.Next() {
		var row ModelStatistics
		var active, idle, ceoActive, ceoIdle int64
		if err := rows.Scan(&row.Model, &row.AgentsStarted, &active, &idle, &ceoActive, &ceoIdle, &row.UpdatedAt); err != nil {
			return nil, err
		}
		row.Active = time.Duration(active)
		row.Idle = time.Duration(idle)
		row.CEOActive = time.Duration(ceoActive)
		row.CEOIdle = time.Duration(ceoIdle)
		out = append(out, row)
	}
	return out, rows.Err()
}
