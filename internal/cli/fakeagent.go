package cli

import (
	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/fakeagent"
)

func addFakeAgentCommand(root *cobra.Command) {
	var scenario, autoRole string
	cmd := &cobra.Command{
		Use:    "fake-agent",
		Hidden: true,
		Short:  "Scenario-driven fake agent CLI (tests and --mock offices)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fakeagent.Run(cmd.OutOrStdout(), scenario, autoRole)
		},
	}
	cmd.Flags().StringVar(&scenario, "scenario", "", "scenario file")
	cmd.Flags().StringVar(&autoRole, "auto-role", "", "use the embedded default scenario for this role")
	root.AddCommand(cmd)
}
