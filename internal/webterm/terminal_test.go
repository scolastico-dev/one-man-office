package webterm

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// controlledTerminal exposes the two OS timing boundaries exercised here:
// a child which does not consume input, and output still readable after Wait.
type controlledTerminal struct {
	readReady    chan struct{}
	exited       chan struct{}
	closed       chan struct{}
	writeStarted chan struct{}
	output       *strings.Reader
	waitErr      error
	resizeMu     sync.Mutex
	resizes      [][2]uint16
	resizeFailAt int
	resizeErr    error
	closeOnce    sync.Once
}

func (p *controlledTerminal) Read(b []byte) (int, error) {
	select {
	case <-p.closed:
		return 0, io.EOF
	case <-p.readReady:
		return p.output.Read(b)
	}
}
func (p *controlledTerminal) Write(b []byte) (int, error) {
	select {
	case p.writeStarted <- struct{}{}:
	default:
	}
	<-p.closed
	return 0, io.ErrClosedPipe
}
func (p *controlledTerminal) Resize(rows, cols uint16) error {
	p.resizeMu.Lock()
	p.resizes = append(p.resizes, [2]uint16{rows, cols})
	call := len(p.resizes)
	err := p.resizeErr
	if call != p.resizeFailAt {
		err = nil
	}
	p.resizeMu.Unlock()
	return err
}
func (p *controlledTerminal) Wait() error  { <-p.exited; return p.waitErr }
func (p *controlledTerminal) Kill() error  { return p.Close() }
func (p *controlledTerminal) Close() error { p.closeOnce.Do(func() { close(p.closed) }); return nil }
func terminalFixture(t *testing.T) *controlledTerminal {
	t.Helper()
	p := &controlledTerminal{readReady: make(chan struct{}), exited: make(chan struct{}), closed: make(chan struct{}), writeStarted: make(chan struct{}, 1), output: strings.NewReader("last output\n")}
	t.Cleanup(func() {
		_ = p.Close()
		select {
		case <-p.exited:
		default:
			close(p.exited)
		}
	})
	return p
}

func TestBlockedTerminalInputIsBoundedAndDoesNotBlockResize(t *testing.T) {
	p := terminalFixture(t)
	i := Own("input", p, nil)
	defer p.Close()
	defer close(p.exited)
	accepted := make(chan error, 1)
	go func() { accepted <- i.input([]byte("first")) }()
	err := <-accepted
	if err != nil {
		t.Fatal(err)
	}
	<-p.writeStarted
	full := false
	for range 100 {
		if err := i.input([]byte("queued")); err != nil {
			full = true
			break
		}
	}
	if !full {
		t.Fatal("terminal input queue is unbounded")
	}
	resized := make(chan error, 1)
	go func() { resized <- i.resize(30, 100) }()
	err = <-resized
	if err != nil {
		t.Fatal(err)
	}
}

func TestProcessExitDrainsFinalTerminalOutput(t *testing.T) {
	p := terminalFixture(t)
	i := Own("tail", p, nil)
	defer p.Close()
	close(p.exited)
	// stopInput closes only after the process waiter observes the exit.
	// Release the final read after that boundary instead of after a delay.
	<-i.stopInput
	close(p.readReady)
	<-i.done
	initial, _, detach := i.subscribe()
	defer detach()
	if string(initial.replay) != "last output\n" {
		t.Fatalf("final output was lost: %q", initial.replay)
	}
}

func TestTerminalSubscribeCapturesModePrefixAndSafeReplay(t *testing.T) {
	p := terminalFixture(t)
	i := Own("snapshot", p, nil)
	defer p.Close()
	i.publish([]byte("\x1b[?1049h\x1b[?1002h\x1b[?1006h\x1b[?1004h\x1b[?2004h"))
	i.publish([]byte("\x1b[?25l" + strings.Repeat("x", replayLimit+32)))
	initial, _, detach := i.subscribe()
	defer detach()
	if string(initial.prefix) != "\x1b[?1049h\x1b[?25l\x1b[?1004h\x1b[?2004h\x1b[?1002h\x1b[?1006h" {
		t.Fatalf("prefix = %q", initial.prefix)
	}
	if len(initial.replay) > replayLimit {
		t.Fatalf("replay length = %d, want <= %d", len(initial.replay), replayLimit)
	}
	if !initial.repaintOnResize {
		t.Fatal("alternate running snapshot did not arm repaint")
	}
	initial.prefix[0] = 'x'
	initial.replay[0] = 'y'
	again, _, detachAgain := i.subscribe()
	defer detachAgain()
	if again.prefix[0] == 'x' || again.replay[0] == 'y' {
		t.Fatal("subscribe returned mutable internal buffers")
	}
}

func TestTerminalReplayChunksStayBoundedAndCutAtObservedSafeOffset(t *testing.T) {
	p := terminalFixture(t)
	i := Own("chunked-replay", p, nil)
	defer p.Close()
	data := append(bytes.Repeat([]byte{'x'}, replayLimit+terminalReplayChunkLimit), []byte("\x1b[31mTAIL")...)
	i.publish(data)
	for index, chunk := range i.replayChunks {
		if len(chunk.data) > terminalReplayChunkLimit {
			t.Fatalf("chunk %d length = %d, want <= %d", index, len(chunk.data), terminalReplayChunkLimit)
		}
	}
	initial, _, detach := i.subscribe()
	defer detach()
	if len(initial.replay) > replayLimit {
		t.Fatalf("replay length = %d, want <= %d", len(initial.replay), replayLimit)
	}
	if got, want := string(initial.replay[len(initial.replay)-4:]), "TAIL"; got != want {
		t.Fatalf("replay suffix = %q, want %q", got, want)
	}
}

