//go:build !windows

package plugins

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type luaCommandLifecycle struct {
	mu  sync.Mutex
	pid int
}

func configureLuaCommandCancellation(cmd *exec.Cmd) (*luaCommandLifecycle, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	lifecycle := &luaCommandLifecycle{}
	cmd.Cancel = func() error {
		lifecycle.mu.Lock()
		pid := lifecycle.pid
		lifecycle.mu.Unlock()
		if pid <= 0 {
			return nil
		}
		err := syscall.Kill(-pid, syscall.SIGKILL)
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return lifecycle, nil
}

func (p *luaCommandLifecycle) Started(cmd *exec.Cmd) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pid = cmd.Process.Pid
	return nil
}

func (p *luaCommandLifecycle) Close() {
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}
