package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/scolastico-dev/one-man-office/internal/company"
	"github.com/spf13/cobra"
)

func addCompanyProjectCommand(root *cobra.Command) {
	cmd := &cobra.Command{
		Use:    "company-project create <destination> | clone <source> <destination>",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "create" {
				if len(args) != 2 {
					return fmt.Errorf("company-project create requires a destination")
				}
				return execSetup(cmd.Context(), args[1])
			}
			if args[0] != "clone" || len(args) != 3 {
				return fmt.Errorf("company-project requires create <destination> or clone <source> <destination>")
			}
			return cloneAndSetup(cmd.Context(), args[1], args[2])
		},
	}
	root.AddCommand(cmd)
}

func cloneAndSetup(ctx context.Context, source, destination string) error {
	clone := exec.CommandContext(ctx, "git", "-c", "protocol.ext.allow=never", "clone", "--", source, destination)
	clone.Stdin = os.Stdin
	clone.Stdout = os.Stdout
	clone.Stderr = os.Stderr
	if err := clone.Run(); err != nil {
		return fmt.Errorf("clone failed: %w", err)
	}
	if err := company.ValidateSetupTree(destination); err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	return execSetupBinary(ctx, binary, destination)
}
