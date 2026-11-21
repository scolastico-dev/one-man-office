package board

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// A seen-filtered, descending or unbounded projection loses device deliveries.
func TestRelayMentionsIndependentBoundedAscending(t *testing.T) {
	ctx := context.Background()
	b, pm := newMentionBoard(t)
	for range 55 {
		if _, err := b.Comment(ctx, pm, 1, api.CommentRequest{Body: "@user " + strings.Repeat("\x01<&界", 2000)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.db.Exec(`UPDATE tickets SET title = ? WHERE id = 1`, strings.Repeat("界", 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetMentionsSeen(ctx, User, 59); err != nil {
		t.Fatal(err)
	}
	pg, err := b.RelayMentionsAfter(ctx, User, 0, 50, relayproto.MaxNotificationPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	if len(pg.Notifications) != 50 || !pg.More || pg.Through != 52 {
		t.Fatalf("page: %+v", pg)
	}
	if pg.Notifications[0].ID != 1 || pg.Notifications[1].ID != 4 {
		t.Fatalf("qualification: %+v", pg.Notifications[:2])
	}
	for i, n := range pg.Notifications {
		raw, err := json.Marshal(n)
		if err != nil || len(raw) > 4096 || len(n.Body) > 384 || len(n.Title) > 128 || !utf8.ValidString(n.Title) || !utf8.ValidString(n.Body) {
			t.Fatalf("notice %d: %v", i, err)
		}
		if !n.TitleTruncated || (i > 1 && !n.BodyTruncated) {
			t.Fatal("missing truncation flag")
		}
		if i > 0 && n.ID <= pg.Notifications[i-1].ID {
			t.Fatal("not ascending")
		}
	}
	next, err := b.RelayMentionsAfter(ctx, User, pg.Through, 50, relayproto.MaxNotificationPlaintext)
	if err != nil || len(next.Notifications) != 7 || next.More || next.Through != 59 {
		t.Fatalf("next: %+v %v", next, err)
	}
	// Budget includes page fields and worst-case escaping, rather than source bytes.
	small, err := b.RelayMentionsAfter(ctx, User, 0, 50, 5000)
	if err != nil || len(small.Notifications) == 0 || len(small.Notifications) >= 50 || !small.More {
		t.Fatalf("small page: %+v %v", small, err)
	}
	small.Batch = make([]byte, 16)
	small.Token = make([]byte, 32)
	small.Acknowledged = make([]byte, 16)
	raw, err := relayproto.EncodeNotificationPage(small)
	if err != nil || len(raw) > 5000 {
		t.Fatalf("budget: %d %v", len(raw), err)
	}
	if _, err = b.RelayMentionsAfter(ctx, pm, 0, 50, relayproto.MaxNotificationPlaintext); err == nil {
		t.Fatal("agent read")
	}
}

func TestRelayMentionsRefreshMissingAndGrowing(t *testing.T) {
	ctx := context.Background()
	b, _ := newMentionBoard(t)
	if _, err := b.db.Exec(`UPDATE comments SET body = ? WHERE id = 1`, strings.Repeat("界", 2000)); err != nil {
		t.Fatal(err)
	}
	ids := []relayproto.Counter{1, 60}
	pg, err := b.RelayMentionsByID(ctx, User, ids)
	if err != nil || len(pg.Notifications) != 2 || pg.Through != 60 || pg.More {
		t.Fatalf("refresh %+v %v", pg, err)
	}
	if !pg.Notifications[0].BodyTruncated || len(pg.Notifications[0].Body) != 384 || !pg.Notifications[1].Unavailable {
		t.Fatalf("refresh %+v", pg)
	}
	if _, err = b.db.Exec(`DELETE FROM comments WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	pg, err = b.RelayMentionsByID(ctx, User, ids)
	if err != nil || !pg.Notifications[0].Unavailable || !pg.Notifications[1].Unavailable {
		t.Fatalf("deleted %+v %v", pg, err)
	}
}

func TestRelayMentionsRejectImpossibleBudgetsAndIDs(t *testing.T) {
	b, _ := newTestBoard(t)
	// Even an online empty page must fit the requested encoded page budget.
	if _, err := b.RelayMentionsAfter(t.Context(), User, 0, 50, 1); err == nil {
		t.Fatal("empty page exceeds byte budget")
	}
	for _, ids := range [][]relayproto.Counter{nil, {0}, {2, 1}, {1, 1}, {relayproto.Counter(1 << 63)}} {
		if _, err := b.RelayMentionsByID(t.Context(), User, ids); err == nil {
			t.Fatal("invalid pending ids", ids)
		}
	}
	for _, args := range []struct {
		after      relayproto.Counter
		limit, max int
	}{{0, -1, 0}, {0, 51, 0}, {0, 50, -1}, {0, 50, relayproto.MaxNotificationPlaintext + 1}, {relayproto.Counter(1 << 63), 50, 0}} {
		if _, err := b.RelayMentionsAfter(t.Context(), User, args.after, args.limit, args.max); err == nil {
			t.Fatal("invalid bounds", args)
		}
	}
}

// Native tool output can contain invalid UTF-8. A single such source must not
// block later notifications or make an existing pending receipt unrecoverable.
func TestRelayMentionsRepairInvalidUTF8BeforeBoundingPreviews(t *testing.T) {
	ctx := t.Context()
	b, _ := newMentionBoard(t)
	c, err := b.Comment(ctx, System, 1, api.CommentRequest{Body: "@user invalid \xff output"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.db.Exec(`UPDATE tickets SET title = ? WHERE id = 1`, "title \xff"); err != nil {
		t.Fatal(err)
	}
	pg, err := b.RelayMentionsAfter(ctx, User, 4, 50, 0)
	if err != nil || len(pg.Notifications) != 1 || pg.Notifications[0].Body != "@user invalid � output" || pg.Notifications[0].Title != "title �" {
		t.Fatalf("invalid source blocked delivery: %+v %v", pg, err)
	}
	if _, err = b.db.Exec(`UPDATE comments SET body = ? WHERE id = ?`, strings.Repeat("x\xff", 200), c.Comment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = b.db.Exec(`UPDATE tickets SET title = ? WHERE id = 1`, strings.Repeat("x\xff", 100)); err != nil {
		t.Fatal(err)
	}
	refresh, err := b.RelayMentionsByID(ctx, User, []relayproto.Counter{relayproto.Counter(c.Comment.ID)})
	if err != nil || len(refresh.Notifications) != 1 {
		t.Fatalf("invalid growing source blocked retry: %+v %v", refresh, err)
	}
	notice := refresh.Notifications[0]
	if notice.Body != strings.Repeat("x�", 96) || notice.Title != strings.Repeat("x�", 32) || !notice.BodyTruncated || !notice.TitleTruncated {
		t.Fatal("normalized previews exceed UTF-8 byte bounds", notice)
	}
}
