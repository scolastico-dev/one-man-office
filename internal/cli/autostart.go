package cli

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/service"
)

func addAutostartCommands(root *cobra.Command) {
	autostart := &cobra.Command{Use: "autostart", Short: "Register or unregister omo for user login"}
	autostart.AddCommand(&cobra.Command{
		Use:   "register",
		Short: "Enable user login autostart with the current server options",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := globalhome.Open()
			if err != nil {
				return err
			}
			if _, err := serverconfig.Load(layout.ServerConfig); err != nil {
				return err
			}
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			location, err := service.RegisterAutostart(cmd.Context(), executable)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "omo autostart registered for next login.\nEntry: %s\n", location)
			return nil
		},
	}, &cobra.Command{
		Use:   "unregister",
		Short: "Disable user login autostart without stopping the running server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := globalhome.Open(); err != nil {
				return err
			}
			if err := service.UnregisterAutostart(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "omo autostart unregistered.")
			return nil
		},
	})
	root.AddCommand(autostart, &cobra.Command{
		Use:    "autostart-run FILE",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registration, err := service.LoadAutostart(args[0])
			if err != nil {
				return err
			}
			if err := os.Setenv("OMO_HOME", registration.Home); err != nil {
				return err
			}
			if err := os.Setenv("PATH", registration.Path); err != nil {
				return err
			}
			if err := os.Chdir(registration.Directory); err != nil {
				return err
			}
			invocation := Root(root.Version)
			if runtime.GOOS == "windows" {
				invocation.SetArgs([]string{"--detached"})
			} else {
				invocation.SetArgs([]string{"--background-id=autostart"})
			}
			return invocation.ExecuteContext(cmd.Context())
		},
	})
}
