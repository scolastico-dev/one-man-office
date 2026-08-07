package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/tui"
)

type officeFlags struct {
	mock              bool
	noTUI             bool
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
	if !f.skipStartupChecks {
		restarted, err := runStartupChecks(cmd, dir, cfg, version, !f.noTUI)
		if err != nil {
			return err
		}
		if restarted {
			return nil
		}
	}
	o, err := office.Open(dir, f.mock)
	if err != nil {
		return err
	}
	defer o.Close()
	for _, w := range o.Warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "WARNING:", w)
	}
	if err := o.Start(); err != nil {
		return err
	}
	if f.noTUI {
		fmt.Fprintln(cmd.OutOrStdout(), "office running (no TUI) — Ctrl+C to stop")
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		return nil
	}
	return tui.Run(o)
}
