package relayserver

import (
	"bytes"
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// Only the transport is replaced: routing, queues, deadlines and teardown are
// production code. All scheduling assertions wait for an exact event or Wait.
type testSocket struct {
	closed     chan struct{}
	once       sync.Once
	pings      chan context.Context
	pongs      chan struct{}
	writes     chan []byte
	blockWrite bool
}

func newTestSocket() *testSocket {
	return &testSocket{closed: make(chan struct{}), pings: make(chan context.Context, 4), pongs: make(chan struct{}, 4), writes: make(chan []byte, 32)}
}
func (c *testSocket) CloseNow() error { c.once.Do(func() { close(c.closed) }); return nil }
func (c *testSocket) Ping(ctx context.Context) error {
	c.pings <- ctx
	select {
	case <-c.pongs:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *testSocket) Write(ctx context.Context, _ websocket.MessageType, b []byte) error {
	if c.blockWrite {
		<-ctx.Done()
		return ctx.Err()
	}
	select {
	case c.writes <- b:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func fakeHost(t *testing.T, s *Server) (*peer, *testSocket) {
	t.Helper()
	c := newTestSocket()
	p := newPeer(c)
	s.mu.Lock()
	s.sockets[p] = true
	s.mu.Unlock()
	if code := s.bind(p, relayproto.Control{V: 1, Type: "host.register", HostID: "office"}); code != "" {
		t.Fatal(code)
	}
	<-p.out // registration reply
	return p, c
}

func fakeUI(t *testing.T, s *Server, h *peer) (*peer, *testSocket) {
	t.Helper()
	c := newTestSocket()
	p := newPeer(c)
	s.mu.Lock()
	s.sockets[p] = true
	s.mu.Unlock()
	if code := s.bind(p, relayproto.Control{V: 1, Type: "ui.attach", HostID: "office"}); code != "" {
		t.Fatal(code)
	}
	<-p.out
	<-h.out
	return p, c
}

func TestStaleRegistrationCannotMutateReplacement(t *testing.T) {
	s, _ := New(Config{})
	defer s.Close()
	old, _ := fakeHost(t, s)
	oldUI, _ := fakeUI(t, s, old)
	s.disconnect(old)
	fresh, _ := fakeHost(t, s)
	freshUI, _ := fakeUI(t, s, fresh)
	s.disconnect(old)
	s.disconnect(oldUI)
	if s.route(old, relayproto.Control{V: 1, Type: "ui.close", Connection: freshUI.connection}) {
		t.Fatal("departed host accepted")
	}
	if s.route(oldUI, relayproto.Control{V: 1, Type: "forward", Connection: freshUI.connection, Payload: []byte("x")}) {
		t.Fatal("departed UI accepted")
	}
	qid := bytes.Repeat([]byte{7}, 16)
	n := &notification{ctx: context.Background(), reply: make(chan []byte, 1)}
	s.mu.Lock()
	fresh.pending[string(qid)] = n
	s.mu.Unlock()
	s.cancelNotification(old, qid, n)
	if s.route(old, relayproto.Control{V: 1, Type: "notification.reply", QID: qid, Body: []byte("foreign registration")}) {
		t.Fatal("stale registration answered replacement GET")
	}
	s.route(fresh, relayproto.Control{V: 1, Type: "notification.reply", QID: qid, Body: []byte("current registration")})
	if b := <-n.reply; string(b) != "current registration" {
		t.Fatalf("wrong registration response: %q", b)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hosts["office"] != fresh || fresh.uis[string(freshUI.connection)] != freshUI || freshUI.dead {
		t.Fatal("stale callback changed replacement")
	}
}

func TestKeepaliveExpiry(t *testing.T) {
	for _, role := range []string{"host", "ui"} {
		t.Run(role, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, _ := New(Config{})
				defer s.Close()
				h, hc := fakeHost(t, s)
				u, uc := fakeUI(t, s, h)
				p, c := h, hc
				if role == "ui" {
					p, c = u, uc
				}
				done := make(chan struct{})
				go func() { defer close(done); p.keepalive(s) }()
				synctest.Wait()
				time.Sleep(20 * time.Second) // virtual time
				ping := <-c.pings
				if ping.Err() != nil {
					t.Fatal("Ping already expired")
				}
				// Start this GET after the Ping, so it is still pending at Pong
				// expiry. Its independent 30s timeout cannot mask host cleanup.
				w := httptest.NewRecorder()
				pollDone := make(chan struct{})
				go func() { defer close(pollDone); s.ServeHTTP(w, pollRequest(context.Background(), "http://relay")) }()
				synctest.Wait()
				<-h.out
				time.Sleep(10 * time.Second) // virtual matching-Pong deadline
				<-done
				<-c.closed
				s.mu.Lock()
				removed := p.dead && (role != "host" || s.hosts["office"] == nil)
				uiDead := u.dead
				s.mu.Unlock()
				if !removed || !uiDead {
					t.Fatal("keepalive retained scoped routing")
				}
				if role == "host" {
					<-uc.closed
					<-pollDone
					if w.Code != 204 {
						t.Fatal(w.Code)
					}
				} else {
					closed, _ := relayproto.DecodeControl(<-h.out)
					if closed.Type != "ui.closed" {
						t.Fatal(closed.Type)
					}
					s.disconnect(h)
					<-pollDone
				}
			})
		})
	}
}

func TestKeepalivePongsAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := New(Config{})
		h, c := fakeHost(t, s)
		done := make(chan struct{})
		go func() { defer close(done); h.keepalive(s) }()
		synctest.Wait()
		for range 2 {
			time.Sleep(20 * time.Second) // virtual interval, matched by the event below
			<-c.pings
			c.pongs <- struct{}{}
			synctest.Wait()
			s.mu.Lock()
			live := s.hosts["office"] == h
			s.mu.Unlock()
			if !live {
				t.Fatal("healthy host removed")
			}
		}
		s.Close()
		<-done
	})
}

