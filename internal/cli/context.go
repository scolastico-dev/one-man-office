package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/proto"
)

func addContextCommands(root *cobra.Command) {
	contextCmd := &cobra.Command{Use: "context", Short: "Durable agent handoff context"}
	var filePath string
	save := &cobra.Command{
		Use:   "save [summary]",
		Short: "Save a concise safe-shutdown handoff to the office database",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary := ""
			switch {
			case filePath != "":
				raw, err := os.ReadFile(filePath)
				if err != nil {
					return err
				}
				summary = string(raw)
			case len(args) == 1:
				summary = args[0]
			default:
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				summary = string(raw)
			}
			summary = strings.TrimSpace(summary)
			if summary == "" {
				return fmt.Errorf("a concise handoff summary is required")
			}
			if err := call("context.save", proto.ContextSaveArgs{Summary: summary}, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "safe-shutdown context saved")
			return nil
		},
	}
	save.Flags().StringVarP(&filePath, "file", "f", "", "read handoff summary from a file instead of arguments or stdin")
	contextCmd.AddCommand(save)
	root.AddCommand(contextCmd)
}
