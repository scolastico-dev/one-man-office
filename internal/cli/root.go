// Package cli defines every omo command.
package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// Root builds the command tree. version is stamped at build time and
// surfaced through `omo --version`.
func Root(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "omo",
		Short:         "one-man-office: supervises AI CLI agents working in parallel",
		Long:          "Start the omo server in the foreground. One omo runs per OMO_HOME;\na second start reports the running one. The access URL grants command\nexecution with your permissions.",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.CompletionOptions.DisableDefaultCmd = true
	// The automatic help command would show up in the command list; the
	// --help flag stays available.
	cmd.SetHelpCommand(&cobra.Command{Use: "help [command]", Hidden: true, Run: func(c *cobra.Command, args []string) {
		target, _, err := c.Root().Find(args)
		if err != nil || target == nil {
			target = c.Root()
		}
		_ = target.Help()
	}})
	// The stock template lists the help command even when it is hidden.
	cmd.SetUsageTemplate(strings.ReplaceAll(cmd.UsageTemplate(), `(or .IsAvailableCommand (eq .Name "help"))`, `.IsAvailableCommand`))
	addServerCommands(cmd)
	addConfigCommand(cmd)
	addAutostartCommands(cmd)
	addUpdateCommand(cmd, version)
	addAICommand(cmd)
	addMCPCommand(cmd)
	addRelayCommand(cmd)
	return cmd
}