func TestKeepaliveDeadlineStartsAtRegistration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := New(Config{})
		defer s.Close()
		h, c := fakeHost(t, s)
		time.Sleep(5 * time.Second) // virtual scheduling delay before goroutine starts
		done := make(chan struct{})
		go func() { defer close(done); h.keepalive(s) }()
		synctest.Wait()
		time.Sleep(15 * time.Second) // first Ping must be due at registration + 20s
		synctest.Wait()
		select {
		case <-c.pings:
		default:
			t.Fatal("Ping deadline moved with goroutine scheduling")
		}
		s.Close()
		<-done
	})
}

func TestBoundedWriterShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := New(Config{})
		defer s.Close()
		h, _ := fakeHost(t, s)
		u, c := fakeUI(t, s, h)
		c.blockWrite = true
		done := make(chan struct{})
		go func() { defer close(done); u.writeLoop(s) }()
		m := relayproto.Control{V: 1, Type: "forward", Connection: u.connection, Payload: []byte("opaque")}
		s.route(h, m)
		synctest.Wait() // writer is now blocked in its first Write
		for range writerQueueSize + 1 {
			s.route(h, m)
		}
		<-done
		<-c.closed
		s.mu.Lock()
		hostLive := s.hosts["office"] == h
		uiGone := h.uis[string(u.connection)] == nil
		s.mu.Unlock()
		if !hostLive || !uiGone {
			t.Fatal("overflow closed wrong connection")
		}
		if m, _ := relayproto.DecodeControl(<-h.out); m.Type != "ui.closed" {
			t.Fatal(m.Type)
		}
	})
}

func TestHostQueueOverflowCancelsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := New(Config{})
		defer s.Close()
		h, c := fakeHost(t, s)
		u, uc := fakeUI(t, s, h)
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { defer close(done); s.ServeHTTP(w, pollRequest(context.Background(), "http://relay")) }()
		synctest.Wait()
		for range writerQueueSize {
			s.route(u, relayproto.Control{V: 1, Type: "forward", Connection: u.connection, Payload: []byte("opaque")})
		}
		<-c.closed
		<-uc.closed
		<-done
		if w.Code != 204 {
			t.Fatal(w.Code)
		}
	})
}
