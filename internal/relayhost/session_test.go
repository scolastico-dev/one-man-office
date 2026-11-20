package relayhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

type hostFixture struct {
	t         *testing.T
	m         *Manager
	store     *Store
	wire      *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	runDone   chan struct{}
	accepted  chan *websocket.Conn
	callbacks Callbacks
}

func newHostFixture(t *testing.T, callbacks Callbacks) *hostFixture {
	t.Helper()
	m, s := managerForTest(t)
	m.SetCallbacks(callbacks)
	accepted := make(chan *websocket.Conn, 1)
	handlerDone := make(chan struct{})
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := websocket.Accept(w, r, nil)
		if e != nil {
			return
		}
		defer c.CloseNow()
		accepted <- c
		<-handlerDone
	}))
	t.Cleanup(relay.Close)
	ctx, cancel := context.WithCancel(context.Background())
	f := &hostFixture{t: t, m: m, store: s, ctx: ctx, cancel: cancel, runDone: make(chan struct{}), accepted: accepted, callbacks: callbacks}
	if e := m.Connect(ConnectOptions{Origin: relay.URL, ClientID: "office"}); e != nil {
		t.Fatal(e)
	}
	go func() { defer close(f.runDone); m.Run(ctx) }()
	f.wire = <-accepted
	f.wire.SetReadLimit(relayproto.MaxControlSize)
	t.Cleanup(func() { f.cancel(); f.m.Close(); <-f.runDone; close(handlerDone); f.wire.CloseNow() })
	reg := f.read()
	if reg.Type != "host.register" || reg.HostID != "office" {
		t.Fatal(reg.Type)
	}
	f.write(relayproto.Control{V: 1, Type: "host.registered", HostID: "office", Registration: bytes.Repeat([]byte{1}, 16)})
	return f
}
func (f *hostFixture) read() relayproto.Control {
	f.t.Helper()
	kind, raw, e := f.wire.Read(f.ctx)
	if e != nil {
		f.t.Fatal(e)
	}
	if kind != websocket.MessageText {
		f.t.Fatal("binary")
	}
	m, e := relayproto.DecodeControl(raw)
	if e != nil {
		f.t.Fatal(e)
	}
	return m
}
func (f *hostFixture) write(m relayproto.Control) {
	f.t.Helper()
	raw, e := relayproto.EncodeControl(m)
	if e != nil {
		f.t.Fatal(e)
	}
	if e = f.wire.Write(f.ctx, websocket.MessageText, raw); e != nil {
		f.t.Fatal(e)
	}
}
func (f *hostFixture) inner(conn []byte, m relayproto.Inner) {
	f.t.Helper()
	raw, e := relayproto.EncodeInner(m)
	if e != nil {
		f.t.Fatal(e)
	}
	f.write(relayproto.Control{V: 1, Type: "forward", Connection: conn, Payload: raw})
}
func (f *hostFixture) readInner(conn []byte) relayproto.Inner {
	f.t.Helper()
	m := f.read()
	if m.Type != "forward" || !bytes.Equal(m.Connection, conn) {
		f.t.Fatalf("unexpected control %s", m.Type)
	}
	in, e := relayproto.DecodeInner(m.Payload)
	if e != nil {
		f.t.Fatal(e)
	}
	return in
}

// The callback-driven store writer is an exact commit barrier, not a timed poll.
func watchPairProof(s *Store) (<-chan struct{}, func()) {
	done := make(chan struct{})
	var once sync.Once
	s.mu.Lock()
	old := s.write
	s.write = func(path string, raw []byte, mode os.FileMode) error {
		e := old(path, raw, mode)
		if e == nil {
			var v State
			if json.Unmarshal(raw, &v) == nil && v.Pair.Phase == relayproto.PairProved {
				once.Do(func() { close(done) })
			}
		}
		return e
	}
	s.mu.Unlock()
	return done, func() { s.mu.Lock(); s.write = old; s.mu.Unlock() }
}

