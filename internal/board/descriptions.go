package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// SetDescription stores a new description version and makes it current. A
// non-zero BaseVersion must equal the current version, otherwise the edit is
// api stale_description with details current_version. Text equal to the
// current description changes nothing and returns the current version.
func (b *Board) SetDescription(ctx context.Context, a Actor, n int64, req api.DescriptionUpdate) (api.DescriptionVersion, error) {
	var out api.DescriptionVersion
	err := b.write(ctx, func(tx *sql.Tx) error {
		t, err := b.descriptionTarget(tx, a, n)
		if err != nil {
			return err
		}
		if req.BaseVersion != 0 && req.BaseVersion != t.DescriptionVersion {
			e := api.Errorf(api.CodeStaleDescription,
				"the description of #%d changed: version %d is current, the edit is based on version %d",
				n, t.DescriptionVersion, req.BaseVersion)
			e.Details = map[string]any{"current_version": t.DescriptionVersion}
			return e
		}
		if t.DescriptionVersion > 0 {
			cur, err := loadVersion(tx, n, t.DescriptionVersion, t.DescriptionVersion)
			if err != nil {
				return err
			}
			if cur.Text == req.Text {
				out = cur
				return nil
			}
		}
		out, err = b.addDescription(tx, a, t, req.Text, nil)
		return err
	})
	return out, err
}

// RestoreDescription makes an older version current again by storing its
// text as a new version that records where it came from.
func (b *Board) RestoreDescription(ctx context.Context, a Actor, n int64, version int) (api.DescriptionVersion, error) {
	var out api.DescriptionVersion
	err := b.write(ctx, func(tx *sql.Tx) error {
		t, err := b.descriptionTarget(tx, a, n)
		if err != nil {
			return err
		}
		old, err := loadVersion(tx, n, version, t.DescriptionVersion)
		if err != nil {
			return err
		}
		out, err = b.addDescription(tx, a, t, old.Text, &version)
		return err
	})
	return out, err
}

// Descriptions returns every description version of a ticket, newest first.
func (b *Board) Descriptions(ctx context.Context, a Actor, n int64) ([]api.DescriptionVersion, error) {
	out := []api.DescriptionVersion{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		t, err := loadTicket(tx, n)
		if err != nil {
			return err
		}
		rows, err := tx.Query(versionSelect+` WHERE v.ticket_id = ? ORDER BY v.number DESC`, n)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scanVersion(rows, t.DescriptionVersion)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}

// descriptionTarget loads a ticket whose description a is about to change
// and applies the edit rules: not on Done; the user always; the CEO on
// tickets it created or supervises; a pm on tasks of its story.
func (b *Board) descriptionTarget(tx *sql.Tx, a Actor, n int64) (ticketRow, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return ticketRow{}, err
	}
	t, err := loadTicket(tx, n)
	if err != nil {
		return t, err
	}
	if t.Col == api.ColumnDone {
		return t, api.Errorf(api.CodeInvalidTransition, "#%d is done; done tickets are read-only", n)
	}
	if !mayEdit(a, t) && !(a.Role == api.RoleCEO && t.Supervisor == api.RoleCEO) {
		return t, api.Errorf(api.CodeForbidden, "a %s cannot change the description of #%d: %s", a.Role, n, editRule)
	}
	return t, nil
}

// addDescription stores body as the next version of t, makes it current,
// emits description.changed and ticket.updated, and tells the agent working
// on the ticket when someone else made the change.
func (b *Board) addDescription(tx *sql.Tx, a Actor, t ticketRow, body string, restoredFrom *int) (api.DescriptionVersion, error) {
	var number int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(number), 0) + 1 FROM description_versions WHERE ticket_id = ?`,
		t.ID).Scan(&number); err != nil {
		return api.DescriptionVersion{}, err
	}
	var from any
	if restoredFrom != nil {
		from = *restoredFrom
	}
	now := b.stamp()
	if _, err := tx.Exec(`INSERT INTO description_versions
		(ticket_id, number, body, author_role, author_session, restored_from, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, t.ID, number, body, a.Role, a.sessionArg(), from, now); err != nil {
		return api.DescriptionVersion{}, err
	}
	if _, err := tx.Exec(`UPDATE tickets SET description_version = ?, updated_at = ? WHERE id = ?`,
		number, now, t.ID); err != nil {
		return api.DescriptionVersion{}, err
	}
	v, err := loadVersion(tx, t.ID, number, number)
	if err != nil {
		return v, err
	}
	if err := emit(tx, api.KindDescriptionChanged, t.ID, a, api.DescriptionChangedData{Ticket: int(t.ID), Version: v}); err != nil {
		return v, err
	}
	if err := b.emitUpdated(tx, a, t.ID, "description"); err != nil {
		return v, err
	}

	working := api.RoleDeveloper
	if t.isStory() {
		working = api.RolePM
	}
	session, err := liveSessionID(tx, t, working)
	if err != nil {
		return v, err
	}
	if session != "" && session != a.Session {
		msg := fmt.Sprintf("Description changed by %s (version %d).", a.Role, number)
		if err := b.systemComment(tx, t.ID, msg, working); err != nil {
			return v, err
		}
	}
	return v, nil
}

