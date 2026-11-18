package webterm

import (
	"fmt"
	"io"
	"sync"
	"time"
)

const replayLimit = 256 * 1024
const terminalInputLimit = 64 << 10

const terminalDrainGrace = 250 * time.Millisecond

// Process is a child attached to a pseudo-terminal.
type Process interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
	Kill() error
	Wait() error
}

// Info describes a terminal for API consumers.
type Info struct {
	ID      string    `json:"id"`
	State   string    `json:"state"`
	Started time.Time `json:"started"`
	Error   string    `json:"error,omitempty"`
}

type terminalInput struct {
	data    []byte
	written func(error)
	// message, when set, is written as one piece through Messenger (see there)
	// instead of data.
	message *terminalMessage
}

type terminalMessage struct{ text, keys string }

// Messenger is a process that takes a whole message: text, then keys (the
// Enter that submits it), written in one go with nothing else getting between
// them. Without it a message is written as its text and then its keys, which
// anything else writing to the process may split.
type Messenger interface {
	SendMessage(text, keys string) error
}

type terminalInitial struct {
	prefix          []byte
	replay          []byte
	repaintOnResize bool
}

// Terminal owns exactly one PTY and a bounded, in-memory replay buffer. A slow
// browser is disconnected without blocking the child or other subscribers.
type Terminal struct {
	mu           sync.Mutex
	resizeMu     sync.Mutex
	info         Info
	process      Process
	modes        terminalModeTracker
	replay       []byte
	replayChunks []terminalReplayChunk
	replayBytes  int
	streams      map[chan []byte]struct{}
	done         chan struct{}
	killOnce     sync.Once
	killErr      error
	inputQueue   chan terminalInput
	stopInput    chan struct{}
	writerDone   chan struct{}
}

// Options configures Start.
type Options struct {
	ID      string
	Command string
	Args    []string
	// Env is the child's environment; nil is childenv.Inherited.
	Env []string
	Dir string
	// OnExit runs after the child exited and its output was drained.
	OnExit func(*Terminal, error)
}

// Start runs a command on a new pseudo-terminal of 30 rows by 100 columns.
func Start(o Options) (*Terminal, error) {
	p, err := startProcess(o.Command, o.Args, o.Dir, o.Env)
	if err != nil {
		return nil, err
	}
	return Own(o.ID, p, o.OnExit), nil
}

// Own wraps an already started process in a Terminal.
func Own(id string, p Process, onExit func(*Terminal, error)) *Terminal {
	i := &Terminal{info: Info{ID: id, State: "running", Started: time.Now()}, process: p, streams: map[chan []byte]struct{}{}, done: make(chan struct{}), inputQueue: make(chan terminalInput, 8), stopInput: make(chan struct{}), writerDone: make(chan struct{})}
	go func() {
		defer close(i.writerDone)
		for {
			select {
			case <-i.stopInput:
				return
			case input := <-i.inputQueue:
				var err error
				if input.message != nil {
					err = writeTerminalMessage(p, *input.message)
				} else {
					err = writeTerminalInput(p, input.data)
				}
				if input.written != nil {
					input.written(err)
				}
				// A failed message is answered to its sender; the process may
				// still be fine, so the writer goes on. A failed write of typed
				// input ends it, as before.
				if err != nil && input.message == nil {
					return
				}
			}
		}
	}()
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 8192)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				i.publish(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		err := p.Wait()
		close(i.stopInput)
		// Wait may win the scheduling race with the reader even though the
		// PTY still contains final output. Give it a bounded drain window;
		// descendants may retain a slave handle after the direct child exits.
		timer := time.NewTimer(terminalDrainGrace)
		select {
		case <-readerDone:
		case <-timer.C:
		}
		timer.Stop()
		_ = p.Close()
		<-readerDone
		<-i.writerDone
		i.mu.Lock()
		i.info.State = "exited"
		i.modes.resetAll()
		if err != nil {
			i.info.Error = err.Error()
		}
		for stream := range i.streams {
			close(stream)
			delete(i.streams, stream)
		}
		i.mu.Unlock()
		if onExit != nil {
			onExit(i, err)
		}
		close(i.done)
	}()
	return i
}

func (i *Terminal) publish(data []byte) {
	i.mu.Lock()
	defer i.mu.Unlock()
	liveChunk := append([]byte(nil), data...)
	for len(data) > 0 {
		chunkLen := min(len(data), terminalReplayChunkLimit)
		chunk := append([]byte(nil), data[:chunkLen]...)
		replayChunk := terminalReplayChunk{data: chunk, before: i.modes.parserSnapshot(), firstSafe: -1}
		if replayChunk.before.safe {
			replayChunk.firstSafe = 0
		}
		i.modes.feed(chunk, func(offset int) {
			if replayChunk.firstSafe < 0 {
				replayChunk.firstSafe = offset
			}
		})
		i.replayChunks = append(i.replayChunks, replayChunk)
		i.replayBytes += len(chunk)
		data = data[chunkLen:]
	}
	i.replayChunks, i.replayBytes = trimTerminalReplay(i.replayChunks, i.replayBytes, replayLimit)
	i.replay = terminalReplayTail(i.replayChunks, i.replayBytes, replayLimit)
	for stream := range i.streams {
		select {
		case stream <- liveChunk:
		default:
			close(stream)
			delete(i.streams, stream)
		}
	}
}

