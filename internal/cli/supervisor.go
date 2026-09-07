package cli

import (
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/modelusage"
	"github.com/scolastico-dev/one-man-office/internal/websupervisor"
	"github.com/spf13/cobra"
)

func addSupervisorCommand(root *cobra.Command) {
	var options websupervisor.Options
	cmd := &cobra.Command{Use: "supervisor", Short: "Run a local web dashboard for trusted offices and shell terminals", Args: cobra.NoArgs,
		Long: "Run a local web dashboard for trusted offices and shell terminals.\nThe access URL grants command execution with your user permissions.\nAgent capacity and Claude/Codex usage caching are shared across child offices.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.MaxAgents < 1 {
				return fmt.Errorf("max-agents must be positive")
			}
			if options.UsageTTL < time.Second {
				return fmt.Errorf("usage-cache-ttl must be at least one second")
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer cancel()
			return websupervisor.Run(ctx, options, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&options.Listen, "listen", "127.0.0.1:8090", "dashboard bind address; exposing it grants command execution to access-URL holders")
	cmd.Flags().IntVar(&options.MaxAgents, "max-agents", 12, "aggregate simultaneous agents across all launched offices (all roles)")
	cmd.Flags().DurationVar(&options.UsageTTL, "usage-cache-ttl", modelusage.DefaultCacheTTL, "shared Claude/Codex usage cache lifetime")
	cmd.Flags().BoolVar(&options.Mock, "mock", false, "launch offices with fake agents and no model calls")
	root.AddCommand(cmd, &cobra.Command{Use: "supervisor-shell", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return websupervisor.RunShell(cmd.Context()) }})
}
