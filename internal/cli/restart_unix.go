//go:build !windows

package cli

import (
	"os"
	"syscall"
)

func restartProcess(executable string) error {
	return syscall.Exec(executable, append([]string{executable}, os.Args[1:]...), os.Environ())
}
