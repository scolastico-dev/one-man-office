//go:build !windows

package main

import (
	"os"
	"syscall"
)

// run replaces the shim with Node, as `exec node` would: the process the
// plugin started is the Node process, with the same identity and signals.
// It returns only when the replacement failed.
func run(target string, args []string) error {
	return syscall.Exec(target, append([]string{target}, args...), os.Environ())
}
