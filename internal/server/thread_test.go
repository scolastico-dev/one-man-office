package server

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// pmMentionsUser has a live pm comment "@user ..." on its own story and
// returns the story number and the comment id.
func pmMentionsUser(t *testing.T, s *Server, text string) (api.AgentSession, int, int) {
	t.Helper()
	pm := liveSession(t, s, api.RolePM)
	n := *pm.Ticket
	res := okJSON[api.CommentResult](t, s.agent(t, pm.ID), 201, "POST", tp(n)+"/comments", api.CommentRequest{Body: "@user " + text})
	return pm, n, res.Comment.ID
}

func TestThreadRoute(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	task := newTask(t, u, addProject(t, s), "thread")
	for i := 0; i < 5; i++ {
		okJSON[api.CommentResult](t, u, 201, "POST", tp(task.Number)+"/comments", api.CommentRequest{Body: fmt.Sprintf("c%d", i)})
	}
	path := tp(task.Number) + "/thread"
	full := okJSON[api.TicketThread](t, u, 200, "GET", path, nil)
	if full.More || len(full.Events) < 6 || full.Events[0].Kind != api.KindTicketCreated {
		t.Fatalf("full thread %+v", full)
	}
	for i := 1; i < len(full.Events); i++ {
		if full.Events[i].ID <= full.Events[i-1].ID {
			t.Fatalf("not ascending: %+v", full.Events)
		}
	}
	page := okJSON[api.TicketThread](t, u, 200, "GET", path+"?limit=2", nil)
	if !page.More || len(page.Events) != 2 || page.Events[1].ID != full.Events[len(full.Events)-1].ID {
		t.Fatalf("newest page %+v", page)
	}
	older := okJSON[api.TicketThread](t, u, 200, "GET", fmt.Sprintf("%s?limit=2&before=%d", path, page.Events[0].ID), nil)
	if len(older.Events) != 2 || older.Events[1].ID >= page.Events[0].ID {
		t.Fatalf("older page %+v", older)
	}
	if got := okJSON[api.TicketThread](t, u, 200, "GET", path+"?limit=0", nil); len(got.Events) != len(full.Events) {
		t.Fatalf("limit 0 should mean the default: %+v", got)
	}

	// Junk is 400, out of range is the board's validation.
	for _, q := range []string{"before=x", "before=1.5", "before=", "before=+3", "before=1&before=2", "limit=abc", "limit=1e3", "limit=0x10", "limit=%205", "before=99999999999999999999"} {
		u.fails(400, api.CodeBadRequest, "GET", path+"?"+q, nil)
	}
	u.fails(422, api.CodeValidation, "GET", path+"?limit=201", nil)
	u.fails(422, api.CodeValidation, "GET", path+"?limit=-1", nil)
	u.fails(422, api.CodeValidation, "GET", path+"?before=-1", nil)
	u.fails(404, api.CodeNotFound, "GET", "/api/tickets/9999/thread", nil)
	u.fails(400, api.CodeBadRequest, "GET", "/api/tickets/abc/thread", nil)
}