func (f *hostFixture) pair(conn []byte, full bool, dropReply ...bool) (Device, *relayproto.Identity, *relayproto.Session) {
	f.t.Helper()
	if e := f.m.PairBegin(); e != nil {
		f.t.Fatal(e)
	}
	proved, unwatch := watchPairProof(f.store)
	defer unwatch()
	v, _ := f.store.Snapshot()
	route := relayproto.Route{Origin: v.Origin, HostID: v.HostID}
	id, _ := relayproto.GenerateIdentity()
	ui, commit, e := relayproto.NewPairUI(route, id, nil)
	if e != nil {
		f.t.Fatal(e)
	}
	f.write(relayproto.Control{V: 1, Type: "ui.open", Connection: conn})
	f.inner(conn, commit)
	reveal, e := ui.Reveal(f.readInner(conn))
	if e != nil {
		f.t.Fatal(e)
	}
	f.inner(conn, reveal)
	transcript, _ := ui.Transcript()
	authContext, _ := transcript.AuthContext()
	hs, init, e := relayproto.NewUIHandshake(authContext, id, nil)
	if e != nil {
		f.t.Fatal(e)
	}
	defer hs.Close()
	f.inner(conn, init)
	finish, proof, e := hs.Finish(f.readInner(conn))
	if e != nil {
		f.t.Fatal(e)
	}
	defer proof.Close()
	session, e := proof.NewSession()
	if e != nil {
		f.t.Fatal(e)
	}
	f.inner(conn, finish)
	<-proved
	pin, _ := transcript.PIN()
	var pk []byte
	if full {
		pin = ""
		pk = id.PublicKey()
	}
	d, e := f.m.PairAccept(transcript.AID, pin, pk, "phone")
	if e != nil {
		f.t.Fatal(e)
	}
	if len(dropReply) > 0 && dropReply[0] {
		return d, id, session
	}
	accepted, e := session.Open(f.readInner(conn))
	if e != nil || accepted.Type != "pair.accepted" || d.State != Active {
		f.t.Fatal("not active after local accept", accepted.Type, e)
	}
	return d, id, session
}
func (f *hostFixture) reconnect(conn []byte, d Device, id *relayproto.Identity) *relayproto.Session {
	f.t.Helper()
	v, _ := f.store.Snapshot()
	host, _ := relayproto.ImportIdentity(v.Seed)
	ac := relayproto.AuthContext{Route: relayproto.Route{Origin: v.Origin, HostID: v.HostID}, Mode: "reconnect", PKH: host.PublicKey(), PKU: id.PublicKey(), PairEpoch: d.PairEpoch}
	hs, init, e := relayproto.NewUIHandshake(ac, id, nil)
	if e != nil {
		f.t.Fatal(e)
	}
	defer hs.Close()
	f.write(relayproto.Control{V: 1, Type: "ui.open", Connection: conn})
	f.inner(conn, init)
	finish, proof, e := hs.Finish(f.readInner(conn))
	if e != nil {
		f.t.Fatal(e)
	}
	defer proof.Close()
	session, e := proof.NewSession()
	if e != nil {
		f.t.Fatal(e)
	}
	f.inner(conn, finish)
	ready, e := session.Open(f.readInner(conn))
	if e != nil || ready.Type != "ready" || ready.State != Active {
		f.t.Fatal(ready.Type, e)
	}
	ack, e := session.Seal(relayproto.PlainRecord{V: 1, Type: "ready.ack", PairEpoch: d.PairEpoch})
	if e != nil {
		f.t.Fatal(e)
	}
	f.inner(conn, ack)
	return session
}
func TestLocalAcceptActivatesConcurrentDevicesAndRevokesAllSessions(t *testing.T) {
	entered := make(chan Capability, 8)
	exited := make(chan string, 8)
	f := newHostFixture(t, Callbacks{ServeSession: func(ctx context.Context, cap Capability, io relayproto.RecordIO) error {
		entered <- cap
		<-ctx.Done()
		exited <- cap.UIID()
		return nil
	}})
	a := bytes.Repeat([]byte{2}, 16)
	d, id, sess := f.pair(a, false)
	defer sess.Close()
	capA := <-entered
	b := bytes.Repeat([]byte{3}, 16)
	other, _, sessB := f.pair(b, true)
	defer sessB.Close()
	capB := <-entered
	c := bytes.Repeat([]byte{4}, 16)
	reconnected := f.reconnect(c, d, id)
	defer reconnected.Close()
	capC := <-entered
	if e := f.m.Unregister(d.UIID); e != nil {
		t.Fatal(e)
	}
	if <-exited != d.UIID || <-exited != d.UIID {
		t.Fatal("wrong callbacks cancelled")
	}
	if f.m.Validate(capA) == nil || f.m.Validate(capC) == nil || f.m.Validate(capB) != nil {
		t.Fatal("capability revocation crossed device")
	}
	v, _ := f.store.Snapshot()
	if len(v.Devices) != 1 || v.Devices[0].UIID != other.UIID {
		t.Fatal("wrong durable device removed")
	}
}
func TestPairCommitSecondAttemptDisconnectAndWrongPINSpendSlot(t *testing.T) {
	for _, failure := range []string{"second", "disconnect", "wrong-pin", "no-possession"} {
		t.Run(failure, func(t *testing.T) {
			f := newHostFixture(t, Callbacks{})
			if e := f.m.PairBegin(); e != nil {
				t.Fatal(e)
			}
			v, _ := f.store.Snapshot()
			id, _ := relayproto.GenerateIdentity()
			ui, commit, _ := relayproto.NewPairUI(relayproto.Route{Origin: v.Origin, HostID: v.HostID}, id, nil)
			conn := bytes.Repeat([]byte{7}, 16)
			f.write(relayproto.Control{V: 1, Type: "ui.open", Connection: conn})
			f.inner(conn, commit)
			host := f.readInner(conn)
			switch failure {
			case "second":
				conn2 := bytes.Repeat([]byte{8}, 16)
				f.write(relayproto.Control{V: 1, Type: "ui.open", Connection: conn2})
				f.inner(conn2, commit)
				if m := f.read(); m.Type != "ui.close" {
					t.Fatal(m.Type)
				}
			case "disconnect":
				f.write(relayproto.Control{V: 1, Type: "ui.closed", Connection: conn})
			case "wrong-pin", "no-possession":
				reveal, _ := ui.Reveal(host)
				f.inner(conn, reveal)
				p, _ := ui.Transcript()
				ac, _ := p.AuthContext()
				hs, init, _ := relayproto.NewUIHandshake(ac, id, nil)
				defer hs.Close()
				f.inner(conn, init)
				challenge := f.readInner(conn)
				if failure == "wrong-pin" {
					proved, unwatch := watchPairProof(f.store)
					defer unwatch()
					finish, proof, e := hs.Finish(challenge)
					if e != nil {
						t.Fatal(e)
					}
					defer proof.Close()
					f.inner(conn, finish)
					<-proved
					pin, _ := p.PIN()
					wrong := "000000"
					if pin == wrong {
						wrong = "000001"
					}
					if _, e = f.m.PairAccept(p.AID, wrong, nil, "phone"); e == nil {
						t.Fatal("wrong PIN accepted")
					}
				} else {
					if _, e := f.m.PairAccept(p.AID, "", id.PublicKey(), "phone"); e == nil {
						t.Fatal("key accepted without private possession")
					}
				}
			}
			// A local pending call synchronizes with transition completion for the local
			// command cases; remote cases are synchronized by an exact durable write.
			if failure == "second" || failure == "disconnect" {
				f.m.mu.Lock()
				p := f.m.current.peers[base64Key(conn)]
				f.m.mu.Unlock()
				if p != nil {
					<-p.done
				}
			}
			after, e := f.store.Snapshot()
			if e != nil || after.Pair.Phase != relayproto.PairSpent || len(after.Devices) != 0 {
				t.Fatal("slot not spent", after.Pair.Phase, e)
			}
		})
	}
}

