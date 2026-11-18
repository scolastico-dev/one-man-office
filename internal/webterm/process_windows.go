//go:build windows

package webterm

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/processtree"
)

type conptyProcess struct {
	terminal *processtree.PTY
	mu       sync.Mutex
	closed   bool
	closeErr error
}

func startProcess(command string, args []string, dir string, env []string) (Process, error) {
	if env == nil {
		env = childenv.Inherited()
	}
	p, err := processtree.StartPTY(command, args, dir, env, 100, 30)
	if err != nil {
		return nil, err
	}
	return &conptyProcess{terminal: p}, nil
}

func (p *conptyProcess) Read(b []byte) (int, error)  { return p.terminal.Read(b) }
func (p *conptyProcess) Write(b []byte) (int, error) { return p.terminal.Write(b) }
func (p *conptyProcess) Resize(rows, cols uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return os.ErrClosed
	}
	return p.terminal.Resize(int(cols), int(rows))
}
func (p *conptyProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	return p.terminal.Kill()
}
func (p *conptyProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		// Close the job first so descendants cannot retain console handles
		// and prevent the pseudo-console close from completing.
		p.closeErr = p.terminal.Close()
	}
	return p.closeErr
}
func (p *conptyProcess) Wait() error {
	code, err := p.terminal.Wait(context.Background())
	if err != nil {
		return err
	}
	if code != 0 {
		return processExitError{code: int(code)}
	}
	return nil
}

type processExitError struct{ code int }

func (e processExitError) Error() string { return fmt.Sprintf("process exited with code %d", e.code) }
func (e processExitError) ExitCode() int { return e.code }

func killTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}
