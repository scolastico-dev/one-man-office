package board

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// ThreadKinds are the event kinds that make up a ticket's thread. The events
// that only repeat current values (ticket.updated, ticket.reordered,
// column.renumbered, agent.updated) are left out.
var ThreadKinds = []string{
	api.KindTicketCreated,
	api.KindTicketMoved,
	api.KindCommentCreated,
	api.KindQuestionAnswered,
	api.KindReviewVerdict,
	api.KindDescriptionChanged,
	api.KindDependencyAdded,
	api.KindDependencyRemoved,
	api.KindAgentCreated,
	api.KindAgentEnded,
	api.KindTicketRetried,
}

const (
	// MaxThreadPage is the most events one thread page holds and the page
	// size when none is asked for.
	MaxThreadPage = 200
	// MaxMentionPage is the most mentions UserMentions lists and the default.
	MaxMentionPage = 50

	stateKeyMentionsSeen = "mentions_seen"
)

// TicketThread returns one page of the thread of ticket n for an actor that
// may read the ticket: the newest limit events of ThreadKinds with an id
// below before (0 = from the newest), oldest first. More is true when an
// older event exists. Pages are cut by event id, so events that arrive
// while a reader pages back never shift a page. limit is 1 to
// MaxThreadPage, 0 meaning MaxThreadPage.
func (b *Board) TicketThread(ctx context.Context, a Actor, n int64, before int64, limit int) (api.TicketThread, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return api.TicketThread{}, err
	}
	if limit == 0 {
		limit = MaxThreadPage
	}
	if limit < 0 || limit > MaxThreadPage {
		return api.TicketThread{}, api.Errorf(api.CodeValidation, "limit must be between 1 and %d", MaxThreadPage)
	}
	if before < 0 {
		return api.TicketThread{}, api.Errorf(api.CodeValidation, "before must not be negative")
	}
	if before == 0 {
		before = math.MaxInt64
	}
	out := api.TicketThread{Events: []api.Event{}}
	err := b.read(ctx, func(tx *sql.Tx) error {
		if err := checkRead(tx, a, n); err != nil {
			return err
		}
		if _, err := loadTicket(tx, n); err != nil {
			return err
		}
		args := []any{n, before}
		for _, k := range ThreadKinds {
			args = append(args, k)
		}
		args = append(args, limit+1)
		rows, err := tx.Query(`SELECT id, kind, actor, detail, created_at FROM events
			WHERE ticket_id = ? AND id < ? AND kind IN (`+placeholders(len(ThreadKinds))+`)
			ORDER BY id DESC LIMIT ?`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		var newestFirst []api.Event
		for rows.Next() {
			var e api.Event
			var actor, detail, at string
			if err := rows.Scan(&e.ID, &e.Kind, &actor, &detail, &at); err != nil {
				return err
			}
			ev, err := apiEvent(e, n, actor, detail, at)
			if err != nil {
				return err
			}
			newestFirst = append(newestFirst, ev)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(newestFirst) > limit {
			out.More = true
			newestFirst = newestFirst[:limit]
		}
		for i := len(newestFirst) - 1; i >= 0; i-- {
			out.Events = append(out.Events, newestFirst[i])
		}
		return nil
	})
	return forActor(a, out, err)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// mentionsOfUser is the shared part of the mention queries: comments that
// mention the user, written by anyone but the user.
const mentionsOfUser = ` FROM comments c
	JOIN comment_mentions m ON m.comment_id = c.id AND m.role = 'user'
	JOIN tickets t ON t.id = c.ticket_id
	WHERE c.author_role <> 'user'`

// UserMentions lists the newest comments that mention the user, newest
// first, at most limit (1 to MaxMentionPage, 0 meaning MaxMentionPage), with
// the marker of what the user has seen. Unread counts every such comment
// newer than the marker, not only those on the page. Only the user reads it.
//
// There is no index on the mentioned role: the page walks comments from the
// newest and stops after limit hits, and the unread count walks only the
// comments newer than the marker.
func (b *Board) UserMentions(ctx context.Context, a Actor, limit int) (api.Notifications, error) {
	if err := userOnly(a, "read notifications"); err != nil {
		return api.Notifications{}, err
	}
	if limit == 0 {
		limit = MaxMentionPage
	}
	if limit < 0 || limit > MaxMentionPage {
		return api.Notifications{}, api.Errorf(api.CodeValidation, "limit must be between 1 and %d", MaxMentionPage)
	}
	var out api.Notifications
	err := b.read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = notifications(tx, limit)
		return err
	})
	return out, err
}

