package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
	"github.com/scolastico-dev/one-man-office/internal/tui"
)

type officeFlags struct {
	mock              bool
	noTUI             bool
	safeMode          bool
	skipStartupChecks bool
}

func runOffice(cmd *cobra.Command, f officeFlags, version string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, office.ConfigPath)); err != nil {
		hint := "run 'omo setup' to create one"
		if _, err := os.Stat(filepath.Join(dir, "omo.yaml")); err == nil {
			hint = "found a legacy ./omo.yaml — move it to " + office.ConfigPath
		}
		return fmt.Errorf("no %s in %s — %s", office.ConfigPath, dir, hint)
	}
	cfg, err := config.Load(filepath.Join(dir, office.ConfigPath))
	if err != nil {
		return err
	}
	if f.mock {
		// Mock mode promises no dependency on an installed model CLI.
		cfg.Startup.CheckSuperpowers = false
	}
	if !f.skipStartupChecks {
		restarted, err := runStartupChecks(cmd, dir, cfg, version, !f.noTUI)
		if err != nil {
			return err
		}
		if restarted {
			return nil
		}
	}
	var o *office.Office
	for {
		o, err = office.Open(dir, f.mock)
		if err == nil {
			break
		}
		var running *office.AlreadyRunningError
		if !errors.As(err, &running) {
			return err
		}
		if !inputIsTerminal(cmd.InOrStdin()) {
			return fmt.Errorf("%w; run 'omo estop' from the office directory first", err)
		}
		fmt.Fprint(cmd.ErrOrStderr(), "omo is already running for this office. Emergency-stop it and start a new session? [y/N] ")
		answer, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if readErr != nil && len(answer) == 0 {
			return fmt.Errorf("%w; run 'omo estop' from the office directory first", err)
		}
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			return err
		}
		if err := sockc.Call(running.Endpoint, "user", "office.estop", nil, nil); err != nil {
			return fmt.Errorf("stop running office: %w", err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for sockc.Probe(running.Endpoint, 100*time.Millisecond) && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		if sockc.Probe(running.Endpoint, 100*time.Millisecond) {
			return fmt.Errorf("running office did not stop within 10 seconds")
		}
	}
	defer o.Close()
	o.SafeMode = f.safeMode
	for _, w := range o.Warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "WARNING:", w)
	}
	if err := o.Start(); err != nil {
		return err
	}
	if f.noTUI {
		status := "office running (no TUI)"
		if f.safeMode {
			status += " in safe mode"
		}
		fmt.Fprintln(cmd.OutOrStdout(), status+" — Ctrl+C to stop")
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-sig:
		case <-o.Sup.EmergencyStop():
			fmt.Fprintln(cmd.OutOrStdout(), "office emergency-stopped")
		}
		return nil
	}
	return tui.Run(o)
}