func TestNotificationAuthenticationCancellationAndRevocation(t *testing.T) {
	started := make(chan string, 8)
	stopped := make(chan string, 8)
	f := newHostFixture(t, Callbacks{ServeSession: func(ctx context.Context, _ Capability, _ relayproto.RecordIO) error { <-ctx.Done(); return nil }, PollNotifications: func(ctx context.Context, d Device, p relayproto.Poll) (relayproto.NotificationEnvelope, error) {
		started <- d.UIID
		<-ctx.Done()
		stopped <- d.UIID
		return relayproto.NotificationEnvelope{}, ctx.Err()
	}})
	d, _, session := f.pair(bytes.Repeat([]byte{21}, 16), false)
	defer session.Close()
	v, _ := f.store.Snapshot()
	nc := relayproto.NotificationContext{Route: relayproto.Route{Origin: v.Origin, HostID: v.HostID}, HostFP: v.HostFP, UIID: d.UIID, PairEpoch: d.PairEpoch}
	poll, e := relayproto.NewPoll(nc, d.NotificationRoot, bytes.Repeat([]byte{22}, 32), nil)
	if e != nil {
		t.Fatal(e)
	}
	send := func(q byte, p relayproto.Poll) {
		f.write(relayproto.Control{V: 1, Type: "notification.get", HostID: v.HostID, UIID: d.UIID, QID: bytes.Repeat([]byte{q}, 16), RequestHeader: p.RequestHeader, RequestMAC: p.RequestMAC})
	}
	bad := poll
	bad.RequestMAC = base64Key(bytes.Repeat([]byte{0}, 48))
	send(23, bad)
	if r := f.read(); r.Type != "notification.empty" || !bytes.Equal(r.QID, bytes.Repeat([]byte{23}, 16)) {
		t.Fatal("invalid MAC response", r.Type)
	}
	send(24, poll)
	if <-started != d.UIID {
		t.Fatal("wrong poll callback")
	}
	f.m.mu.Lock()
	work := f.m.current.polls[base64Key(bytes.Repeat([]byte{24}, 16))]
	f.m.mu.Unlock()
	f.write(relayproto.Control{V: 1, Type: "notification.cancel", QID: bytes.Repeat([]byte{24}, 16)})
	<-work.done
	if <-stopped != d.UIID {
		t.Fatal("cancel did not join callback")
	}
	send(25, poll)
	<-started
	if e := f.m.Unregister(d.UIID); e != nil {
		t.Fatal(e)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("revoke did not await notification callback")
	}
}
func TestMissingCallbacksNeverAdmitApplicationOrNotifications(t *testing.T) {
	f := newHostFixture(t, Callbacks{})
	d, _, session := f.pair(bytes.Repeat([]byte{31}, 16), false)
	defer session.Close()
	if r := f.read(); r.Type != "ui.close" {
		t.Fatal(r.Type)
	}
	v, _ := f.store.Snapshot()
	nc := relayproto.NotificationContext{Route: relayproto.Route{Origin: v.Origin, HostID: v.HostID}, HostFP: v.HostFP, UIID: d.UIID, PairEpoch: d.PairEpoch}
	poll, e := relayproto.NewPoll(nc, d.NotificationRoot, bytes.Repeat([]byte{32}, 32), nil)
	if e != nil {
		t.Fatal(e)
	}
	f.write(relayproto.Control{V: 1, Type: "notification.get", HostID: v.HostID, UIID: d.UIID, QID: bytes.Repeat([]byte{33}, 16), RequestHeader: poll.RequestHeader, RequestMAC: poll.RequestMAC})
	if r := f.read(); r.Type != "notification.empty" {
		t.Fatal(r.Type)
	}
}

