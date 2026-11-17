package board

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/images"
)

func newImage(name string, size int64) NewImage {
	return NewImage{ID: images.NewID(), Name: name, ContentType: images.TypePNG, Size: size}
}

// Who may add an image is who may comment; who may see one is who may read
// its own ticket. Only the user deletes.
func TestImageAccessMatrix(t *testing.T) {
	f := newScopeFixture(t)
	ctx := context.Background()
	// One image on every ticket, added by the user.
	ids := map[string]string{}
	for _, rel := range scopeRelations {
		im, err := f.b.AddImage(ctx, User, int64(f.ticket[rel]), newImage(rel+".png", 10))
		if err != nil {
			t.Fatal(err)
		}
		ids[rel] = im.ID
	}
	for name, a := range f.actor {
		for _, rel := range scopeRelations {
			n := int64(f.ticket[rel])
			check := func(what string, allowed bool, err error) {
				t.Helper()
				if allowed && err != nil {
					t.Errorf("%s %s %s: %v", name, what, rel, err)
				}
				if !allowed && errCode(err) != api.CodeForbidden {
					t.Errorf("%s %s %s: error %v, want forbidden", name, what, rel, err)
				}
			}
			check("checks an upload to", wantWrite[name][rel], f.b.CheckImageUpload(ctx, a, n, 10))
			im, err := f.b.AddImage(ctx, a, n, newImage("by "+name+".png", 10))
			check("adds an image to", wantWrite[name][rel], err)
			if err == nil && im.Author.Role != a.Role {
				t.Errorf("%s: author %+v", name, im.Author)
			}
			_, err = f.b.Image(ctx, a, ids[rel])
			check("reads the image of", wantRead[name][rel], err)
			_, err = f.b.TicketImages(ctx, a, n)
			check("lists the images of", wantRead[name][rel], err)
		}
		if a.Role != api.RoleUser {
			_, err := f.b.DeleteImage(ctx, a, ids["T"])
			if errCode(err) != api.CodeForbidden {
				t.Errorf("%s deletes an image: %v, want forbidden", name, err)
			}
		}
	}
	if _, err := f.b.DeleteImage(ctx, User, ids["T"]); err != nil {
		t.Fatal(err)
	}
	_, err := f.b.Image(ctx, User, ids["T"])
	wantCode(t, err, api.CodeNotFound)
}

// An agent's image record carries no session id for an agent reader.
func TestImageAuthorSessionHiddenFromAgents(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := int64(mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "t", Projects: []string{"api"}}).Number)
	dev := agent(t, b, api.RoleDeveloper, n)
	im, err := b.AddImage(ctx, dev, n, newImage("shot.png", 5))
	if err != nil {
		t.Fatal(err)
	}
	if im.Author.Session != "" || im.Author.Name == "" {
		t.Errorf("agent sees author %+v", im.Author)
	}
	asUser, err := b.Image(ctx, User, im.ID)
	if err != nil || asUser.Author.Session != dev.Session {
		t.Errorf("user sees author %+v, %v", asUser.Author, err)
	}
	list, err := b.TicketImages(ctx, dev, n)
	if err != nil || len(list) != 1 || list[0].Author.Session != "" {
		t.Errorf("agent list %+v, %v", list, err)
	}
}

func TestAddImageRecordsAndEmits(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := int64(mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "t", Projects: []string{"api"}}).Number)
	img := newImage("a]b.png", 1234)
	im, err := b.AddImage(ctx, User, n, img)
	if err != nil {
		t.Fatal(err)
	}
	if im.ID != img.ID || im.Ticket != int(n) || im.Name != "a]b.png" || im.Size != 1234 || im.ContentType != images.TypePNG ||
		im.State != api.ImageStored || im.Markdown != `![a\]b.png](omo-image:`+img.ID+`)` {
		t.Fatalf("image = %+v", im)
	}
	events, err := db.AllEvents(b.db)
	if err != nil {
		t.Fatal(err)
	}
	last := events[0] // newest first
	if last.Kind != api.KindImageAdded {
		t.Fatalf("last event %s", last.Kind)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(last.Detail), &data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 4 || data["id"] != img.ID || data["name"] != "a]b.png" || data["size"] != float64(1234) || data["ticket"] != float64(n) {
		t.Errorf("event data %v: only ids, the name and the size", data)
	}
	for _, bad := range []NewImage{
		{ID: "../x", Name: "x", ContentType: images.TypePNG, Size: 1},
		{ID: images.NewID(), Name: "x", ContentType: "image/svg+xml", Size: 1},
		{ID: images.NewID(), Name: "x", ContentType: images.TypePNG, Size: 0},
	} {
		_, err := b.AddImage(ctx, User, n, bad)
		wantCode(t, err, api.CodeValidation)
	}
	_, err = b.AddImage(ctx, User, 999, newImage("x.png", 1))
	wantCode(t, err, api.CodeNotFound)
}

