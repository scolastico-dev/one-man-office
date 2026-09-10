//go:build !windows

package company

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type commandLifecycle struct {
	mu  sync.Mutex
	pid int
}

func configureCommandCancellation(cmd *exec.Cmd) (*commandLifecycle, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	lifecycle := &commandLifecycle{}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		lifecycle.mu.Lock()
		lifecycle.pid = pid
		lifecycle.mu.Unlock()
		err := syscall.Kill(-pid, syscall.SIGKILL)
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return lifecycle, nil
}

func (p *commandLifecycle) Started(cmd *exec.Cmd) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pid = cmd.Process.Pid
	return nil
}

func (p *commandLifecycle) Close() {
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}
