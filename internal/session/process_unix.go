//go:build !windows

package session

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type ptyProcess struct {
	cmd  *exec.Cmd
	ptmx *os.File
}

func startProcess(o Options) (terminalProcess, error) {
	cmd := exec.Command(o.Cmd, o.Args...)
	cmd.Dir = o.Dir
	cmd.Env = append(os.Environ(), o.Env...)
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
	return &ptyProcess{cmd: cmd, ptmx: ptmx}, nil
}

func (p *ptyProcess) Read(b []byte) (int, error)  { return p.ptmx.Read(b) }
func (p *ptyProcess) Write(b []byte) (int, error) { return p.ptmx.Write(b) }
func (p *ptyProcess) Resize(rows, cols uint16) error {
	return pty.Setsize(p.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}
func (p *ptyProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
func (p *ptyProcess) Wait() error  { return p.cmd.Wait() }
func (p *ptyProcess) Close() error { return p.ptmx.Close() }
