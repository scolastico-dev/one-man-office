// Package cli defines every omo command. The root command (no subcommand)
// starts the office; subcommands are the agent-facing verbs.
package cli

import "github.com/spf13/cobra"

// Root builds the command tree. version is stamped at build time and
// surfaced through `omo --version`.
func Root(version string) *cobra.Command {
	var f officeFlags
	cmd := &cobra.Command{
		Use:           "omo",
		Short:         "one-man-office — supervises AI CLI agents working in parallel",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOffice(cmd, f, version)
		},
	}
	cmd.Flags().BoolVar(&f.mock, "mock", false, "run the office on scenario-driven fake agents")
	cmd.Flags().BoolVar(&f.noTUI, "no-tui", false, "headless mode (CI): no TUI, stop with Ctrl+C")
	cmd.Flags().BoolVar(&f.skipStartupChecks, "skip-startup-checks", false, "skip release and editable-template freshness checks")
	addMailCommands(cmd)
	addAgentVerbCommands(cmd)
	addJobCommands(cmd)
	addPowerCommands(cmd)
	addReloadCommand(cmd)
	addLogsCommand(cmd)
	addTypeCommand(cmd)
	addFakeAgentCommand(cmd)
	addSetupCommand(cmd)
	addRepoCommands(cmd)
	return cmd
}
