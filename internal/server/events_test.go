package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// streamTimeout bounds every wait for a stream frame.
const streamTimeout = 10 * time.Second

// frame is one parsed server-sent event block.
type frame struct {
	id      string
	event   string
	data    string
	comment string
	retry   string
}

// stream reads frames of an open event stream in the background.
type stream struct {
	t      *testing.T
	resp   *http.Response
	frames chan frame
	done   chan struct{} // closed when the body ends
}

func openStream(t *testing.T, b *browser, s *Server, query string, hdr map[string]string) *stream {
	t.Helper()
	h := bearer(s)
	for k, v := range hdr {
		h[k] = v
	}
	resp := b.do("GET", "/api/events"+query, nil, h)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q", ct)
	}
	st := &stream{t: t, resp: resp, frames: make(chan frame, 4096), done: make(chan struct{})}
	go st.read()
	return st
}

func (st *stream) read() {
	defer close(st.done)
	r := bufio.NewReader(st.resp.Body)
	var f frame
	empty := true
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			if !empty {
				st.frames <- f
			}
			f, empty = frame{}, true
			continue
		}
		empty = false
		if c, ok := strings.CutPrefix(line, ":"); ok {
			f.comment = strings.TrimSpace(c)
			continue
		}
		k, v, _ := strings.Cut(line, ": ")
		switch k {
		case "id":
			f.id = v
		case "event":
			f.event = v
		case "data":
			f.data = v
		case "retry":
			f.retry = v
		}
	}
}

// next returns the next frame that is not the retry hint or a ping.
func (st *stream) next() frame {
	st.t.Helper()
	deadline := time.After(streamTimeout)
	for {
		select {
		case f := <-st.frames:
			if f.retry != "" || f.comment != "" {
				continue
			}
			return f
		case <-st.done:
			select {
			case f := <-st.frames:
				if f.retry != "" || f.comment != "" {
					continue
				}
				return f
			default:
			}
			st.t.Fatal("stream ended while waiting for a frame")
		case <-deadline:
			st.t.Fatal("timed out waiting for a stream frame")
		}
	}
}

// nextEvent returns the next event frame decoded.
func (st *stream) nextEvent() api.Event {
	st.t.Helper()
	f := st.next()
	var ev api.Event
	if err := json.Unmarshal([]byte(f.data), &ev); err != nil {
		st.t.Fatalf("decode %q: %v", f.data, err)
	}
	if f.id != strconv.FormatInt(ev.ID, 10) || f.event != ev.Kind {
		st.t.Fatalf("frame id %q event %q does not match data %+v", f.id, f.event, ev)
	}
	return ev
}

func (st *stream) close() { st.resp.Body.Close() }

// waitEnded waits until the server ends the stream.
func (st *stream) waitEnded() {
	st.t.Helper()
	select {
	case <-st.done:
	case <-time.After(streamTimeout):
		st.t.Fatal("stream did not end")
	}
}

// waitFanOut waits until the broker has fanned out every event up to id, so
// a subscriber opened after it sees nothing older live.
func waitFanOut(t *testing.T, s *Server, id int64) {
	t.Helper()
	deadline := time.Now().Add(streamTimeout)
	for s.events.sent() < id {
		if time.Now().After(deadline) {
			t.Fatalf("broker stuck at %d, want %d", s.events.sent(), id)
		}
		s.events.Wake()
		time.Sleep(5 * time.Millisecond)
	}
}

// ticketMaker creates tasks in one project of s.
type ticketMaker struct {
	t       *testing.T
	s       *Server
	project string
}

func newTicketMaker(t *testing.T, s *Server) *ticketMaker {
	t.Helper()
	p, err := s.settings.AddProject(newRepo(t), "")
	if err != nil {
		t.Fatal(err)
	}
	return &ticketMaker{t: t, s: s, project: p.ID}
}

func (m *ticketMaker) create() int {
	m.t.Helper()
	tk, err := m.s.board.Create(context.Background(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{m.project}})
	if err != nil {
		m.t.Fatal(err)
	}
	return tk.Number
}

