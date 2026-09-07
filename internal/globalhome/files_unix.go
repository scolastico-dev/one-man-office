//go:build !windows

package globalhome

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockFile(f *os.File) error         { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }
func unlockFile(f *os.File)             { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
func replaceFile(from, to string) error { return os.Rename(from, to) }