func TestTerminalReplayFirstSafeOffsetDistinguishesChunkStartAndAfterByte(t *testing.T) {
	p := terminalFixture(t)
	i := Own("safe-offset", p, nil)
	defer p.Close()
	i.publish([]byte("x\x1b["))
	i.publish([]byte("31m"))
	if got := i.replayChunks[0].firstSafe; got != 0 {
		t.Fatalf("safe chunk firstSafe = %d, want pre-chunk zero", got)
	}
	if got, want := i.replayChunks[1].firstSafe, 3; got != want {
		t.Fatalf("completed escape firstSafe = %d, want after-byte offset %d", got, want)
	}
}

func TestTerminalSubscribeClearsModeStateAfterProcessExit(t *testing.T) {
	p := terminalFixture(t)
	i := Own("exit-modes", p, nil)
	defer p.Close()
	i.publish([]byte("\x1b[?1049h\x1b[?2004h"))
	close(p.exited)
	<-i.done
	initial, _, detach := i.subscribe()
	defer detach()
	if len(initial.prefix) != 0 || initial.repaintOnResize {
		t.Fatalf("exited snapshot retained mode state: %+v", initial)
	}
}

func TestTerminalRepaintWigglesAndEndsAtRequestedSize(t *testing.T) {
	p := terminalFixture(t)
	i := Own("repaint", p, nil)
	defer p.Close()
	if err := i.repaint(40, 120); err != nil {
		t.Fatal(err)
	}
	p.resizeMu.Lock()
	got := append([][2]uint16(nil), p.resizes...)
	p.resizeMu.Unlock()
	if want := [][2]uint16{{40, 119}, {40, 120}}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("resize calls = %v, want %v", got, want)
	}
}

func TestTerminalRepaintAttemptsFinalSizeAfterWiggleFailure(t *testing.T) {
	p := terminalFixture(t)
	p.resizeFailAt = 1
	p.resizeErr = errors.New("temporary resize failure")
	i := Own("repaint-error", p, nil)
	defer p.Close()
	if err := i.repaint(40, 120); err == nil {
		t.Fatal("repaint hid temporary resize failure")
	}
	p.resizeMu.Lock()
	got := append([][2]uint16(nil), p.resizes...)
	p.resizeMu.Unlock()
	if want := [][2]uint16{{40, 119}, {40, 120}}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("resize calls after failure = %v, want %v", got, want)
	}
}

func TestTerminalReplayDoesNotRetainContinuationAfterIncompleteEscape(t *testing.T) {
	p := terminalFixture(t)
	i := Own("replay-boundary", p, nil)
	defer p.Close()
	first := append([]byte{'\x1b', '['}, bytes.Repeat([]byte{'0'}, replayLimit)...)
	i.publish(first)
	i.publish([]byte("htail"))
	initial, _, detach := i.subscribe()
	defer detach()
	if got, want := string(initial.replay), "tail"; got != want {
		t.Fatalf("replay after split escape = %q, want %q", got, want)
	}
}

func TestTerminalReplayRestartsAtEscapeAfterDiscardedIntermediateSequence(t *testing.T) {
	for _, test := range []struct {
		name  string
		first []byte
	}{
		{name: "intermediate", first: append([]byte{'\x1b'}, bytes.Repeat([]byte{'('}, replayLimit+1)...)},
		{name: "csi", first: append([]byte{'\x1b', '['}, bytes.Repeat([]byte{'0'}, replayLimit)...)},
		{name: "osc", first: append([]byte{'\x1b', ']'}, bytes.Repeat([]byte{'x'}, replayLimit+1)...)},
		{name: "dcs", first: append([]byte{'\x1b', 'P'}, bytes.Repeat([]byte{'x'}, replayLimit+1)...)},
		{name: "apc", first: append([]byte{'\x1b', '^'}, bytes.Repeat([]byte{'x'}, replayLimit+1)...)},
		{name: "pm", first: append([]byte{'\x1b', '_'}, bytes.Repeat([]byte{'x'}, replayLimit+1)...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := terminalFixture(t)
			i := Own("replay-restart-"+test.name, p, nil)
			defer p.Close()
			i.publish(test.first)
			i.publish([]byte("\x1b[31mTAIL"))
			initial, _, detach := i.subscribe()
			defer detach()
			if got, want := string(initial.replay), "TAIL"; got != want {
				t.Fatalf("replay after restarted escape = %q, want %q", got, want)
			}
		})
	}
}

func TestTerminalReplayRestartsAtSplitStringEscape(t *testing.T) {
	p := terminalFixture(t)
	i := Own("replay-split-string-restart", p, nil)
	defer p.Close()
	first := append(append([]byte{'\x1b', ']'}, bytes.Repeat([]byte{'x'}, replayLimit+1)...), '\x1b')
	i.publish(first)
	i.publish([]byte("[31mTAIL"))
	initial, _, detach := i.subscribe()
	defer detach()
	if got, want := string(initial.replay), "TAIL"; got != want {
		t.Fatalf("replay after split string restart = %q, want %q", got, want)
	}
}

func TestProcessExitBoundsDrainForInheritedTerminalHandles(t *testing.T) {
	p := terminalFixture(t)
	i := Own("drain", p, nil)
	defer p.Close()
	close(p.exited)
	<-i.done
}

func TestProcessExitCallbackReceivesWaitResult(t *testing.T) {
	p := terminalFixture(t)
	called := make(chan error, 1)
	_ = Own("callback", p, func(_ *Terminal, err error) { called <- err })
	defer p.Close()
	close(p.exited)
	err := <-called
	if err != nil {
		t.Fatalf("wait result = %v", err)
	}
}
