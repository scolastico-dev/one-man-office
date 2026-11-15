//go:build windows

package gitops

import (
	"os/exec"
	"strconv"
)

// killWithChildren kills git and its child processes when the command's
// context ends.
func killWithChildren(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
