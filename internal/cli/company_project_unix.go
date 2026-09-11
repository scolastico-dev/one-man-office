//go:build !windows

package cli

import (
	"context"
	"os"
	"syscall"
)

func execSetup(ctx context.Context, destination string) error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	return execSetupBinary(ctx, binary, destination)
}

func execSetupBinary(_ context.Context, binary, destination string) error {
	return syscall.Exec(binary, []string{binary, "setup", destination}, os.Environ())
}
