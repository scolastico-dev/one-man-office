//go:build !windows

package usage

import "syscall"

func init() {
	extraBadFile = func(path string) { syscall.Mkfifo(path, 0o600) }
}
