package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// MaxUINotificationPage bounds events scanned, rather than matching notices,
// so even a quiet board's backlog takes bounded work per poll.
const MaxUINotificationPage = 200

// UserNotificationFeed derives system notifications from committed events in
// one read transaction. Delivery state belongs entirely to the caller: neither
// another UI's cursor nor the browser bell's seen marker is changed or consulted.
// An empty cursor means 0. Pages can be empty while HasMore is true.
func (b *Board) UserNotificationFeed(ctx context.Context, a Actor, cursor string, limit int) (api.UINotificationFeed, error) {
	out := api.UINotificationFeed{Notifications: []api.UINotification{}}
	if err := userOnly(a, "poll UI notifications"); err != nil {
		return out, err
	}
	if cursor == "" {
		cursor = "0"
	}
	after, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || after < 0 || strconv.FormatInt(after, 10) != cursor {
		return out, api.Errorf(api.CodeBadRequest, "cursor must be a canonical non-negative decimal event ID")
	}
	if limit == 0 {
		limit = MaxUINotificationPage
	}
	if limit < 1 || limit > MaxUINotificationPage {
		return out, api.Errorf(api.CodeValidation, "limit must be between 1 and %d", MaxUINotificationPage)
	}
	out.NextCursor = cursor
	err = b.read(ctx, func(tx *sql.Tx) error {
		var oldest, latest int64
		if err := tx.QueryRow(`SELECT COALESCE((SELECT MIN(id) FROM events), 0),
			COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'events'), 0)`).Scan(&oldest, &latest); err != nil {
			return err
		}
		// AUTOINCREMENT retains the high-water mark even if all events were
		// removed. MIN(id) alone would mistake lost history for a fresh board.
		floor := latest
		if oldest > 0 {
			floor = oldest - 1
		}
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE id = ?)`, after).Scan(&exists); err != nil {
			return err
		}
		if after < floor || after > latest || (after != floor && after != latest && !exists) {
			e := api.Errorf(api.CodeConflict, "notification cursor is outside retained event history; choose a recovery cursor explicitly")
			e.Details = api.UINotificationCursorReset{ResetCursor: strconv.FormatInt(floor, 10), LatestCursor: strconv.FormatInt(latest, 10)}
			return e
		}
		events, err := db.EventsSince(tx, after, limit+1, false)
		if err != nil {
			return err
		}
		if len(events) > limit {
			out.HasMore = true
			events = events[:limit]
		}
		for _, e := range events {
			notice, err := uiNotification(tx, e)
			if err != nil {
				return err
			}
			if notice != nil {
				out.Notifications = append(out.Notifications, *notice)
			}
			out.NextCursor = strconv.FormatInt(e.ID, 10)
		}
		return nil
	})
	return out, err
}

// uiNotification uses event payloads, never today's mutable comment/ticket,
// so retries keep the same ID, content and destination after a later edit.
func uiNotification(q db.Queryer, e db.Event) (*api.UINotification, error) {
	item := api.UINotification{ID: "event:" + strconv.FormatInt(e.ID, 10)}
	switch e.Kind {
	case api.KindCommentCreated:
		var data api.CommentCreatedData
		if err := json.Unmarshal([]byte(e.Detail), &data); err != nil {
			return nil, err
		}
		c := data.Comment
		if c.Author.Role == api.RoleUser {
			return nil, nil
		}
		if c.QuestionTo == api.RoleUser && c.AnsweredBy == nil {
			item.Kind = "question"
			item.Title = fmt.Sprintf("Question for you on #%d", c.Ticket)
		} else {
			for _, m := range c.Mentions {
				if m.Role == api.RoleUser {
					item.Kind = "mention"
					break
				}
			}
			if item.Kind == "" {
				return nil, nil
			}
			item.Title = fmt.Sprintf("Mention on #%d", c.Ticket)
		}
		item.Body = c.Body
		item.Destination = fmt.Sprintf("/%d?tab=comments&comment=%d", c.Ticket, c.ID)
	case api.KindTicketCreated, api.KindTicketUpdated, api.KindTicketMoved:
		var data api.TicketCreatedData // All three payloads contain ticket.
		if err := json.Unmarshal([]byte(e.Detail), &data); err != nil {
			return nil, err
		}
		ticket := data.Ticket
		kind := uiWaitingKind(ticket)
		if kind == "" {
			return nil, nil
		}
		var previous string
		err := q.QueryRow(`SELECT detail FROM events WHERE ticket_id = ? AND id < ?
			AND kind IN (?, ?, ?) ORDER BY id DESC LIMIT 1`, e.TicketID, e.ID,
			api.KindTicketCreated, api.KindTicketUpdated, api.KindTicketMoved).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			var before api.TicketCreatedData
			if err := json.Unmarshal([]byte(previous), &before); err != nil {
				return nil, err
			}
			if uiWaitingKind(before.Ticket) == kind {
				return nil, nil
			}
		}
		item.Kind = kind
		item.Body = ticket.Title
		item.Destination = fmt.Sprintf("/%d", ticket.Number)
		if kind == "attention" {
			item.Title = fmt.Sprintf("#%d needs attention", ticket.Number)
		} else {
			item.Title = fmt.Sprintf("#%d waits for your review", ticket.Number)
		}
	default:
		return nil, nil
	}
	return &item, nil
}

// uiWaitingKind follows the bell's needs-me and category precedence. Questions
// are emitted by their comment, which has the precise text and destination.
func uiWaitingKind(t api.TicketSummary) string {
	if t.Attention != "" && t.Supervisor == api.RoleUser {
		return "attention"
	}
	if t.QuestionsForUser > 0 {
		return ""
	}
	if t.ReviewStage == api.ReviewStageHuman {
		return "review"
	}
	return ""
}
