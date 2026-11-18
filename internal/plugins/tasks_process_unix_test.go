//go:build !windows

package plugins

import "syscall"

func taskProcessAlive(pid int) bool { return syscall.Kill(pid, 0) != syscall.ESRCH }