func lastEventID(t *testing.T, s *Server) int64 {
	t.Helper()
	id, err := db.LastEventID(s.db)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStreamDeliversLiveEventsInOrder(t *testing.T) {
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	st := openStream(t, newBrowser(t, s), s, "", nil)
	var tickets []int
	for range 3 {
		tickets = append(tickets, m.create())
	}
	var prev int64
	for i := range 3 {
		ev := st.nextEvent()
		if ev.Kind != api.KindTicketCreated {
			t.Fatalf("event %d kind %q", i, ev.Kind)
		}
		if ev.ID <= prev {
			t.Fatalf("ids not increasing: %d after %d", ev.ID, prev)
		}
		prev = ev.ID
		if ev.Ticket == nil || *ev.Ticket != tickets[i] {
			t.Fatalf("event %d ticket %v, want %d", i, ev.Ticket, tickets[i])
		}
		if ev.Actor.Role != api.RoleUser || ev.At.IsZero() {
			t.Fatalf("event %d actor %+v at %v", i, ev.Actor, ev.At)
		}
		var data api.TicketCreatedData
		if err := json.Unmarshal(ev.Data, &data); err != nil || data.Ticket.Number != tickets[i] {
			t.Fatalf("event %d data %s: %v", i, ev.Data, err)
		}
	}
}

func TestStreamResumesWithoutGapOrDuplicate(t *testing.T) {
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	b := newBrowser(t, s)
	st := openStream(t, b, s, "", nil)
	m.create()
	m.create()
	first, second := st.nextEvent(), st.nextEvent()
	if second.ID <= first.ID {
		t.Fatalf("ids %d, %d", first.ID, second.ID)
	}
	st.close()

	var want []int
	for range 3 {
		want = append(want, m.create())
	}
	st = openStream(t, b, s, "", map[string]string{"Last-Event-ID": strconv.FormatInt(second.ID, 10)})
	prev := second.ID
	for i := range 3 {
		ev := st.nextEvent()
		if ev.ID <= prev || ev.Kind != api.KindTicketCreated || *ev.Ticket != want[i] {
			t.Fatalf("event %d: id %d (after %d) kind %q ticket %v, want ticket %d", i, ev.ID, prev, ev.Kind, ev.Ticket, want[i])
		}
		prev = ev.ID
	}
	// The next event is the next change: nothing was repeated in between.
	n := m.create()
	if ev := st.nextEvent(); ev.ID <= prev || *ev.Ticket != n {
		t.Fatalf("after replay got id %d ticket %v, want ticket %d", ev.ID, ev.Ticket, n)
	}
}

func TestStreamResumesFromSinceQuery(t *testing.T) {
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	since := lastEventID(t, s)
	n := m.create()
	st := openStream(t, newBrowser(t, s), s, "?since="+strconv.FormatInt(since, 10), nil)
	if ev := st.nextEvent(); *ev.Ticket != n {
		t.Fatalf("ticket %v, want %d", ev.Ticket, n)
	}
}

func TestStreamSendsResyncWhenTooFarBehind(t *testing.T) {
	s := newTestServer(t)
	since := lastEventID(t, s)
	var last int64
	for range 600 {
		id, err := db.AppendEvent(s.db, api.KindServerPaused, `{"role":"user"}`, 0, `{"paused":true}`)
		if err != nil {
			t.Fatal(err)
		}
		last = id
	}
	st := openStream(t, newBrowser(t, s), s, "?since="+strconv.FormatInt(since, 10), nil)
	f := st.next()
	if f.event != "resync" {
		t.Fatalf("first frame %+v, want resync", f)
	}
	var data api.ResyncData
	if err := json.Unmarshal([]byte(f.data), &data); err != nil || data.Latest != last {
		t.Fatalf("resync data %q, want latest %d", f.data, last)
	}
	id, err := db.AppendEvent(s.db, api.KindServerPaused, `{"role":"user"}`, 0, `{"paused":false}`)
	if err != nil {
		t.Fatal(err)
	}
	s.events.Wake()
	if ev := st.nextEvent(); ev.ID != id {
		t.Fatalf("after resync got id %d, want %d", ev.ID, id)
	}
}

func TestStreamResyncsOnUnknownFutureID(t *testing.T) {
	s := newTestServer(t)
	latest := lastEventID(t, s)
	st := openStream(t, newBrowser(t, s), s, "?since="+strconv.FormatInt(latest+100, 10), nil)
	f := st.next()
	var data api.ResyncData
	if f.event != "resync" || json.Unmarshal([]byte(f.data), &data) != nil || data.Latest != latest {
		t.Fatalf("frame %+v, want resync latest %d", f, latest)
	}
}

func TestStreamRejectsBadResumePoint(t *testing.T) {
	s := newTestServer(t)
	resp := newBrowser(t, s).do("GET", "/api/events?since=abc", nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
	if code := errorCode(t, resp.Body); code != "bad_request" {
		t.Fatalf("code %q", code)
	}
}

func TestPluginAuditEventsAreNotStreamed(t *testing.T) {
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	st := openStream(t, newBrowser(t, s), s, "", nil)
	if _, err := db.AppendEvent(s.db, "plugin_manual_requested", "cli", 0, "requested"); err != nil {
		t.Fatal(err)
	}
	s.events.Wake()
	n := m.create()
	ev := st.nextEvent()
	if ev.Kind != api.KindTicketCreated || *ev.Ticket != n {
		t.Fatalf("got %q ticket %v, want ticket.created for %d", ev.Kind, ev.Ticket, n)
	}
}

func TestStreamRequiresAuth(t *testing.T) {
	s := newTestServer(t)
	resp := newBrowser(t, s).do("GET", "/api/events", nil, nil)
	wantStatus(t, resp.StatusCode, http.StatusUnauthorized)
	if code := errorCode(t, resp.Body); code != "unauthorized" {
		t.Fatalf("code %q", code)
	}
}

func TestStreamSendsRetryAndPing(t *testing.T) {
	s := newTestServer(t)
	s.events.pingInterval = 10 * time.Millisecond // before any handler goroutine exists
	st := openStream(t, newBrowser(t, s), s, "", nil)
	deadline := time.After(streamTimeout)
	var retry, ping bool
	for !retry || !ping {
		select {
		case f := <-st.frames:
			retry = retry || f.retry == "3000"
			ping = ping || f.comment == "ping"
		case <-deadline:
			t.Fatalf("retry %v ping %v", retry, ping)
		}
	}
}

func TestStreamEndsOnServerClose(t *testing.T) {
	s := newTestServer(t)
	st := openStream(t, newBrowser(t, s), s, "", nil)
	s.Close()
	st.waitEnded()
}

func TestStreamOverSocket(t *testing.T) {
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	req := newRequest(t, "GET", "http://omo/api/events", nil).WithContext(ctx)
	rec := &pipeRecorder{header: http.Header{}, w: pw}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.SocketHandler().ServeHTTP(rec, req)
		pw.Close()
	}()
	st := &stream{t: t, resp: &http.Response{Body: pr}, frames: make(chan frame, 64), done: make(chan struct{})}
	go st.read()
	n := m.create()
	if ev := st.nextEvent(); *ev.Ticket != n {
		t.Fatalf("ticket %v, want %d", ev.Ticket, n)
	}
	cancel()
	pr.Close()
	<-done
}

// pipeRecorder is a minimal streaming ResponseWriter.
type pipeRecorder struct {
	header http.Header
	w      io.Writer
}

func (p *pipeRecorder) Header() http.Header         { return p.header }
func (p *pipeRecorder) WriteHeader(int)             {}
func (p *pipeRecorder) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeRecorder) Flush()                      {}

func TestStreamIsUserOnly(t *testing.T) {
	s := newTestServer(t)
	m := newTicketMaker(t, s)

	// The user gets the stream over TCP.
	st := openStream(t, newBrowser(t, s), s, "", nil)
	n := m.create()
	if ev := st.nextEvent(); *ev.Ticket != n {
		t.Fatalf("ticket %v, want %d", ev.Ticket, n)
	}
	st.close()

	// An agent session on the socket is forbidden.
	sess := liveSession(t, s, api.RoleDeveloper)
	rec := socketDo(t, s, "GET", "/api/events", nil, sess.ID)
	wantStatus(t, rec.Code, http.StatusForbidden)
	if code := errorCode(t, rec.Body); code != api.CodeForbidden {
		t.Fatalf("code %q", code)
	}
}

func TestStreamAnswers503WhenBrokerClosed(t *testing.T) {
	s := newTestServer(t)
	b := newBrowser(t, s)
	hdr := bearer(s)
	s.Close()
	resp := b.do("GET", "/api/events", nil, hdr)
	wantStatus(t, resp.StatusCode, http.StatusServiceUnavailable)
	if code := errorCode(t, resp.Body); code != api.CodeUnavailable {
		t.Fatalf("code %q", code)
	}
}
