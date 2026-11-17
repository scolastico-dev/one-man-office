package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/images"
)

// terminalUpload sends an image dropped into a terminal through the socket
// as session ("" = user).
func terminalUpload(t *testing.T, s *Server, name string, data []byte, session string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest("POST", "http://omo/api/terminal-images", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(ImageNameHeader, url.PathEscape(name))
	if session != "" {
		req.Header.Set(SessionHeader, session)
	}
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	return rec
}

// The user's upload is stored in the terminal folder of the images folder;
// the answer names its absolute path and no record is made.
func TestTerminalImageUpload(t *testing.T) {
	s := newTestServer(t)
	data := testPNG(t)
	rec := terminalUpload(t, s, "../Screen shot.png", data, "")
	wantStatus(t, rec.Code, http.StatusCreated)
	got := decodeBody[api.TerminalImage](t, rec.Body)
	dir := filepath.Join(s.opts.Layout.Images, images.TerminalFolder)
	if filepath.Dir(got.Path) != dir || !filepath.IsAbs(got.Path) || filepath.Ext(got.Path) != ".png" {
		t.Fatalf("path %q, want a .png file in %s", got.Path, dir)
	}
	if got.Name != "Screen shot.png" || got.ContentType != images.TypePNG || got.Size != int64(len(data)) {
		t.Fatalf("answer %+v", got)
	}
	stored, err := os.ReadFile(got.Path)
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatalf("stored file: %v", err)
	}
	if known, err := s.board.ImageFileNames(t.Context(), board.System); err != nil || len(known) != 0 {
		t.Fatalf("records: %v, %v", known, err)
	}
}

func TestTerminalImageRefusals(t *testing.T) {
	s := newTestServer(t)
	rec := terminalUpload(t, s, "notes.txt", []byte("just text"), "")
	wantStatus(t, rec.Code, http.StatusUnprocessableEntity)
	rec = terminalUpload(t, s, "empty.png", nil, "")
	wantStatus(t, rec.Code, http.StatusUnprocessableEntity)
	big := append(testPNG(t), make([]byte, images.MaxBytes)...)
	rec = terminalUpload(t, s, "big.png", big, "")
	wantStatus(t, rec.Code, http.StatusRequestEntityTooLarge)
	// Agents have the terminals of the user only through the user.
	sess := liveSession(t, s, api.RoleDeveloper)
	rec = terminalUpload(t, s, "a.png", testPNG(t), sess.ID)
	wantStatus(t, rec.Code, http.StatusForbidden)
	entries, _ := os.ReadDir(filepath.Join(s.opts.Layout.Images, images.TerminalFolder))
	if len(entries) != 0 {
		t.Fatalf("refused uploads left %d files", len(entries))
	}
}

// The upload needs the raw content type like the ticket upload.
func TestTerminalImageNeedsOctetStream(t *testing.T) {
	s := newTestServer(t)
	req, err := http.NewRequest("POST", "http://omo/api/terminal-images", bytes.NewReader(testPNG(t)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	if rec.Code == http.StatusCreated {
		t.Fatal("an upload as application/json was taken")
	}
}

// Terminal images go images.keep_days_after_done days after their upload;
// 0 days keeps them.
func TestTerminalImageSweep(t *testing.T) {
	s := newTestServer(t)
	rec := terminalUpload(t, s, "a.png", testPNG(t), "")
	wantStatus(t, rec.Code, http.StatusCreated)
	im := decodeBody[api.TerminalImage](t, rec.Body)
	ctx := t.Context()
	now := time.Now()
	s.sweepImages(ctx, now.Add(59*24*time.Hour))
	if _, err := os.Stat(im.Path); err != nil {
		t.Fatalf("gone after 59 days: %v", err)
	}
	setKeepDays(t, s, 0)
	s.sweepImages(ctx, now.Add(5000*24*time.Hour))
	if _, err := os.Stat(im.Path); err != nil {
		t.Fatalf("gone with keep for ever: %v", err)
	}
	setKeepDays(t, s, 60)
	s.sweepImages(ctx, now.Add(61*24*time.Hour))
	if _, err := os.Stat(im.Path); !os.IsNotExist(err) {
		t.Fatalf("still there after 61 days: %v", err)
	}
}

func TestTerminalImageRouteInTable(t *testing.T) {
	s := newTestServer(t)
	for _, row := range s.routeTable {
		if row.pattern == "POST /api/terminal-images" {
			if row.access != userOnly {
				t.Fatalf("access %s, want user only", row.access)
			}
			return
		}
	}
	t.Fatal("POST /api/terminal-images is not in the route table")
}
