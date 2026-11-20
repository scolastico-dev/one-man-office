package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/relayhost"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
	"github.com/scolastico-dev/one-man-office/internal/relayserver"
)

// A test-only native protocol peer. All production startup, application HTTP,
// pairing, crypto, host lifecycle and standalone relay handlers stay real.
type relayUI struct {
	handle     []byte
	started    bool
	t          *testing.T
	ctx        context.Context
	cancel     context.CancelFunc
	ws         *websocket.Conn
	session    *relayproto.Session
	id         *relayproto.Identity
	transcript relayproto.PairTranscript
	uiID       string
	sendMu, mu sync.Mutex
	next       uint64
	streams    map[relayproto.Counter]*relayUIConn
	changed    chan struct{}
	done       chan struct{}
	err        error
}

func relayFixture(t *testing.T, runners ...agents.Runner) (*Server, string) {
	t.Helper()
	relay, e := relayserver.New(relayserver.Config{})
	if e != nil {
		t.Fatal(e)
	}
	endpoint := httptest.NewServer(relay)
	t.Cleanup(endpoint.Close)
	t.Cleanup(func() { relay.Close() })
	var s *Server
	if len(runners) == 0 {
		s = newTestServer(t)
	} else {
		old := gitVersion
		gitVersion = func() (int, int, error) { return 2, 47, nil }
		t.Cleanup(func() { gitVersion = old })
		var e error
		s, e = newServer(Options{Layout: testHome(t), Config: testConfig(), Version: "test", Runner: runners[0]}, io.Discard)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { s.Close() })
	}
	if e := s.relay.Connect(relayhost.ConnectOptions{Origin: endpoint.URL, ClientID: "office"}); e != nil {
		t.Fatal(e)
	}
	waitRelayState(t, func() bool { return s.relay.Status().State == "connected" })
	return s, endpoint.URL
}
func waitRelayState(t *testing.T, condition func() bool) {
	t.Helper()
	for !condition() {
		if t.Context().Err() != nil {
			t.Fatal(t.Context().Err())
		}
		runtime.Gosched()
	}
}
func newRelayUI(t *testing.T, s *Server, origin string, prior *relayUI, beforeAccept ...func(*relayUI)) *relayUI {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ws, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(origin, "http")+"/v1/ws", nil)
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	ws.SetReadLimit(relayproto.MaxControlSize)
	u := &relayUI{t: t, ctx: ctx, cancel: cancel, ws: ws, next: 1, streams: map[relayproto.Counter]*relayUIConn{}, changed: make(chan struct{}), done: make(chan struct{})}
	t.Cleanup(u.close)
	u.control(relayproto.Control{V: 1, Type: "ui.attach", HostID: "office"})
	attached := u.readControl()
	if attached.Type != "ui.attached" {
		t.Fatal(attached.Type)
	}
	u.handle = attached.Connection
	if prior == nil {
		if e := s.relay.PairBegin(); e != nil {
			t.Fatal(e)
		}
		u.id, e = relayproto.GenerateIdentity()
		if e != nil {
			t.Fatal(e)
		}
		pair, commit, e := relayproto.NewPairUI(relayproto.Route{Origin: origin, HostID: "office"}, u.id, nil)
		if e != nil {
			t.Fatal(e)
		}
		u.inner(commit)
		reveal, e := pair.Reveal(u.readInner())
		if e != nil {
			t.Fatal(e)
		}
		u.inner(reveal)
		u.transcript, e = pair.Transcript()
		if e != nil {
			t.Fatal(e)
		}
		ac, e := u.transcript.AuthContext()
		if e != nil {
			t.Fatal(e)
		}
		u.authenticate(ac)
		waitRelayState(t, func() bool { p, e := s.relay.PairPending(); return e == nil && p.Phase == relayproto.PairProved })
		if len(beforeAccept) > 0 {
			beforeAccept[0](u)
			return u
		}
		pin, _ := u.transcript.PIN()
		d, e := s.relay.PairAccept(u.transcript.AID, pin, nil, "test device")
		if e != nil {
			t.Fatal(e)
		}
		u.uiID = d.UIID
		accepted, e := u.session.Open(u.readInner())
		if e != nil || accepted.Type != "pair.accepted" {
			t.Fatal(accepted.Type, e)
		}
	} else {
		u.id = prior.id
		u.transcript = prior.transcript
		u.uiID = prior.uiID
		u.authenticate(relayproto.AuthContext{Route: relayproto.Route{Origin: origin, HostID: "office"}, Mode: "reconnect", PKH: u.transcript.PKH, PKU: u.id.PublicKey(), PairEpoch: u.transcript.PairEpoch})
		ready, e := u.session.Open(u.readInner())
		if e != nil || ready.Type != "ready" || ready.State != "active" {
			t.Fatal(ready.Type, e)
		}
		u.send(relayproto.PlainRecord{V: 1, Type: "ready.ack", PairEpoch: u.transcript.PairEpoch})
	}
	u.started = true
	go u.receive()
	return u
}
func (u *relayUI) authenticate(ac relayproto.AuthContext) {
	u.t.Helper()
	hs, init, e := relayproto.NewUIHandshake(ac, u.id, nil)
	if e != nil {
		u.t.Fatal(e)
	}
	defer hs.Close()
	u.inner(init)
	finish, proof, e := hs.Finish(u.readInner())
	if e != nil {
		u.t.Fatal(e)
	}
	defer proof.Close()
	u.session, e = proof.NewSession()
	if e != nil {
		u.t.Fatal(e)
	}
	u.inner(finish)
}
func (u *relayUI) control(c relayproto.Control) {
	u.t.Helper()
	b, e := relayproto.EncodeControl(c)
	if e == nil {
		e = u.ws.Write(u.ctx, websocket.MessageText, b)
	}
	if e != nil {
		u.t.Fatal(e)
	}
}
func (u *relayUI) readControl() relayproto.Control {
	u.t.Helper()
	_, b, e := u.ws.Read(u.ctx)
	if e != nil {
		u.t.Fatal(e)
	}
	c, e := relayproto.DecodeControl(b)
	if e != nil {
		u.t.Fatal(e)
	}
	return c
}
func (u *relayUI) inner(i relayproto.Inner) {
	u.t.Helper()
	b, e := relayproto.EncodeInner(i)
	if e != nil {
		u.t.Fatal(e)
	}
	u.control(relayproto.Control{V: 1, Type: "forward", Payload: b, Connection: u.handle})
}
func (u *relayUI) readInner() relayproto.Inner {
	u.t.Helper()
	c := u.readControl()
	if c.Type != "forward" {
		u.t.Fatal(c.Type)
	}
	i, e := relayproto.DecodeInner(c.Payload)
	if e != nil {
		u.t.Fatal(e)
	}
	return i
}
func (u *relayUI) send(r relayproto.PlainRecord) error {
	u.sendMu.Lock()
	defer u.sendMu.Unlock()
	i, e := u.session.Seal(r)
	if e != nil {
		return e
	}
	b, e := relayproto.EncodeInner(i)
	if e != nil {
		return e
	}
	raw, e := relayproto.EncodeControl(relayproto.Control{V: 1, Type: "forward", Payload: b, Connection: u.handle})
	if e != nil {
		return e
	}
	return u.ws.Write(u.ctx, websocket.MessageText, raw)
}
func (u *relayUI) notifyLocked() { close(u.changed); u.changed = make(chan struct{}) }
func (u *relayUI) fail(e error) {
	u.mu.Lock()
	if u.err == nil {
		u.err = e
		u.notifyLocked()
	}
	u.mu.Unlock()
	u.cancel()
	u.ws.CloseNow()
}
func (u *relayUI) receive() {
	defer close(u.done)
	for {
		_, b, e := u.ws.Read(u.ctx)
		if e != nil {
			u.fail(e)
			return
		}
		control, e := relayproto.DecodeControl(b)
		if e != nil || control.Type != "forward" {
			u.fail(errors.New("unexpected relay control"))
			return
		}
		in, e := relayproto.DecodeInner(control.Payload)
		if e != nil {
			u.fail(e)
			return
		}
		r, e := u.session.Open(in)
		if e != nil {
			u.fail(e)
			return
		}
		u.mu.Lock()
		c := u.streams[r.ID]
		if c == nil {
			u.mu.Unlock()
			u.fail(errors.New("unknown stream"))
			return
		}
		ackReset := false
		switch r.Type {
		case "stream.opened":
			c.opened = true
		case "stream.data":
			c.input = append(c.input, r.Data...)
			if len(c.input) > 65536 {
				u.mu.Unlock()
				u.fail(errors.New("receive credit exceeded"))
				return
			}
		case "stream.credit":
			c.credit += int(r.Bytes)
		case "stream.fin":
			c.fin = true
		case "stream.reset":
			c.reset = true
			if c.opened && !c.sentReset {
				c.sentReset = true
				ackReset = true
			}
		default:
			u.mu.Unlock()
			u.fail(errors.New("unexpected record"))
			return
		}
		u.notifyLocked()
		u.mu.Unlock()
		if ackReset {
			if e := u.send(relayproto.PlainRecord{V: 1, Type: "stream.reset", ID: c.id, Code: "closed"}); e != nil {
				u.fail(e)
				return
			}
		}
	}
}
func (u *relayUI) dial(context.Context, string, string) (net.Conn, error) {
	u.mu.Lock()
	id := relayproto.Counter(u.next)
	u.next += 2
	c := &relayUIConn{u: u, id: id, credit: 65536}
	u.streams[id] = c
	u.mu.Unlock()
	if e := u.send(relayproto.PlainRecord{V: 1, Type: "stream.open", ID: id}); e != nil {
		return nil, e
	}
	for {
		u.mu.Lock()
		ok, reset, e, ch := c.opened, c.reset, u.err, u.changed
		u.mu.Unlock()
		if e != nil {
			return nil, e
		}
		if reset {
			return nil, net.ErrClosed
		}
		if ok {
			return c, nil
		}
		<-ch
	}
}
func (u *relayUI) client() *http.Client {
	tr := &http.Transport{DialContext: u.dial, DisableKeepAlives: true}
	u.t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}
