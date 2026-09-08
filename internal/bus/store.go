package bus

import (
	"database/sql"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/config"
)

type Priority string

const SystemSender = "omo"

const (
	PrioLow    Priority = "low"
	PrioNormal Priority = "normal"
	PrioHigh   Priority = "high"
	PrioUrgent Priority = "urgent"
)

func ValidPriority(p Priority) bool {
	switch p {
	case PrioLow, PrioNormal, PrioHigh, PrioUrgent:
		return true
	}
	return false
}

type Message struct {
	ID        int64
	From      string
	To        string
	Subject   string
	Body      string
	Priority  Priority
	CreatedAt string
	Read      bool
}

type Store struct {
	DB  *sql.DB
	Dir Directory
	// Notify is called after commit for recipients whose inbox changed from
	// empty to unread. Further mail stays durable without repeatedly waking or
	// interrupting a recipient that already knows it has mail.
	Notify func(recipients []string) // may be nil
}

// Send validates routing, resolves the target to concrete recipients and
// inserts one row per recipient. Broadcast ("" target) excludes the sender.
func (s *Store) Send(from, target, subject, body string, prio Priority) ([]int64, error) {
	if !ValidPriority(prio) {
		return nil, fmt.Errorf("invalid priority %q", prio)
	}
	if err := Allowed(s.Dir, from, target); err != nil {
		return nil, err
	}
	var recipients []string
	switch {
	case target == "":
		for _, a := range s.Dir.LivingAgents() {
			if a != from {
				recipients = append(recipients, a)
			}
		}
	case target == "user":
		recipients = []string{"user"}
	case config.IsRole(target):
		for _, a := range s.Dir.AgentsByRole(target) {
			if a != from {
				recipients = append(recipients, a)
			}
		}
		if len(recipients) == 0 {
			return nil, fmt.Errorf("no living agent with role %q", target)
		}
	default:
		if _, ok := s.Dir.Role(target); !ok {
			return nil, fmt.Errorf("unknown recipient %q", target)
		}
		recipients = []string{target}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var ids []int64
	var notifyRecipients []string
	for _, r := range recipients {
		if s.Notify != nil {
			var unread int
			if err := tx.QueryRow(
				`SELECT COUNT(*) FROM messages WHERE to_target = ? AND read_at IS NULL`, r,
			).Scan(&unread); err != nil {
				return nil, err
			}
			if unread == 0 {
				notifyRecipients = append(notifyRecipients, r)
			}
		}
		res, err := tx.Exec(
			`INSERT INTO messages (from_agent, to_target, subject, body, priority) VALUES (?,?,?,?,?)`,
			from, r, subject, body, string(prio))
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if s.Notify != nil && len(notifyRecipients) > 0 {
		s.Notify(notifyRecipients)
	}
	return ids, nil
}

const prioOrder = `CASE priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END`

func (s *Store) Inbox(agent string) ([]Message, error) {
	return s.messages(agent, true)
}

// All returns read and unread messages for history views, newest first.
func (s *Store) All(agent string) ([]Message, error) { return s.messages(agent, false) }

// History returns all office mail for the user's read-only overview. Unread
// user mail is always first; each group is otherwise newest first.
func (s *Store) History() ([]Message, error) {
	rows, err := s.DB.Query(
		`SELECT id, from_agent, to_target, subject, body, priority, created_at, read_at FROM messages
		 ORDER BY CASE WHEN to_target = 'user' AND read_at IS NULL THEN 0 ELSE 1 END, id DESC`)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

func (s *Store) messages(agent string, unreadOnly bool) ([]Message, error) {
	where := `WHERE to_target = ?`
	order := ` ORDER BY id DESC`
	if unreadOnly {
		where += ` AND read_at IS NULL`
		order = ` ORDER BY ` + prioOrder + `, id`
	}
	rows, err := s.DB.Query(
		`SELECT id, from_agent, to_target, subject, body, priority, created_at, read_at FROM messages `+
			where+order, agent)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var readAt sql.NullString
		if err := rows.Scan(&m.ID, &m.From, &m.To, &m.Subject, &m.Body, &m.Priority, &m.CreatedAt, &readAt); err != nil {
			return nil, err
		}
		m.Read = readAt.Valid
		out = append(out, m)
	}
	return out, rows.Err()
}

// Read returns the message and marks it read. Only the recipient may read it.
func (s *Store) Read(agent string, id int64) (*Message, error) {
	var m Message
	var readAt sql.NullString
	err := s.DB.QueryRow(
		`SELECT id, from_agent, to_target, subject, body, priority, created_at, read_at
		 FROM messages WHERE id = ? AND to_target = ?`, id, agent).
		Scan(&m.ID, &m.From, &m.To, &m.Subject, &m.Body, &m.Priority, &m.CreatedAt, &readAt)
	if err != nil {
		return nil, fmt.Errorf("message %d not found for %s", id, agent)
	}
	m.Read = readAt.Valid
	if !m.Read {
		if _, err := s.DB.Exec(`UPDATE messages SET read_at = datetime('now') WHERE id = ?`, id); err != nil {
			return nil, err
		}
		m.Read = true
	}
	return &m, nil
}

func (s *Store) UnreadCount(agent string) (int, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE to_target = ? AND read_at IS NULL`, agent).Scan(&n)
	return n, err
}