func (f *hostFixture) restart() {
	f.t.Helper()
	f.cancel()
	f.m.Close()
	<-f.runDone
	f.wire.CloseNow()
	store, e := OpenStore(f.store.dir)
	if e != nil {
		f.t.Fatal(e)
	}
	v, _ := store.Snapshot()
	m, e := NewManager(store, Options{Origin: v.Origin, Enabled: true, Callbacks: f.callbacks})
	if e != nil {
		f.t.Fatal(e)
	}
	f.m = m
	f.store = store
	ctx, cancel := context.WithCancel(context.Background())
	f.ctx = ctx
	f.cancel = cancel
	f.runDone = make(chan struct{})
	go func() { defer close(f.runDone); m.Run(ctx) }()
	f.wire = <-f.accepted
	f.wire.SetReadLimit(relayproto.MaxControlSize)
	reg := f.read()
	if reg.Type != "host.register" || reg.HostID != v.HostID {
		f.t.Fatal("restart replaced registration")
	}
	f.write(relayproto.Control{V: 1, Type: "host.registered", HostID: v.HostID, Registration: bytes.Repeat([]byte{52}, 16)})
}
func TestLostAcceptanceReplyRestartRecoversSameActiveDevice(t *testing.T) {
	entered := make(chan Capability, 8)
	f := newHostFixture(t, Callbacks{ServeSession: func(ctx context.Context, cap Capability, _ relayproto.RecordIO) error {
		entered <- cap
		<-ctx.Done()
		return nil
	}})
	d, id, initial := f.pair(bytes.Repeat([]byte{51}, 16), true, true)
	defer initial.Close()
	before, _ := f.store.Snapshot()
	f.restart()
	after, _ := f.store.Snapshot()
	if !bytes.Equal(before.Seed, after.Seed) || len(after.Devices) != 1 || !bytes.Equal(after.Devices[0].NotificationRoot, d.NotificationRoot) || after.Devices[0].State != Active {
		t.Fatal("lost accepted reply lost authorization")
	}
	session := f.reconnect(bytes.Repeat([]byte{53}, 16), after.Devices[0], id)
	defer session.Close()
	for {
		cap := <-entered
		if cap.Context().Err() == nil {
			if e := f.m.Validate(cap); e != nil {
				t.Fatal(e)
			}
			break
		}
	}
}

