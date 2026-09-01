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

// AllEvents returns the complete office event history, newest first. It is
// intended for history views; incremental consumers should use EventsSince.
func AllEvents(q Queryer) ([]Event, error) {
	rows, err := q.Query(
		`SELECT id, kind, agent, job_id, detail, created_at FROM events ORDER BY id DESC`)
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

func CountEvents(q Queryer) (int, error) {
	var count int
	err := q.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count)
	return count, err
}

// EventsPage returns a bounded newest-first window for interactive history
// views. Incremental consumers should continue to use EventsSince.
func EventsPage(q Queryer, offset, limit int) ([]Event, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		return nil, nil
	}
	rows, err := q.Query(
		`SELECT id, kind, agent, job_id, detail, created_at FROM events ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Kind, &event.Agent, &event.JobID, &event.Detail, &event.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}
