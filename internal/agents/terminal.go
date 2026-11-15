package agents

import (
	"io"
	"sync"
	"time"
)

// outputBufferLimit bounds the agent output waiting for the web terminal.
// The terminal reads continuously and never waits for a viewer, so the
// buffer only fills if that reader stalls; then the oldest bytes go (the
// terminal's replay is smaller than this anyway).
const outputBufferLimit = 1 << 20

// outputPipe carries the raw agent output to the web terminal. Unlike
// io.Pipe its writer never blocks: the agent's output loop must not wait for
// anybody.
type outputPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	ended  bool // no more writes will come
	closed bool // the reader went away
}

func newOutputPipe() *outputPipe {
	p := &outputPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// write copies chunk into the buffer; it never blocks.
func (p *outputPipe) write(chunk []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended || p.closed || len(chunk) == 0 {
		return
	}
	p.buf = append(p.buf, chunk...)
	if over := len(p.buf) - outputBufferLimit; over > 0 {
		p.buf = append(p.buf[:0:0], p.buf[over:]...)
	}
	p.cond.Broadcast()
}

// end marks the end of the output; the reader gets io.EOF once drained.
func (p *outputPipe) end() {
	p.mu.Lock()
	p.ended = true
	p.mu.Unlock()
	p.cond.Broadcast()
}

func (p *outputPipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 && !p.ended && !p.closed {
		p.cond.Wait()
	}
	if p.closed {
		return 0, io.EOF
	}
	if len(p.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	return n, nil
}

func (p *outputPipe) close() {
	p.mu.Lock()
	p.closed = true
	p.buf = nil
	p.mu.Unlock()
	p.cond.Broadcast()
}

// inputClock remembers when a person last typed into the terminal.
type inputClock struct {
	mu   sync.Mutex
	last time.Time
}

func (c *inputClock) record(t time.Time) {
	c.mu.Lock()
	c.last = t
	c.mu.Unlock()
}

func (c *inputClock) lastInput() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// terminalSession is what the web terminal needs from the agent's PTY.
type terminalSession interface {
	SendText(text string) error
	Resize(rows, cols uint16) error
	Done() <-chan struct{}
	ExitErr() error
}

// terminalAdapter presents the agent's PTY to webterm.Own: it reads the
// teed output, writes viewer input to the PTY and records when it arrived.
type terminalAdapter struct {
	sess   terminalSession
	out    *outputPipe
	input  *inputClock
	now    func() time.Time
	kill   func() error
	closed sync.Once
}

func (a *terminalAdapter) Read(b []byte) (int, error) { return a.out.Read(b) }

// Write is input from a viewer. It counts as a person typing, which holds
// back notifications.
func (a *terminalAdapter) Write(b []byte) (int, error) {
	a.input.record(a.now())
	if err := a.sess.SendText(string(b)); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (a *terminalAdapter) Resize(rows, cols uint16) error { return a.sess.Resize(rows, cols) }
func (a *terminalAdapter) Kill() error                    { return a.kill() }

func (a *terminalAdapter) Wait() error {
	<-a.sess.Done()
	return a.sess.ExitErr()
}

// Close only detaches the reader; the PTY belongs to the session.
func (a *terminalAdapter) Close() error {
	a.closed.Do(a.out.close)
	return nil
}
