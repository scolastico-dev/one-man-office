package db

type Event struct {
	ID        int64
	Kind      string
	Agent     string
	JobID     int64
	Detail    string
	CreatedAt string
}

func AppendEvent(q Queryer, kind, agent string, jobID int64, detail string) error {
	_, err := q.Exec(`INSERT INTO events (kind, agent, job_id, detail) VALUES (?,?,?,?)`,
		kind, agent, jobID, detail)
	return err
}

func EventsSince(q Queryer, id int64) ([]Event, error) {
	rows, err := q.Query(
		`SELECT id, kind, agent, job_id, detail, created_at FROM events WHERE id > ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Kind, &e.Agent, &e.JobID, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func LastEventID(q Queryer) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM events`).Scan(&id)
	return id, err
}

func LastEvents(q Queryer, n int) ([]Event, error) {
	rows, err := q.Query(
		`SELECT id, kind, agent, job_id, detail, created_at FROM events ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Kind, &e.Agent, &e.JobID, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