func (i *Terminal) subscribe() (terminalInitial, <-chan []byte, func()) {
	i.mu.Lock()
	defer i.mu.Unlock()
	stream := make(chan []byte, 64)
	if i.info.State == "exited" {
		close(stream)
	} else {
		i.streams[stream] = struct{}{}
	}
	initial := terminalInitial{
		prefix:          i.modes.prefix(),
		replay:          append([]byte(nil), i.replay...),
		repaintOnResize: i.info.State == "running" && i.modes.alternate(),
	}
	if i.info.State == "exited" {
		initial.prefix = nil
		initial.repaintOnResize = false
	}
	return initial, stream, func() {
		i.mu.Lock()
		defer i.mu.Unlock()
		if _, ok := i.streams[stream]; ok {
			delete(i.streams, stream)
			close(stream)
		}
	}
}

// Info returns a snapshot of the terminal state.
func (i *Terminal) Info() Info { i.mu.Lock(); defer i.mu.Unlock(); return i.info }

func (i *Terminal) input(data []byte) error {
	return i.queueInput(data, nil)
}

func (i *Terminal) queueInput(data []byte, written func(error)) error {
	if len(data) > terminalInputLimit {
		return fmt.Errorf("terminal input exceeds 64 KiB")
	}
	return i.enqueue(terminalInput{data: append([]byte(nil), data...), written: written})
}

// queueMessage queues text and keys to be written as one message.
func (i *Terminal) queueMessage(text, keys string, written func(error)) error {
	if len(text) > terminalInputLimit || len(keys) > 16 {
		return fmt.Errorf("terminal message exceeds 64 KiB")
	}
	return i.enqueue(terminalInput{message: &terminalMessage{text: text, keys: keys}, written: written})
}

func (i *Terminal) enqueue(in terminalInput) error {
	select {
	case <-i.stopInput:
		return fmt.Errorf("terminal has exited")
	case <-i.writerDone:
		return fmt.Errorf("terminal input is closed")
	default:
	}
	select {
	case i.inputQueue <- in:
		return nil
	default:
		return fmt.Errorf("terminal input queue is full")
	}
}

func (i *Terminal) resize(rows, cols uint16) error {
	if !validTerminalSize(rows, cols) {
		return fmt.Errorf("terminal size must be 1–500 rows and 2–1000 columns")
	}
	i.resizeMu.Lock()
	defer i.resizeMu.Unlock()
	return i.process.Resize(rows, cols)
}

func validTerminalSize(rows, cols uint16) bool {
	return rows >= 1 && cols >= 2 && rows <= 500 && cols <= 1000
}

// repaint serializes the temporary neighboring size and the final requested
// size with ordinary resizes so the PTY observes one coherent redraw.
func (i *Terminal) repaint(rows, cols uint16) error {
	if !validTerminalSize(rows, cols) {
		return fmt.Errorf("terminal size must be 1–500 rows and 2–1000 columns")
	}
	neighbor := cols - 1
	if neighbor < 2 {
		neighbor = cols + 1
	}
	i.resizeMu.Lock()
	defer i.resizeMu.Unlock()
	wiggleErr := i.process.Resize(rows, neighbor)
	finalErr := i.process.Resize(rows, cols)
	if wiggleErr != nil {
		return wiggleErr
	}
	return finalErr
}

// Done is closed after the process exited, its output was drained and OnExit ran.
func (i *Terminal) Done() <-chan struct{} { return i.done }

// Kill force-stops the process and all of its descendants.
func (i *Terminal) Kill() error {
	i.killOnce.Do(func() {
		select {
		case <-i.done:
			return
		default:
		}
		i.killErr = i.process.Kill()
	})
	return i.killErr
}

// A terminal writer may consume only part of a large input. Acknowledge only
// after every byte has been written, including split UTF-8 and paste markers.
func writeTerminalInput(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func writeTerminalMessage(p io.Writer, m terminalMessage) error {
	if messenger, ok := p.(Messenger); ok {
		return messenger.SendMessage(m.text, m.keys)
	}
	if err := writeTerminalInput(p, []byte(m.text)); err != nil {
		return err
	}
	return writeTerminalInput(p, []byte(m.keys))
}
