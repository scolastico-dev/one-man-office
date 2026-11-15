// Package cli defines every omo command.
package cli

import "github.com/spf13/cobra"

// Root builds the command tree. version is stamped at build time and
// surfaced through `omo --version`.
func Root(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "omo",
		Short:         "one-man-office: supervises AI CLI agents working in parallel",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addUpdateCommand(cmd, version)
	return cmd
}