const versionSelect = `SELECT v.number, v.body, v.author_role, COALESCE(v.author_session, ''),
	COALESCE(s.name, ''), v.restored_from, v.created_at
	FROM description_versions v LEFT JOIN agent_sessions s ON s.id = v.author_session`

// scanVersion reads one version row; current is the ticket's version in use.
func scanVersion(row interface{ Scan(...any) error }, current int) (api.DescriptionVersion, error) {
	var v api.DescriptionVersion
	var from sql.NullInt64
	var created string
	if err := row.Scan(&v.Number, &v.Text, &v.Author.Role, &v.Author.Session, &v.Author.Name, &from, &created); err != nil {
		return v, err
	}
	if from.Valid {
		f := int(from.Int64)
		v.RestoredFrom = &f
	}
	v.Current = v.Number == current
	var err error
	v.CreatedAt, err = parseTime(created)
	return v, err
}

// loadVersion reads one version of ticket n; a missing version is api
// not_found.
func loadVersion(q db.Queryer, n int64, number, current int) (api.DescriptionVersion, error) {
	v, err := scanVersion(q.QueryRow(versionSelect+` WHERE v.ticket_id = ? AND v.number = ?`, n, number), current)
	if errors.Is(err, sql.ErrNoRows) {
		return v, api.Errorf(api.CodeNotFound, "#%d has no description version %d", n, number)
	}
	return v, err
}

// systemComment posts a comment by the system on ticket n that mentions one
// role, recording the role's live session (if any), and emits
// comment.created.
func (b *Board) systemComment(tx *sql.Tx, n int64, body string, mention api.Role) error {
	t, err := loadTicket(tx, n)
	if err != nil {
		return err
	}
	session, err := liveSessionID(tx, t, mention)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO comments (ticket_id, author_role, body, created_at) VALUES (?, 'system', ?, ?)`,
		n, body, b.stamp())
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	var sessionArg any
	if session != "" {
		sessionArg = session
	}
	if _, err := tx.Exec(`INSERT INTO comment_mentions (comment_id, role, session_id) VALUES (?, ?, ?)`,
		id, mention, sessionArg); err != nil {
		return err
	}
	c, err := loadComment(tx, id)
	if err != nil {
		return err
	}
	return emit(tx, api.KindCommentCreated, n, System, api.CommentCreatedData{Comment: c})
}

// loadComment reads one comment with its mentions.
func loadComment(q db.Queryer, id int64) (api.Comment, error) {
	var c api.Comment
	var replyTo, answeredBy sql.NullInt64
	var created string
	err := q.QueryRow(`SELECT c.id, c.ticket_id, c.author_role, COALESCE(c.author_session, ''), COALESCE(s.name, ''),
		c.body, c.reply_to, c.question_to, c.answered_by, c.created_at
		FROM comments c LEFT JOIN agent_sessions s ON s.id = c.author_session WHERE c.id = ?`, id).Scan(
		&c.ID, &c.Ticket, &c.Author.Role, &c.Author.Session, &c.Author.Name,
		&c.Body, &replyTo, &c.QuestionTo, &answeredBy, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return c, api.Errorf(api.CodeNotFound, "comment %d does not exist", id)
	}
	if err != nil {
		return c, err
	}
	if replyTo.Valid {
		v := int(replyTo.Int64)
		c.ReplyTo = &v
	}
	if answeredBy.Valid {
		v := int(answeredBy.Int64)
		c.AnsweredBy = &v
	}
	if c.CreatedAt, err = parseTime(created); err != nil {
		return c, err
	}
	c.Mentions = []api.Mention{}
	err = each(q, `SELECT role, COALESCE(session_id, '') FROM comment_mentions WHERE comment_id = ? ORDER BY rowid`,
		id, func(rows *sql.Rows) error {
			var m api.Mention
			if err := rows.Scan(&m.Role, &m.Session); err != nil {
				return err
			}
			c.Mentions = append(c.Mentions, m)
			return nil
		})
	return c, err
}
