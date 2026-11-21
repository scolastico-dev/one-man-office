package board

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestUINotificationFeedCommentsAndReplay(t *testing.T) {
	ctx := context.Background()
	b, _ := newTestBoard(t)
	story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "Story", Projects: []string{"api"}})
	pm := agent(t, b, api.RolePM, int64(story.Number))
	initial, err := b.UserNotificationFeed(ctx, User, "", 0)
	if err != nil || len(initial.Notifications) != 0 || initial.Notifications == nil || initial.HasMore {
		t.Fatalf("initial: %+v %v", initial, err)
	}
	comment := func(a Actor, req api.CommentRequest) api.Comment {
		t.Helper()
		r, e := b.Comment(ctx, a, int64(story.Number), req)
		if e != nil {
			t.Fatal(e)
		}
		return r.Comment
	}
	mention := comment(pm, api.CommentRequest{Body: "@user mention"})
	question := comment(pm, api.CommentRequest{Body: "@user which option?", Question: true, To: "user"})
	comment(User, api.CommentRequest{Body: "@user myself"})
	comment(pm, api.CommentRequest{Body: "ordinary"})
	all, err := b.UserNotificationFeed(ctx, User, initial.NextCursor, 0)
	if err != nil || all.HasMore || len(all.Notifications) != 2 {
		t.Fatalf("all: %+v %v", all, err)
	}
	for i, want := range []struct {
		kind    string
		comment api.Comment
	}{{"mention", mention}, {"question", question}} {
		got := all.Notifications[i]
		if got.Kind != want.kind || got.Title == "" || got.Body != want.comment.Body || got.Destination != fmt.Sprintf("/%d?tab=comments&comment=%d", story.Number, want.comment.ID) || got.ID == "" {
			t.Fatalf("item %d: %+v", i, got)
		}
	}
	if all.Notifications[0].ID == all.Notifications[1].ID {
		t.Fatal("IDs are not unique")
	}
	// Answering, marking the bell and renaming the ticket never rewrite a committed delivery.
	comment(User, api.CommentRequest{Body: "option A", ReplyTo: &question.ID})
	if _, err = b.SetMentionsSeen(ctx, User, question.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Update(ctx, User, int64(story.Number), api.UpdateTicket{Title: api.Some("renamed")}); err != nil {
		t.Fatal(err)
	}
	retry, err := b.UserNotificationFeed(ctx, User, initial.NextCursor, 0)
	if err != nil || !reflect.DeepEqual(retry.Notifications, all.Notifications) {
		t.Fatalf("replay changed: %+v %v", retry, err)
	}
	bell, err := b.UserMentions(ctx, User, 0)
	if err != nil || bell.SeenThrough != question.ID {
		t.Fatalf("bell: %+v %v", bell, err)
	}
	// Pages are bounded by scanned events, including pages containing no notices.
	cursor := initial.NextCursor
	var paged []api.UINotification
	sawEmpty := false
	for {
		pg, e := b.UserNotificationFeed(ctx, User, cursor, 1)
		if e != nil {
			t.Fatal(e)
		}
		if pg.Notifications == nil {
			t.Fatal("null list")
		}
		if len(pg.Notifications) == 0 && pg.HasMore {
			sawEmpty = true
		}
		paged = append(paged, pg.Notifications...)
		if pg.HasMore && pg.NextCursor == cursor {
			t.Fatal("cursor stalled")
		}
		cursor = pg.NextCursor
		if !pg.HasMore {
			break
		}
	}
	if !sawEmpty || !reflect.DeepEqual(paged, all.Notifications) {
		t.Fatalf("paged: %+v, empty=%v", paged, sawEmpty)
	}
	empty, err := b.UserNotificationFeed(ctx, User, cursor, 1)
	if err != nil || len(empty.Notifications) != 0 || empty.HasMore || empty.NextCursor != cursor {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	for _, a := range []Actor{pm, System} {
		_, err = b.UserNotificationFeed(ctx, a, cursor, 0)
		wantCode(t, err, api.CodeForbidden)
	}
}

func TestUINotificationFeedWaitingTransitions(t *testing.T) {
	ctx := context.Background()
	b, _ := newTestBoard(t)
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "Task", Projects: []string{"api"}, Review: api.ReviewHuman})
	dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
	initial, err := b.UserNotificationFeed(ctx, User, "0", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.SetAttention(ctx, int64(tk.Number), "help needed"); err != nil {
		t.Fatal(err)
	}
	// An unrelated summary update must not produce another attention notification.
	if _, err = b.Update(ctx, User, int64(tk.Number), api.UpdateTicket{Title: api.Some("renamed")}); err != nil {
		t.Fatal(err)
	}
	if err = b.SetAttention(ctx, int64(tk.Number), ""); err != nil {
		t.Fatal(err)
	}
	if err = b.SetAttention(ctx, int64(tk.Number), "help again"); err != nil {
		t.Fatal(err)
	}
	if err = b.SetAttention(ctx, int64(tk.Number), ""); err != nil {
		t.Fatal(err)
	}
	force(t, b, tk.Number, api.ColumnInProgress, "")
	if _, err = b.Done(ctx, dev, api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Comment(ctx, User, int64(tk.Number), api.CommentRequest{Body: "ordinary"}); err != nil {
		t.Fatal(err)
	}
	got, err := b.UserNotificationFeed(ctx, User, initial.NextCursor, 0)
	if err != nil || len(got.Notifications) != 5 {
		t.Fatalf("transitions: %+v %v", got, err)
	}
	for i, kind := range []string{"attention", "mention", "attention", "mention", "review"} {
		if got.Notifications[i].Kind != kind || (kind != "mention" && got.Notifications[i].Destination != fmt.Sprintf("/%d", tk.Number)) || got.Notifications[i].Title == "" || got.Notifications[i].Body == "" {
			t.Fatalf("item %d: %+v", i, got.Notifications[i])
		}
	}
	// Replay across page boundaries must compare with the preceding committed summary.
	cursor := initial.NextCursor
	var items []api.UINotification
	for {
		pg, e := b.UserNotificationFeed(ctx, User, cursor, 1)
		if e != nil {
			t.Fatal(e)
		}
		items = append(items, pg.Notifications...)
		cursor = pg.NextCursor
		if !pg.HasMore {
			break
		}
	}
	if !reflect.DeepEqual(items, got.Notifications) {
		t.Fatalf("paged transitions: %+v != %+v", items, got.Notifications)
	}
}

func TestUINotificationFeedCursors(t *testing.T) {
	ctx := context.Background()
	b, _ := newTestBoard(t)
	for _, cursor := range []string{"-1", "01", "1.0", "+1", "x", "9223372036854775808"} {
		_, err := b.UserNotificationFeed(ctx, User, cursor, 0)
		wantCode(t, err, api.CodeBadRequest)
	}
	for _, limit := range []int{-1, 201} {
		_, err := b.UserNotificationFeed(ctx, User, "0", limit)
		wantCode(t, err, api.CodeValidation)
	}
	_, err := b.UserNotificationFeed(ctx, User, "1", 0)
	wantCode(t, err, api.CodeConflict)
	ids := []int64{}
	for i := 0; i < 5; i++ {
		id, e := db.AppendEvent(b.db, "unrelated", "{}", 0, "{}")
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	// Retention drops a prefix. Never silently treat a stale cursor as caught up.
	if _, err = b.db.Exec(`DELETE FROM events WHERE id <= ?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"0", strconv.FormatInt(ids[0], 10), "999"} {
		_, err = b.UserNotificationFeed(ctx, User, cursor, 0)
		wantCode(t, err, api.CodeConflict)
		e := err.(*api.Error)
		want := api.UINotificationCursorReset{ResetCursor: strconv.FormatInt(ids[1], 10), LatestCursor: strconv.FormatInt(ids[4], 10)}
		if !reflect.DeepEqual(e.Details, want) {
			t.Fatalf("recovery: %+v, want %+v", e.Details, want)
		}
	}
	recovered, err := b.UserNotificationFeed(ctx, User, strconv.FormatInt(ids[1], 10), 1)
	if err != nil || !recovered.HasMore || recovered.NextCursor != strconv.FormatInt(ids[2], 10) {
		t.Fatalf("recovered: %+v %v", recovered, err)
	}
	// An interior gap also invalidates a previously-issued cursor.
	if _, err = b.db.Exec(`DELETE FROM events WHERE id = ?`, ids[3]); err != nil {
		t.Fatal(err)
	}
	_, err = b.UserNotificationFeed(ctx, User, strconv.FormatInt(ids[3], 10), 0)
	wantCode(t, err, api.CodeConflict)
	// Reads create no queue, cursor state or events.
	before, _ := db.LastEventID(b.db)
	if _, err = b.UserNotificationFeed(ctx, User, strconv.FormatInt(ids[4], 10), 0); err != nil {
		t.Fatal(err)
	}
	after, _ := db.LastEventID(b.db)
	if before != after {
		t.Fatal("read wrote events")
	}
	var states int
	if err = b.read(ctx, func(tx *sql.Tx) error { return tx.QueryRow(`SELECT COUNT(*) FROM server_state`).Scan(&states) }); err != nil || states != 0 {
		t.Fatalf("stored UI state: %d %v", states, err)
	}
}

func TestUINotificationFeedFullyRetainedAway(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	id, err := db.AppendEvent(b.db, "unrelated", "{}", 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.db.Exec(`DELETE FROM events`); err != nil {
		t.Fatal(err)
	}
	_, err = b.UserNotificationFeed(ctx, User, "0", 0)
	wantCode(t, err, api.CodeConflict)
	e := err.(*api.Error)
	want := api.UINotificationCursorReset{ResetCursor: strconv.FormatInt(id, 10), LatestCursor: strconv.FormatInt(id, 10)}
	if !reflect.DeepEqual(e.Details, want) {
		t.Fatalf("lost all history: %+v", e.Details)
	}
	empty, err := b.UserNotificationFeed(ctx, User, want.ResetCursor, 0)
	if err != nil || empty.NextCursor != want.ResetCursor || empty.HasMore || len(empty.Notifications) != 0 {
		t.Fatalf("reset: %+v %v", empty, err)
	}
	next, err := db.AppendEvent(b.db, "unrelated", "{}", 0, "{}")
	if err != nil {
		t.Fatal(err)
	}
	pg, err := b.UserNotificationFeed(ctx, User, want.ResetCursor, 0)
	if err != nil || pg.NextCursor != strconv.FormatInt(next, 10) || pg.HasMore {
		t.Fatalf("new history: %+v %v", pg, err)
	}
}
