package board

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// NewSession describes an agent session the engine is about to start. Ticket
// is 0 for the CEO and required for every other role.
type NewSession struct {
	Role    api.Role
	Ticket  int64
	Project string
	Name    string
	Model   string
	Workdir string
	LogPath string
}

// sessionRow is one row of agent_sessions.
type sessionRow struct {
	ID        string
	Role      api.Role
	Ticket    int64 // 0 = none (CEO)
	Project   string
	Name      string
	Model     string
	State     api.SessionState
	Workdir   string
	EndReason string
	StartedAt string
	EndedAt   sql.NullString
}

const sessionSelect = `SELECT id, role, COALESCE(ticket_id, 0), project_id, name, model, state, workdir,
	end_reason, started_at, ended_at FROM agent_sessions`

func scanSession(row interface{ Scan(...any) error }) (sessionRow, error) {
	var s sessionRow
	err := row.Scan(&s.ID, &s.Role, &s.Ticket, &s.Project, &s.Name, &s.Model, &s.State, &s.Workdir,
		&s.EndReason, &s.StartedAt, &s.EndedAt)
	return s, err
}

func (s sessionRow) api() (api.AgentSession, error) {
	out := api.AgentSession{
		ID:        s.ID,
		Role:      s.Role,
		Project:   s.Project,
		Name:      s.Name,
		Model:     s.Model,
		State:     s.State,
		Workdir:   s.Workdir,
		EndReason: s.EndReason,
	}
	if s.Ticket != 0 {
		v := int(s.Ticket)
		out.Ticket = &v
	}
	var err error
	if out.StartedAt, err = parseTime(s.StartedAt); err != nil {
		return out, err
	}
	out.EndedAt, err = parseNullTime(s.EndedAt)
	return out, err
}

func (s sessionRow) live() bool { return s.State != api.SessionEnded }

// loadSession reads one session; a missing session is api not_found.
func loadSession(q db.Queryer, id string) (sessionRow, error) {
	s, err := scanSession(q.QueryRow(sessionSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return s, api.Errorf(api.CodeNotFound, "agent session %s does not exist", id)
	}
	return s, err
}

// newSessionID returns 128 random bits as 32 hex characters.
func newSessionID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// checkSessionBinding enforces which ticket a session of a role may be bound
// to: the CEO to none, a pm to a story, a developer to a task, a reviewer to
// any ticket and, when given, one of its projects.
func checkSessionBinding(q db.Queryer, s NewSession) error {
	switch s.Role {
	case api.RoleCEO:
		if s.Ticket != 0 {
			return api.Errorf(api.CodeValidation, "a ceo session is not bound to a ticket")
		}
		return nil
	case api.RolePM, api.RoleDeveloper, api.RoleReviewer:
	default:
		return api.Errorf(api.CodeValidation, "agent sessions have the role ceo, pm, developer or reviewer, not %q", s.Role)
	}
	if s.Ticket == 0 {
		return api.Errorf(api.CodeValidation, "a %s session needs a ticket", s.Role)
	}
	t, err := loadTicket(q, s.Ticket)
	if err != nil {
		if errCode(err) == api.CodeNotFound {
			return api.Errorf(api.CodeValidation, "ticket #%d does not exist", s.Ticket)
		}
		return err
	}
	switch {
	case s.Role == api.RolePM && !t.isStory():
		return api.Errorf(api.CodeValidation, "a pm session works on a story; #%d is a task", s.Ticket)
	case s.Role == api.RoleDeveloper && t.isStory():
		return api.Errorf(api.CodeValidation, "a developer session works on a task; #%d is a story", s.Ticket)
	}
	if s.Project != "" {
		var n int
		if err := q.QueryRow(`SELECT COUNT(*) FROM ticket_projects WHERE ticket_id = ? AND project_id = ?`,
			s.Ticket, s.Project).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return api.Errorf(api.CodeValidation, "project %q is not a project of #%d", s.Project, s.Ticket)
		}
	}
	return nil
}

// CreateSession records a new agent session in state starting and emits
// agent.created.
func (b *Board) CreateSession(ctx context.Context, s NewSession) (api.AgentSession, error) {
	var out api.AgentSession
	err := b.write(ctx, func(tx *sql.Tx) error {
		if err := checkSessionBinding(tx, s); err != nil {
			return err
		}
		id, err := newSessionID()
		if err != nil {
			return err
		}
		var ticket any
		if s.Ticket != 0 {
			ticket = s.Ticket
		}
		if _, err := tx.Exec(`INSERT INTO agent_sessions
			(id, role, ticket_id, project_id, name, model, state, workdir, log_path, started_at)
			VALUES (?, ?, ?, ?, ?, ?, 'starting', ?, ?, ?)`,
			id, s.Role, ticket, s.Project, s.Name, s.Model, s.Workdir, s.LogPath, b.stamp()); err != nil {
			return err
		}
		out, err = b.sessionChanged(tx, id, api.KindAgentCreated)
		return err
	})
	return out, err
}

