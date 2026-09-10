//go:build !windows

package company

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

type ptyProcess struct {
	cmd       *exec.Cmd
	terminal  *os.File
	closeOnce sync.Once
	closeErr  error
}

func startProcess(command string, args []string, dir string, env []string) (terminalProcess, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Env = env
	if env == nil {
		cmd.Env = os.Environ()
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
	return pty.Setsize(p.terminal, &pty.Winsize{Rows: rows, Cols: cols})
}
func (p *ptyProcess) Close() error {
	p.closeOnce.Do(func() { p.closeErr = p.terminal.Close() })
	return p.closeErr
}
func (p *ptyProcess) Wait() error { return p.cmd.Wait() }
func (p *ptyProcess) Kill() error { return killTree(p.cmd.Process) }

// Agents create their own PTY sessions, so killing only the office process group
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
	if output, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=").Output(); err == nil {
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