func TestLateRevokedPeerFramePreservesOtherDevice(t *testing.T) {
	entered := make(chan Capability, 2)
	f := newHostFixture(t, Callbacks{ServeSession: func(ctx context.Context, cap Capability, io relayproto.RecordIO) error {
		entered <- cap
		for {
			record, e := io.Receive(ctx)
			if e != nil {
				return e
			}
			if e = io.Send(ctx, relayproto.PlainRecord{V: 1, Type: "stream.opened", ID: record.ID}); e != nil {
				return e
			}
		}
	}})
	a := bytes.Repeat([]byte{61}, 16)
	b := bytes.Repeat([]byte{62}, 16)
	d, _, sa := f.pair(a, false)
	defer sa.Close()
	<-entered
	_, _, sb := f.pair(b, false)
	defer sb.Close()
	capB := <-entered
	if e := f.m.Unregister(d.UIID); e != nil {
		t.Fatal(e)
	}
	if closed := f.read(); closed.Type != "ui.close" || !bytes.Equal(closed.Connection, a) {
		t.Fatal("wrong endpoint closed")
	}
	late, e := sa.Seal(relayproto.PlainRecord{V: 1, Type: "stream.open", ID: 1})
	if e != nil {
		t.Fatal(e)
	}
	f.inner(a, late)
	request, e := sb.Seal(relayproto.PlainRecord{V: 1, Type: "stream.open", ID: 1})
	if e != nil {
		t.Fatal(e)
	}
	f.inner(b, request)
	opened, e := sb.Open(f.readInner(b))
	if e != nil || opened.Type != "stream.opened" || opened.ID != 1 {
		t.Fatal("other device lost", e)
	}
	if e = f.m.Validate(capB); e != nil {
		t.Fatal(e)
	}
}

func TestFailedStateSaveCancelsActiveAuthority(t *testing.T) {
	entered := make(chan Capability, 1)
	exited := make(chan struct{})
	f := newHostFixture(t, Callbacks{ServeSession: func(ctx context.Context, cap Capability, _ relayproto.RecordIO) error {
		entered <- cap
		<-ctx.Done()
		close(exited)
		return nil
	}})
	d, _, session := f.pair(bytes.Repeat([]byte{71}, 16), false)
	defer session.Close()
	cap := <-entered
	f.store.mu.Lock()
	f.store.write = func(string, []byte, os.FileMode) error { return errors.New("disk failure") }
	f.store.mu.Unlock()
	if e := f.m.Unregister(d.UIID); !errors.Is(e, ErrState) {
		t.Fatal("failed revocation reported success", e)
	}
	<-exited
	if e := f.m.Validate(cap); e == nil {
		t.Fatal("failed save left authority live")
	}
	if f.m.Status().State != "state_error" {
		t.Fatal(f.m.Status())
	}
}
