package board

import (
	"context"
	"database/sql"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// The methods in this file are the engine's view of the board. They run as
// the system: session ids are visible.

// EngineSnapshot is everything the engine plans from, read in one
// transaction so it is consistent.
type EngineSnapshot struct {
	Paused bool
	// Tickets holds every ticket that is not done, every done ticket that
	// still records a worktree, and every exported ticket whose export is
	// not merged yet, by number.
	Tickets []EngineTicket
	// Sessions holds every live agent session, oldest first.
	Sessions []EngineSession
	// LastEventID is the newest event at the time of the snapshot.
	LastEventID int64
}

// EngineTicket is one ticket as the engine sees it. Supervisor, Review and
// WhenApproved are the effective values.
type EngineTicket struct {
	ID           int64
	Type         api.TicketType
	Title        string
	Column       api.Column
	Resolution   api.Resolution
	Rank         float64
	StoryID      int64 // 0 = none
	Model        string
	Supervisor   api.Role
	ReviewStage  api.ReviewStage
	ReviewRound  int
	Review       api.Review
	WhenApproved api.WhenApproved
	MaxParallel  int // 0 = no limit
	Busy         bool
	FailedStep   api.FailedStep
	Attention    string
	// DependsOn lists the tickets this one depends on; WaitingFor those of
	// them that still block it. Waiting is len(WaitingFor) > 0.
	DependsOn    []int64
	WaitingFor   []api.Waiting
	Waiting      bool
	ExportMerged bool
	// DeleteBranch and DiscardUncommitted come from the newest cancel of a
	// cancelled ticket.
	DeleteBranch       bool
	DiscardUncommitted bool
	StartBlocked       api.StartBlocked
	// Projects in their stored order; none for a task without a project.
	Projects []EngineProject
	// Workdir is the working folder of a task without a project ("" until
	// work starts).
	Workdir string
	// AttentionOwner identifies who may replace or clear the current flag.
	AttentionOwner AttentionOwner
	// Verdicts maps a project to its reviewer's verdict in the current
	// review round.
	Verdicts map[string]string
}

// ReviewKeys lists what one review round of t collects a verdict for: each
// project of t, or, for a task without a project, the ticket itself under
// the key "" (NoProjectKey). A reviewer session works on one key, a verdict
// is stored under one, and a round ends once every key has one.
func (t EngineTicket) ReviewKeys() []string {
	ids := make([]string, len(t.Projects))
	for i, p := range t.Projects {
		ids[i] = p.ID
	}
	return reviewKeysOf(ids)
}

// NoProjectKey is the review key of a task without a project: the verdict
// of its round is given for the ticket itself.
const NoProjectKey = ""

// reviewKeysOf is ReviewKeys for a ticket with the projects ids.
func reviewKeysOf(ids []string) []string {
	if len(ids) == 0 {
		return []string{NoProjectKey}
	}
	return ids
}

// EngineProject is one project of a ticket.
type EngineProject struct {
	ID, Base, Branch, Worktree, ExportURL string
	// Start and End are the commits recorded for the project's diff (see
	// RecordCommits); "" until the ticket is first merged or exported.
	Start, End string
}

// EngineSession is one live agent session. Ticket is 0 for the CEO.
type EngineSession struct {
	ID          string
	Role        api.Role
	Ticket      int64
	Project     string
	Purpose     api.Purpose
	ReviewRound int
	State       api.SessionState
	Name        string
	Model       string
	Workdir     string
	StartedAt   time.Time
}

// EngineSnapshot reads the engine's view of the board in one transaction. It
// runs the same number of queries however many tickets and sessions exist.
func (b *Board) EngineSnapshot(ctx context.Context) (EngineSnapshot, error) {
	var out EngineSnapshot
	err := b.read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = b.engineSnapshot(tx)
		return err
	})
	return out, err
}

