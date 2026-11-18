package agents

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"time"
)

// outputBufferLimit bounds the agent output waiting for the web terminal.
// The terminal reads continuously and never waits for a viewer, so the
// buffer only fills if that reader stalls; then the oldest bytes go (the
// terminal's replay is smaller than this anyway).
const outputBufferLimit = 1 << 20

// outputTrimScan is how far past the dropped bytes a trim looks for a line
// end, so the reader resumes at the start of a line rather than inside an
// escape sequence.
const outputTrimScan = 4 << 10

// outputPipe carries the raw agent output to the web terminal. Unlike
// io.Pipe its writer never blocks: the agent's output loop must not wait for
// anybody. The bytes live in a ring buffer, so a write copies only itself.
type outputPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	ring   []byte // allocated on first write, outputBufferLimit bytes
	start  int    // offset of the oldest unread byte
	size   int    // unread bytes
	ended  bool   // no more writes will come
	closed bool   // the reader went away
}

func newOutputPipe() *outputPipe {
	p := &outputPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// write copies chunk into the buffer; it never blocks. When the buffer is
// full the oldest bytes are dropped, up to the next line end if there is
// one within outputTrimScan bytes.
func (p *outputPipe) write(chunk []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended || p.closed || len(chunk) == 0 {
		return
	}
	if p.ring == nil {
		p.ring = make([]byte, outputBufferLimit)
	}
	if len(chunk) >= len(p.ring) {
		chunk = chunk[len(chunk)-len(p.ring):]
		p.start, p.size = 0, 0
	}
	if over := p.size + len(chunk) - len(p.ring); over > 0 {
		p.drop(over)
		p.dropToLineEnd()
	}
	end := (p.start + p.size) % len(p.ring)
	n := copy(p.ring[end:], chunk)
	copy(p.ring, chunk[n:])
	p.size += len(chunk)
	p.cond.Broadcast()
}

// drop discards the n oldest bytes.
func (p *outputPipe) drop(n int) {
	p.start = (p.start + n) % len(p.ring)
	p.size -= n
}

// dropToLineEnd discards the oldest bytes up to and including the first
// line end within outputTrimScan bytes; with none there it keeps them.
func (p *outputPipe) dropToLineEnd() {
	scan := min(p.size, outputTrimScan)
	first := p.ring[p.start:min(p.start+scan, len(p.ring))]
	if i := bytes.IndexByte(first, '\n'); i >= 0 {
		p.drop(i + 1)
		return
	}
	if rest := scan - len(first); rest > 0 {
		if i := bytes.IndexByte(p.ring[:rest], '\n'); i >= 0 {
			p.drop(len(first) + i + 1)
		}
	}
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
	for p.size == 0 && !p.ended && !p.closed {
		p.cond.Wait()
	}
	if p.closed || p.size == 0 {
		return 0, io.EOF
	}
	n := copy(b[:min(len(b), p.size)], p.ring[p.start:min(p.start+p.size, len(p.ring))])
	if n < len(b) && n < p.size {
		n += copy(b[n:min(len(b), p.size)], p.ring)
	}
	p.drop(n)
	return n, nil
}

func (p *outputPipe) close() {
	p.mu.Lock()
	p.closed = true
	p.ring, p.start, p.size = nil, 0, 0
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
	// SendTextAndKeysIf writes text and then keys while holding the input.
	SendTextAndKeysIf(text, keys string, ready func() bool) (bool, error)
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

// Write is input from a viewer. Keys a person pressed count as typing,
// which holds back notifications; what the browser terminal sends by itself
// does not (see typedByPerson).
func (a *terminalAdapter) Write(b []byte) (int, error) {
	if typedByPerson(b) {
		a.input.record(a.now())
	}
	if err := a.sess.SendText(string(b)); err != nil {
		return 0, err
	}
	return len(b), nil
}

// SendMessage is a message from a viewer's draft box: the text (already a
// paste, with its markers) and then keys, written while nothing else can write,
// so a notification line cannot end up inside it. It counts as typing.
func (a *terminalAdapter) SendMessage(text, keys string) error {
	a.input.record(a.now())
	_, err := a.sess.SendTextAndKeysIf(text, keys, func() bool { return true })
	return err
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

// typedByPerson reports whether viewer input contains anything other than
// what a terminal emulator sends without a key press: focus reports
// (ESC [ I, ESC [ O), mouse reports (X10, SGR and urxvt), answers to
// queries (device attributes, cursor position and status reports, mode
// reports, window reports, OSC and DCS strings) and the bracketed-paste
// markers themselves. Plain bytes, a lone Escape and every other escape
// sequence (cursor keys, function keys, Alt combinations) are keys.
func typedByPerson(b []byte) bool {
	for i := 0; i < len(b); {
		if b[i] != 0x1b {
			return true
		}
		if i+1 == len(b) {
			return true // the Escape key
		}
		switch b[i+1] {
		case '[':
			n, automatic := csiReport(b[i+2:])
			if !automatic {
				return true
			}
			i += 2 + n
		case ']', 'P', '_', '^':
			i += 2 + stringEnd(b[i+2:])
		default:
			return true // Alt+key, SS3 keys
		}
	}
	return false
}

// csiReport looks at a CSI sequence after "ESC [". It returns its length
// and whether it is something a terminal sends by itself. An incomplete
// sequence counts as automatic: it is the cut-off end of a report.
func csiReport(b []byte) (int, bool) {
	if len(b) > 0 && b[0] == 'M' { // X10 mouse: ESC [ M Cb Cx Cy
		return min(4, len(b)), true
	}
	j := 0
	for j < len(b) && b[j] >= 0x30 && b[j] <= 0x3f {
		j++
	}
	params := string(b[:j])
	for j < len(b) && b[j] >= 0x20 && b[j] <= 0x2f {
		j++
	}
	if j == len(b) {
		return j, true
	}
	final := b[j]
	private := params != "" && strings.ContainsRune("<=>?", rune(params[0]))
	switch {
	case params == "" && (final == 'I' || final == 'O'): // focus in/out
	case params != "" && params[0] == '<' && (final == 'M' || final == 'm'): // SGR mouse
	case params != "" && final == 'M': // urxvt mouse
	case private && final == 'c': // device attributes
	// A modified F3 (ESC [ 1 ; 2 R) looks the same as a cursor position
	// report; the report is far more common in viewer input.
	case final == 'R': // cursor position report
	case final == 'n': // status report
	case final == 'y': // mode report (DECRPM)
	case final == 't': // window report
	case (params == "200" || params == "201") && final == '~': // paste markers
	default:
		return j + 1, false
	}
	return j + 1, true
}

// stringEnd is the length of an OSC/DCS/APC/PM body including its
// terminator (BEL or ESC \), or of everything if it is cut off.
func stringEnd(b []byte) int {
	for j := 0; j < len(b); j++ {
		if b[j] == 0x07 {
			return j + 1
		}
		if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
			return j + 2
		}
	}
	return len(b)
}