func TestNotificationRoutes(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	_, n, c1 := pmMentionsUser(t, s, "first")
	_, _, c2 := pmMentionsUser(t, s, "second")

	got := okJSON[api.Notifications](t, u, 200, "GET", "/api/notifications", nil)
	if got.Unread != 2 || got.SeenThrough != 0 || len(got.Mentions) != 2 || got.Mentions[0].Comment.ID != c2 || !got.Mentions[0].Unread {
		t.Fatalf("notifications %+v", got)
	}
	if got.Mentions[1].Comment.ID != c1 || got.Mentions[1].Title == "" || n == 0 {
		t.Fatalf("mention %+v", got.Mentions[1])
	}
	// Reading changes nothing.
	again := okJSON[api.Notifications](t, u, 200, "GET", "/api/notifications", nil)
	if again.SeenThrough != 0 || again.Unread != 2 {
		t.Fatalf("reading moved the marker: %+v", again)
	}

	st := openStream(t, newBrowser(t, s), s, "", nil)
	defer st.close()
	after := okJSON[api.Notifications](t, u, 200, "PUT", "/api/notifications/seen", api.NotificationsSeen{Through: c1})
	if after.SeenThrough != c1 || after.Unread != 1 {
		t.Fatalf("after %+v", after)
	}
	ev := st.nextEvent()
	if ev.Kind != api.KindNotificationsSeen || ev.Ticket != nil {
		t.Fatalf("event %+v", ev)
	}

	// A lower value changes nothing and emits nothing: the next frame is the
	// one produced by the later, higher mark.
	low := okJSON[api.Notifications](t, u, 200, "PUT", "/api/notifications/seen", api.NotificationsSeen{Through: 0})
	if low.SeenThrough != c1 {
		t.Fatalf("marker moved back: %+v", low)
	}
	okJSON[api.Notifications](t, u, 200, "PUT", "/api/notifications/seen", api.NotificationsSeen{Through: c2})
	next := st.nextEvent()
	if next.Kind != api.KindNotificationsSeen || next.ID <= ev.ID {
		t.Fatalf("next event %+v after %+v", next, ev)
	}
	u.fails(422, api.CodeValidation, "PUT", "/api/notifications/seen", api.NotificationsSeen{Through: c2 + 1000})
	u.fails(400, api.CodeBadRequest, "PUT", "/api/notifications/seen", map[string]any{"through": 1, "x": 2})
}

