package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"github.com/scolastico-dev/one-man-office/internal/selfupdate"
)

func TestStartupAcceptsSelfUpdateAndRestarts(t *testing.T) {
	dir := t.TempDir()
	if _, err := office.Setup(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Startup.CheckSuperpowers = false
	restoreStartupHooks(t)
	inputIsTerminal = func(io.Reader) bool { return true }
	latestRelease = func(context.Context) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: "v1.1.0"}, nil
	}
	installed, restarted := false, false
	installRelease = func(_ context.Context, release selfupdate.Release, target string) error {
		installed = release.Tag == "v1.1.0" && target != ""
		return nil
	}
	currentExecutable = func() (string, error) { return filepath.Join(dir, "omo"), nil }
	launchRestart = func(string) error { restarted = true; return nil }
	cmd, _, _ := startupCommand("yes\n")
	got, err := runStartupChecks(cmd, dir, cfg, "1.0.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if !got || !installed || !restarted {
		t.Fatalf("restarted=%v installed=%v launched=%v", got, installed, restarted)
	}
}

func TestStartupAcceptsTemplateUpdateAndRestarts(t *testing.T) {
	dir := t.TempDir()
	if _, err := office.Setup(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, office.TemplatesVersionPath), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Startup.CheckSelfUpdate = false
	cfg.Startup.CheckSuperpowers = false
	restoreStartupHooks(t)
	inputIsTerminal = func(io.Reader) bool { return true }
	currentExecutable = func() (string, error) { return filepath.Join(dir, "omo"), nil }
	restarted := false
	launchRestart = func(string) error { restarted = true; return nil }
	cmd, _, _ := startupCommand("y\n")
	got, err := runStartupChecks(cmd, dir, cfg, "1.0.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if !got || !restarted {
		t.Fatalf("restarted=%v launched=%v", got, restarted)
	}
	if outdated, err := office.TemplatesOutdated(dir); err != nil || outdated {
		t.Fatalf("templates still outdated=%v, err=%v", outdated, err)
	}
}

func TestStartupTemplateCheckCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	if _, err := office.Setup(dir); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, office.TemplatesVersionPath), []byte("stale\n"), 0o644)
	cfg, _ := config.Load(filepath.Join(dir, office.ConfigPath))
	cfg.Startup.CheckSelfUpdate = false
	cfg.Startup.CheckTemplates = false
	cfg.Startup.CheckSuperpowers = false
	cmd, out, stderr := startupCommand("yes\n")
	restarted, err := runStartupChecks(cmd, dir, cfg, "1.0.0", true)
	if err != nil || restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("disabled checks wrote output: %q %q", out.String(), stderr.String())
	}
}

func TestStartupReportsSuperpowersWarnings(t *testing.T) {
	restoreStartupHooks(t)
	superpowersCheck = func(context.Context, *config.Config) []string {
		return []string{"Superpowers is missing for codex"}
	}
	cfg := &config.Config{Startup: config.Startup{CheckSuperpowers: true, CheckTimeout: config.Duration(time.Second)}}
	cmd, _, stderr := startupCommand("")
	if restarted, err := runStartupChecks(cmd, t.TempDir(), cfg, "dev", false); err != nil || restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
	if !strings.Contains(stderr.String(), "WARNING: Superpowers is missing for codex") {
		t.Fatalf("warning output = %q", stderr.String())
	}
}

func TestStartupRefreshesConfiguredPlugins(t *testing.T) {
	restoreStartupHooks(t)
	cfg := &config.Config{
		Startup: config.Startup{CheckTimeout: config.Duration(time.Second)},
		Plugins: config.Plugins{UpdateOnStart: true, Installed: map[string]config.Plugin{
			"nudge": {Source: "https://example.test/nudge.git", Enabled: true},
		}},
	}
	called := false
	pluginSyncAll = func(context.Context, string, config.Plugins) ([]pluginmanager.Result, []error) {
		called = true
		return []pluginmanager.Result{{Name: "nudge", Revision: "1234567890abcdef", Changed: true}}, nil
	}
	cmd, _, stderr := startupCommand("")
	if restarted, err := runStartupChecks(cmd, t.TempDir(), cfg, "dev", false); err != nil || restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
	if !called || !strings.Contains(stderr.String(), "updated plugin nudge to 1234567890ab") {
		t.Fatalf("plugin update called=%v output=%q", called, stderr.String())
	}
}

func startupCommand(input string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{}
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	return cmd, out, stderr
}

func restoreStartupHooks(t *testing.T) {
	t.Helper()
	oldLatest, oldInstall := latestRelease, installRelease
	oldExecutable, oldRestart, oldTerminal := currentExecutable, launchRestart, inputIsTerminal
	oldSuperpowersCheck := superpowersCheck
	oldPluginSyncAll := pluginSyncAll
	t.Cleanup(func() {
		latestRelease, installRelease = oldLatest, oldInstall
		currentExecutable, launchRestart, inputIsTerminal = oldExecutable, oldRestart, oldTerminal
		superpowersCheck = oldSuperpowersCheck
		pluginSyncAll = oldPluginSyncAll
	})
}
