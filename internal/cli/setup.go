package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/office"
)

func addSetupCommand(root *cobra.Command) {
	var update bool
	cmd := &cobra.Command{
		Use:   "setup [dir]",
		Short: "Scaffold an office, or replace its editable templates with --update",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			if update {
				replaced, err := office.UpdateTemplates(dir)
				if err != nil {
					return err
				}
				for _, path := range replaced {
					fmt.Fprintln(cmd.OutOrStdout(), "replaced", path)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "config, database, logs, worktrees and other office state were not changed")
				return nil
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			created, err := office.Setup(dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(created) == 0 {
				fmt.Fprintln(out, "office already set up — nothing to do")
			}
			for _, c := range created {
				fmt.Fprintln(out, "created", c)
			}
			fmt.Fprintf(out, "\nNext: add your repos and check the profiles in %s, then run 'omo'.\n", office.ConfigPath)
			fmt.Fprintln(out, "Try it first with fake agents and no model calls: omo --mock")
			return nil
		},
	}
	cmd.Flags().BoolVar(&update, "update", false, "replace .omo/messages and .omo/prompts with this version's defaults")
	root.AddCommand(cmd)
}
