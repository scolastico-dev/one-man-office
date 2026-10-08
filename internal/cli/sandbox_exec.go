package cli

import (
	"errors"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/sandbox"
	"github.com/spf13/cobra"
)

func addSandboxExecCommand(root *cobra.Command) {
	var policy string
	command := &cobra.Command{
		Use:    "__sandbox-exec --policy <absolute-json> -- <cmd> <args...>",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if policy == "" {
				return errors.New("sandbox policy path is required")
			}
			if err := sandbox.Exec(policy, args[0], args[1:]); err != nil {
				return fmt.Errorf("sandbox launch: %w", err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&policy, "policy", "", "prepared policy JSON")
	root.AddCommand(command)
}