func TestImageLimits(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	n := int64(mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "t", Projects: []string{"api"}}).Number)
	other := int64(mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "o", Projects: []string{"api"}}).Number)
	err := b.CheckImageUpload(ctx, User, n, images.MaxBytes+1)
	wantCode(t, err, api.CodeTooLarge)
	for i := range images.MaxPerTicket {
		if _, err := b.AddImage(ctx, User, n, newImage("x.png", 1)); err != nil {
			t.Fatalf("image %d: %v", i, err)
		}
	}
	_, err = b.AddImage(ctx, User, n, newImage("x.png", 1))
	wantCode(t, err, api.CodeConflict)
	if !strings.Contains(err.Error(), "100 images") {
		t.Errorf("message: %v", err)
	}
	// An expired image counts for nothing.
	if _, err := b.db.Exec(`UPDATE images SET expired_at = ? WHERE ticket_id = ?`, b.stamp(), n); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddImage(ctx, User, n, newImage("x.png", 1)); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	// The overall limit counts every stored image.
	if err := b.CheckImageUpload(ctx, User, n, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.Exec(`INSERT INTO images (id, ticket_id, author_role, name, content_type, size, created_at) VALUES (?, ?, 'user', 'x', 'image/png', ?, ?)`,
		images.NewID(), other, images.MaxTotalBytes-5, b.stamp()); err != nil {
		t.Fatal(err)
	}
	err = b.CheckImageUpload(ctx, User, n, 10)
	wantCode(t, err, api.CodeConflict)
}

// Expiry: images of a ticket done (or cancelled) before the cutoff, and
// uploaded before it, are due; a ticket taken out of Done keeps them.
func TestImagesToExpire(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	mk := func(title string) int64 {
		return int64(mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: title, Projects: []string{"api"}}).Number)
	}
	done, cancelled, open, reopened, late := mk("done"), mk("cancelled"), mk("open"), mk("reopened"), mk("late")
	add := func(n int64) string {
		im, err := b.AddImage(ctx, User, n, newImage("x.png", 3))
		if err != nil {
			t.Fatal(err)
		}
		return im.ID
	}
	dImg, cImg, oImg, rImg := add(done), add(cancelled), add(open), add(reopened)
	forceDone(t, b, int(done), api.ResolutionCompleted)
	forceDone(t, b, int(cancelled), api.ResolutionCancelled)
	forceDone(t, b, int(late), api.ResolutionCompleted)
	lImg := add(late) // uploaded after its ticket closed
	cutoff := b.now()
	// Moved out of Done: closed_at is cleared with the column.
	if _, err := b.db.Exec(`UPDATE tickets SET col='planning', resolution='', closed_at=NULL WHERE id=?`, reopened); err != nil {
		t.Fatal(err)
	}
	_ = rImg

	if _, err := b.ImagesToExpire(ctx, User, cutoff, 10); errCode(err) != api.CodeForbidden {
		t.Fatalf("user lists due images: %v", err)
	}
	due, err := b.ImagesToExpire(ctx, System, cutoff, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, im := range due {
		got[im.ID] = true
	}
	if len(got) != 3 || !got[dImg] || !got[cImg] || !got[lImg] || got[oImg] {
		t.Fatalf("due = %v; want the done, cancelled and late ones", got)
	}
	// Before the late upload's own time only the two older ones are due.
	early, err := b.ImagesToExpire(ctx, System, cutoff.Add(-2*time.Millisecond), 10)
	if err != nil || len(early) != 2 {
		t.Fatalf("early due = %+v, %v", early, err)
	}
	one, err := b.ImagesToExpire(ctx, System, cutoff, 1)
	if err != nil || len(one) != 1 {
		t.Fatalf("limit: %+v, %v", one, err)
	}

	if err := b.ExpireImage(ctx, User, dImg); errCode(err) != api.CodeForbidden {
		t.Fatalf("user expires: %v", err)
	}
	if err := b.ExpireImage(ctx, System, dImg); err != nil {
		t.Fatal(err)
	}
	im, err := b.Image(ctx, User, dImg)
	if err != nil || im.State != api.ImageExpired || im.ExpiredAt == nil {
		t.Fatalf("after expiry: %+v, %v", im, err)
	}
	if err := b.ExpireImage(ctx, System, dImg); err != nil {
		t.Fatalf("expire twice: %v", err)
	}
	if err := b.ExpireImage(ctx, System, images.NewID()); err != nil {
		t.Fatalf("expire unknown: %v", err)
	}
	due, _ = b.ImagesToExpire(ctx, System, cutoff, 10)
	if len(due) != 2 {
		t.Fatalf("an expired image is due again: %+v", due)
	}
	files, err := b.ImageFileNames(ctx, System)
	if err != nil || len(files) != 5 || !files[dImg+".png"] {
		t.Fatalf("files = %v, %v", files, err)
	}
}
