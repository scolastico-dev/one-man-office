package cli

import "github.com/spf13/cobra"

// addAICommand registers the group the agent verbs hang off.
func addAICommand(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:   "ai",
		Short: "Commands for agents working on the board",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	})
}
