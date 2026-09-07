package websupervisor

import (
	"fmt"
	"io"
	"sync"
	"time"
)

const replayLimit = 256 * 1024

const terminalDrainGrace = 250 * time.Millisecond

type terminalProcess interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
	Kill() error
	Wait() error
}

type InstanceInfo struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Mode    string    `json:"mode"`
	State   string    `json:"state"`
	Started time.Time `json:"started"`
	Error   string    `json:"error,omitempty"`
}

// Instance owns exactly one PTY and a bounded, in-memory replay buffer. A slow
// browser is disconnected without blocking the child or other subscribers.
type Instance struct {
	mu         sync.Mutex
	resizeMu   sync.Mutex
	info       InstanceInfo
	process    terminalProcess
	replay     []byte
	streams    map[chan []byte]struct{}
	done       chan struct{}
	killOnce   sync.Once
	killErr    error
	inputQueue chan []byte
	stopInput  chan struct{}
	writerDone chan struct{}
}

func startInstance(id, path, mode, command string, args, env []string, onExit func()) (*Instance, error) {
	p, err := startProcess(command, args, path, env)
	if err != nil {
		return nil, err
	}
	return ownInstance(id, path, mode, p, onExit), nil
}

func ownInstance(id, path, mode string, p terminalProcess, onExit func()) *Instance {
	i := &Instance{info: InstanceInfo{ID: id, Path: path, Mode: mode, State: "running", Started: time.Now()}, process: p, streams: map[chan []byte]struct{}{}, done: make(chan struct{}), inputQueue: make(chan []byte, 8), stopInput: make(chan struct{}), writerDone: make(chan struct{})}
	go func() {
		defer close(i.writerDone)
		for {
			select {
			case <-i.stopInput:
				return
			case data := <-i.inputQueue:
				if _, err := p.Write(data); err != nil {
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
		if onExit != nil {
			onExit()
		}
		i.mu.Lock()
		i.info.State = "exited"
		if err != nil {
			i.info.Error = err.Error()
		}
		for stream := range i.streams {
			close(stream)
			delete(i.streams, stream)
		}
		i.mu.Unlock()
		close(i.done)
	}()
	return i
}

func (i *Instance) publish(data []byte) {
	i.mu.Lock()
	defer i.mu.Unlock()
	chunk := append([]byte(nil), data...)
	i.replay = append(i.replay, chunk...)
	if len(i.replay) > replayLimit {
		i.replay = append([]byte(nil), i.replay[len(i.replay)-replayLimit:]...)
	}
	for stream := range i.streams {
		select {
		case stream <- chunk:
		default:
			close(stream)
			delete(i.streams, stream)
		}
	}
}

func (i *Instance) subscribe() ([]byte, <-chan []byte, func()) {
	i.mu.Lock()
	defer i.mu.Unlock()
	stream := make(chan []byte, 64)
	if i.info.State == "exited" {
		close(stream)
	} else {
		i.streams[stream] = struct{}{}
	}
	return append([]byte(nil), i.replay...), stream, func() {
		i.mu.Lock()
		defer i.mu.Unlock()
		if _, ok := i.streams[stream]; ok {
			delete(i.streams, stream)
			close(stream)
		}
	}
}

func (i *Instance) snapshot() InstanceInfo { i.mu.Lock(); defer i.mu.Unlock(); return i.info }

func (i *Instance) input(data []byte) error {
	if len(data) > 64<<10 {
		return fmt.Errorf("terminal input exceeds 64 KiB")
	}
	select {
	case <-i.stopInput:
		return fmt.Errorf("terminal has exited")
	case <-i.writerDone:
		return fmt.Errorf("terminal input is closed")
	default:
	}
	select {
	case i.inputQueue <- append([]byte(nil), data...):
		return nil
	default:
		return fmt.Errorf("terminal input queue is full")
	}
}

func (i *Instance) resize(rows, cols uint16) error {
	if rows < 1 || cols < 2 || rows > 500 || cols > 1000 {
		return fmt.Errorf("terminal size must be 1–500 rows and 2–1000 columns")
	}
	i.resizeMu.Lock()
	defer i.resizeMu.Unlock()
	return i.process.Resize(rows, cols)
}

func (i *Instance) kill() error {
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
