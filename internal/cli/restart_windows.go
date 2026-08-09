//go:build windows

package cli

import (
	"os"
	"os/exec"
)

func restartProcess(executable string) error {
	child := exec.Command(executable, os.Args[1:]...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.Dir, _ = os.Getwd()
	return child.Run()
}