func userOnly(a Actor, what string) error {
	if a.Role != api.RoleUser {
		return api.Errorf(api.CodeForbidden, "only the user can %s", what)
	}
	return nil
}

// notifications builds the mention list through q.
func notifications(q db.Queryer, limit int) (api.Notifications, error) {
	out := api.Notifications{Mentions: []api.MentionNotice{}}
	seen, err := mentionsSeen(q)
	if err != nil {
		return out, err
	}
	out.SeenThrough = int(seen)
	if err := q.QueryRow(`SELECT COUNT(*)`+mentionsOfUser+` AND c.id > ?`, seen).Scan(&out.Unread); err != nil {
		return out, err
	}
	type hit struct {
		id    int64
		title string
	}
	var hits []hit
	if err := func() error {
		rows, err := q.Query(`SELECT c.id, t.title`+mentionsOfUser+` ORDER BY c.id DESC LIMIT ?`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h hit
			if err := rows.Scan(&h.id, &h.title); err != nil {
				return err
			}
			hits = append(hits, h)
		}
		return rows.Err()
	}(); err != nil {
		return out, err
	}
	for _, h := range hits {
		c, err := loadComment(q, h.id)
		if err != nil {
			return out, err
		}
		out.Mentions = append(out.Mentions, api.MentionNotice{Comment: c, Title: h.title, Unread: h.id > seen})
	}
	return out, nil
}

// mentionsSeen reads the marker through q; 0 when none is stored.
func mentionsSeen(q db.Queryer) (int64, error) {
	var v string
	err := q.QueryRow(`SELECT value FROM server_state WHERE key = ?`, stateKeyMentionsSeen).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, nil
	}
	return n, nil
}

// SetMentionsSeen moves the user's seen marker forward to through, which
// must lie between 0 and the newest comment id. A value at or below the
// stored marker changes nothing and emits nothing; otherwise the marker is
// stored and notifications.seen is emitted in the same transaction. The
// result is the new notification list. Only the user may call it.
func (b *Board) SetMentionsSeen(ctx context.Context, a Actor, through int) (api.Notifications, error) {
	if err := userOnly(a, "mark notifications as seen"); err != nil {
		return api.Notifications{}, err
	}
	if through < 0 {
		return api.Notifications{}, api.Errorf(api.CodeValidation, "through must not be negative")
	}
	var out api.Notifications
	err := b.writeChanges(ctx, func(tx *sql.Tx) (bool, error) {
		var newest int64
		if err := tx.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM comments`).Scan(&newest); err != nil {
			return false, err
		}
		if int64(through) > newest {
			return false, api.Errorf(api.CodeValidation, "through %d is newer than the newest comment (%d)", through, newest)
		}
		seen, err := mentionsSeen(tx)
		if err != nil {
			return false, err
		}
		changed := int64(through) > seen
		if changed {
			if _, err := tx.Exec(`INSERT INTO server_state (key, value) VALUES (?, ?)
				ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
				stateKeyMentionsSeen, strconv.Itoa(through)); err != nil {
				return false, err
			}
			if err := emit(tx, api.KindNotificationsSeen, 0, User, api.NotificationsSeen{Through: through}); err != nil {
				return false, err
			}
		}
		out, err = notifications(tx, MaxMentionPage)
		return changed, err
	})
	return out, err
}

// SessionLogPath returns the saved log file of agent session id. Only the
// user may ask; an unknown session, or one with no saved log, is not_found.
func (b *Board) SessionLogPath(ctx context.Context, a Actor, id string) (string, error) {
	if err := userOnly(a, "read a saved agent log"); err != nil {
		return "", err
	}
	var path string
	err := b.read(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT log_path FROM agent_sessions WHERE id = ?`, id).Scan(&path)
		if errors.Is(err, sql.ErrNoRows) {
			return api.Errorf(api.CodeNotFound, "that agent session does not exist")
		}
		return err
	})
	if err == nil && path == "" {
		return "", api.Errorf(api.CodeNotFound, "that agent session has no saved log")
	}
	return path, err
}