func (u *relayUI) close() {
	u.fail(net.ErrClosed)
	if u.started {
		<-u.done
	}
	if u.session != nil {
		u.session.Close()
	}
}

type relayUIConn struct {
	u                                     *relayUI
	id                                    relayproto.Counter
	input                                 []byte
	credit                                int
	opened, fin, reset, closed, sentReset bool
	readMu, writeMu                       sync.Mutex
}

func (c *relayUIConn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	u := c.u
	for {
		u.mu.Lock()
		if c.reset || c.closed {
			u.mu.Unlock()
			return 0, net.ErrClosed
		}
		if len(c.input) > 0 {
			n := copy(b, c.input)
			c.input = c.input[n:]
			u.mu.Unlock()
			e := u.send(relayproto.PlainRecord{V: 1, Type: "stream.credit", ID: c.id, Bytes: relayproto.Counter(n)})
			return n, e
		}
		if c.fin {
			u.mu.Unlock()
			return 0, io.EOF
		}
		e, ch := u.err, u.changed
		u.mu.Unlock()
		if e != nil {
			return 0, e
		}
		<-ch
	}
}
func (c *relayUIConn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	u := c.u
	written := 0
	for len(b) > 0 {
		u.mu.Lock()
		if c.closed || c.reset {
			u.mu.Unlock()
			return written, net.ErrClosed
		}
		if u.err != nil {
			e := u.err
			u.mu.Unlock()
			return written, e
		}
		n := min(c.credit, len(b), 16384)
		if n == 0 {
			ch := u.changed
			u.mu.Unlock()
			<-ch
			continue
		}
		c.credit -= n
		u.mu.Unlock()
		if e := u.send(relayproto.PlainRecord{V: 1, Type: "stream.data", ID: c.id, Data: bytes.Clone(b[:n])}); e != nil {
			return written, e
		}
		written += n
		b = b[n:]
	}
	return written, nil
}
func (c *relayUIConn) Close() error {
	c.u.mu.Lock()
	if c.closed {
		c.u.mu.Unlock()
		return nil
	}
	c.closed = true
	fin := c.fin
	alreadyReset := c.sentReset
	if !fin {
		c.sentReset = true
	}
	c.u.notifyLocked()
	c.u.mu.Unlock()
	if alreadyReset {
		return nil
	}
	r := relayproto.PlainRecord{V: 1, Type: "stream.reset", ID: c.id, Code: "cancelled"}
	if fin {
		r.Type = "stream.fin"
		r.Code = ""
	}
	return c.u.send(r)
}
func (c *relayUIConn) LocalAddr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *relayUIConn) RemoteAddr() net.Addr             { return c.LocalAddr() }
func (c *relayUIConn) SetDeadline(time.Time) error      { return nil }
func (c *relayUIConn) SetReadDeadline(time.Time) error  { return nil }
func (c *relayUIConn) SetWriteDeadline(time.Time) error { return nil }
func relayRequest(t *testing.T, client *http.Client, method, path string, body io.Reader, headers http.Header) *http.Response {
	t.Helper()
	r, e := http.NewRequest(method, "http://omo-relay.invalid"+path, body)
	if e != nil {
		t.Fatal(e)
	}
	r.Header = headers
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	response, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	return response
}
func relayJSON(t *testing.T, response *http.Response, v any) {
	t.Helper()
	defer response.Body.Close()
	if e := json.NewDecoder(response.Body).Decode(v); e != nil {
		t.Fatal(e)
	}
}
