package server

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/images"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// uploadRequest builds a raw image upload of data named name to ticket n.
func uploadRequest(t *testing.T, target string, n int, name string, data []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest("POST", target+"/api/tickets/"+strconv.Itoa(n)+"/images", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(ImageNameHeader, url.PathEscape(name))
	return req
}

// socketUpload sends an upload through the socket as session ("" = user).
func socketUpload(t *testing.T, s *Server, n int, name string, data []byte, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := uploadRequest(t, "http://omo", n, name, data)
	if session != "" {
		req.Header.Set(SessionHeader, session)
	}
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	return rec
}

func userTicket(t *testing.T, s *Server) int {
	t.Helper()
	sess := liveSession(t, s, api.RoleDeveloper)
	return *sess.Ticket
}

func TestImageUploadByTheUserInTheBrowser(t *testing.T) {
	s := newTestServer(t)
	n := userTicket(t, s)
	b := newBrowser(t, s)
	data := testPNG(t)
	req := uploadRequest(t, b.srv.URL, n, "../Screen shot ä.png", data)
	req.Host = hostOmoLocalhost + ":" + b.port
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := b.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	wantStatus(t, resp.StatusCode, http.StatusCreated)
	im := decodeBody[api.Image](t, resp.Body)
	if !images.ValidID(im.ID) || im.Ticket != n || im.Name != "Screen shot ä.png" || im.ContentType != images.TypePNG ||
		im.Size != int64(len(data)) || im.State != api.ImageStored || im.Author.Role != api.RoleUser {
		t.Fatalf("image = %+v", im)
	}
	if im.Path != filepath.Join(s.opts.Layout.Images, im.ID+".png") || im.Markdown != "![Screen shot ä.png](omo-image:"+im.ID+")" {
		t.Fatalf("path %q markdown %q", im.Path, im.Markdown)
	}
	got, err := os.ReadFile(im.Path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("stored file: %v", err)
	}
	// The original name never reaches the disk.
	entries, _ := os.ReadDir(s.opts.Layout.Images)
	if len(entries) != 1 || entries[0].Name() != im.ID+".png" {
		t.Fatalf("images folder: %v", entries)
	}

	// Bytes: the verified type, never sniffed, inline under a made-up name,
	// a policy that runs nothing, cached for the id's life.
	resp = b.do("GET", "/api/images/"+im.ID+"/content", nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, data) {
		t.Fatal("served bytes differ")
	}
	for k, want := range map[string]string{
		"Content-Type":                 "image/png",
		"X-Content-Type-Options":       "nosniff",
		"Content-Disposition":          `inline; filename="image-` + im.ID + `.png"; filename*=UTF-8''Screen%20shot%20%C3%A4.png`,
		"Content-Security-Policy":      imageCSP,
		"Cross-Origin-Resource-Policy": "same-origin",
		"Cache-Control":                "private, max-age=31536000, immutable",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	// Without the token nothing is served.
	resp = b.do("GET", "/api/images/"+im.ID+"/content", nil, nil)
	wantStatus(t, resp.StatusCode, http.StatusUnauthorized)
}

// Other origins can neither upload nor read, and an upload needs the raw
// content type (a form can send none without a preflight).
func TestImageUploadIsGuardedLikeJSONRoutes(t *testing.T) {
	s := newTestServer(t)
	n := userTicket(t, s)
	b := newBrowser(t, s)
	send := func(mutate func(*http.Request)) int {
		req := uploadRequest(t, b.srv.URL, n, "a.png", testPNG(t))
		req.Host = hostOmoLocalhost + ":" + b.port
		req.Header.Set("Authorization", "Bearer "+s.token)
		mutate(req)
		resp, err := b.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for name, c := range map[string]struct {
		mutate func(*http.Request)
		want   int
	}{
		"other origin":    {func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, http.StatusForbidden},
		"cross-site":      {func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, http.StatusForbidden},
		"form type":       {func(r *http.Request) { r.Header.Set("Content-Type", "multipart/form-data; boundary=x") }, http.StatusBadRequest},
		"text type":       {func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, http.StatusBadRequest},
		"json type":       {func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }, http.StatusBadRequest},
		"no token":        {func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		"same origin":     {func(r *http.Request) { r.Header.Set("Origin", "http://"+hostOmoLocalhost+":"+b.port) }, http.StatusCreated},
		"bad name header": {func(r *http.Request) { r.Header.Set(ImageNameHeader, "%zz") }, http.StatusBadRequest},
	} {
		if got := send(c.mutate); got != c.want {
			t.Errorf("%s: status %d, want %d", name, got, c.want)
		}
	}
	// Another route keeps refusing a raw body.
	req, _ := http.NewRequest("POST", "http://omo/api/tickets/"+strconv.Itoa(n)+"/comments", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	wantStatus(t, rec.Code, http.StatusBadRequest)
}

func TestImageUploadRefusals(t *testing.T) {
	s := newTestServer(t)
	n := userTicket(t, s)
	check := func(name string, rec *httptest.ResponseRecorder, status int, code, text string) {
		t.Helper()
		if rec.Code != status {
			t.Errorf("%s: status %d, want %d (%s)", name, rec.Code, status, rec.Body.String())
			return
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"`+code+`"`) || !strings.Contains(body, text) {
			t.Errorf("%s: answer %s, want code %s and %q", name, body, code, text)
		}
	}
	check("svg", socketUpload(t, s, n, "x.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), ""),
		http.StatusUnprocessableEntity, api.CodeValidation, "SVG is refused")
	check("html named png", socketUpload(t, s, n, "x.png", []byte("<html><script>alert(1)</script>"), ""),
		http.StatusUnprocessableEntity, api.CodeValidation, "not a PNG, JPEG, GIF or WebP")
	check("empty", socketUpload(t, s, n, "x.png", nil, ""), http.StatusUnprocessableEntity, api.CodeValidation, "empty")
	check("unknown ticket", socketUpload(t, s, 999, "x.png", testPNG(t), ""), http.StatusNotFound, api.CodeNotFound, "#999")
	big := append(testPNG(t), make([]byte, images.MaxBytes)...)
	check("too large", socketUpload(t, s, n, "x.png", big, ""), http.StatusRequestEntityTooLarge, api.CodeTooLarge, "10 MiB")

	// A declared length over the limit is refused before the body is read.
	read := false
	req := uploadRequest(t, "http://omo", n, "x.png", nil)
	req.Body = io.NopCloser(readFunc(func(p []byte) (int, error) { read = true; return 0, io.EOF }))
	req.ContentLength = images.MaxBytes + 1
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	check("declared too large", rec, http.StatusRequestEntityTooLarge, api.CodeTooLarge, "10 MiB")
	if read {
		t.Error("the body of a too large upload was read")
	}
	if entries, _ := os.ReadDir(s.opts.Layout.Images); len(entries) != 0 {
		t.Errorf("refused uploads left files: %v", entries)
	}
}

type readFunc func([]byte) (int, error)

func (f readFunc) Read(p []byte) (int, error) { return f(p) }

// Agents upload where they may comment and read the images of tickets they
// may read; an image id from another ticket gives nothing outside the
// agent's scope; only the user deletes.
func TestImageRoutesForAgents(t *testing.T) {
	s := newTestServer(t)
	dev := liveSession(t, s, api.RoleDeveloper)
	other := liveSession(t, s, api.RoleDeveloper)
	own, foreign := *dev.Ticket, *other.Ticket

	rec := socketUpload(t, s, own, "mine.png", testPNG(t), dev.ID)
	wantStatus(t, rec.Code, http.StatusCreated)
	mine := decodeBody[api.Image](t, rec.Body)
	if mine.Author.Role != api.RoleDeveloper || mine.Author.Session != "" || mine.Path == "" {
		t.Fatalf("agent's image %+v", mine)
	}
	rec = socketUpload(t, s, foreign, "theirs.png", testPNG(t), dev.ID)
	wantStatus(t, rec.Code, http.StatusForbidden)
	rec = socketUpload(t, s, foreign, "theirs.png", testPNG(t), "")
	wantStatus(t, rec.Code, http.StatusCreated)
	theirs := decodeBody[api.Image](t, rec.Body)

	for path, want := range map[string]int{
		"/api/images/" + mine.ID:                            http.StatusOK,
		"/api/images/" + mine.ID + "/content":               http.StatusOK,
		"/api/tickets/" + strconv.Itoa(own) + "/images":     http.StatusOK,
		"/api/images/" + theirs.ID:                          http.StatusForbidden,
		"/api/images/" + theirs.ID + "/content":             http.StatusForbidden,
		"/api/tickets/" + strconv.Itoa(foreign) + "/images": http.StatusForbidden,
		"/api/images/..%2F..%2Fomo.db":                      http.StatusNotFound,
		"/api/images/not-a-uuid/content":                    http.StatusNotFound,
		"/api/images/" + images.NewID() + "/content":        http.StatusNotFound,
	} {
		if got := socketDo(t, s, "GET", path, nil, dev.ID).Code; got != want {
			t.Errorf("agent GET %s: %d, want %d", path, got, want)
		}
	}
	rec = socketDo(t, s, "GET", "/api/tickets/"+strconv.Itoa(own)+"/images", nil, dev.ID)
	list := decodeBody[[]api.Image](t, rec.Body)
	if len(list) != 1 || list[0].ID != mine.ID || list[0].Path == "" {
		t.Fatalf("list %+v", list)
	}

	wantStatus(t, socketDo(t, s, "DELETE", "/api/images/"+mine.ID, nil, dev.ID).Code, http.StatusForbidden)
	if _, err := os.Stat(mine.Path); err != nil {
		t.Fatalf("an agent's refused delete removed the file: %v", err)
	}
	wantStatus(t, socketDo(t, s, "DELETE", "/api/images/"+mine.ID, nil, "").Code, http.StatusNoContent)
	if _, err := os.Stat(mine.Path); !os.IsNotExist(err) {
		t.Fatalf("file after delete: %v", err)
	}
	wantStatus(t, socketDo(t, s, "GET", "/api/images/"+mine.ID, nil, "").Code, http.StatusNotFound)
	wantStatus(t, socketDo(t, s, "DELETE", "/api/images/"+mine.ID, nil, "").Code, http.StatusNotFound)
	kinds := imageEventKinds(t, s)
	if !slices.Equal(kinds, []string{api.KindImageAdded, api.KindImageAdded, api.KindImageDeleted}) {
		t.Fatalf("events %v", kinds)
	}
}

func imageEventKinds(t *testing.T, s *Server) []string {
	t.Helper()
	all, err := db.AllEvents(s.db)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for i := len(all) - 1; i >= 0; i-- {
		if strings.HasPrefix(all[i].Kind, "image.") {
			kinds = append(kinds, all[i].Kind)
		}
	}
	return kinds
}

// The route table states the rule of every image route.
func TestImageRoutesInTable(t *testing.T) {
	s := newTestServer(t)
	want := map[string]routeAccess{
		"POST /api/tickets/{n}/images": agentWrite,
		"GET /api/tickets/{n}/images":  agentRead,
		"GET /api/images/{id}":         agentRead,
		"GET /api/images/{id}/content": agentRead,
		"DELETE /api/images/{id}":      userOnly,
	}
	got := map[string]routeAccess{}
	for _, row := range s.routeTable {
		if strings.Contains(row.pattern, "/images") {
			got[row.pattern] = row.access
		}
	}
	if len(got) != len(want) {
		t.Fatalf("image routes in the table: %v", got)
	}
	for p, a := range want {
		if got[p] != a {
			t.Errorf("%s: %s, want %s", p, got[p], a)
		}
	}
}

// A planted link or an oversized file under images/ is never served or
// followed; the record reads as missing.
func TestImageFilePlantedByAnAgentIsNotServed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges on Windows")
	}
	s := newTestServer(t)
	n := userTicket(t, s)
	rec := socketUpload(t, s, n, "a.png", testPNG(t), "")
	wantStatus(t, rec.Code, http.StatusCreated)
	im := decodeBody[api.Image](t, rec.Body)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(im.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, im.Path); err != nil {
		t.Fatal(err)
	}
	rec = socketDo(t, s, "GET", "/api/images/"+im.ID+"/content", nil, "")
	wantStatus(t, rec.Code, http.StatusNotFound)
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatal("the link was followed")
	}
	rec = socketDo(t, s, "GET", "/api/images/"+im.ID, nil, "")
	if got := decodeBody[api.Image](t, rec.Body); got.State != api.ImageMissing || got.Path != "" {
		t.Fatalf("a linked file reads as %+v", got)
	}
	os.Remove(im.Path)
	f, err := os.Create(im.Path)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(images.MaxBytes + 1)
	f.Close()
	wantStatus(t, socketDo(t, s, "GET", "/api/images/"+im.ID+"/content", nil, "").Code, http.StatusNotFound)
	os.Remove(im.Path)
	rec = socketDo(t, s, "GET", "/api/images/"+im.ID, nil, "")
	if got := decodeBody[api.Image](t, rec.Body); got.State != api.ImageMissing {
		t.Fatalf("a deleted file reads as %+v", got)
	}
}

func setKeepDays(t *testing.T, s *Server, days int) {
	t.Helper()
	g := s.settings.General()
	g.Images.KeepDaysAfterDone = days
	if err := s.settings.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
}

// The sweep, with a clock of its own: images of a ticket done for longer
// than the setting lose their file and read as expired; a ticket moved out
// of Done keeps them; 0 days keeps everything; files without a record are
// counted and left alone.
func TestImageSweep(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	done, reopened := userTicket(t, s), userTicket(t, s)
	up := func(n int) api.Image {
		rec := socketUpload(t, s, n, "a.png", testPNG(t), "")
		wantStatus(t, rec.Code, http.StatusCreated)
		return decodeBody[api.Image](t, rec.Body)
	}
	a, b := up(done), up(reopened)
	for _, n := range []int{done, reopened} {
		if _, err := s.board.Cancel(ctx, board.User, int64(n), api.CancelRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	// The board keeps done tickets where they are; a ticket that is not
	// done (any more) has no closing time.
	if _, err := s.db.Exec(`UPDATE tickets SET col = 'planning', resolution = '', closed_at = NULL WHERE id = ?`, reopened); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(s.opts.Layout.Images, "notes.txt")
	if err := os.WriteFile(stray, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	if got := s.sweepImages(ctx, now.Add(59*24*time.Hour)); got != 0 {
		t.Fatalf("after 59 days %d expired", got)
	}
	setKeepDays(t, s, 0)
	if got := s.sweepImages(ctx, now.Add(5000*24*time.Hour)); got != 0 {
		t.Fatalf("keep for ever: %d expired", got)
	}
	setKeepDays(t, s, 60)
	if got := s.sweepImages(ctx, now.Add(61*24*time.Hour)); got != 1 {
		t.Fatalf("after 61 days %d expired, want 1", got)
	}
	if _, err := os.Stat(a.Path); !os.IsNotExist(err) {
		t.Fatalf("expired file still there: %v", err)
	}
	if _, err := os.Stat(b.Path); err != nil {
		t.Fatalf("the reopened ticket's image is gone: %v", err)
	}
	rec := socketDo(t, s, "GET", "/api/images/"+a.ID, nil, "")
	if got := decodeBody[api.Image](t, rec.Body); got.State != api.ImageExpired || got.ExpiredAt == nil || got.Path != "" {
		t.Fatalf("expired image reads as %+v", got)
	}
	rec = socketDo(t, s, "GET", "/api/images/"+a.ID+"/content", nil, "")
	wantStatus(t, rec.Code, http.StatusNotFound)
	if !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("content of an expired image: %s", rec.Body.String())
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("the sweep removed a file omo did not write: %v", err)
	}
	if s.imageStray.Load() != 1 {
		t.Fatalf("stray count %d", s.imageStray.Load())
	}
	if got := s.sweepImages(ctx, now.Add(61*24*time.Hour)); got != 0 {
		t.Fatalf("second sweep expired %d", got)
	}
	kinds := imageEventKinds(t, s)
	if kinds[len(kinds)-1] != api.KindImageExpired {
		t.Fatalf("events %v", kinds)
	}
}
