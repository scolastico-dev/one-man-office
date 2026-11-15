//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package fakeagent

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