func (b *Board) engineSnapshot(q db.Queryer) (EngineSnapshot, error) {
	out := EngineSnapshot{Tickets: []EngineTicket{}, Sessions: []EngineSession{}}
	var err error
	if out.Paused, err = paused(q); err != nil {
		return out, err
	}
	if out.LastEventID, err = db.LastEventID(q); err != nil {
		return out, err
	}

	p := b.env.Policy()
	rows, err := q.Query(ticketSelect + ` WHERE t.col <> 'done'
		OR t.id IN (SELECT ticket_id FROM ticket_projects WHERE worktree <> '')
		OR (t.resolution = 'exported' AND t.export_merged = 0)
		ORDER BY t.id`)
	if err != nil {
		return out, err
	}
	var ids, cancelled []int64
	for rows.Next() {
		r, err := scanTicket(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Tickets = append(out.Tickets, engineTicket(p, r))
		ids = append(ids, r.ID)
		if r.Resolution == api.ResolutionCancelled {
			cancelled = append(cancelled, r.ID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	byID := make(map[int64]*EngineTicket, len(out.Tickets))
	for i := range out.Tickets {
		byID[out.Tickets[i].ID] = &out.Tickets[i]
	}

	if len(ids) > 0 {
		raw, err := jsonIDs(ids)
		if err != nil {
			return out, err
		}
		if err := each(q, `SELECT ticket_id, project_id, base_branch, branch, worktree, export_url, start_commit, end_commit FROM ticket_projects
			WHERE ticket_id`+idsIn+` ORDER BY ticket_id, position`, raw, func(rows *sql.Rows) error {
			var id int64
			var ep EngineProject
			if err := rows.Scan(&id, &ep.ID, &ep.Base, &ep.Branch, &ep.Worktree, &ep.ExportURL, &ep.Start, &ep.End); err != nil {
				return err
			}
			byID[id].Projects = append(byID[id].Projects, ep)
			return nil
		}); err != nil {
			return out, err
		}
		if err := each(q, `SELECT d.ticket_id, d.depends_on_id, o.resolution, o.export_merged
			FROM dependencies d JOIN tickets o ON o.id = d.depends_on_id
			WHERE d.ticket_id`+idsIn+` ORDER BY d.ticket_id, d.depends_on_id`, raw, func(rows *sql.Rows) error {
			var id, dep int64
			var res api.Resolution
			var merged bool
			if err := rows.Scan(&id, &dep, &res, &merged); err != nil {
				return err
			}
			t := byID[id]
			t.DependsOn = append(t.DependsOn, dep)
			if reason, waiting := waitingReason(res, merged); waiting {
				t.WaitingFor = append(t.WaitingFor, api.Waiting{Ticket: int(dep), Reason: reason})
				t.Waiting = true
			}
			return nil
		}); err != nil {
			return out, err
		}
		if err := each(q, `SELECT v.ticket_id, v.project_id, v.verdict FROM review_verdicts v
			JOIN tickets t ON t.id = v.ticket_id AND v.round = t.review_round
			WHERE v.ticket_id`+idsIn, raw, func(rows *sql.Rows) error {
			var id int64
			var project, verdict string
			if err := rows.Scan(&id, &project, &verdict); err != nil {
				return err
			}
			byID[id].Verdicts[project] = verdict
			return nil
		}); err != nil {
			return out, err
		}
	}

	// The newest cancel of each cancelled ticket; only cancelled tickets
	// that still record a worktree are in the snapshot, so this is rare.
	if len(cancelled) > 0 {
		raw, err := jsonIDs(cancelled)
		if err != nil {
			return out, err
		}
		if err := each(q, `SELECT ticket_id, COALESCE(json_extract(detail, '$.delete_branch'), 0),
			COALESCE(json_extract(detail, '$.discard_uncommitted'), 0) FROM events
			WHERE id IN (SELECT MAX(id) FROM events WHERE kind = '`+api.KindTicketMoved+`'
				AND ticket_id`+idsIn+` AND json_extract(detail, '$.action') = '`+ActionCancel+`' GROUP BY ticket_id)`,
			raw, func(rows *sql.Rows) error {
				var id int64
				var del, discard bool
				if err := rows.Scan(&id, &del, &discard); err != nil {
					return err
				}
				byID[id].DeleteBranch, byID[id].DiscardUncommitted = del, discard
				return nil
			}); err != nil {
			return out, err
		}
	}

	srows, err := q.Query(`SELECT id, role, COALESCE(ticket_id, 0), project_id, purpose, review_round, state,
		name, model, workdir, started_at FROM agent_sessions WHERE state <> 'ended' ORDER BY started_at, id`)
	if err != nil {
		return out, err
	}
	defer srows.Close()
	for srows.Next() {
		var s EngineSession
		var started string
		if err := srows.Scan(&s.ID, &s.Role, &s.Ticket, &s.Project, &s.Purpose, &s.ReviewRound, &s.State,
			&s.Name, &s.Model, &s.Workdir, &started); err != nil {
			return out, err
		}
		if s.StartedAt, err = parseTime(started); err != nil {
			return out, err
		}
		out.Sessions = append(out.Sessions, s)
	}
	return out, srows.Err()
}

// engineTicket converts a row; list fields are empty, never nil.
func engineTicket(p Policy, r ticketRow) EngineTicket {
	t := EngineTicket{
		ID:             r.ID,
		Type:           r.Type,
		Title:          r.Title,
		Column:         r.Col,
		Resolution:     r.Resolution,
		Rank:           r.Rank,
		StoryID:        r.StoryID,
		Model:          r.Model,
		Supervisor:     effectiveSupervisor(p, r),
		ReviewStage:    r.ReviewStage,
		ReviewRound:    r.ReviewRound,
		Review:         effectiveReview(p, r),
		WhenApproved:   effectiveWhenApproved(p, r),
		Busy:           r.Busy,
		FailedStep:     r.FailedStep,
		Attention:      r.Attention,
		AttentionOwner: r.AttentionOwner,
		DependsOn:      []int64{},
		WaitingFor:     []api.Waiting{},
		ExportMerged:   r.ExportMerged,
		StartBlocked:   r.StartBlocked,
		Projects:       []EngineProject{},
		Workdir:        r.Workdir,
		Verdicts:       map[string]string{},
	}
	if r.MaxParallel != nil {
		t.MaxParallel = *r.MaxParallel
	}
	return t
}

// EventsAfter returns up to limit streamed events newer than after, oldest
// first, with session ids. A limit of 0 or less means no limit.
func (b *Board) EventsAfter(ctx context.Context, after int64, limit int) ([]api.Event, error) {
	out := []api.Event{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		evs, err := db.EventsSince(tx, after, limit, true)
		if err != nil {
			return err
		}
		for _, e := range evs {
			ev, err := apiEvent(api.Event{ID: e.ID, Kind: e.Kind}, e.TicketID, e.Actor, e.Detail, e.CreatedAt)
			if err != nil {
				return err
			}
			if e.TicketID == 0 {
				ev.Ticket = nil
			}
			out = append(out, ev)
		}
		return nil
	})
	return out, err
}

// LastEventID returns the id of the newest event, or 0 when there is none.
func (b *Board) LastEventID(ctx context.Context) (int64, error) {
	var id int64
	err := b.read(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = db.LastEventID(tx)
		return err
	})
	return id, err
}

// OpenQuestion is a question comment that has no answer yet.
type OpenQuestion struct {
	Comment, Ticket int64
	To              api.Role
	AuthorRole      api.Role
	AuthorSession   string // "" for the user and the system
	CreatedAt       time.Time
}

// OpenQuestions returns every open question on every ticket, oldest first.
func (b *Board) OpenQuestions(ctx context.Context) ([]OpenQuestion, error) {
	out := []OpenQuestion{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id, ticket_id, question_to, author_role, COALESCE(author_session, ''), created_at
			FROM comments WHERE question_to <> '' AND answered_by IS NULL ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var q OpenQuestion
			var created string
			if err := rows.Scan(&q.Comment, &q.Ticket, &q.To, &q.AuthorRole, &q.AuthorSession, &created); err != nil {
				return err
			}
			if q.CreatedAt, err = parseTime(created); err != nil {
				return err
			}
			out = append(out, q)
		}
		return rows.Err()
	})
	return out, err
}

// CommentByID returns one comment with its mentions, session ids included.
func (b *Board) CommentByID(ctx context.Context, id int64) (api.Comment, error) {
	var out api.Comment
	err := b.read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = loadComment(tx, id)
		return err
	})
	return out, err
}

// LastCommentID returns the id of the newest comment, or 0 when there is
// none.
func (b *Board) LastCommentID(ctx context.Context) (int64, error) {
	var id int64
	err := b.read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM comments`).Scan(&id)
	})
	return id, err
}

// SessionCommentedAfter reports whether session wrote a comment on ticket
// newer than the comment afterComment.
func (b *Board) SessionCommentedAfter(ctx context.Context, session string, ticket, afterComment int64) (bool, error) {
	var ok bool
	err := b.read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM comments WHERE ticket_id = ? AND author_session = ? AND id > ?)`,
			ticket, session, afterComment).Scan(&ok)
	})
	return ok, err
}
