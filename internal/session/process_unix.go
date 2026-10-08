//go:build !windows

package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

type ptyProcess struct {
	cmd    *exec.Cmd
	ptmx   *os.File
	marker string
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
	var ptmx *os.File
	var err error
	if o.PrepareLauncher == nil {
		ptmx, err = pty.StartWithSize(cmd, &pty.Winsize{Rows: o.Rows, Cols: o.Cols})
	} else {
		var tty *os.File
		ptmx, tty, err = pty.Open()
		if err != nil {
			return nil, err
		}
		defer tty.Close()
		if err = pty.Setsize(ptmx, &pty.Winsize{Rows: o.Rows, Cols: o.Cols}); err != nil {
			ptmx.Close()
			return nil, err
		}
		launch, launchErr := o.PrepareLauncher(tty.Name())
		if launchErr != nil {
			ptmx.Close()
			return nil, launchErr
		}
		if launch.Cmd == "" {
			ptmx.Close()
			return nil, fmt.Errorf("sandbox launcher command is empty")
		}
		cmd = exec.Command(launch.Cmd, launch.Args...)
		cmd.Dir = o.Dir
		// The launcher supplies a complete, already-filtered environment.
		// Re-merging os.Environ here would restore denied parent credentials.
		cmd.Env = append(append([]string(nil), launch.Env...), sessionMarkerEnv+"="+marker)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
		err = cmd.Start()
		if err != nil {
			ptmx.Close()
		}
	}
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
	return &ptyProcess{cmd: cmd, ptmx: ptmx, marker: marker}, nil
}

func (p *ptyProcess) Read(b []byte) (int, error)  { return p.ptmx.Read(b) }
func (p *ptyProcess) Write(b []byte) (int, error) { return p.ptmx.Write(b) }
func (p *ptyProcess) Resize(rows, cols uint16) error {
	return pty.Setsize(p.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}
func (p *ptyProcess) Kill() error {
	return killSessionProcess(p.cmd.Process, p.marker)
}
func (p *ptyProcess) Wait() error { return p.cmd.Wait() }
func (p *ptyProcess) Close() error {
	// Also sweep after a natural agent exit. A detached child may have closed
	// the PTY and been reparented before the session pump observes EOF.
	var leader *os.Process
	if p.cmd.ProcessState == nil {
		leader = p.cmd.Process
	}
	_ = killSessionProcess(leader, p.marker)
	return p.ptmx.Close()
}
