package board

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func countEvents(t *testing.T, b *Board, kind string) int {
	t.Helper()
	var n int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ?`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTicketThreadPagesAndKinds(t *testing.T) {
	ctx := context.Background()
	b, _ := newTestBoard(t)
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	for i := 0; i < 450; i++ {
		if _, err := b.Comment(ctx, User, 1, api.CommentRequest{Body: fmt.Sprintf("c%d", i)}); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{api.KindTicketUpdated, api.KindTicketReordered, api.KindColumnRenumbered, api.KindAgentUpdated} {
			if _, err := b.db.Exec(`INSERT INTO events (kind, ticket_id, actor, detail) VALUES (?, 1, '{}', '{}')`, k); err != nil {
				t.Fatal(err)
			}
		}
	}
	var got []api.Event
	var before int64
	pages := 0
	for {
		pg, err := b.TicketThread(ctx, User, 1, before, 0)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(pg.Events) > MaxThreadPage || len(pg.Events) == 0 {
			t.Fatalf("page %d holds %d events", pages, len(pg.Events))
		}
		for i := 1; i < len(pg.Events); i++ {
			if pg.Events[i].ID <= pg.Events[i-1].ID {
				t.Fatalf("page %d not ascending", pages)
			}
		}
		got = append(pg.Events, got...)
		if !pg.More {
			break
		}
		if len(pg.Events) != MaxThreadPage {
			t.Fatalf("page %d is short (%d) but More is set", pages, len(pg.Events))
		}
		// Events arriving between pages must not shift older pages.
		if _, err := b.Comment(ctx, User, 1, api.CommentRequest{Body: "late"}); err != nil {
			t.Fatal(err)
		}
		before = pg.Events[0].ID
	}
	if pages != 3 || len(got) != 451 {
		t.Fatalf("%d pages, %d events; want 3 pages, 451 events", pages, len(got))
	}
	for i, e := range got {
		if !strings.HasPrefix(e.Kind, "comment.") && e.Kind != api.KindTicketCreated {
			t.Fatalf("event %d has kind %s", i, e.Kind)
		}
		if i > 0 && e.ID <= got[i-1].ID {
			t.Fatal("events not ascending across pages")
		}
	}
	// A small limit and an exact fit.
	pg, err := b.TicketThread(ctx, User, 1, 0, 1)
	if err != nil || len(pg.Events) != 1 || !pg.More {
		t.Fatalf("limit 1: %+v %v", pg, err)
	}
	b2, _ := newTestBoard(t)
	mustCreate(t, b2, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	pg, err = b2.TicketThread(ctx, User, 1, 0, 1)
	if err != nil || len(pg.Events) != 1 || pg.More || pg.Events[0].Kind != api.KindTicketCreated {
		t.Fatalf("exact fit: %+v %v", pg, err)
	}
	// An empty page is an empty list, never null.
	pg, err = b2.TicketThread(ctx, User, 1, pg.Events[0].ID, 0)
	if err != nil || pg.Events == nil || len(pg.Events) != 0 || pg.More {
		t.Fatalf("empty page: %+v %v", pg, err)
	}
}

func TestTicketThreadScope(t *testing.T) {
	ctx := context.Background()
	b, _ := newTestBoard(t)
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "A", Projects: []string{"api"}})
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "B", Projects: []string{"api"}})
	dev := agent(t, b, api.RoleDeveloper, 1)
	if _, err := b.TicketThread(ctx, dev, 1, 0, 10); err != nil {
		t.Fatal(err)
	}
	_, err := b.TicketThread(ctx, dev, 2, 0, 10)
	wantCode(t, err, api.CodeForbidden)
	_, err = b.TicketThread(ctx, User, 1, 0, MaxThreadPage+1)
	wantCode(t, err, api.CodeValidation)
	_, err = b.TicketThread(ctx, User, 1, 0, -1)
	wantCode(t, err, api.CodeValidation)
	_, err = b.TicketThread(ctx, User, 1, -5, 10)
	wantCode(t, err, api.CodeValidation)
	_, err = b.TicketThread(ctx, User, 99, 0, 10)
	wantCode(t, err, api.CodeNotFound)
}

func newMentionBoard(t *testing.T) (*Board, Actor) {
	t.Helper()
	ctx := context.Background()
	b, _ := newTestBoard(t)
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "Story <b>one</b>", Projects: []string{"api"}})
	pm := agent(t, b, api.RolePM, 1)
	if _, err := b.Comment(ctx, pm, 1, api.CommentRequest{Body: "@user decide <script>x</script>"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Comment(ctx, User, 1, api.CommentRequest{Body: "@user to myself"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Comment(ctx, pm, 1, api.CommentRequest{Body: "no mention"}); err != nil {
		t.Fatal(err)
	}
	if err := b.write(ctx, func(tx *sql.Tx) error { return b.systemComment(tx, 1, "system says hi", api.RoleUser) }); err != nil {
		t.Fatal(err)
	}
	return b, pm
}

func TestUserMentions(t *testing.T) {
	ctx := context.Background()
	b, pm := newMentionBoard(t)
	n, err := b.UserMentions(ctx, User, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Mentions) != 2 || n.Unread != 2 || n.SeenThrough != 0 {
		t.Fatalf("%+v", n)
	}
	if n.Mentions[0].Comment.Author.Role != api.RoleSystem || n.Mentions[1].Comment.Author.Role != api.RolePM ||
		n.Mentions[0].Comment.ID < n.Mentions[1].Comment.ID {
		t.Fatalf("order or authors: %+v", n.Mentions)
	}
	for _, m := range n.Mentions {
		if m.Title != "Story <b>one</b>" || !m.Unread {
			t.Fatalf("%+v", m)
		}
	}
	if !strings.Contains(n.Mentions[1].Comment.Body, "<script>x</script>") {
		t.Fatalf("body altered: %q", n.Mentions[1].Comment.Body)
	}
	// Unread counts beyond the page.
	for i := 0; i < 5; i++ {
		if _, err := b.Comment(ctx, pm, 1, api.CommentRequest{Body: "@user again"}); err != nil {
			t.Fatal(err)
		}
	}
	n, err = b.UserMentions(ctx, User, 3)
	if err != nil || len(n.Mentions) != 3 || n.Unread != 7 {
		t.Fatalf("%+v %v", n, err)
	}
	_, err = b.UserMentions(ctx, User, MaxMentionPage+1)
	wantCode(t, err, api.CodeValidation)
	for _, a := range []Actor{pm, System, agent(t, b, api.RoleCEO, 0)} {
		_, err = b.UserMentions(ctx, a, 10)
		wantCode(t, err, api.CodeForbidden)
	}
}

func TestSetMentionsSeenMonotonic(t *testing.T) {
	ctx := context.Background()
	b, pm := newMentionBoard(t)
	var newest int
	if err := b.db.QueryRow(`SELECT MAX(id) FROM comments`).Scan(&newest); err != nil {
		t.Fatal(err)
	}
	n, err := b.SetMentionsSeen(ctx, User, 1)
	if err != nil || n.SeenThrough != 1 || n.Unread != 1 || n.Mentions[1].Unread || !n.Mentions[0].Unread {
		t.Fatalf("%+v %v", n, err)
	}
	if got := countEvents(t, b, api.KindNotificationsSeen); got != 1 {
		t.Fatalf("%d events", got)
	}
	for _, v := range []int{1, 0} {
		n, err = b.SetMentionsSeen(ctx, User, v)
		if err != nil || n.SeenThrough != 1 {
			t.Fatalf("through %d: %+v %v", v, n, err)
		}
	}
	if got := countEvents(t, b, api.KindNotificationsSeen); got != 1 {
		t.Fatalf("no-op emitted: %d events", got)
	}
	_, err = b.SetMentionsSeen(ctx, User, newest+1)
	wantCode(t, err, api.CodeValidation)
	_, err = b.SetMentionsSeen(ctx, User, -1)
	wantCode(t, err, api.CodeValidation)
	n, err = b.SetMentionsSeen(ctx, User, newest)
	if err != nil || n.SeenThrough != newest || n.Unread != 0 {
		t.Fatalf("%+v %v", n, err)
	}
	var detail string
	if err := b.db.QueryRow(`SELECT detail FROM events WHERE kind = ? ORDER BY id DESC LIMIT 1`, api.KindNotificationsSeen).Scan(&detail); err != nil ||
		!strings.Contains(detail, fmt.Sprintf(`"through":%d`, newest)) {
		t.Fatalf("%s %v", detail, err)
	}
	var ticket sql.NullInt64
	if err := b.db.QueryRow(`SELECT ticket_id FROM events WHERE kind = ? LIMIT 1`, api.KindNotificationsSeen).Scan(&ticket); err != nil || ticket.Valid {
		t.Fatalf("ticket %v %v", ticket, err)
	}
	for _, a := range []Actor{pm, System} {
		_, err = b.SetMentionsSeen(ctx, a, 1)
		wantCode(t, err, api.CodeForbidden)
	}
}

func TestSessionLogPathUserOnly(t *testing.T) {
	ctx := context.Background()
	b, _ := newTestBoard(t)
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	dev := agent(t, b, api.RoleDeveloper, 1)
	if _, err := b.db.Exec(`UPDATE agent_sessions SET log_path = '/logs/a.log' WHERE id = ?`, dev.Session); err != nil {
		t.Fatal(err)
	}
	p, err := b.SessionLogPath(ctx, User, dev.Session)
	if err != nil || p != "/logs/a.log" {
		t.Fatalf("%q %v", p, err)
	}
	_, err = b.SessionLogPath(ctx, User, "nope")
	wantCode(t, err, api.CodeNotFound)
	other := agent(t, b, api.RoleCEO, 0)
	if _, err := b.SessionLogPath(ctx, User, other.Session); err == nil {
		t.Fatal("a session without a log is not found")
	}
	for _, a := range []Actor{dev, System, other} {
		_, err = b.SessionLogPath(ctx, a, dev.Session)
		wantCode(t, err, api.CodeForbidden)
	}
}

func TestNewResultsRedactedForAgents(t *testing.T) {
	ctx := context.Background()
	sb := newSessionBoard(t)
	pm := sb.agents["pm"]
	th, err := sb.b.TicketThread(ctx, pm, 2, 0, 0)
	if err != nil || len(th.Events) == 0 {
		t.Fatalf("%+v %v", th, err)
	}
	raw := fmt.Sprint(th)
	for _, e := range th.Events {
		raw += string(e.Data)
	}
	for _, id := range sb.ids {
		if strings.Contains(raw, id) {
			t.Fatalf("pm sees session %s in the thread", id)
		}
	}
	// The user sees the ids in the same thread.
	uth, err := sb.b.TicketThread(ctx, User, 2, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var all bytes.Buffer
	for _, e := range uth.Events {
		all.Write(e.Data)
	}
	if !bytes.Contains(all.Bytes(), []byte(sb.agents["developer"].Session)) {
		t.Fatal("user does not see mention session ids in the thread")
	}
	// forActor blanks a Notifications value for an agent.
	n, err := sb.b.UserMentions(ctx, User, 0)
	if err != nil {
		t.Fatal(err)
	}
	n.Mentions = append(n.Mentions, api.MentionNotice{Comment: api.Comment{
		Author: api.Actor{Session: "secret"}, Mentions: []api.Mention{{Role: api.RoleDeveloper, Session: "secret"}}}})
	red, _ := forActor(pm, n, nil)
	if strings.Contains(fmt.Sprint(red), "secret") {
		t.Fatalf("notifications keep session ids: %+v", red)
	}
}
