package board

import (
	"context"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestDescriptionEditCreatesVersion(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Description: "v1", Projects: []string{"api"}})
	v, err := b.SetDescription(ctx, User, int64(tk.Number), api.DescriptionUpdate{Text: "v2", BaseVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if v.Number != 2 || v.Text != "v2" || !v.Current || v.Author.Role != api.RoleUser || v.RestoredFrom != nil {
		t.Fatalf("version = %+v", v)
	}
	got, err := b.Get(ctx, User, int64(tk.Number))
	if err != nil {
		t.Fatal(err)
	}
	if got.DescriptionVersion != 2 || got.Description != "v2" {
		t.Fatalf("ticket description = %d %q", got.DescriptionVersion, got.Description)
	}
	vs, err := b.Descriptions(ctx, User, int64(tk.Number))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].Number != 2 || vs[1].Number != 1 {
		t.Fatalf("versions = %+v", vs)
	}
	if vs[1].Text != "v1" || vs[1].Current || !vs[0].Current {
		t.Fatalf("old version = %+v", vs[1])
	}

	evs, err := db.EventsSince(b.db, 0, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	var changed, updated bool
	for _, e := range evs {
		switch e.Kind {
		case api.KindDescriptionChanged:
			changed = true
		case api.KindTicketUpdated:
			updated = true
		}
	}
	if !changed || !updated {
		t.Fatalf("events: description.changed %v, ticket.updated %v", changed, updated)
	}
}

func TestTaskWithoutDescriptionGetsFirstVersion(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	if tk.DescriptionVersion != 0 {
		t.Fatalf("DescriptionVersion = %d", tk.DescriptionVersion)
	}
	vs, err := b.Descriptions(ctx, User, int64(tk.Number))
	if err != nil {
		t.Fatal(err)
	}
	if vs == nil || len(vs) != 0 {
		t.Fatalf("versions = %#v, want empty slice", vs)
	}
	v, err := b.SetDescription(ctx, User, int64(tk.Number), api.DescriptionUpdate{Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Number != 1 {
		t.Fatalf("Number = %d", v.Number)
	}
}

func TestStaleDescriptionRejected(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Description: "v1", Projects: []string{"api"}})
	if _, err := b.SetDescription(ctx, User, int64(tk.Number), api.DescriptionUpdate{Text: "v2"}); err != nil {
		t.Fatal(err)
	}
	_, err := b.SetDescription(ctx, User, int64(tk.Number), api.DescriptionUpdate{Text: "v3", BaseVersion: 1})
	wantCode(t, err, api.CodeStaleDescription)
	e := err.(*api.Error)
	d, ok := e.Details.(map[string]any)
	if !ok || d["current_version"] != 2 {
		t.Fatalf("details = %#v", e.Details)
	}
}

func TestRestoreCreatesNewVersion(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Description: "v1", Projects: []string{"api"}})
	n := int64(tk.Number)
	if _, err := b.SetDescription(ctx, User, n, api.DescriptionUpdate{Text: "v2"}); err != nil {
		t.Fatal(err)
	}
	v, err := b.RestoreDescription(ctx, User, n, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Number != 3 || v.RestoredFrom == nil || *v.RestoredFrom != 1 || v.Text != "v1" || !v.Current {
		t.Fatalf("restored = %+v", v)
	}
	got, _ := b.Get(ctx, User, n)
	if got.Description != "v1" || got.DescriptionVersion != 3 {
		t.Fatalf("ticket = %d %q", got.DescriptionVersion, got.Description)
	}
	_, err = b.RestoreDescription(ctx, User, n, 9)
	wantCode(t, err, api.CodeNotFound)
}

