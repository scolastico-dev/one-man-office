//go:build linux

package session

import (
	"errors"

	"golang.org/x/sys/unix"
)

const maxNiceValue = 19

func lowerProcessPriority(pid, increment int) error {
	rawPriority, err := unix.Getpriority(unix.PRIO_PROCESS, pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return nil // The child completed before its priority could be changed.
		}
		return err
	}
	// Linux's raw getpriority result is 20 minus the user-visible nice value.
	currentNice := 20 - rawPriority
	targetNice := min(currentNice+increment, maxNiceValue)
	if targetNice <= currentNice {
		return nil
	}
	if err := unix.Setpriority(unix.PRIO_PROCESS, pid, targetNice); errors.Is(err, unix.ESRCH) {
		return nil
	} else {
		return err
	}
}
