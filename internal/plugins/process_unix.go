//go:build !windows

package plugins

import (
	"os/exec"
	"sync"
	"syscall"
)

// processGroup ends a program together with everything it started: the
// program runs in its own process group, and cancelling its command or
// closing the group kills the whole group.
type processGroup struct {
	mu      sync.Mutex
	pid     int
	stopped bool
}

// newProcessGroup prepares cmd to run in its own group. Call started after
// cmd.Start and close once cmd ended.
func newProcessGroup(cmd *exec.Cmd) (*processGroup, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	g := &processGroup{}
	cmd.Cancel = func() error {
		g.kill()
		return nil
	}
	return g, nil
}

func (g *processGroup) started(cmd *exec.Cmd) error {
	g.mu.Lock()
	g.pid = cmd.Process.Pid
	stopped := g.stopped
	g.mu.Unlock()
	if stopped {
		g.kill()
	}
	return nil
}

func (g *processGroup) kill() {
	g.mu.Lock()
	// Cancel can run before started publishes the PID. Remember it so
	// publication also ends a process whose cancellation arrived first.
	g.stopped = true
	pid := g.pid
	g.mu.Unlock()
	if pid <= 0 {
		return
	}
	// The group may be gone already (ESRCH); nothing is left to do then.
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

// close kills what is left of the group.
func (g *processGroup) close() { g.kill() }
