package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/modelusage"
	"github.com/scolastico-dev/one-man-office/internal/supervisorservice"
	"github.com/scolastico-dev/one-man-office/internal/websupervisor"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func supervisorFlags(cmd *cobra.Command, options *websupervisor.Options) {
	cmd.Flags().StringVar(&options.Listen, "listen", "127.0.0.1:8090", "dashboard bind address; exposing it grants command execution to access-URL holders")
	cmd.Flags().IntVar(&options.MaxAgents, "max-agents", 12, "aggregate simultaneous agents across all launched offices (all roles)")
	cmd.Flags().DurationVar(&options.UsageTTL, "usage-cache-ttl", modelusage.DefaultCacheTTL, "shared Claude/Codex usage cache lifetime")
	cmd.Flags().BoolVar(&options.Mock, "mock", false, "launch offices with fake agents and no model calls")
	cmd.Flags().BoolVar(&options.Unsafe, "unsafe", false, "disable dashboard token authentication (unsafe; keep access restricted)")
	cmd.Flags().StringVar(&options.BasicAuth, "basic-auth", "", "use HTTP Basic authentication as USER:PASSWORD (trusted networks only)")
	cmd.Flags().BoolVar(&options.NoOriginCheck, "no-origin-check", false, "disable Origin verification for a trusted reverse proxy")
}

func validateSupervisor(options websupervisor.Options) error {
	if options.MaxAgents < 1 {
		return fmt.Errorf("max-agents must be positive")
	}
	if options.UsageTTL < time.Second {
		return fmt.Errorf("usage-cache-ttl must be at least one second")
	}
	return websupervisor.ValidateOptions(options)
}

// Snapshot every setting, including defaults and explicit false values, so a
// later binary's defaults cannot change the registered invocation.
func supervisorArgs(cmd *cobra.Command) []string {
	var args []string
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		switch flag.Name {
		case "help", "detached", "background-id":
			return
		}
		args = append(args, "--"+flag.Name+"="+flag.Value.String())
	})
	return args
}

func addSupervisorCommand(root *cobra.Command) {
	var options websupervisor.Options
	var detached bool
	var backgroundID string
	cmd := &cobra.Command{Use: "supervisor", Short: "Run a local web dashboard for trusted offices and shell terminals", Args: cobra.NoArgs,
		Long: "Run a local web dashboard for trusted offices and shell terminals.\nThe access URL grants command execution with your permissions.\nUse --detached to run in the background and 'supervisor stop' to stop it.\nOne supervisor runs per OMO_HOME; autostart registration applies at user login.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateSupervisor(options); err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			if detached {
				executable, err := os.Executable()
				if err != nil {
					return err
				}
				return supervisorservice.StartDetached(ctx, executable, supervisorArgs(cmd), cmd.OutOrStdout())
			}
			var out io.Writer = cmd.OutOrStdout()
			if backgroundID != "" {
				out = nil
			}
			return supervisorservice.Run(ctx, options, out, backgroundID)
		}}
	supervisorFlags(cmd, &options)
	cmd.Flags().BoolVarP(&detached, "detached", "d", false, "start in the background and print the access URL when ready")
	cmd.Flags().StringVar(&backgroundID, "background-id", "", "internal detached launch identity")
	_ = cmd.Flags().MarkHidden("background-id")
	stop := &cobra.Command{Use: "stop", Short: "Stop the supervisor and clean up its owned offices and shells", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		err := supervisorservice.Stop(ctx)
		if errors.Is(err, supervisorservice.ErrNotRunning) {
			fmt.Fprintln(cmd.OutOrStdout(), "Supervisor is not running.")
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Supervisor stopped.")
		return nil
	}}
	autostart := &cobra.Command{Use: "autostart", Short: "Register or unregister the supervisor for user login"}
	var startupOptions websupervisor.Options
	register := &cobra.Command{Use: "register", Short: "Save these supervisor settings and enable user login autostart", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := validateSupervisor(startupOptions); err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		location, err := supervisorservice.RegisterAutostart(cmd.Context(), executable, append([]string{"supervisor"}, supervisorArgs(cmd)...))
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Supervisor autostart registered for next login.\nEntry: %s\n", location)
		return nil
	}}
	supervisorFlags(register, &startupOptions)
	unregister := &cobra.Command{Use: "unregister", Short: "Disable user login autostart without stopping the running supervisor", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := supervisorservice.UnregisterAutostart(cmd.Context()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Supervisor autostart unregistered.")
		return nil
	}}
	autostart.AddCommand(register, unregister)
	autoRun := &cobra.Command{Use: "autostart-run SETTINGS", Hidden: true, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		registration, err := supervisorservice.LoadAutostart(args[0])
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
		flags := append([]string(nil), registration.Args...)
		if runtime.GOOS == "windows" {
			flags = append(flags, "--detached")
		} else {
			flags = append(flags, "--background-id=autostart")
		}
		invocation.SetArgs(flags)
		return invocation.ExecuteContext(cmd.Context())
	}}
	cmd.AddCommand(stop, autostart, autoRun)
	root.AddCommand(cmd, &cobra.Command{Use: "supervisor-shell", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return websupervisor.RunShell(cmd.Context()) }})
}
