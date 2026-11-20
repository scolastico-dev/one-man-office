package relaytransport

import (
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

type writeOp struct {
	data []byte
	done chan error
}

func (o *writeOp) finish(e error) { o.done <- e }

// Conn is a flow-controlled byte stream. Its deadlines also cover waiting for
// peer credit and the shared record writer. CloseWrite preserves read access.
type Conn struct {
	mux                                    *Mux
	id                                     relayproto.Counter
	readMu, writeMu                        sync.Mutex
	input                                  []byte
	recvCredit, sendCredit, credit         int
	opening, opened, accepted, acceptReady bool
	remoteFin, localFin, finQueued, closed bool
	reset                                  string
	resetting, peerReset, resetSent        bool
	failure                                error
	pending                                *writeOp
	readDeadline, writeDeadline            time.Time
}

func (c *Conn) LocalAddr() net.Addr  { return c.mux.Addr() }
func (c *Conn) RemoteAddr() net.Addr { return address("authenticated-ui") }
func (c *Conn) SetDeadline(t time.Time) error {
	c.mux.mu.Lock()
	defer c.mux.mu.Unlock()
	c.readDeadline = t
	c.writeDeadline = t
	c.mux.signalLocked()
	return nil
}
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.mux.mu.Lock()
	defer c.mux.mu.Unlock()
	c.readDeadline = t
	c.mux.signalLocked()
	return nil
}
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.mux.mu.Lock()
	defer c.mux.mu.Unlock()
	c.writeDeadline = t
	c.mux.signalLocked()
	return nil
}
func waitChange(ch <-chan struct{}, deadline time.Time) error {
	if deadline.IsZero() {
		<-ch
		return nil
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ch:
		return nil
	case <-timer.C:
		return os.ErrDeadlineExceeded
	}
}
func (c *Conn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	m := c.mux
	for {
		m.mu.Lock()
		if c.failure != nil || c.closed {
			e := c.failure
			if e == nil {
				e = net.ErrClosed
			}
			m.mu.Unlock()
			return 0, e
		}
		if !c.readDeadline.IsZero() && !time.Now().Before(c.readDeadline) {
			m.mu.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		if len(b) == 0 {
			m.mu.Unlock()
			return 0, nil
		}
		if len(c.input) > 0 {
			n := copy(b, c.input)
			c.input = c.input[n:]
			c.credit += n
			m.signalLocked()
			m.mu.Unlock()
			return n, nil
		}
		if c.remoteFin {
			m.mu.Unlock()
			return 0, io.EOF
		}
		ch, d := m.changed, c.readDeadline
		m.mu.Unlock()
		if e := waitChange(ch, d); e != nil {
			return 0, e
		}
	}
}
func (c *Conn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	written := 0
	m := c.mux
	for len(b) > 0 {
		m.mu.Lock()
		if c.failure != nil || c.closed || c.finQueued {
			e := c.failure
			if e == nil {
				e = net.ErrClosed
			}
			m.mu.Unlock()
			return written, e
		}
		if !c.writeDeadline.IsZero() && !time.Now().Before(c.writeDeadline) {
			m.mu.Unlock()
			return written, os.ErrDeadlineExceeded
		}
		if c.sendCredit == 0 {
			ch, d := m.changed, c.writeDeadline
			m.mu.Unlock()
			if e := waitChange(ch, d); e != nil {
				return written, e
			}
			continue
		}
		n := min(len(b), relayproto.MaxStreamData)
		// A partial grant must be usable without waiting for a whole data chunk.
		if c.sendCredit > 0 {
			n = min(n, c.sendCredit)
		}
		op := &writeOp{data: append([]byte(nil), b[:n]...), done: make(chan error, 1)}
		c.pending = op
		m.signalLocked()
		m.mu.Unlock()
		for {
			m.mu.Lock()
			if c.failure != nil || c.closed {
				e := c.failure
				if e == nil {
					e = net.ErrClosed
				}
				m.mu.Unlock()
				return written, e
			}
			d, ch := c.writeDeadline, m.changed
			m.mu.Unlock()
			select {
			case e := <-op.done:
				if e != nil {
					return written, e
				}
				written += n
				b = b[n:]
				goto sent
			default:
			}
			if e := waitChange(ch, d); e != nil {
				m.mu.Lock()
				if c.pending == op {
					c.pending = nil
				}
				m.resetLocked(c, "cancelled")
				m.signalLocked()
				m.mu.Unlock()
				return written, e
			}
		}
	sent:
	}
	return written, nil
}
func (c *Conn) CloseWrite() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	m := c.mux
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.failure != nil {
		return c.failure
	}
	c.finQueued = true
	m.signalLocked()
	return nil
}
func (c *Conn) Close() error {
	m := c.mux
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.pending != nil {
		c.pending.finish(net.ErrClosed)
		c.pending = nil
	}
	if len(c.input) > 0 {
		m.resetLocked(c, "closed")
	} else {
		c.finQueued = true
	}
	m.signalLocked()
	return nil
}

var _ net.Conn = (*Conn)(nil)
