package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// RelayMentionsAfter projects retained user mentions in one consistent read.
// Its cursor belongs to a paired device and never consults mentions_seen.
// maxBytes bounds the encoded page including the largest receipt fields.
func (b *Board) RelayMentionsAfter(ctx context.Context, a Actor, after relayproto.Counter, limit, maxBytes int) (relayproto.NotificationPage, error) {
	out := relayproto.NotificationPage{Through: after, Notifications: []relayproto.Notification{}}
	if err := userOnly(a, "read relay notifications"); err != nil {
		return out, err
	}
	if limit == 0 {
		limit = 50
	}
	if maxBytes == 0 {
		maxBytes = relayproto.MaxNotificationPlaintext
	}
	if after > math.MaxInt64 || limit < 1 || limit > 50 || maxBytes < 1 || maxBytes > relayproto.MaxNotificationPlaintext {
		return out, api.Errorf(api.CodeValidation, "invalid relay notification bounds")
	}
	// Empty responses also carry a cursor and may acknowledge the last batch.
	empty := out
	empty.Acknowledged = make([]byte, 16)
	raw, err := relayproto.EncodeNotificationPage(empty)
	if err != nil || len(raw) > maxBytes {
		return out, api.Errorf(api.CodeValidation, "relay notification byte budget cannot hold an empty page")
	}
	err = b.read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.id, c.ticket_id, c.author_role, c.created_at, t.title, c.body`+mentionsOfUser+` AND c.id > ? ORDER BY c.id ASC LIMIT ?`, int64(after), limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			n, err := relayNotice(rows)
			if err != nil {
				return err
			}
			if len(out.Notifications) == limit {
				out.More = true
				break
			}
			candidate := out
			candidate.Notifications = append(out.Notifications, n)
			candidate.Through = n.ID
			candidate.Batch = make([]byte, 16)
			candidate.Token = make([]byte, 32)
			candidate.Acknowledged = make([]byte, 16)
			raw, err := relayproto.EncodeNotificationPage(candidate)
			if err != nil {
				return err
			}
			if len(raw) > maxBytes {
				if len(out.Notifications) == 0 {
					return api.Errorf(api.CodeValidation, "relay notification byte budget cannot hold one preview")
				}
				out.More = true
				break
			}
			out.Notifications = candidate.Notifications
			out.Through = n.ID
		}
		return rows.Err()
	})
	return out, err
}

// RelayMentionsByID refreshes exactly the ordered IDs of a persisted batch.
// Missing or no longer qualifying sources remain explicit unavailable entries.
func (b *Board) RelayMentionsByID(ctx context.Context, a Actor, ids []relayproto.Counter) (relayproto.NotificationPage, error) {
	out := relayproto.NotificationPage{Notifications: []relayproto.Notification{}}
	if err := userOnly(a, "read relay notifications"); err != nil {
		return out, err
	}
	if len(ids) == 0 || len(ids) > 50 {
		return out, api.Errorf(api.CodeValidation, "invalid relay notification ids")
	}
	var previous relayproto.Counter
	for _, id := range ids {
		if id <= previous || id > math.MaxInt64 {
			return out, api.Errorf(api.CodeValidation, "invalid relay notification ids")
		}
		previous = id
	}
	out.Through = previous
	err := b.read(ctx, func(tx *sql.Tx) error {
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = int64(id)
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.id, c.ticket_id, c.author_role, c.created_at, t.title, c.body`+mentionsOfUser+` AND c.id IN (`+placeholders(len(ids))+`) ORDER BY c.id`, args...)
		if err != nil {
			return err
		}
		found := map[relayproto.Counter]relayproto.Notification{}
		for rows.Next() {
			n, e := relayNotice(rows)
			if e != nil {
				rows.Close()
				return e
			}
			found[n.ID] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			n, ok := found[id]
			if !ok {
				n = relayproto.Notification{ID: id, Unavailable: true}
			}
			out.Notifications = append(out.Notifications, n)
		}
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+mentionsOfUser+` AND c.id > ?)`, int64(out.Through)).Scan(&out.More)
	})
	return out, err
}

func relayNotice(rows *sql.Rows) (relayproto.Notification, error) {
	var n relayproto.Notification
	var id, ticket int64
	if err := rows.Scan(&id, &ticket, &n.AuthorRole, &n.CreatedAt, &n.Title, &n.Body); err != nil {
		return n, err
	}
	n.ID = relayproto.Counter(id)
	n.Ticket = relayproto.Counter(ticket)
	n.Title, n.TitleTruncated = relayPreview(n.Title, 128)
	n.Body, n.BodyTruncated = relayPreview(n.Body, 384)
	// Validate the actual escaped wire representation, not the source length.
	_, err := json.Marshal(n)
	return n, err
}
func relayPreview(s string, max int) (string, bool) {
	// Native tool output may contain arbitrary bytes. Repair before measuring
	// the UTF-8 wire preview, so one source cannot block delivery or retries.
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= max {
		return s, false
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}

// RelayMentionsHead is the ID of the newest retained mention of the user, 0
// when there is none. A device paired now starts its cursor there.
func (b *Board) RelayMentionsHead(ctx context.Context, a Actor) (relayproto.Counter, error) {
	if err := userOnly(a, "read relay notifications"); err != nil {
		return 0, err
	}
	var head int64
	err := b.read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(c.id), 0)`+mentionsOfUser).Scan(&head)
	})
	return relayproto.Counter(head), err
}
