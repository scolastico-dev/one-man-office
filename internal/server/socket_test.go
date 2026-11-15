package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestSocketWithoutSessionIsUser(t *testing.T) {
	s := newTestServer(t)
	rec := socketDo(t, s, "GET", "/api/me", nil, "")
	wantStatus(t, rec.Code, http.StatusOK)
	me := decodeBody[api.Me](t, rec.Body)
	if me.Actor.Role != api.RoleUser || me.Ticket != nil {
		t.Fatalf("me %+v", me)
	}
}

func TestSocketSessionIsAgent(t *testing.T) {
	s := newTestServer(t)
	sess := liveSession(t, s, api.RoleDeveloper)
	rec := socketDo(t, s, "GET", "/api/me", nil, sess.ID)
	wantStatus(t, rec.Code, http.StatusOK)
	me := decodeBody[api.Me](t, rec.Body)
	if me.Actor.Role != api.RoleDeveloper || me.Actor.Session != sess.ID || me.Ticket == nil || *me.Ticket != *sess.Ticket {
		t.Fatalf("me %+v", me)
	}
}

func TestSocketRejectsEndedSession(t *testing.T) {
	s := newTestServer(t)
	sess := liveSession(t, s, api.RoleCEO)
	wantStatus(t, socketDo(t, s, "GET", "/api/me", nil, sess.ID).Code, http.StatusOK)
	if err := s.board.EndSession(context.Background(), sess.ID, "test"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{sess.ID, "0123456789abcdef0123456789abcdef"} {
		rec := socketDo(t, s, "GET", "/api/me", nil, id)
		wantStatus(t, rec.Code, http.StatusUnauthorized)
		if code := errorCode(t, rec.Body); code != api.CodeUnknownSession {
			t.Fatalf("session %s: code %q", id, code)
		}
	}
	// A present but empty header is not "no header".
	req := newRequest(t, "GET", "http://omo/api/me", nil)
	req.Header[SessionHeader] = []string{""}
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	wantStatus(t, rec.Code, http.StatusUnauthorized)
	// An ended session must not reach a user-only route either.
	wantStatus(t, socketDo(t, s, "PUT", "/api/server/paused", map[string]bool{"paused": true}, sess.ID).Code, http.StatusUnauthorized)
}

func TestAgentCannotPause(t *testing.T) {
	s := newTestServer(t)
	sess := liveSession(t, s, api.RoleDeveloper)
	rec := socketDo(t, s, "PUT", "/api/server/paused", map[string]bool{"paused": true}, sess.ID)
	wantStatus(t, rec.Code, http.StatusForbidden)
	if code := errorCode(t, rec.Body); code != api.CodeForbidden {
		t.Fatalf("code %q", code)
	}
	if paused, _ := s.board.Paused(context.Background()); paused {
		t.Fatal("agent paused the server")
	}
	// Agents may read the server info.
	wantStatus(t, socketDo(t, s, "GET", "/api/server", nil, sess.ID).Code, http.StatusOK)
}
