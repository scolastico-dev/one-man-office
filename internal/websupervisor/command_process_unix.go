//go:build !windows

package websupervisor

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type commandLifecycle struct {
	pid int
}

func configureCommandCancellation(cmd *exec.Cmd) (*commandLifecycle, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	lifecycle := &commandLifecycle{}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		lifecycle.pid = cmd.Process.Pid
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return lifecycle, nil
}

func (p *commandLifecycle) Started(cmd *exec.Cmd) error {
	p.pid = cmd.Process.Pid
	return nil
}

func (p *commandLifecycle) Close() {
	if p.pid > 0 {
		_ = syscall.Kill(-p.pid, syscall.SIGKILL)
	}
}
