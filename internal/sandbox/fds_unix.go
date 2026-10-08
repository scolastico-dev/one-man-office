//go:build !windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// closeInheritedFDs marks every open descriptor above stderr close-on-exec.
// /dev/fd enumerates the live descriptor table, including entries beyond 1023.
// Marking rather than closing keeps Go runtime descriptors usable until exec.
func closeInheritedFDs(first int) error {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return fmt.Errorf("enumerate inherited descriptors: %w", err)
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd < first {
			continue
		}
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if errors.Is(err, unix.EBADF) {
			continue
		} // enumeration descriptor closed by ReadDir
		if err != nil {
			return fmt.Errorf("inspect inherited descriptor %d: %w", fd, err)
		}
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, flags|unix.FD_CLOEXEC); err != nil {
			return fmt.Errorf("close inherited descriptor %d on exec: %w", fd, err)
		}
	}
	return nil
}
