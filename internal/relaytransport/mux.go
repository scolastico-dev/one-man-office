// Package relaytransport carries HTTP connections over authenticated relay records.
package relaytransport

import (
	"context"
	"math"
	"net"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// Mux is a private listener. One receiver demultiplexes records and one fair
// writer serializes them. No application read or write runs in either loop.
type Mux struct {
	ctx         context.Context
	cancel      context.CancelFunc
	records     relayproto.RecordIO
	authorize   func() error
	mu          sync.Mutex
	streams     map[relayproto.Counter]*Conn
	order       []*Conn
	cursor      int
	next        relayproto.Counter
	refused     []relayproto.PlainRecord
	controlTurn bool
	wake        chan struct{}
	changed     chan struct{}
	err         error
	wg          sync.WaitGroup
}

// New starts an authenticated host-side multiplexer. authorize is checked for
// every stream; nil denies admission. The caller owns and must Close/Wait it.
func New(ctx context.Context, records relayproto.RecordIO, authorize func() error) *Mux {
	ctx, cancel := context.WithCancel(ctx)
	m := &Mux{ctx: ctx, cancel: cancel, records: records, authorize: authorize, streams: map[relayproto.Counter]*Conn{}, next: 1, wake: make(chan struct{}, 1), changed: make(chan struct{})}
	m.wg.Add(3)
	go func() { defer m.wg.Done(); <-ctx.Done(); m.fail(ctx.Err()) }()
	go m.receive()
	go m.write()
	return m
}
func (m *Mux) signalLocked() {
	close(m.changed)
	m.changed = make(chan struct{})
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Mux) fail(err error) {
	m.mu.Lock()
	if m.err != nil {
		m.mu.Unlock()
		return
	}
	m.err = err
	m.cancel()
	for _, c := range m.streams {
		c.failure = err
		c.input = nil
		if c.pending != nil {
			c.pending.finish(err)
			c.pending = nil
		}
	}
	m.streams = map[relayproto.Counter]*Conn{}
	m.order = nil
	m.signalLocked()
	m.mu.Unlock()
	_ = m.records.Close()
}
func (m *Mux) Close() error { m.fail(net.ErrClosed); return nil }
func (m *Mux) Wait() error  { m.wg.Wait(); m.mu.Lock(); defer m.mu.Unlock(); return m.err }
func (m *Mux) receive() {
	defer m.wg.Done()
	for {
		r, e := m.records.Receive(m.ctx)
		if e != nil {
			m.fail(e)
			return
		}
		if e = m.dispatch(r); e != nil {
			m.fail(e)
			return
		}
	}
}
func (m *Mux) dispatch(r relayproto.PlainRecord) error {
	// RecordIO normally supplies codec-validated records. Validate here too so
	// alternate adapters cannot weaken the byte bounds or record union.
	if _, e := relayproto.EncodePlain(r); e != nil {
		return relayproto.ErrProtocol
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if r.Type == "stream.open" {
		if r.ID != m.next || m.next == 0 {
			return relayproto.ErrProtocol
		}
		if uint64(m.next) > math.MaxUint64-2 {
			m.next = 0
		} else {
			m.next += 2
		}
		if m.authorize == nil || m.authorize() != nil {
			return relayproto.ErrAuthentication
		}
		if len(m.streams) >= relayproto.MaxStreams {
			if len(m.refused) >= relayproto.MaxStreams {
				return relayproto.ErrProtocol
			}
			r.Type = "stream.reset"
			r.Code = "refused"
			m.refused = append(m.refused, r)
			m.signalLocked()
			return nil
		}
		c := &Conn{mux: m, id: r.ID, sendCredit: relayproto.InitialCredit, recvCredit: relayproto.InitialCredit, opening: true}
		m.streams[r.ID] = c
		m.order = append(m.order, c)
		m.signalLocked()
		return nil
	}
	c := m.streams[r.ID]
	if c == nil || !c.opened || c.peerReset {
		return relayproto.ErrProtocol
	}
	switch r.Type {
	case "stream.data":
		if c.remoteFin || len(r.Data) > c.recvCredit {
			return relayproto.ErrProtocol
		}
		c.recvCredit -= len(r.Data)
		if !c.closed {
			c.input = append(c.input, r.Data...)
		}
	case "stream.credit":
		if uint64(r.Bytes) > uint64(relayproto.InitialCredit-c.sendCredit) {
			return relayproto.ErrProtocol
		}
		c.sendCredit += int(r.Bytes)
	case "stream.fin":
		if c.remoteFin {
			return relayproto.ErrProtocol
		}
		c.remoteFin = true
	case "stream.reset":
		c.peerReset = true
		if !c.resetting {
			m.resetLocked(c, "closed")
		}
	default:
		return relayproto.ErrProtocol
	}
	m.retireLocked(c)
	m.signalLocked()
	return nil
}

// resetLocked drops payloads immediately but keeps the small credit/state
// snapshot until the reciprocal reset proves that the peer stopped sending.
func (m *Mux) resetLocked(c *Conn, code string) {
	if c.resetting {
		return
	}
	c.resetting = true
	c.reset = code
	c.failure = net.ErrClosed
	c.closed = true
	c.input = nil
	c.credit = 0
	if c.pending != nil {
		c.pending.finish(net.ErrClosed)
		c.pending = nil
	}
}
func (m *Mux) removeLocked(c *Conn) {
	delete(m.streams, c.id)
	for i, s := range m.order {
		if s == c {
			m.order = append(m.order[:i], m.order[i+1:]...)
			if m.cursor > i {
				m.cursor--
			}
			break
		}
	}
}
func (m *Mux) retireLocked(c *Conn) {
	// Retain the final credit exchange: in-flight grants remain valid until all
	// bytes in both directions were consumed, even when FIN already arrived.
	if c.resetting {
		if c.resetSent && c.peerReset {
			m.removeLocked(c)
		}
		return
	}
	if c.localFin && c.remoteFin && c.sendCredit == relayproto.InitialCredit && len(c.input) == 0 && c.credit == 0 && c.recvCredit == relayproto.InitialCredit {
		m.removeLocked(c)
	}
}

type sendItem struct {
	record relayproto.PlainRecord
	conn   *Conn
	op     *writeOp
	credit int
}

func (m *Mux) nextLocked() *sendItem {
	if m.controlTurn && len(m.refused) > 0 {
		m.controlTurn = false
		r := m.refused[0]
		m.refused = m.refused[1:]
		return &sendItem{record: r}
	}
	// At most one record per ready stream per round, including its controls.
	n := len(m.order)
	for range n {
		if m.cursor >= len(m.order) {
			m.cursor = 0
		}
		c := m.order[m.cursor]
		m.cursor++
		item := &sendItem{conn: c, record: relayproto.PlainRecord{V: 1, ID: c.id}}
		switch {
		case c.opening:
			c.opening = false
			c.opened = true
			item.record.Type = "stream.opened"
		case c.reset != "":
			item.record.Type = "stream.reset"
			item.record.Code = c.reset
			c.reset = ""
			c.resetSent = true
		case c.credit > 0:
			item.record.Type = "stream.credit"
			item.credit = c.credit
			c.credit = 0
			c.recvCredit += item.credit
			item.record.Bytes = relayproto.Counter(item.credit)
		case c.pending != nil && c.sendCredit >= len(c.pending.data):
			item.op = c.pending
			c.pending = nil
			c.sendCredit -= len(item.op.data)
			item.record.Type = "stream.data"
			item.record.Data = item.op.data
		case c.finQueued && !c.localFin && c.pending == nil:
			c.localFin = true
			item.record.Type = "stream.fin"
		default:
			continue
		}
		m.controlTurn = true
		return item
	}
	if len(m.refused) > 0 {
		r := m.refused[0]
		m.refused = m.refused[1:]
		return &sendItem{record: r}
	}
	return nil
}
func (m *Mux) write() {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		if m.err != nil {
			m.mu.Unlock()
			return
		}
		item := m.nextLocked()
		m.mu.Unlock()
		if item == nil {
			select {
			case <-m.ctx.Done():
				return
			case <-m.wake:
			}
			continue
		}
		e := m.records.Send(m.ctx, item.record)
		m.mu.Lock()
		c := item.conn
		if item.op != nil {
			item.op.finish(e)
		}
		if c != nil && e == nil {
			switch item.record.Type {
			case "stream.opened":
				c.acceptReady = true
			case "stream.reset":
				// The reciprocal reset is the ordered end of in-flight traffic.
			}
			m.retireLocked(c)
		}
		m.signalLocked()
		m.mu.Unlock()
		if e != nil {
			m.fail(e)
			return
		}
	}
}

var _ net.Listener = (*Mux)(nil)
