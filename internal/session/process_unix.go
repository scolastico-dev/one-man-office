//go:build !windows

package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

type ptyProcess struct {
	cmd    *exec.Cmd
	ptmx   *os.File
	marker string

	exited  chan struct{}
	exitErr error
	// mu orders signals against reaping: while reaped is false the main
	// process's id still belongs to it (alive or a zombie).
	mu     sync.Mutex
	reaped bool
}

const sessionMarkerEnv = "OMO_INTERNAL_SESSION_SCOPE"

func startProcess(o Options) (terminalProcess, error) {
	cmd := exec.Command(o.Cmd, o.Args...)
	cmd.Dir = o.Dir
	var markerBytes [16]byte
	if _, err := rand.Read(markerBytes[:]); err != nil {
		return nil, fmt.Errorf("create process scope: %w", err)
	}
	marker := hex.EncodeToString(markerBytes[:])
	cmd.Env = processEnvironment(append(o.Env, sessionMarkerEnv+"="+marker))
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: o.Rows, Cols: o.Cols})
	if err != nil {
		return nil, err
	}
	if o.LowerPriority {
		// Go exposes no safe pre-exec callback. Adjust the new child by PID
		// before exposing the session; its descendants inherit this nice value.
		if err := lowerProcessPriority(cmd.Process.Pid, o.NiceIncrement); err != nil {
			_ = cmd.Process.Kill()
			_ = ptmx.Close()
			_ = cmd.Wait()
			return nil, fmt.Errorf("lower process priority: %w", err)
		}
	}
	p := &ptyProcess{cmd: cmd, ptmx: ptmx, marker: marker, exited: make(chan struct{})}
	go p.watch()
	return p, nil
}

// watch records the main process's exit without waiting for the terminal.
func (p *ptyProcess) watch() {
	var err error
	if awaitExit(p.cmd.Process.Pid) {
		// The process is a zombie: reap it while no signal can be in flight.
		p.mu.Lock()
		err = p.cmd.Wait()
		p.reaped = true
		p.mu.Unlock()
	} else {
		err = p.cmd.Wait()
		p.mu.Lock()
		p.reaped = true
		p.mu.Unlock()
	}
	p.exitErr = err
	close(p.exited)
}

func (p *ptyProcess) Exited() <-chan struct{} { return p.exited }
func (p *ptyProcess) Pid() int                { return p.cmd.Process.Pid }

func (p *ptyProcess) Read(b []byte) (int, error)  { return p.ptmx.Read(b) }
func (p *ptyProcess) Write(b []byte) (int, error) { return p.ptmx.Write(b) }
func (p *ptyProcess) Resize(rows, cols uint16) error {
	return pty.Setsize(p.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

// Kill ends the main process and everything it started. After the main
// process exited it still ends what it left behind.
func (p *ptyProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.reaped {
		return killSessionProcess(p.cmd.Process, p.marker)
	}
	return killLeftovers(p.cmd.Process.Pid, p.marker)
}

func (p *ptyProcess) Wait() error {
	<-p.exited
	return p.exitErr
}

func (p *ptyProcess) Close() error {
	// Also sweep after a natural agent exit. A detached child may have closed
	// the PTY and been reparented before the session pump observes EOF.
	_ = p.Kill()
	return p.ptmx.Close()
}

// killLeftovers ends what an exited main process left behind: every
// process with the session marker (where the system can list them) and
// the main process's group. The group is signalled only while no process
// has the main process's id, so the id cannot have been reused for a new
// group leader.
func killLeftovers(pid int, marker string) error {
	err := killSessionProcess(nil, marker)
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if groupErr := syscall.Kill(-pid, syscall.SIGKILL); groupErr != nil && !errors.Is(groupErr, syscall.ESRCH) && err == nil {
			err = groupErr
		}
	}
	return err
}
