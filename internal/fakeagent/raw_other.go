//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package fakeagent

import (
	"errors"
	"os"
)

// rawInput is not available here: the composer modes need a Unix terminal.
func rawInput(*os.File) error {
	return errors.New("the input-line modes of the fake agent need a Unix terminal")
}
