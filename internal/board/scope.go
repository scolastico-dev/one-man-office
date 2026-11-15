package board

import (
	"context"
	"database/sql"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// readRule and commentRule name the read and write scope of each role.
const (
	readRule = "the user, the CEO and product managers read every ticket; a developer or reviewer reads its own ticket, " +
		"that ticket's story and the tickets it depends on, a story's reviewer also the story's tasks"
	commentRule = "the user and the CEO comment on every ticket, a product manager on its story and the story's tasks, " +
		"a developer or reviewer only on its own ticket"
)

// readScope returns the SQL condition on tickets t that limits what a may
// read, with its arguments. all is true when a reads every ticket.
func readScope(a Actor) (cond string, args []any, all bool) {
	switch a.Role {
	case api.RoleUser, api.RoleCEO, api.RolePM, api.RoleSystem:
		return "", nil, true
	case api.RoleDeveloper, api.RoleReviewer:
		if a.Ticket == 0 {
			break
		}
		cond = `(t.id = ? OR t.id = (SELECT story_id FROM tickets WHERE id = ?)
			OR t.id IN (SELECT depends_on_id FROM dependencies WHERE ticket_id = ?)`
		args = []any{a.Ticket, a.Ticket, a.Ticket}
		if a.Role == api.RoleReviewer {
			// Only a story has tasks, so this adds nothing for a task.
			cond += ` OR t.story_id = ?`
			args = append(args, a.Ticket)
		}
		return cond + `)`, args, false
	}
	return `0 = 1`, nil, false
}

// canRead reports whether a may read ticket n. For an actor with a limited
// scope a ticket that does not exist is simply not readable, so the answer
// does not reveal which tickets exist.
func canRead(q db.Queryer, a Actor, n int64) (bool, error) {
	cond, args, all := readScope(a)
	if all {
		return true, nil
	}
	var ok bool
	err := q.QueryRow(`SELECT EXISTS (SELECT 1 FROM tickets t WHERE t.id = ? AND `+cond+`)`,
		append([]any{n}, args...)...).Scan(&ok)
	return ok, err
}

// checkRead is canRead as an api forbidden error. Every method that takes a
// ticket number calls it before it loads the ticket, so a developer or
// reviewer cannot tell a missing ticket from one outside its scope, on reads
// and on writes alike.
func checkRead(q db.Queryer, a Actor, n int64) error {
	ok, err := canRead(q, a, n)
	if err != nil {
		return err
	}
	if !ok {
		return api.Errorf(api.CodeForbidden, "a %s cannot read #%d: %s", a.Role, n, readRule)
	}
	return nil
}

// canComment returns nil when a may comment on ticket n, api forbidden when
// not. A developer or reviewer asking about another ticket is refused
// whether or not that ticket exists.
func canComment(q db.Queryer, a Actor, n int64) error {
	switch a.Role {
	case api.RoleUser, api.RoleCEO, api.RoleSystem:
		return nil
	case api.RolePM:
		t, err := loadTicket(q, n)
		if err != nil {
			return err
		}
		if a.Ticket != 0 && (t.ID == a.Ticket || (t.inStory() && t.StoryID == a.Ticket)) {
			return nil
		}
	case api.RoleDeveloper, api.RoleReviewer:
		if a.Ticket != 0 && n == a.Ticket {
			return nil
		}
	}
	return api.Errorf(api.CodeForbidden, "a %s cannot comment on #%d: %s", a.Role, n, commentRule)
}

// TicketSessions lists every agent session of ticket n, newest first, for
// an actor that may read the ticket.
func (b *Board) TicketSessions(ctx context.Context, a Actor, n int64) ([]api.AgentSession, error) {
	var out []api.AgentSession
	err := b.read(ctx, func(tx *sql.Tx) error {
		if err := checkRead(tx, a, n); err != nil {
			return err
		}
		if _, err := loadTicket(tx, n); err != nil {
			return err
		}
		var err error
		out, err = sessions(tx, n, false)
		return err
	})
	return out, err
}

// TicketEvents returns the streamed events of ticket n, oldest first: the
// ticket's history for its comment thread.
func (b *Board) TicketEvents(ctx context.Context, a Actor, n int64) ([]api.Event, error) {
	out := []api.Event{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		if err := checkRead(tx, a, n); err != nil {
			return err
		}
		if _, err := loadTicket(tx, n); err != nil {
			return err
		}
		return each(tx, `SELECT id, kind, actor, detail, created_at FROM events
			WHERE ticket_id = ? AND instr(kind, '.') > 0 ORDER BY id`, n, func(rows *sql.Rows) error {
			var e api.Event
			var actor, detail, at string
			if err := rows.Scan(&e.ID, &e.Kind, &actor, &detail, &at); err != nil {
				return err
			}
			ev, err := apiEvent(e, n, actor, detail, at)
			if err != nil {
				return err
			}
			out = append(out, ev)
			return nil
		})
	})
	return out, err
}
