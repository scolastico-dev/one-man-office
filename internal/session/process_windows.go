//go:build windows

package session

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/processtree"
)

type conptyProcess struct {
	pty      *processtree.PTY
	mu       sync.Mutex
	closed   bool
	closeErr error
	exited   chan struct{}
	exitErr  error
}

func startProcess(o Options) (terminalProcess, error) {
	p, err := processtree.StartPTY(o.Cmd, o.Args, o.Dir, processEnvironment(o.Env), int(o.Cols), int(o.Rows))
	if err != nil {
		return nil, err
	}
	cp := &conptyProcess{pty: p, exited: make(chan struct{})}
	go cp.watch()
	return cp, nil
}

// watch records the main process's exit. The job object keeps whatever it
// left behind killable until Close.
func (p *conptyProcess) watch() {
	code, err := p.pty.Wait(context.Background())
	if err == nil && code != 0 {
		err = fmt.Errorf("process exited with code %d", code)
	}
	p.exitErr = err
	close(p.exited)
}

func (p *conptyProcess) Exited() <-chan struct{} { return p.exited }
func (p *conptyProcess) Pid() int                { return p.pty.Pid() }

func (p *conptyProcess) Read(b []byte) (int, error)  { return p.pty.Read(b) }
func (p *conptyProcess) Write(b []byte) (int, error) { return p.pty.Write(b) }
func (p *conptyProcess) Resize(rows, cols uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return os.ErrClosed
	}
	return p.pty.Resize(int(cols), int(rows))
}
func (p *conptyProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	return p.pty.Kill()
}
func (p *conptyProcess) Wait() error {
	<-p.exited
	return p.exitErr
}
func (p *conptyProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		// Job closure kills descendants before ConPTY closes its pipes.
		p.closeErr = p.pty.Close()
	}
	return p.closeErr
}
