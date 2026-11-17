package db

// Event is one row of the append-only event log.
type Event struct {
	ID        int64
	Kind      string
	TicketID  int64
	Actor     string
	Detail    string
	CreatedAt string
}

// AppendEvent appends an event and returns its id. A ticketID of 0 stores
// NULL.
func AppendEvent(q Queryer, kind, actor string, ticketID int64, detail string) (int64, error) {
	var ticket any
	if ticketID != 0 {
		ticket = ticketID
	}
	res, err := q.Exec(`INSERT INTO events (kind, ticket_id, actor, detail) VALUES (?,?,?,?)`,
		kind, ticket, actor, detail)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const eventColumns = `id, kind, COALESCE(ticket_id, 0), actor, detail, created_at`

// EventsSince returns up to limit events with an id greater than afterID in
// ascending order. A limit of 0 or less means no limit. With streamedOnly,
// only kinds that contain a dot are returned.
func EventsSince(q Queryer, afterID int64, limit int, streamedOnly bool) ([]Event, error) {
	query := `SELECT ` + eventColumns + ` FROM events WHERE id > ?`
	if streamedOnly {
		query += ` AND instr(kind, '.') > 0`
	}
	query += ` ORDER BY id`
	args := []any{afterID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	return queryEvents(q, query, args...)
}

// LastEventID returns the highest event id, or 0 when the log is empty.
func LastEventID(q Queryer) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM events`).Scan(&id)
	return id, err
}

// AllEvents returns the complete event history, newest first. Incremental
// consumers should use EventsSince.
func AllEvents(q Queryer) ([]Event, error) {
	return queryEvents(q, `SELECT `+eventColumns+` FROM events ORDER BY id DESC`)
}

func queryEvents(q Queryer, query string, args ...any) ([]Event, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Kind, &e.TicketID, &e.Actor, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
