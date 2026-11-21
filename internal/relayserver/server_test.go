package relayserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

func testRelay(t *testing.T, cfg Config) (*Server, string) {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); h.Close() })
	return s, h.URL
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	return ctx
}

func dial(t *testing.T, addr string, header http.Header) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(testContext(t), "ws"+strings.TrimPrefix(addr, "http")+"/v1/ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(relayproto.MaxControlSize)
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func send(t *testing.T, c *websocket.Conn, m relayproto.Control) {
	t.Helper()
	m.V = 1
	b, err := relayproto.EncodeControl(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Write(testContext(t), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func receive(t *testing.T, c *websocket.Conn, kind string) relayproto.Control {
	t.Helper()
	typ, b, err := c.Read(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	m, err := relayproto.DecodeControl(b)
	if err != nil || typ != websocket.MessageText || m.Type != kind {
		t.Fatalf("got %s %q: %v; want %s", typ, b, err, kind)
	}
	return m
}

func host(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	c := dial(t, addr, nil)
	send(t, c, relayproto.Control{Type: "host.register", HostID: "office"})
	m := receive(t, c, "host.registered")
	if m.HostID != "office" || len(m.Registration) != 16 {
		t.Fatalf("registration: %+v", m)
	}
	return c
}

func attach(t *testing.T, addr string, h *websocket.Conn) (*websocket.Conn, []byte) {
	t.Helper()
	c := dial(t, addr, nil)
	send(t, c, relayproto.Control{Type: "ui.attach", HostID: "office"})
	m := receive(t, c, "ui.attached")
	opened := receive(t, h, "ui.open")
	if !bytes.Equal(m.Connection, opened.Connection) {
		t.Fatal("different UI handles")
	}
	return c, m.Connection
}

func TestAdmission(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			cfg := Config{}
			if enabled {
				cfg.PasswordFile = filepath.Join(t.TempDir(), "secret")
				if err := os.WriteFile(cfg.PasswordFile, []byte("s3cret\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, addr := testRelay(t, cfg)
			for _, password := range []string{"", "wrong", "s3cret"} {
				for _, path := range []string{"/v1/ws", "/notifications/office/" + strings.Repeat("a", 96)} {
					req, _ := http.NewRequestWithContext(testContext(t), "GET", addr+path, nil)
					if password != "" {
						req.SetBasicAuth("relay", password)
					}
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					resp.Body.Close()
					if resp.Header.Get("Cache-Control") != "no-store" {
						t.Fatal("missing no-store")
					}
					denied := enabled && password != "s3cret"
					if (resp.StatusCode == 401) != denied {
						t.Fatalf("%s admission enabled=%v password=%q: %d", path, enabled, password, resp.StatusCode)
					}
					if denied && resp.Header.Get("WWW-Authenticate") == "" {
						t.Fatal("missing Basic challenge")
					}
				}
			}
			// Successful admission must actually permit an upgrade and registration.
			req, _ := http.NewRequest("GET", addr, nil)
			req.SetBasicAuth("relay", "s3cret")
			c := dial(t, addr, req.Header)
			send(t, c, relayproto.Control{Type: "host.register", HostID: "office"})
			receive(t, c, "host.registered")
		})
	}
}

func TestRoutingAndCollision(t *testing.T) {
	_, addr := testRelay(t, Config{})
	h := host(t, addr)
	loser := dial(t, addr, nil)
	send(t, loser, relayproto.Control{Type: "host.register", HostID: "office"})
	if m := receive(t, loser, "error"); m.Code != "id_in_use" {
		t.Fatalf("collision: %+v", m)
	}
	if _, _, err := loser.Read(testContext(t)); err == nil {
		t.Fatal("loser still open")
	}
	u1, id1 := attach(t, addr, h)
	u2, id2 := attach(t, addr, h)
	if bytes.Equal(id1, id2) {
		t.Fatal("reused UI handle")
	}
	payload := []byte{0, 255, 1} // The relay must never parse endpoint content.
	for _, p := range []struct {
		c  *websocket.Conn
		id []byte
	}{{u1, id1}, {u2, id2}} {
		send(t, p.c, relayproto.Control{Type: "forward", Connection: p.id, Payload: payload})
		m := receive(t, h, "forward")
		if !bytes.Equal(m.Connection, p.id) || !bytes.Equal(m.Payload, payload) {
			t.Fatal("changed forward")
		}
		send(t, h, m)
		receive(t, p.c, "forward")
	}
	// A UI cannot impersonate a different attached socket.
	send(t, u1, relayproto.Control{Type: "forward", Connection: id2, Payload: payload})
	if _, _, err := u1.Read(testContext(t)); err == nil {
		t.Fatal("foreign UI handle accepted")
	}
	if m := receive(t, h, "ui.closed"); !bytes.Equal(m.Connection, id1) {
		t.Fatal("wrong UI removed")
	}
	send(t, u2, relayproto.Control{Type: "forward", Connection: id2, Payload: payload})
	receive(t, h, "forward")
	send(t, h, relayproto.Control{Type: "ui.close", Connection: id2})
	if _, _, err := u2.Read(testContext(t)); err == nil {
		t.Fatal("host did not close UI")
	}
}

func TestHostLossAndRestart(t *testing.T) {
	s, addr := testRelay(t, Config{})
	h := host(t, addr)
	u, _ := attach(t, addr, h)
	h.CloseNow()
	if _, _, err := u.Read(testContext(t)); err == nil {
		t.Fatal("UI survived host loss")
	}
	h = host(t, addr)
	u, _ = attach(t, addr, h)
	s.Close()
	if _, _, err := u.Read(testContext(t)); err == nil {
		t.Fatal("UI survived shutdown")
	}
	_, fresh := testRelay(t, Config{})
	orphan := dial(t, fresh, nil)
	send(t, orphan, relayproto.Control{Type: "ui.attach", HostID: "office"})
	if m := receive(t, orphan, "error"); m.Code != "host_offline" {
		t.Fatalf("restart retained host: %+v", m)
	}
	host(t, fresh)
}

func TestWireLimitsAndRoles(t *testing.T) {
	for _, test := range []struct {
		name          string
		first, second string
	}{
		{"inner-size", `{"v":1,"type":"host.register","host_id":"office"}`, fmt.Sprintf(`{"v":1,"type":"forward","connection":"AAAAAAAAAAAAAAAAAAAAAA","payload":"%s"}`, base64.RawURLEncoding.EncodeToString(make([]byte, relayproto.MaxInnerSize+1)))},
		{"outer-size", strings.Repeat(" ", relayproto.MaxControlSize+1), ""},
		{"reply-size", `{"v":1,"type":"host.register","host_id":"office"}`, fmt.Sprintf(`{"v":1,"type":"notification.reply","qid":"AAAAAAAAAAAAAAAAAAAAAA","body":"%s"}`, base64.RawURLEncoding.EncodeToString(make([]byte, relayproto.MaxNotificationSize+1)))},
		{"role-switch", `{"v":1,"type":"host.register","host_id":"office"}`, `{"v":1,"type":"ui.attach","host_id":"office"}`},
		{"duplicate-field", `{"v":1,"type":"host.register","host_id":"office","host_id":"other"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, addr := testRelay(t, Config{})
			c := dial(t, addr, nil)
			if err := c.Write(testContext(t), websocket.MessageText, []byte(test.first)); err != nil && test.second != "" {
				t.Fatal(err)
			}
			if test.second != "" {
				receive(t, c, "host.registered")
				// Oversize input may be refused while still being written. In
				// either case, require the socket to be closed in the assertion.
				_ = c.Write(testContext(t), websocket.MessageText, []byte(test.second))
			}
			if _, _, err := c.Read(testContext(t)); err == nil {
				t.Fatal("invalid wire or role change accepted")
			}
		})
	}
}

func TestOpaqueMessageBoundaries(t *testing.T) {
	_, addr := testRelay(t, Config{})
	h := host(t, addr)
	u, id := attach(t, addr, h)
	payload := bytes.Repeat([]byte{255}, relayproto.MaxInnerSize)
	send(t, u, relayproto.Control{Type: "forward", Connection: id, Payload: payload})
	if got := receive(t, h, "forward"); !bytes.Equal(got.Payload, payload) {
		t.Fatal("maximum inner payload changed")
	}
	result := startPoll(t, pollRequest(testContext(t), addr))
	get := receive(t, h, "notification.get")
	body := bytes.Repeat([]byte{255}, relayproto.MaxNotificationSize)
	send(t, h, relayproto.Control{Type: "notification.reply", QID: get.QID, Body: body})
	checkPoll(t, result, 200, body)
}
