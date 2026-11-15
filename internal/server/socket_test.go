package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
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

// leftoverSocket creates an omo-sock-* folder in the temporary directory and
// links it from the run folder of l, as a run of omo would.
func leftoverSocket(t *testing.T, l globalhome.Layout) (dir, sock string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("named pipes leave no folder")
	}
	dir, err := os.MkdirTemp("", "omo-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock = filepath.Join(dir, "omo.sock")
	if err := os.Symlink(sock, filepath.Join(l.Run, "omo.sock")); err != nil {
		t.Fatal(err)
	}
	return dir, sock
}

func TestStaleSocketDirKeptWhileServerAnswers(t *testing.T) {
	l := testHome(t)
	dir, sock := leftoverSocket(t, l)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	removeStaleSocketDir(l.Run)
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("live socket removed: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("live socket folder removed: %v", err)
	}
}

func TestStaleSocketDirRemovedWhenDead(t *testing.T) {
	l := testHome(t)
	dir, sock := leftoverSocket(t, l)
	// A socket file nobody listens on, as a crash leaves it.
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	removeStaleSocketDir(l.Run)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dead leftover kept: %v", err)
	}
}

func TestSocketMeForAgentWithoutTicket(t *testing.T) {
	s := newTestServer(t)
	sess := liveSession(t, s, api.RoleCEO)
	rec := socketDo(t, s, "GET", "/api/me", nil, sess.ID)
	wantStatus(t, rec.Code, http.StatusOK)
	me := decodeBody[api.Me](t, rec.Body)
	if me.Actor.Role != api.RoleCEO || me.Actor.Session != sess.ID || me.Ticket != nil {
		t.Fatalf("me %+v", me)
	}
}

func TestSocketServerInfo(t *testing.T) {
	s := newTestServer(t)
	s.mu.Lock()
	s.address = "http://omo.localhost:1/"
	s.mu.Unlock()
	agent := liveSession(t, s, api.RoleDeveloper)
	for name, session := range map[string]string{"user": "", "agent": agent.ID} {
		rec := socketDo(t, s, "GET", "/api/server", nil, session)
		wantStatus(t, rec.Code, http.StatusOK)
		info := decodeBody[api.ServerInfo](t, rec.Body)
		if info.Version != "test" || info.Paused || info.Address != "http://omo.localhost:1/" || info.StartedAt.IsZero() {
			t.Fatalf("%s: info %+v", name, info)
		}
	}
}