// lastComment returns the newest comment of ticket n with its mentions.
func lastComment(t *testing.T, b *Board, n int) (api.Comment, bool) {
	t.Helper()
	var id int64
	err := b.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM comments WHERE ticket_id = ?`, n).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		return api.Comment{}, false
	}
	c, err := loadComment(b.db, id)
	if err != nil {
		t.Fatal(err)
	}
	return c, true
}

func TestDescriptionChangeNotifiesWorkingAgent(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Description: "v1", Projects: []string{"api"}})
	dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
	if _, err := b.SetDescription(ctx, User, int64(tk.Number), api.DescriptionUpdate{Text: "v2"}); err != nil {
		t.Fatal(err)
	}
	c, ok := lastComment(t, b, tk.Number)
	if !ok {
		t.Fatal("no comment posted")
	}
	if c.Author.Role != api.RoleSystem || c.Body != "Description changed by user (version 2)." {
		t.Fatalf("comment = %+v", c)
	}
	if len(c.Mentions) != 1 || c.Mentions[0].Role != api.RoleDeveloper || c.Mentions[0].Session != dev.Session {
		t.Fatalf("mentions = %+v", c.Mentions)
	}
	evs, err := db.EventsSince(b.db, 0, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		found = found || e.Kind == api.KindCommentCreated
	}
	if !found {
		t.Fatal("no comment.created event")
	}
}

func TestStoryDescriptionChangeNotifiesPM(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	pm := agent(t, b, api.RolePM, int64(s.Number))
	ceo := agent(t, b, api.RoleCEO, 0)
	// The user changes the supervisor so the CEO may edit.
	if _, err := b.Update(ctx, User, int64(s.Number), api.UpdateTicket{Supervisor: api.Some(api.RoleCEO)}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetDescription(ctx, ceo, int64(s.Number), api.DescriptionUpdate{Text: "goal"}); err != nil {
		t.Fatal(err)
	}
	c, ok := lastComment(t, b, s.Number)
	if !ok || c.Body != "Description changed by ceo (version 2)." || len(c.Mentions) != 1 ||
		c.Mentions[0].Role != api.RolePM || c.Mentions[0].Session != pm.Session {
		t.Fatalf("comment = %+v", c)
	}
}

func TestOwnEditDoesNotNotify(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Description: "v1", Projects: []string{"api"}})
	if _, err := b.SetDescription(ctx, User, int64(tk.Number), api.DescriptionUpdate{Text: "v2"}); err != nil {
		t.Fatal(err)
	}
	if c, ok := lastComment(t, b, tk.Number); ok {
		t.Fatalf("unexpected comment %+v", c)
	}
}

func TestDescriptionPermissions(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	s := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(s.Number)})
	other := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "O", Projects: []string{"api"}})
	pm := agent(t, b, api.RolePM, int64(s.Number))
	dev := agent(t, b, api.RoleDeveloper, int64(task.Number))
	ceo := agent(t, b, api.RoleCEO, 0)

	if _, err := b.SetDescription(ctx, pm, int64(task.Number), api.DescriptionUpdate{Text: "by pm"}); err != nil {
		t.Fatalf("pm on task of its story: %v", err)
	}
	_, err := b.SetDescription(ctx, pm, int64(other.Number), api.DescriptionUpdate{Text: "x"})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.SetDescription(ctx, dev, int64(task.Number), api.DescriptionUpdate{Text: "x"})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.SetDescription(ctx, ceo, int64(s.Number), api.DescriptionUpdate{Text: "x"})
	wantCode(t, err, api.CodeForbidden)
	_, err = b.RestoreDescription(ctx, dev, int64(task.Number), 1)
	wantCode(t, err, api.CodeForbidden)

	own := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeStory, Title: "C"})
	if _, err := b.SetDescription(ctx, ceo, int64(own.Number), api.DescriptionUpdate{Text: "by ceo"}); err != nil {
		t.Fatalf("ceo on own story: %v", err)
	}

	forceDone(t, b, own.Number, api.ResolutionCompleted)
	_, err = b.SetDescription(ctx, User, int64(own.Number), api.DescriptionUpdate{Text: "late"})
	wantCode(t, err, api.CodeInvalidTransition)
	_, err = b.RestoreDescription(ctx, User, int64(own.Number), 1)
	wantCode(t, err, api.CodeInvalidTransition)
}
