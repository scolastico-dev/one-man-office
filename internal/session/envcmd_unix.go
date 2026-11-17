//go:build !windows

package session

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup puts cmd in a process group of its own and makes the
// context's cancellation kill that whole group, so nothing the command
// started outlives a timeout.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
