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
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
	"github.com/scolastico-dev/one-man-office/internal/superpowercache"
	"github.com/scolastico-dev/one-man-office/internal/tui"
)

var (
	pluginDependencySync       = pluginmanager.Sync
	pluginDependencySetEnabled = pluginmanager.SetEnabled
)

type officeFlags struct {
	mock              bool
	noTUI             bool
	safeMode          bool
	skipStartupChecks bool
	readOnly          bool
	trustOffice       bool
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
	dir, err = ensureOfficeTrust(cmd, dir, f.trustOffice)
	if err != nil {
		return err
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
		var missing *plugins.MissingDependenciesError
		if errors.As(err, &missing) {
			if depErr := installMissingPluginDependencies(cmd, dir, cfg, missing, !f.noTUI && inputIsTerminal(cmd.InOrStdin())); depErr != nil {
				return depErr
			}
			continue
		}
		var mismatch *plugins.DependencyVersionMismatchError
		if errors.As(err, &mismatch) {
			advice := dependencyVersionMismatchError(mismatch)
			if !f.noTUI && inputIsTerminal(cmd.InOrStdin()) {
				fmt.Fprintln(cmd.OutOrStdout(), advice)
			}
			return advice
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

func installMissingPluginDependencies(cmd *cobra.Command, dir string, cfg *config.Config, missing *plugins.MissingDependenciesError, interactive bool) error {
	if missing == nil || len(missing.Dependencies) == 0 {
		return nil
	}
	if !interactive {
		dependency := missing.Dependencies[0]
		return missingDependencyInstallError(missing, dependency.Dependency)
	}
	if cfg.Plugins.Installed == nil {
		cfg.Plugins.Installed = make(map[string]config.Plugin)
	}
	input := bufio.NewReader(cmd.InOrStdin())
	for _, dependency := range missing.Dependencies {
		entry, configured := cfg.Plugins.Installed[dependency.Name]
		if configured && !entry.Enabled {
			fmt.Fprintf(cmd.OutOrStdout(), "%s requires disabled plugin %s.\n", strings.Join(dependency.RequiredBy, ", "), dependency.Name)
			if !askYesNo(input, cmd.OutOrStdout(), "Enable it now?") {
				return fmt.Errorf("%w; required plugin %s remains disabled", missing, dependency.Name)
			}
			if err := pluginDependencySetEnabled(filepath.Join(dir, office.ConfigPath), dependency.Name, true); err != nil {
				return fmt.Errorf("enable required plugin %s: %w", dependency.Name, err)
			}
			entry.Enabled = true
			cfg.Plugins.Installed[dependency.Name] = entry
			fmt.Fprintf(cmd.OutOrStdout(), "enabled required plugin %s\n", dependency.Name)
			continue
		}
		if !configured {
			entry = config.Plugin{Source: dependency.Source, Subpath: dependency.Subpath, Branch: dependency.Branch, Enabled: true}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s requires plugin %s from %q%s.\n", strings.Join(dependency.RequiredBy, ", "), dependency.Name, entry.Source, dependencySubpathLabel(entry.Subpath))
		if !askYesNo(input, cmd.OutOrStdout(), "Install it now?") {
			return missingDependencyInstallError(missing, plugins.Dependency{Name: dependency.Name, Source: entry.Source, Subpath: entry.Subpath, Branch: entry.Branch})
		}
		result, err := pluginDependencySync(cmd.Context(), dir, dependency.Name, entry)
		if err != nil {
			return fmt.Errorf("install required plugin %s: %w", dependency.Name, err)
		}
		cfg.Plugins.Installed[dependency.Name] = entry
		fmt.Fprintf(cmd.OutOrStdout(), "installed required plugin %s at %s\n", dependency.Name, shortRevision(result.Revision))
	}
	return nil
}

func dependencySubpathLabel(subpath string) string {
	if subpath == "" {
		return ""
	}
	return fmt.Sprintf(" (subpath %q)", subpath)
}

func missingDependencyInstallError(missing *plugins.MissingDependenciesError, dependency plugins.Dependency) error {
	branch := ""
	if dependency.Branch != "" {
		branch = fmt.Sprintf(", and --branch %q", dependency.Branch)
	}
	if dependency.Subpath == "" {
		return fmt.Errorf("%w; run 'omo plugin install' with source %q and --name %q%s", missing, dependency.Source, dependency.Name, branch)
	}
	return fmt.Errorf("%w; run 'omo plugin install' with source %q, --name %q, and --subpath %q%s", missing, dependency.Source, dependency.Name, dependency.Subpath, branch)
}

func dependencyVersionMismatchError(mismatch *plugins.DependencyVersionMismatchError) error {
	if mismatch == nil || len(mismatch.Mismatches) == 0 {
		return mismatch
	}
	installation := mismatch.Mismatches[0].InstallationName
	if installation == "" {
		installation = mismatch.Mismatches[0].Name
	}
	return fmt.Errorf("%w; run 'omo plugin update %s'", mismatch, installation)
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
	if f.trustOffice {
		conflicts = append(conflicts, "--trust-office")
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("--read-only cannot be combined with %s", strings.Join(conflicts, ", "))
	}
	return nil
}
