//go:build !windows

package gitops

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// killWithChildren puts git in its own process group. When the command's
// context ends the group gets SIGTERM, which lets git remove its lock files
// (index.lock, ref locks) on the way out; whatever is left of the group
// after grace gets SIGKILL. The caller sets cmd.WaitDelay to grace and calls
// the returned function once Wait has returned.
func killWithChildren(cmd *exec.Cmd, grace time.Duration) (afterWait func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var (
		mu       sync.Mutex
		pid      int
		canceled time.Time
	)
	cmd.Cancel = func() error {
		mu.Lock()
		pid, canceled = cmd.Process.Pid, time.Now()
		mu.Unlock()
		return syscall.Kill(-pid, syscall.SIGTERM)
	}
	return func() {
		mu.Lock()
		p, at := pid, canceled
		mu.Unlock()
		if p == 0 {
			return
		}
		for deadline := at.Add(grace); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if syscall.Kill(-p, 0) != nil {
				return // the whole group is gone
			}
		}
		_ = syscall.Kill(-p, syscall.SIGKILL)
	}
}

// openNoFollow opens path for reading without following a link at its last
// element and without blocking on a named pipe.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
