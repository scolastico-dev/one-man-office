//go:build linux

package session

import (
	"errors"

	"golang.org/x/sys/unix"
)

// awaitExit blocks until pid has exited and leaves it unreaped (a zombie
// keeps its id). It reports false if it could not wait that way.
func awaitExit(pid int) bool {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err == nil {
			return true
		}
		if !errors.Is(err, unix.EINTR) {
			return false
		}
	}
}
