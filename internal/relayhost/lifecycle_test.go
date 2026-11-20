package relayhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

type pipeListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = l.conn })
	if c != nil {
		return c, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}
func (l *pipeListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}
func (l *pipeListener) Addr() net.Addr { return l.conn.LocalAddr() }

// pipeDial keeps WebSocket/network behavior real while all blocking is in-memory
// and visible to synctest. Every created server and pipe is joined by cleanup.
func pipeDial(t *testing.T, handle func(context.Context, *websocket.Conn, net.Conn)) func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return func(ctx context.Context, address string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		client, server := net.Pipe()
		listener := &pipeListener{conn: server, closed: make(chan struct{})}
		handlerDone := make(chan struct{})
		serveDone := make(chan struct{})
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(handlerDone)
			ws, e := websocket.Accept(w, r, nil)
			if e != nil {
				return
			}
			defer ws.CloseNow()
			handle(ctx, ws, server)
		})}
		go func() { defer close(serveDone); _ = srv.Serve(listener) }()
		tr := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return client, nil }}
		t.Cleanup(func() {
			client.Close()
			server.Close()
			srv.Close()
			tr.CloseIdleConnections()
			<-serveDone
			<-handlerDone
		})
		return websocket.Dial(ctx, "ws://relay.example/v1/ws", &websocket.DialOptions{HTTPClient: &http.Client{Transport: tr}, HTTPHeader: headers})
	}
}
func pipeRead(t *testing.T, ctx context.Context, ws *websocket.Conn) relayproto.Control {
	t.Helper()
	_, raw, e := ws.Read(ctx)
	if e != nil {
		t.Error(e)
		return relayproto.Control{}
	}
	m, e := relayproto.DecodeControl(raw)
	if e != nil {
		t.Error(e)
	}
	return m
}
func pipeWrite(t *testing.T, ctx context.Context, ws *websocket.Conn, m relayproto.Control) {
	t.Helper()
	raw, e := relayproto.EncodeControl(m)
	if e != nil {
		t.Error(e)
		return
	}
	if e = ws.Write(ctx, websocket.MessageText, raw); e != nil {
		t.Error(e)
	}
}

func TestCollisionBackoffKeepsSavedIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := managerForTest(t)
		calls := 0
		m.opts.Dial = pipeDial(t, func(ctx context.Context, ws *websocket.Conn, raw net.Conn) {
			r := pipeRead(t, ctx, ws)
			if r.Type != "host.register" || r.HostID != "office" {
				t.Error("wrong registration")
			}
			calls++
			pipeWrite(t, ctx, ws, relayproto.Control{V: 1, Type: "error", Code: "id_in_use"})
			<-ctx.Done()
		})
		if e := m.Connect(ConnectOptions{Origin: "https://relay.example", ClientID: "office"}); e != nil {
			t.Fatal(e)
		}
		done := make(chan struct{})
		go func() { defer close(done); m.Run(context.Background()) }()
		defer func() { m.Close(); <-done }()
		synctest.Wait()
		if calls != 1 || m.Status().State != "collision" {
			t.Fatal(calls, m.Status())
		}
		for i, seconds := range []int{1, 2, 4, 8, 16, 30, 30} {
			if m.Status().RetrySeconds != seconds {
				t.Fatal(m.Status())
			}
			time.Sleep(time.Duration(seconds) * time.Second)
			synctest.Wait()
			if calls != i+2 {
				t.Fatal("retry count", calls)
			}
		}
	})
}
func TestKeepaliveClosesHalfOpenSocketAndRedials(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := managerForTest(t)
		calls := 0
		m.opts.Dial = pipeDial(t, func(ctx context.Context, ws *websocket.Conn, raw net.Conn) {
			r := pipeRead(t, ctx, ws)
			calls++
			pipeWrite(t, ctx, ws, relayproto.Control{V: 1, Type: "host.registered", HostID: r.HostID, Registration: bytes.Repeat([]byte{1}, 16)})
			_, _ = io.Copy(io.Discard, raw) // Drain Ping bytes without replying with Pong.
		})
		if e := m.Connect(ConnectOptions{Origin: "https://relay.example", ClientID: "office"}); e != nil {
			t.Fatal(e)
		}
		done := make(chan struct{})
		go func() { defer close(done); m.Run(context.Background()) }()
		defer func() { m.Close(); <-done }()
		synctest.Wait()
		if m.Status().State != "connected" || calls != 1 {
			t.Fatal(m.Status(), calls)
		}
		time.Sleep(20 * time.Second)
		synctest.Wait()
		if calls != 1 {
			t.Fatal("redial before pong deadline")
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if m.Status().State != "retrying" {
			t.Fatal(m.Status())
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls != 2 || m.Status().State != "connected" {
			t.Fatal(m.Status(), calls)
		}
	})
}
func TestAdmissionFailureDoesNotReplaceIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, s := managerForTest(t)
		calls := 0
		m.opts.Dial = func(ctx context.Context, _ string, header http.Header) (*websocket.Conn, *http.Response, error) {
			calls++
			r := &http.Request{Header: header}
			user, pass, ok := r.BasicAuth()
			if !ok || user != "relay" || pass != "secret" {
				t.Error("admission header missing")
			}
			return nil, &http.Response{StatusCode: 401}, errors.New("denied")
		}
		secret := "secret"
		if e := m.Connect(ConnectOptions{Origin: "https://relay.example", Password: &secret}); e != nil {
			t.Fatal(e)
		}
		before, _ := s.Snapshot()
		done := make(chan struct{})
		go func() { defer close(done); m.Run(context.Background()) }()
		synctest.Wait()
		if m.Status().State != "admission_denied" || calls != 1 {
			t.Fatal(m.Status())
		}
		if e := m.Disconnect(); e != nil {
			t.Fatal(e)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		after, _ := s.Snapshot()
		if calls != 1 || after.HostID != before.HostID || !bytes.Equal(after.Seed, before.Seed) {
			t.Fatal("disabled retry or identity replacement")
		}
		m.Close()
		<-done
	})
}

func TestRevocationWaitsForAdmittedWriteCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, s := managerForTest(t)
		if e := m.Connect(ConnectOptions{Origin: "https://relay.example"}); e != nil {
			t.Fatal(e)
		}
		id, _ := relayproto.GenerateIdentity()
		ui, _ := relayproto.Fingerprint("ui", id.PublicKey())
		d := Device{UIID: ui, PublicKey: id.PublicKey(), PairEpoch: bytes.Repeat([]byte{1}, 16), NotificationRoot: bytes.Repeat([]byte{2}, 32), AID: bytes.Repeat([]byte{3}, 16), AcceptedAt: time.Now().UTC(), State: Active, Generation: 1}
		if e := s.Update(func(v *State) error { v.NextGeneration = 1; v.Devices = append(v.Devices, d); return nil }); e != nil {
			t.Fatal(e)
		}
		c := &connection{ctx: context.Background(), sendq: make(chan outbound, 1)}
		admitted := make(chan struct{})
		release := make(chan struct{})
		writerDone := make(chan struct{})
		var once sync.Once
		defer func() { once.Do(func() { close(release) }); <-writerDone }()
		go func() { defer close(writerDone); out := <-c.sendq; close(admitted); <-release; out.done <- nil }()
		callDone := make(chan error, 1)
		go func() {
			callDone <- m.WithDevice(context.Background(), ui, func(ctx context.Context, _ Device) error {
				return c.send(ctx, relayproto.Control{V: 1, Type: "notification.empty", QID: bytes.Repeat([]byte{4}, 16)})
			})
		}()
		<-admitted
		revoked := make(chan error, 1)
		go func() { revoked <- m.Unregister(ui) }()
		synctest.Wait()
		select {
		case e := <-revoked:
			t.Errorf("unregister returned before in-flight writer completed: %v", e)
			revoked <- e
		default:
		}
		once.Do(func() { close(release) })
		if e := <-revoked; e != nil {
			t.Fatal(e)
		}
		if e := <-callDone; e != nil && !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	})
}
