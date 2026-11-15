package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/server"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/service"
)

func addServerCommands(root *cobra.Command) {
	var detached bool
	var backgroundID string
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return startServer(ctx, cmd, root.Version, detached, backgroundID)
	}
	root.Flags().BoolVarP(&detached, "detached", "d", false, "start in the background and print the access URL when ready")
	root.Flags().StringVar(&backgroundID, "background-id", "", "internal detached launch identity")
	_ = root.Flags().MarkHidden("background-id")

	root.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the running omo server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := globalhome.Open(); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			err := service.Stop(ctx)
			if errors.Is(err, service.ErrNotRunning) {
				fmt.Fprintln(cmd.OutOrStdout(), "omo is not running.")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "omo stopped.")
			return nil
		},
	})
}

func startServer(ctx context.Context, cmd *cobra.Command, version string, detached bool, backgroundID string) error {
	layout, err := globalhome.Open()
	if err != nil {
		return err
	}
	config, err := serverconfig.Load(layout.ServerConfig)
	if err != nil {
		return err
	}
	// A false answer can also mean a server that is still starting; the run
	// lock inside service.Run decides in that case.
	if url, running, err := service.Running(ctx); err != nil {
		return err
	} else if running {
		fmt.Fprintf(cmd.OutOrStdout(), "omo is already running: %s\n", url)
		return nil
	}
	if detached {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		return service.StartDetached(ctx, executable, cmd.OutOrStdout())
	}
	var out io.Writer = cmd.OutOrStdout()
	if backgroundID != "" {
		out = nil
	}
	options := server.Options{Layout: layout, Config: config, Version: version}
	return service.Run(ctx, out, backgroundID, func(ctx context.Context, out io.Writer, ready func(string) error) error {
		return server.Run(ctx, options, out, ready)
	})
}
