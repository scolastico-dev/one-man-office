//go:build windows

package gitops

import (
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
)

// killWithChildren kills git and its child processes when the command's
// context ends. Windows has no SIGTERM a console program like git could
// clean up on, so there is no grace period: the tree is ended at once, which
// can leave index.lock behind.
func killWithChildren(cmd *exec.Cmd, grace time.Duration) (afterWait func()) {
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.Env = childenv.Inherited()
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return func() {}
}

// openNoFollow opens path for reading. The caller has checked with Lstat
// that it is a regular file and compares the opened file with that.
func openNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}
