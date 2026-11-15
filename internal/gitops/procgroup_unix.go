//go:build !windows

package gitops

import (
	"os/exec"
	"syscall"
)

// killWithChildren puts git in its own process group and kills the whole
// group when the command's context ends.
func killWithChildren(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
