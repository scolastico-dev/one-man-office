//go:build windows

package gitops

import (
	"os/exec"
	"strconv"
	"time"
)

// killWithChildren kills git and its child processes when the command's
// context ends. Windows has no SIGTERM a console program like git could
// clean up on, so there is no grace period: the tree is ended at once, which
// can leave index.lock behind.
func killWithChildren(cmd *exec.Cmd, grace time.Duration) (afterWait func()) {
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return func() {}
}
