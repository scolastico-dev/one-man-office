package db

import "database/sql"

// Incident is one smoke-alarm finding and its current resolution state.
type Incident struct {
	ID         int64
	Agent      string
	Class      string
	Detail     string
	State      string
	CreatedAt  string
	ResolvedAt sql.NullString
}

// AllIncidents returns the complete incident history, newest first.
func AllIncidents(q Queryer) ([]Incident, error) {
	rows, err := q.Query(
		`SELECT id, agent, class, detail, state, created_at, resolved_at FROM incidents ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		var incident Incident
		if err := rows.Scan(&incident.ID, &incident.Agent, &incident.Class, &incident.Detail,
			&incident.State, &incident.CreatedAt, &incident.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, incident)
	}
	return out, rows.Err()
}

// CloseOpenIncidentsForRestart resolves findings whose diagnosing firefighter
// no longer exists after an office restart. A new smoke round may file a fresh
// incident if the underlying problem remains.
func CloseOpenIncidentsForRestart(q Queryer) (int64, error) {
	result, err := q.Exec(`UPDATE incidents
		SET state = 'resolved', resolved_at = datetime('now')
		WHERE state = 'open'`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
