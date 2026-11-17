package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// eventIDOf parses the Omo-Event-Id header of resp.
func eventIDOf(t *testing.T, resp *http.Response) int64 {
	t.Helper()
	v := resp.Header.Get(eventIDHeader)
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		t.Fatalf("%s = %q", eventIDHeader, v)
	}
	return id
}

// lastEventOfKind returns the id of the newest event of kind.
func lastEventOfKind(t *testing.T, s *Server, kind string) int64 {
	t.Helper()
	evs, err := db.EventsSince(s.db, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, e := range evs {
		if e.Kind == kind {
			id = e.ID
		}
	}
	if id == 0 {
		t.Fatalf("no %s event", kind)
	}
	return id
}

func TestEventIDOnCreateEqualsTicketCreatedEvent(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	b := newBrowser(t, s)
	resp := asUser(b, s, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{m.project}})
	wantStatus(t, resp.StatusCode, http.StatusCreated)
	if got, want := eventIDOf(t, resp), lastEventID(t, s); got != want {
		t.Fatalf("%s = %d, want %d", eventIDHeader, got, want)
	}
	if got, want := eventIDOf(t, resp), lastEventOfKind(t, s, api.KindTicketCreated); got != want {
		t.Fatalf("%s = %d, want the ticket.created event %d", eventIDHeader, got, want)
	}
}

func TestEventIDOnMoveCoversMovedEvent(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	n := m.create()
	b := newBrowser(t, s)
	resp := asUser(b, s, "POST", fmt.Sprintf("/api/tickets/%d/move", n), api.MoveRequest{Column: api.ColumnReady})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got, want := eventIDOf(t, resp), lastEventOfKind(t, s, api.KindTicketMoved); got < want {
		t.Fatalf("%s = %d, below the ticket.moved event %d", eventIDHeader, got, want)
	}
}

func TestEventIDOnProjectAdd(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	s := newTestServer(t)
	b := newBrowser(t, s)
	resp := asUser(b, s, "POST", "/api/projects", api.CreateProject{Path: newRepo(t), Name: "demo"})
	wantStatus(t, resp.StatusCode, http.StatusCreated)
	// The settings event is written by a background worker; the header must
	// already cover it without the test waiting for the worker.
	got := eventIDOf(t, resp)
	s.waitSettingsEvents(context.Background())
	if want := lastEventOfKind(t, s, api.KindProjectChanged); got < want {
		t.Fatalf("%s = %d, below the project.changed event %d", eventIDHeader, got, want)
	}
}

func TestEventIDIgnoresEventsTheStreamDoesNotDeliver(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	m.create()
	streamed := lastEventID(t, s)
	if _, err := db.AppendEvent(s.db, "plugin_run", "{}", 0, "{}"); err != nil {
		t.Fatal(err)
	}
	// A request that changes nothing reports the newest id the stream delivers.
	resp := asUser(newBrowser(t, s), s, "DELETE", "/api/projects?missing=true", nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := eventIDOf(t, resp); got != streamed {
		t.Fatalf("%s = %d, want %d", eventIDHeader, got, streamed)
	}
}

func TestNoEventIDOnGetOrError(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	n := m.create()
	b := newBrowser(t, s)
	resp := asUser(b, s, "GET", "/api/tickets", nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if v := resp.Header.Get(eventIDHeader); v != "" {
		t.Fatalf("GET carries %s = %q", eventIDHeader, v)
	}
	resp = asUser(b, s, "POST", fmt.Sprintf("/api/tickets/%d/move", n), api.MoveRequest{Column: api.ColumnDone})
	wantStatus(t, resp.StatusCode, http.StatusConflict)
	if v := resp.Header.Get(eventIDHeader); v != "" {
		t.Fatalf("refused move carries %s = %q", eventIDHeader, v)
	}
}

func TestEventIDOnSocketListener(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	rec := socketDo(t, s, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "w", Projects: []string{m.project}}, "")
	wantStatus(t, rec.Code, http.StatusCreated)
	if got, want := rec.Header().Get(eventIDHeader), strconv.FormatInt(lastEventID(t, s), 10); got != want {
		t.Fatalf("%s = %q, want %q", eventIDHeader, got, want)
	}
}

func TestEventStreamStillFlushesThroughWrapper(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	s := newTestServer(t)
	m := newTicketMaker(t, s)
	st := openStream(t, newBrowser(t, s), s, "", nil)
	m.create()
	if ev := st.nextEvent(); ev.Kind != api.KindTicketCreated {
		t.Fatalf("event kind %q", ev.Kind)
	}
}
