//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
)

// run starts Node with the standard streams of the shim and exits with its
// code: Windows has no exec. The plugin's kill-on-close job holds both
// processes, so ending the task ends Node as well.
func run(target string, args []string) error {
	cmd := exec.Command(target, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err == nil {
		os.Exit(0)
	}
	return err
}
