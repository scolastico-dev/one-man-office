//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package fakeagent

import (
	"os"

	"golang.org/x/sys/unix"
)

// rawInput switches the terminal on f to character input without echo, so
// the fake can echo and submit itself like a full-screen CLI.
func rawInput(f *os.File) error {
	fd := int(f.Fd())
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return err
	}
	t.Lflag &^= unix.ECHO | unix.ICANON
	t.Iflag &^= unix.ICRNL
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	return unix.IoctlSetTermios(fd, ioctlSetTermios, t)
}
