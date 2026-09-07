package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

func TestInstallMissingPluginDependenciesPromptsAndInstalls(t *testing.T) {
	restoreDependencyHooks(t)
	var installedName string
	var installed config.Plugin
	pluginDependencySync = func(_ context.Context, _ string, name string, entry config.Plugin) (pluginmanager.Result, error) {
		installedName, installed = name, entry
		return pluginmanager.Result{Name: name, Revision: "1234567890abcdef"}, nil
	}
	cfg := &config.Config{Plugins: config.Plugins{Installed: map[string]config.Plugin{}}}
	missing := &plugins.MissingDependenciesError{Dependencies: []plugins.MissingDependency{{
		Dependency: plugins.Dependency{Name: "collector", Source: "https://example.test/plugins.git", Subpath: "collector"},
		RequiredBy: []string{"reporter"},
	}}}
	cmd, out, _ := dependencyCommand("yes\n")

	if err := installMissingPluginDependencies(cmd, t.TempDir(), cfg, missing, true); err != nil {
		t.Fatal(err)
	}
	if installedName != "collector" || installed.Source != "https://example.test/plugins.git" || installed.Subpath != "collector" || !installed.Enabled {
		t.Fatalf("installed %q as %+v", installedName, installed)
	}
	if !strings.Contains(out.String(), "reporter requires plugin collector") || !strings.Contains(out.String(), "Install it now?") {
		t.Fatalf("prompt = %q", out.String())
	}
}

func TestInstallMissingPluginDependenciesFailsWithoutInteractiveApproval(t *testing.T) {
	restoreDependencyHooks(t)
	called := false
	pluginDependencySync = func(context.Context, string, string, config.Plugin) (pluginmanager.Result, error) {
		called = true
		return pluginmanager.Result{}, nil
	}
	cfg := &config.Config{Plugins: config.Plugins{Installed: map[string]config.Plugin{}}}
	missing := &plugins.MissingDependenciesError{Dependencies: []plugins.MissingDependency{{
		Dependency: plugins.Dependency{Name: "collector", Source: "https://example.test/collector.git"},
		RequiredBy: []string{"reporter"},
	}}}
	cmd, _, _ := dependencyCommand("")

	err := installMissingPluginDependencies(cmd, t.TempDir(), cfg, missing, false)
	if err == nil || !strings.Contains(err.Error(), "omo plugin install") {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("dependency install ran without approval")
	}
}

func TestInstallMissingPluginDependenciesEnablesConfiguredDependency(t *testing.T) {
	restoreDependencyHooks(t)
	enabled := false
	pluginDependencySetEnabled = func(_ string, name string, value bool) error {
		enabled = name == "collector" && value
		return nil
	}
	pluginDependencySync = func(context.Context, string, string, config.Plugin) (pluginmanager.Result, error) {
		t.Fatal("disabled dependency should be enabled without reinstalling")
		return pluginmanager.Result{}, nil
	}
	cfg := &config.Config{Plugins: config.Plugins{Installed: map[string]config.Plugin{
		"collector": {Source: "https://example.test/collector.git", Enabled: false},
	}}}
	missing := &plugins.MissingDependenciesError{Dependencies: []plugins.MissingDependency{{
		Dependency: plugins.Dependency{Name: "collector", Source: "https://example.test/collector.git"}, RequiredBy: []string{"reporter"},
	}}}
	cmd, out, _ := dependencyCommand("y\n")
	if err := installMissingPluginDependencies(cmd, t.TempDir(), cfg, missing, true); err != nil {
		t.Fatal(err)
	}
	if !enabled || !cfg.Plugins.Installed["collector"].Enabled || !strings.Contains(out.String(), "Enable it now?") {
		t.Fatalf("enabled=%v config=%+v output=%q", enabled, cfg.Plugins.Installed["collector"], out.String())
	}
}

func dependencyCommand(input string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{}
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	return cmd, out, stderr
}

func restoreDependencyHooks(t *testing.T) {
	t.Helper()
	oldSync, oldEnable := pluginDependencySync, pluginDependencySetEnabled
	t.Cleanup(func() { pluginDependencySync, pluginDependencySetEnabled = oldSync, oldEnable })
}
