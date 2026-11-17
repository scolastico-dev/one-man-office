package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/server"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/service"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// stopGrace is how long a stop may take beyond the agents' handoff.
const stopGrace = 30 * time.Second

// stopServer is the seam the stop command's tests replace.
var stopServer = service.Stop

// serverRunning is the seam that lets tests decide whether a server answers.
var serverRunning = service.Running

// detectAgentsAtFirstStart is the one switch for agent detection at a first
// start. When true, the first start of omo on a home (no settings.yaml and no
// profile yet) looks for claude, codex, agy and gemini on PATH and writes a
// profile for each one found, whose models start the program with the
// argument that lets it work without asking for permission, and assigns
// models to the roles that have none. It never runs a program it finds and
// never changes an existing profile or role. When false, only the "Detect
// agent programs" button does this. TestFirstStartDetectionSwitch pins it.
const detectAgentsAtFirstStart = true

// agentLookPath finds agent programs for the detection at a first start. The
// package's tests set it to nil, so no test looks at the real PATH.
var agentLookPath = exec.LookPath

// firstStartDetection is server.Options.DetectAgents: nil turns the
// detection at a first start off.
func firstStartDetection() func(string) (string, error) {
	if !detectAgentsAtFirstStart || agentLookPath == nil {
		return nil
	}
	return agentLookPath
}

func addServerCommands(root *cobra.Command) {
	var detached bool
	var backgroundID string
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx, hurry, stop := stopSignals(cmd.Context())
		defer stop()
		return startServer(ctx, hurry, cmd, root.Version, detached, backgroundID)
	}
	root.Flags().BoolVarP(&detached, "detached", "d", false, "start in the background and print the access URL when ready")
	root.Flags().StringVar(&backgroundID, "background-id", "", "internal detached launch identity")
	_ = root.Flags().MarkHidden("background-id")

	root.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the running omo server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := globalhome.Open()
			if err != nil {
				return err
			}
			// The server asks its agents for a handoff first, which takes up
			// to stop.handoff_timeout, then needs some time to shut down.
			handoff := time.Duration(settings.LoadGeneral(layout.Settings).Stop.HandoffTimeout)
			ctx, cancel := context.WithTimeout(cmd.Context(), handoff+stopGrace)
			defer cancel()
			// Only a server that answers has agents to ask; a stop with
			// nothing running just says so.
			if _, running, _ := serverRunning(ctx); running {
				fmt.Fprintf(cmd.OutOrStdout(), "asking agents for a handoff (up to %s)...\n", handoff)
			}
			err = stopServer(ctx)
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

// stopSignals returns a context that ends at the first interrupt or
// termination signal (a graceful stop: agents are asked for a handoff) and a
// channel that is closed at the second (the stop goes on at once, without
// waiting for the handoff). After the second, signals get their default
// handling again, so a third one ends the process.
func stopSignals(parent context.Context) (ctx context.Context, hurry <-chan struct{}, stop func()) {
	ctx, cancel := context.WithCancel(parent)
	hurried := make(chan struct{})
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		defer signal.Stop(signals)
		select {
		case <-signals:
			cancel()
		case <-done:
			return
		}
		select {
		case <-signals:
			close(hurried)
		case <-done:
		}
	}()
	return ctx, hurried, func() {
		close(done)
		cancel()
	}
}

func startServer(ctx context.Context, hurry <-chan struct{}, cmd *cobra.Command, version string, detached bool, backgroundID string) error {
	layout, err := globalhome.Open()
	if errors.Is(err, globalhome.ErrOldInstallation) {
		goOn, oldErr := meetOldInstallation(ctx, cmd)
		if !goOn {
			return oldErr
		}
		layout, err = globalhome.Open()
	}
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
	options := server.Options{
		Layout: layout, Config: config, Version: version, Runner: agents.NewPTYRunner(), Hurry: hurry,
		DetectAgents: firstStartDetection(),
	}
	return service.Run(ctx, out, backgroundID, func(ctx context.Context, out io.Writer, ready func(string) error) error {
		// A second signal or a second `omo stop` ends the handoff wait.
		options.Hurry = mergeHurry(hurry, service.Hurry(ctx))
		return server.Run(ctx, options, out, ready)
	})
}

// mergeHurry returns a channel that closes when either input closes. A nil
// input never closes.
func mergeHurry(a, b <-chan struct{}) <-chan struct{} {
	switch {
	case b == nil:
		return a
	case a == nil:
		return b
	}
	out := make(chan struct{})
	go func() {
		select {
		case <-a:
		case <-b:
		}
		close(out)
	}()
	return out
}
