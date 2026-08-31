package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
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
	"github.com/scolastico-dev/one-man-office/internal/superpowercache"
	"github.com/scolastico-dev/one-man-office/internal/tui"
)

type officeFlags struct {
	mock              bool
	noTUI             bool
	safeMode          bool
	skipStartupChecks bool
	readOnly          bool
}

func runOffice(cmd *cobra.Command, f officeFlags, version string) error {
	if err := validateOfficeFlags(f); err != nil {
		return err
	}
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
	if f.readOnly {
		o, err := office.OpenReadOnly(dir)
		if err != nil {
			return err
		}
		defer o.Close()
		return tui.RunReadOnly(o)
	}
	cfg, err := config.Load(filepath.Join(dir, office.ConfigPath))
	if err != nil {
		return err
	}
	if !f.mock && version != "test" && version != "dev" {
		ctx, cancel := startupContext(time.Duration(cfg.Startup.CheckTimeout) * 12)
		_, cacheErr := superpowercache.Ensure(ctx)
		cancel()
		if cacheErr != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: could not install/update bundled Superpowers:", cacheErr)
		}
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
			if !writeOfficeExitReason(cmd.OutOrStdout(), o.Sup.ExitReason()) {
				fmt.Fprintln(cmd.OutOrStdout(), "office emergency-stopped")
			}
		}
		return nil
	}
	runErr := tui.Run(o)
	writeOfficeExitReason(cmd.OutOrStdout(), o.Sup.ExitReason())
	return runErr
}

func writeOfficeExitReason(out io.Writer, reason string) bool {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return false
	}
	fmt.Fprintln(out, "omo exited:", reason)
	return true
}

func validateOfficeFlags(f officeFlags) error {
	if !f.readOnly {
		return nil
	}
	var conflicts []string
	if f.mock {
		conflicts = append(conflicts, "--mock")
	}
	if f.noTUI {
		conflicts = append(conflicts, "--no-tui")
	}
	if f.safeMode {
		conflicts = append(conflicts, "--safe-mode")
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("--read-only cannot be combined with %s", strings.Join(conflicts, ", "))
	}
	return nil
}
