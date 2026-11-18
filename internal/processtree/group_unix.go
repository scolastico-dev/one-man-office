//go:build !windows

package processtree

import (
	"os/exec"
	"sync"
	"syscall"
)

type Group struct {
	mu     sync.Mutex
	pid    int
	killed bool
}

func New(cmd *exec.Cmd) (*Group, error) {
	attr := &syscall.SysProcAttr{}
	if cmd.SysProcAttr != nil {
		*attr = *cmd.SysProcAttr
	}
	attr.Setpgid = true
	cmd.SysProcAttr = attr
	g := &Group{}
	cmd.Cancel = func() error { return g.Kill() }
	return g, nil
}

func (g *Group) Start(cmd *exec.Cmd) error { return cmd.Start() }

// Started records the PID after Start, preserving the callers' cancellation
// registration order. Setpgid already took effect before the child ran. A
// Kill that came between Start and Started found no PID to signal: it is
// remembered and carried out here, so a cancellation in that window cannot
// leave the program running.
func (g *Group) Started(cmd *exec.Cmd) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pid = cmd.Process.Pid
	if g.killed {
		_ = syscall.Kill(-g.pid, syscall.SIGKILL)
	}
	return nil
}

// Kill holds mu while it signals, so Started cannot record the PID between
// the check and the note that the group was killed.
func (g *Group) Kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.killed = true
	if g.pid > 0 {
		_ = syscall.Kill(-g.pid, syscall.SIGKILL)
	}
	return nil
}
func (g *Group) Close() { _ = g.Kill() }

// These wrappers preserve each Unix caller's existing cancellation policy.
func Run(cmd *exec.Cmd) error                      { return cmd.Run() }
func Output(cmd *exec.Cmd) ([]byte, error)         { return cmd.Output() }
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) { return cmd.CombinedOutput() }
