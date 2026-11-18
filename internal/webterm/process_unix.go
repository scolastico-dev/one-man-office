//go:build !windows

package webterm

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
)

type ptyProcess struct {
	cmd      *exec.Cmd
	terminal *os.File
	// terminalMu keeps ioctl's descriptor access inside the file's lifetime.
	// creack/pty.Setsize calls File.Fd, which cannot race with File.Close.
	// closed also forbids descriptor access while in-flight I/O finishes closing it.
	terminalMu sync.Mutex
	closed     bool
	closeOnce  sync.Once
	closeErr   error
}

func startProcess(command string, args []string, dir string, env []string) (Process, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Env = env
	if env == nil {
		cmd.Env = childenv.Inherited()
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		return nil, err
	}
	return &ptyProcess{cmd: cmd, terminal: f}, nil
}

func (p *ptyProcess) Read(b []byte) (int, error)  { return p.terminal.Read(b) }
func (p *ptyProcess) Write(b []byte) (int, error) { return p.terminal.Write(b) }
func (p *ptyProcess) Resize(rows, cols uint16) error {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	if p.closed {
		return os.ErrClosed
	}
	return pty.Setsize(p.terminal, &pty.Winsize{Rows: rows, Cols: cols})
}
func (p *ptyProcess) Close() error {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	p.closeOnce.Do(func() {
		p.closed = true
		p.closeErr = p.terminal.Close()
	})
	return p.closeErr
}
func (p *ptyProcess) Wait() error { return p.cmd.Wait() }
func (p *ptyProcess) Kill() error { return killTree(p.cmd.Process) }

// Agents create their own PTY sessions, so killing only the owner process group
// misses them. Snapshot descendants first and kill deepest-first, including
// every observed descendant's process group. This is force-stop, not a sandbox:
// deliberately detached/reparented commands cannot be contained this way.
func killTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	// Freeze the owner before inspecting descendants: otherwise killing an
	// agent can trigger its replacement between the snapshot and root kill.
	if err := process.Signal(syscall.SIGSTOP); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	_ = syscall.Kill(-process.Pid, syscall.SIGSTOP)
	children := map[int][]int{}
	ps := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=")
	ps.Env = childenv.Inherited()
	if output, err := ps.Output(); err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			pid, e1 := strconv.Atoi(fields[0])
			parent, e2 := strconv.Atoi(fields[1])
			if e1 == nil && e2 == nil {
				children[parent] = append(children[parent], pid)
			}
		}
	}
	var stop func(int)
	var freeze func(int)
	freeze = func(pid int) {
		_ = syscall.Kill(-pid, syscall.SIGSTOP)
		_ = syscall.Kill(pid, syscall.SIGSTOP)
		for _, child := range children[pid] {
			freeze(child)
		}
	}
	for _, child := range children[process.Pid] {
		freeze(child)
	}
	stop = func(pid int) {
		for _, child := range children[pid] {
			stop(child)
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	for _, child := range children[process.Pid] {
		stop(child)
	}
	_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
	err := process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