// SetSessionState changes a live session's state (starting, running or
// waiting) and emits agent.updated. Ending a session is EndSession.
func (b *Board) SetSessionState(ctx context.Context, id string, st api.SessionState) error {
	if !slices.Contains([]api.SessionState{api.SessionStarting, api.SessionRunning, api.SessionWaiting}, st) {
		return api.Errorf(api.CodeValidation, "session state %q cannot be set; use starting, running or waiting", st)
	}
	return b.write(ctx, func(tx *sql.Tx) error {
		s, err := loadSession(tx, id)
		if err != nil {
			return err
		}
		if !s.live() {
			return api.Errorf(api.CodeConflict, "agent session %s has ended", id)
		}
		if s.State == st {
			return nil
		}
		if _, err := tx.Exec(`UPDATE agent_sessions SET state = ? WHERE id = ?`, st, id); err != nil {
			return err
		}
		_, err = b.sessionChanged(tx, id, api.KindAgentUpdated)
		return err
	})
}

// EndSession marks a session ended with a reason and emits agent.ended.
// Ending an already ended session changes nothing.
func (b *Board) EndSession(ctx context.Context, id, reason string) error {
	return b.write(ctx, func(tx *sql.Tx) error {
		s, err := loadSession(tx, id)
		if err != nil {
			return err
		}
		if !s.live() {
			return nil
		}
		if _, err := tx.Exec(`UPDATE agent_sessions SET state = 'ended', end_reason = ?, ended_at = ? WHERE id = ?`,
			reason, b.stamp(), id); err != nil {
			return err
		}
		_, err = b.sessionChanged(tx, id, api.KindAgentEnded)
		return err
	})
}

// sessionChanged emits kind for the session and ticket.updated (derived) for
// its ticket, whose agents list changed.
func (b *Board) sessionChanged(tx *sql.Tx, id, kind string) (api.AgentSession, error) {
	s, err := loadSession(tx, id)
	if err != nil {
		return api.AgentSession{}, err
	}
	out, err := s.api()
	if err != nil {
		return out, err
	}
	if err := emit(tx, kind, s.Ticket, System, api.AgentData{Agent: out}); err != nil {
		return out, err
	}
	return out, b.touch(tx, System, s.Ticket)
}

// AgentSessions lists every agent session, newest first, optionally only
// the live ones. The user, the CEO and product managers may list them; a
// developer or reviewer lists the sessions of its own ticket with
// TicketSessions.
func (b *Board) AgentSessions(ctx context.Context, a Actor, liveOnly bool) ([]api.AgentSession, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return nil, err
	}
	switch a.Role {
	case api.RoleUser, api.RoleCEO, api.RolePM, api.RoleSystem:
	default:
		return nil, api.Errorf(api.CodeForbidden,
			"a %s cannot list every agent session: the user, the CEO and product managers list them; "+
				"a developer or reviewer lists the sessions of its own ticket", a.Role)
	}
	var out []api.AgentSession
	err := b.read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = sessions(tx, 0, liveOnly)
		return err
	})
	return out, err
}

// sessions lists the sessions of a ticket (0 = every session) through q,
// newest first; the result is empty, never nil.
func sessions(q db.Queryer, ticket int64, liveOnly bool) ([]api.AgentSession, error) {
	query := sessionSelect + ` WHERE 1 = 1`
	var args []any
	if ticket != 0 {
		query += ` AND ticket_id = ?`
		args = append(args, ticket)
	}
	if liveOnly {
		query += ` AND state <> 'ended'`
	}
	query += ` ORDER BY started_at DESC, rowid DESC`
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.AgentSession{}
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		as, err := s.api()
		if err != nil {
			return nil, err
		}
		out = append(out, as)
	}
	return out, rows.Err()
}

// ResolveSession returns the actor behind a live session. An unknown or
// ended session is api unknown_session; it never falls back to the user.
func (b *Board) ResolveSession(ctx context.Context, id string) (Actor, error) {
	var out Actor
	err := b.read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = resolveSession(tx, id)
		return err
	})
	return out, err
}

// resolveSession is ResolveSession through q.
func resolveSession(q db.Queryer, id string) (Actor, error) {
	s, err := scanSession(q.QueryRow(sessionSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !s.live()) {
		return Actor{}, api.Errorf(api.CodeUnknownSession, "agent session %q is unknown or has ended", id)
	}
	if err != nil {
		return Actor{}, err
	}
	return Actor{Role: s.Role, Session: s.ID, Ticket: s.Ticket, Project: s.Project, Name: s.Name}, nil
}

// errCode returns the api error code of err, or "".
func errCode(err error) string {
	var e *api.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// liveSessionID returns the newest live session of role that works on
// ticket t, or "" if there is none. A pm works on the ticket's story, the
// CEO on no ticket; the user and the system have no session.
func liveSessionID(q db.Queryer, t ticketRow, role api.Role) (string, error) {
	query := `SELECT id FROM agent_sessions WHERE state <> 'ended' AND role = ?`
	args := []any{role}
	switch role {
	case api.RoleCEO:
		query += ` AND ticket_id IS NULL`
	case api.RolePM:
		story := t.StoryID
		if t.isStory() {
			story = t.ID
		}
		if story == 0 {
			return "", nil
		}
		query += ` AND ticket_id = ?`
		args = append(args, story)
	case api.RoleDeveloper, api.RoleReviewer:
		query += ` AND ticket_id = ?`
		args = append(args, t.ID)
	default:
		return "", nil
	}
	var id string
	err := q.QueryRow(query+` ORDER BY started_at DESC, rowid DESC LIMIT 1`, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
