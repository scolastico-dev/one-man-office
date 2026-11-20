package relayhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

const endpointQueue = 32

func base64Key(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
func retryDelay(attempt int) time.Duration {
	if attempt >= 5 {
		return 30 * time.Second
	}
	return time.Second << attempt
}

// Run owns exactly one outbound connection. Close and context cancellation join
// its reader, writer, keepalive, all endpoint callbacks and notification work.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	if m.running || m.closed {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.mu.Unlock()
	defer close(m.runDone)
	runCtx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	attempts := 0
	for {
		m.mu.Lock()
		// Configuration is sampled under this lock; consume its previous wakeup so
		// it cannot bypass the next mandatory retry delay.
		select {
		case <-m.wake:
		default:
		}
		enabled := m.enabled && !m.closed
		var c *connection
		if enabled {
			v, e := m.store.Snapshot()
			if e != nil {
				m.mu.Unlock()
				return
			}
			cc, ce := context.WithCancel(runCtx)
			c = &connection{m: m, ctx: cc, cancel: ce, done: make(chan struct{}), state: v, sendq: make(chan outbound, endpointQueue), writerDone: make(chan struct{}), peers: map[string]*peer{}, polls: map[string]*notificationWork{}}
			m.current = c
		}
		m.mu.Unlock()
		if !enabled {
			select {
			case <-runCtx.Done():
				return
			case <-m.wake:
				continue
			}
		}
		code := c.run()
		c.cancel()
		m.mu.Lock()
		if m.current == c {
			m.current = nil
		}
		if c.registered {
			attempts = 0
		}
		delay := retryDelay(attempts)
		if attempts < 5 {
			attempts++
		}
		if m.enabled {
			m.state = code
			m.retry = int(delay / time.Second)
		}
		close(c.done)
		enabled = m.enabled
		m.mu.Unlock()
		if runCtx.Err() != nil {
			return
		}
		if !enabled {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-runCtx.Done():
			timer.Stop()
			return
		case <-m.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

type outbound struct {
	message relayproto.Control
	ctx     context.Context
	done    chan error
}
type notificationWork struct {
	ui     string
	cancel context.CancelFunc
	done   chan struct{}
}
type connection struct {
	m          *Manager
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	state      State
	ws         *websocket.Conn
	registered bool
	sendq      chan outbound
	writerDone chan struct{}
	peers      map[string]*peer             // manager lock
	polls      map[string]*notificationWork // manager lock
	wg         sync.WaitGroup
}

func (c *connection) run() string {
	headers := http.Header{}
	if c.state.AdmissionSecret != "" {
		r := &http.Request{Header: headers}
		r.SetBasicAuth("relay", c.state.AdmissionSecret)
	}
	address := strings.Replace(strings.Replace(c.state.Origin, "https://", "wss://", 1), "http://", "ws://", 1) + "/v1/ws"
	dial := c.m.opts.Dial
	if dial == nil {
		dial = func(ctx context.Context, address string, header http.Header) (*websocket.Conn, *http.Response, error) {
			return websocket.Dial(ctx, address, &websocket.DialOptions{HTTPHeader: header})
		}
	}
	ws, response, e := dial(c.ctx, address, headers)
	if e != nil {
		if response != nil && response.StatusCode == http.StatusUnauthorized {
			return "admission_denied"
		}
		return "retrying"
	}
	c.ws = ws
	ws.SetReadLimit(relayproto.MaxControlSize)
	stop := context.AfterFunc(c.ctx, func() { ws.CloseNow() })
	c.wg.Add(1)
	go c.writer()
	defer func() {
		c.cancel()
		ws.CloseNow()
		stop()
		c.m.mu.Lock()
		for _, p := range c.peers {
			p.cancel()
		}
		for _, q := range c.polls {
			q.cancel()
		}
		c.m.mu.Unlock()
		c.wg.Wait()
		c.m.mu.Lock()
		if c.m.current == c && c.m.pair != nil {
			_ = c.m.pair.Abort()
		}
		c.m.mu.Unlock()
	}()
	if e = c.send(c.ctx, relayproto.Control{V: 1, Type: "host.register", HostID: c.state.HostID}); e != nil {
		return "retrying"
	}
	// One read loop continuously handles pings/control, independently of every
	// endpoint and callback. Registration is its first and only role transition.
	first, e := c.read()
	if e != nil {
		return "retrying"
	}
	if first.Type == "error" && first.Code == "id_in_use" {
		return "collision"
	}
	if first.Type != "host.registered" || first.HostID != c.state.HostID {
		return "retrying"
	}
	c.registered = true
	c.m.mu.Lock()
	if c.m.current == c && c.m.enabled {
		c.m.state = "connected"
		c.m.retry = 0
	}
	c.m.mu.Unlock()
	c.wg.Add(1)
	go c.keepalive()
	for {
		control, e := c.read()
		if e != nil {
			return "retrying"
		}
		if e = c.dispatch(control); e != nil {
			return "retrying"
		}
	}
}
func (c *connection) read() (relayproto.Control, error) {
	kind, raw, e := c.ws.Read(c.ctx)
	if e != nil {
		return relayproto.Control{}, e
	}
	if kind != websocket.MessageText {
		return relayproto.Control{}, relayproto.ErrProtocol
	}
	return relayproto.DecodeControl(raw)
}
func (c *connection) writer() {
	defer c.wg.Done()
	defer close(c.writerDone)
	for {
		select {
		case <-c.ctx.Done():
			return
		case out := <-c.sendq:
			ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			if e := out.ctx.Err(); e != nil {
				cancel()
				if out.done != nil {
					out.done <- e
				}
				continue
			}
			raw, e := relayproto.EncodeControl(out.message)
			if e == nil {
				e = c.ws.Write(ctx, websocket.MessageText, raw)
			}
			cancel()
			if out.done != nil {
				out.done <- e
			}
			if e != nil {
				c.cancel()
				return
			}
		}
	}
}
func (c *connection) send(ctx context.Context, message relayproto.Control) error {
	done := make(chan error, 1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		return c.ctx.Err()
	case c.sendq <- outbound{message: message, ctx: ctx, done: done}:
	}
	// Once admitted, cancellation must join the writer. Returning on the
	// endpoint context alone would let unregister finish with a write in flight.
	select {
	case e := <-done:
		if e == nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return e
	case <-c.writerDone:
		return context.Canceled
	}
}
func (c *connection) offer(message relayproto.Control) {
	select {
	case c.sendq <- outbound{message: message, ctx: c.ctx}:
	default:
		c.cancel()
	}
}
func (c *connection) keepalive() {
	defer c.wg.Done()
	timer := time.NewTicker(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-timer.C:
			ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			e := c.ws.Ping(ctx)
			cancel()
			if e != nil {
				c.ws.CloseNow()
				c.cancel()
				return
			}
		}
	}
}
func (c *connection) dispatch(control relayproto.Control) error {
	m := c.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != c || !m.enabled || c.ctx.Err() != nil {
		return context.Canceled
	}
	key := base64Key(control.Connection)
	switch control.Type {
	case "ui.open":
		if c.peers[key] != nil || len(c.peers) >= 128 {
			return relayproto.ErrProtocol
		}
		ctx, cancel := context.WithCancel(c.ctx)
		p := &peer{conn: c, key: key, handle: bytes.Clone(control.Connection), ctx: ctx, cancel: cancel, done: make(chan struct{}), input: make(chan relayproto.Inner, endpointQueue), accepted: make(chan Device, 1)}
		c.peers[key] = p
		c.wg.Add(1)
		go p.run()
	case "ui.closed":
		if p := c.peers[key]; p != nil {
			p.cancel()
		}
	case "forward":
		p := c.peers[key]
		if p == nil {
			// Frames already queued when the host closed this endpoint may arrive
			// after its cleanup. They cannot affect another device or registration.
			return nil
		}
		in, e := relayproto.DecodeInner(control.Payload)
		if e != nil {
			p.cancel()
			return nil
		}
		select {
		case p.input <- in:
		default:
			p.cancel()
		}
	case "notification.get":
		return c.startNotificationLocked(control)
	case "notification.cancel":
		if q := c.polls[base64Key(control.QID)]; q != nil {
			q.cancel()
		}
	default:
		return relayproto.ErrProtocol
	}
	return nil
}

type peer struct {
	conn       *connection
	key        string
	handle     []byte
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	input      chan relayproto.Inner
	accepted   chan Device
	ui         string // manager lock, from saved identity or local acceptance only
	epoch      []byte
	generation uint64
	pairing    bool // manager lock
	transcript relayproto.PairTranscript
	handshake  *relayproto.HostHandshake
	records    *recordIO
}

func (p *peer) run() {
	defer p.conn.wg.Done()
	defer func() {
		p.cancel()
		if p.handshake != nil {
			p.handshake.Close()
		}
		if p.records != nil {
			p.records.Close()
		}
		m := p.conn.m
		m.mu.Lock()
		if m.current == p.conn {
			v, e := m.store.Snapshot()
			if e == nil && m.pair != nil && v.Pair.Connection == p.key && v.Pair.Phase != relayproto.PairAccepted {
				_ = m.pair.Abort()
			}
			delete(p.conn.peers, p.key)
			p.conn.offer(relayproto.Control{V: 1, Type: "ui.close", Connection: p.handle})
		}
		close(p.done)
		m.mu.Unlock()
	}()
	first, e := p.receiveInner()
	if e != nil {
		return
	}
	if first.Type == "pair.commit" {
		if e = p.pairHandshake(first); e != nil {
			return
		}
		select {
		case <-p.ctx.Done():
			return
		case <-p.input:
			return // Any second attempt or premature record spends the slot.
		case d := <-p.accepted:
			if e = p.records.Send(p.ctx, relayproto.PlainRecord{V: 1, Type: "pair.accepted", AID: d.AID, HostFP: p.conn.state.HostFP, UIID: d.UIID, PairEpoch: d.PairEpoch}); e != nil {
				return
			}
		}
	} else if first.Type == "auth.init" {
		if e = p.reconnectHandshake(first); e != nil {
			return
		}
	} else {
		return
	}
	m := p.conn.m
	m.mu.Lock()
	callback := m.callbacks.ServeSession
	ui := p.ui
	epoch := bytes.Clone(p.epoch)
	m.mu.Unlock()
	if callback == nil {
		return
	}
	cap, e := m.capability(ui, epoch, p.ctx)
	if e != nil {
		return
	}
	_ = callback(p.ctx, cap, p.records)
}
func (p *peer) receiveInner() (relayproto.Inner, error) {
	select {
	case <-p.ctx.Done():
		return relayproto.Inner{}, p.ctx.Err()
	case in := <-p.input:
		return in, nil
	}
}
func (p *peer) sendInner(in relayproto.Inner) error {
	raw, e := relayproto.EncodeInner(in)
	if e != nil {
		return e
	}
	return p.conn.send(p.ctx, relayproto.Control{V: 1, Type: "forward", Connection: p.handle, Payload: raw})
}
func (p *peer) pairHandshake(first relayproto.Inner) error {
	m := p.conn.m
	m.mu.Lock()
	p.pairing = true
	if m.pair == nil || p.ctx.Err() != nil || !m.enabled {
		m.mu.Unlock()
		return ErrPair
	}
	reply, e := m.pair.Commit(p.key, first)
	if e != nil {
		m.abortPairPeerLocked()
		_ = m.pair.Abort()
		m.mu.Unlock()
		return e
	}
	m.mu.Unlock()
	if e = p.sendInner(reply); e != nil {
		return e
	}
	reveal, e := p.receiveInner()
	if e != nil {
		return e
	}
	m.mu.Lock()
	if p.ctx.Err() != nil || m.current != p.conn {
		m.mu.Unlock()
		return context.Canceled
	}
	transcript, e := m.pair.Reveal(p.key, reveal)
	m.mu.Unlock()
	if e != nil {
		return e
	}
	p.transcript = transcript
	ac, e := transcript.AuthContext()
	if e != nil {
		return e
	}
	init, e := p.receiveInner()
	if e != nil {
		return e
	}
	proof, e := p.authenticate(ac, init)
	if e != nil {
		return e
	}
	defer proof.Close()
	m.mu.Lock()
	if p.ctx.Err() != nil || m.current != p.conn {
		m.mu.Unlock()
		return context.Canceled
	}
	e = m.pair.Prove(p.key, proof)
	m.mu.Unlock()
	if e != nil {
		return e
	}
	return p.makeRecords(proof)
}
func (p *peer) reconnectHandshake(init relayproto.Inner) error {
	m := p.conn.m
	m.mu.Lock()
	d, e := m.findDeviceLocked(init.UIID, init.PairEpoch, true)
	if e != nil || !m.enabled {
		m.mu.Unlock()
		return ErrDenied
	}
	p.ui = d.UIID
	p.epoch = bytes.Clone(d.PairEpoch)
	p.generation = d.Generation
	ac := relayproto.AuthContext{Route: relayproto.Route{Origin: p.conn.state.Origin, HostID: p.conn.state.HostID}, Mode: "reconnect", PKH: m.identity.PublicKey(), PKU: d.PublicKey, PairEpoch: d.PairEpoch}
	m.mu.Unlock()
	proof, e := p.authenticate(ac, init)
	if e != nil {
		return e
	}
	defer proof.Close()
	if e = p.makeRecords(proof); e != nil {
		return e
	}
	m.mu.Lock()
	current, e := m.findDeviceLocked(d.UIID, d.PairEpoch, true)
	if e == nil && current.Generation != d.Generation {
		e = ErrDenied
	}
	m.mu.Unlock()
	if e != nil {
		return e
	}
	if e = p.records.Send(p.ctx, relayproto.PlainRecord{V: 1, Type: "ready", State: Active, AID: d.AID, PairEpoch: d.PairEpoch}); e != nil {
		return e
	}
	ack, e := p.records.receiveControl(p.ctx)
	if e != nil || ack.Type != "ready.ack" || !bytes.Equal(ack.PairEpoch, d.PairEpoch) {
		return relayproto.ErrProtocol
	}
	return nil
}
func (p *peer) authenticate(ac relayproto.AuthContext, init relayproto.Inner) (*relayproto.Authenticated, error) {
	m := p.conn.m
	m.mu.Lock()
	id := m.identity
	m.mu.Unlock()
	hs, e := relayproto.NewHostHandshake(ac, id, nil)
	if e != nil {
		return nil, e
	}
	p.handshake = hs
	challenge, e := hs.Challenge(init)
	if e != nil {
		return nil, e
	}
	if e = p.sendInner(challenge); e != nil {
		return nil, e
	}
	finish, e := p.receiveInner()
	if e != nil {
		return nil, e
	}
	return hs.Finish(finish)
}
func (p *peer) makeRecords(proof *relayproto.Authenticated) error {
	s, e := proof.NewSession()
	if e != nil {
		return e
	}
	p.records = &recordIO{peer: p, crypto: s}
	return nil
}

type recordIO struct {
	peer      *peer
	crypto    *relayproto.Session
	sendMu    sync.Mutex
	receiveMu sync.Mutex
}

func (r *recordIO) Send(ctx context.Context, p relayproto.PlainRecord) error {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	if ctx.Err() != nil || r.peer.ctx.Err() != nil {
		return context.Canceled
	}
	in, e := r.crypto.Seal(p)
	if e == nil {
		raw, err := relayproto.EncodeInner(in)
		e = err
		if e == nil {
			e = r.peer.conn.send(ctx, relayproto.Control{V: 1, Type: "forward", Connection: r.peer.handle, Payload: raw})
		}
	}
	if e != nil {
		r.peer.cancel()
		r.crypto.Close()
	}
	return e
}
func (r *recordIO) receiveControl(ctx context.Context) (relayproto.PlainRecord, error) {
	r.receiveMu.Lock()
	defer r.receiveMu.Unlock()
	var in relayproto.Inner
	select {
	case <-ctx.Done():
		return relayproto.PlainRecord{}, ctx.Err()
	case <-r.peer.ctx.Done():
		return relayproto.PlainRecord{}, r.peer.ctx.Err()
	case in = <-r.peer.input:
	}
	p, e := r.crypto.Open(in)
	if e != nil {
		r.peer.cancel()
	}
	return p, e
}
func (r *recordIO) Receive(ctx context.Context) (relayproto.PlainRecord, error) {
	p, e := r.receiveControl(ctx)
	if e == nil && !strings.HasPrefix(p.Type, "stream.") {
		r.peer.cancel()
		return relayproto.PlainRecord{}, relayproto.ErrProtocol
	}
	return p, e
}
func (r *recordIO) Close() error { r.peer.cancel(); return r.crypto.Close() }

func (c *connection) startNotificationLocked(control relayproto.Control) error {
	key := base64Key(control.QID)
	if c.polls[key] != nil || len(c.polls) >= 128 {
		return relayproto.ErrProtocol
	}
	if control.HostID != c.state.HostID {
		return relayproto.ErrProtocol
	}
	ctx, cancel := context.WithCancel(c.ctx)
	work := &notificationWork{ui: control.UIID, cancel: cancel, done: make(chan struct{})}
	c.polls[key] = work
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer func() { cancel(); c.m.mu.Lock(); delete(c.polls, key); close(work.done); c.m.mu.Unlock() }()
		reply := relayproto.Control{V: 1, Type: "notification.empty", QID: control.QID}
		_ = c.m.WithDevice(ctx, control.UIID, func(ctx context.Context, d Device) error {
			poll := relayproto.Poll{RequestHeader: control.RequestHeader, RequestMAC: control.RequestMAC}
			nc := relayproto.NotificationContext{Route: relayproto.Route{Origin: c.state.Origin, HostID: c.state.HostID}, HostFP: c.state.HostFP, UIID: d.UIID, PairEpoch: d.PairEpoch}
			if _, e := relayproto.VerifyPoll(nc, d.NotificationRoot, poll); e != nil {
				return e
			}
			c.m.mu.Lock()
			callback := c.m.callbacks.PollNotifications
			c.m.mu.Unlock()
			if callback == nil {
				return ErrDenied
			}
			env, e := callback(ctx, d, poll)
			if e != nil {
				return e
			}
			raw, e := relayproto.EncodeNotificationEnvelope(env)
			if e != nil {
				return e
			}
			// Recheck authorization and registration before admitting a reply. Sending
			// remains in this joined work so revoke cannot return while it is in flight.
			c.m.mu.Lock()
			active, e := c.m.findDeviceLocked(d.UIID, d.PairEpoch, true)
			if e == nil && (active.Generation != d.Generation || c.m.current != c) {
				e = ErrDenied
			}
			c.m.mu.Unlock()
			if e != nil {
				return e
			}
			reply = relayproto.Control{V: 1, Type: "notification.reply", QID: control.QID, Body: raw}
			return c.send(ctx, reply)
		})
		if reply.Type == "notification.empty" && ctx.Err() == nil {
			_ = c.send(ctx, reply)
		}
	}()
	return nil
}

var _ relayproto.RecordIO = (*recordIO)(nil)
