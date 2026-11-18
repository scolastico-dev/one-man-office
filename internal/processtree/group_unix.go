//go:build !windows

package processtree

import (
	"os/exec"
	"sync"
	"syscall"
)

type Group struct {
	mu  sync.Mutex
	pid int
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
// registration order. Setpgid already took effect before the child ran.
func (g *Group) Started(cmd *exec.Cmd) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pid = cmd.Process.Pid
	return nil
}
func (g *Group) Kill() error {
	g.mu.Lock()
	pid := g.pid
	g.mu.Unlock()
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	return nil
}
func (g *Group) Close() { _ = g.Kill() }

// These wrappers preserve each Unix caller's existing cancellation policy.
func Run(cmd *exec.Cmd) error                      { return cmd.Run() }
func Output(cmd *exec.Cmd) ([]byte, error)         { return cmd.Output() }
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) { return cmd.CombinedOutput() }
