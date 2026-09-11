//go:build windows

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

func execSetup(ctx context.Context, destination string) error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	return execSetupBinary(ctx, binary, destination)
}

func execSetupBinary(ctx context.Context, binary, destination string) error {
	setup := exec.CommandContext(ctx, binary, "setup", destination)
	setup.Stdin = os.Stdin
	setup.Stdout = os.Stdout
	setup.Stderr = os.Stderr
	if err := setup.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}