func TestSessionLogRoute(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	logs := s.opts.Layout.Logs
	logPath := filepath.Join(logs, "agent.log")
	sess := func(path string) string {
		t.Helper()
		id := newLoggedSession(t, s, path)
		return "/api/sessions/" + id + "/log"
	}
	get := func(route string) (*http.Response, string) {
		t.Helper()
		resp := u.b.do("GET", route, nil, bearer(s))
		data, err := readAll(resp)
		if err != nil {
			t.Fatal(err)
		}
		return resp, data
	}

	// Rotated segments are joined oldest first.
	for suffix, text := range map[string]string{".2": "oldest\n", ".1": "older\n", "": "newest\n"} {
		if err := os.WriteFile(logPath+suffix, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	route := sess(logPath)
	resp, body := get(route)
	if resp.StatusCode != 200 || body != "oldest\nolder\nnewest\n" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type %q", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", resp.Header)
	}
	if resp.Header.Get("Omo-Log-Truncated") != "" {
		t.Fatal("small log marked truncated")
	}

	// Markup stays text.
	if err := os.WriteFile(logPath+".2", []byte("<script>alert(1)</script>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, body = get(route)
	if !strings.HasPrefix(body, "<script>") || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("type %q body %q", resp.Header.Get("Content-Type"), body)
	}

	// The cap keeps the tail, cut at a line start, and says so.
	big := strings.Repeat("0123456789abcdef\n", (maxLogBytes/17)+1000)
	if err := os.WriteFile(logPath+".2", []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, body = get(route)
	if resp.Header.Get("Omo-Log-Truncated") != "true" || len(body) > maxLogBytes || len(body) < maxLogBytes-200 {
		t.Fatalf("truncated %q, %d bytes", resp.Header.Get("Omo-Log-Truncated"), len(body))
	}
	if !strings.HasSuffix(body, "older\nnewest\n") || !strings.HasPrefix(body, "0123456789abcdef\n") {
		t.Fatalf("tail %q head %q", body[len(body)-20:], body[:20])
	}

	// A single live file above the cap is cut the same way.
	for _, suffix := range []string{".2", ".1"} {
		os.Remove(logPath + suffix)
	}
	if err := os.WriteFile(logPath, []byte("partial-line"+big), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, body = get(route)
	if resp.Header.Get("Omo-Log-Truncated") != "true" || len(body) > maxLogBytes || !strings.HasPrefix(body, "0123456789abcdef\n") {
		t.Fatalf("single file: %q %d", resp.Header.Get("Omo-Log-Truncated"), len(body))
	}

	// A missing file is 404 with the message, not 500.
	os.Remove(logPath)
	status, data := u.do("GET", route, nil)
	if status != 404 || !strings.Contains(string(data), "The log of this session is no longer kept.") {
		t.Fatalf("missing: %d %s", status, data)
	}
	u.fails(404, api.CodeNotFound, "GET", "/api/sessions/nope/log", nil)

	// A path outside the logs folder is refused, also through a symlink,
	// and a rotated segment that is a link out is skipped.
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.log")
	os.WriteFile(secret, []byte("SECRET\n"), 0o644)
	u.fails(404, api.CodeNotFound, "GET", sess(secret), nil)
	u.fails(404, api.CodeNotFound, "GET", sess(filepath.Join(logs, "..", "secret.log")), nil)
	link := filepath.Join(logs, "link.log")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	u.fails(404, api.CodeNotFound, "GET", sess(link), nil)
	dirLink := filepath.Join(logs, "dir")
	os.Symlink(outside, dirLink)
	u.fails(404, api.CodeNotFound, "GET", sess(filepath.Join(dirLink, "secret.log")), nil)
	linked := filepath.Join(logs, "seg.log")
	os.WriteFile(linked, []byte("mine\n"), 0o644)
	os.Symlink(secret, linked+".1")
	if _, body := get(sess(linked)); body != "mine\n" {
		t.Fatalf("segment outside logs served: %q", body)
	}
	// A directory or a request-chosen path never matters: only the id is read.
	u.fails(404, api.CodeNotFound, "GET", sess(logs), nil)
	status, _ = u.do("GET", "/api/sessions/"+url.PathEscape("../../etc/passwd")+"/log", nil)
	if status != 404 {
		t.Fatalf("traversal id: %d", status)
	}
}

func TestThreadNotificationLogRoutesScope(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	dev := liveSession(t, s, api.RoleDeveloper)
	other := newTask(t, u, addProject(t, s), "elsewhere")
	d := s.agent(t, dev.ID)

	// The thread follows the read rule of the ticket.
	if status, data := d.do("GET", tp(*dev.Ticket)+"/thread", nil); status != 200 {
		t.Fatalf("own ticket thread: %d %s", status, data)
	}
	d.fails(403, api.CodeForbidden, "GET", tp(other.Number)+"/thread", nil)
	logPath := filepath.Join(s.opts.Layout.Logs, "d.log")
	os.WriteFile(logPath, []byte("x\n"), 0o644)
	route := "/api/sessions/" + newLoggedSession(t, s, logPath) + "/log"
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/notifications"},
		{"PUT", "/api/notifications/seen"},
		{"GET", route},
	} {
		var body any
		if c.method == "PUT" {
			body = api.NotificationsSeen{Through: 0}
		}
		d.fails(403, api.CodeForbidden, c.method, c.path, body)
	}
	if status, _ := u.do("GET", route, nil); status != 200 {
		t.Fatalf("user log: %d", status)
	}

	// The user's stream is the only one: an agent cannot open it.
	rec := socketDo(t, s, "GET", "/api/events", nil, dev.ID)
	wantStatus(t, rec.Code, http.StatusForbidden)
}

// newLoggedSession creates a developer session whose saved log is path and
// returns its id.
func newLoggedSession(t *testing.T, s *Server, path string) string {
	t.Helper()
	p, err := s.settings.AddProject(newRepo(t), "")
	if err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(t.Context())
	tk, err := s.board.Create(t.Context(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "logged", Projects: []string{p.ID}})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.board.CreateSession(t.Context(), board.NewSession{Role: api.RoleDeveloper, Name: "agent", Ticket: int64(tk.Number), LogPath: path})
	if err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

func readAll(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return string(data), err
}
