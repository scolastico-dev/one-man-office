package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
)

func TestLoadRecommendedPluginsValidatesUserCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_plugins.json")
	raw := `[{"name":"report","description":"Generate reports","source":"https://github.com/example/report.git","subpath":"omo","branch":"main","official":true}]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	plugins, err := loadRecommendedPlugins(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 3 || plugins[2] != (recommendedPlugin{Name: "report", Description: "Generate reports", Source: "https://github.com/example/report.git", Subpath: "omo", Branch: "main", Official: true}) {
		t.Fatalf("recommended plugins = %#v, want official defaults followed by report", plugins)
	}
	if err := os.WriteFile(path, []byte(`[{"name":"report","source":"https://example.com/a.git","unknown":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRecommendedPlugins(path); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field was accepted: %v", err)
	}
}

func TestLoadRecommendedPluginsMergesEmbeddedOfficialDefaultsForEmptyCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_plugins.json")
	if err := os.WriteFile(path, []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plugins, err := loadRecommendedPlugins(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []recommendedPlugin{
		{Name: "autoshutdown", Description: "Safely stop an office after a configurable idle period", Official: true, Source: "https://github.com/scolastico-dev/one-man-office.git", Subpath: "plugins/autoshutdown", Branch: "release"},
		{Name: "pushover", Description: "Send Pushover notifications for stable unread user mail and manual alerts", Official: true, Source: "https://github.com/scolastico-dev/one-man-office.git", Subpath: "plugins/pushover", Branch: "release"},
	}
	if !reflect.DeepEqual(plugins, want) {
		t.Fatalf("recommended plugins = %#v, want %#v", plugins, want)
	}
}

func TestLoadRecommendedPluginsUserEntryOverridesEmbeddedDefaultByName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_plugins.json")
	raw := `[
  {"name":"pushover","description":"Private notification fork","source":"https://example.com/pushover.git","subpath":"plugins/custom-pushover","branch":"testing"},
  {"name":"report","description":"Generate reports","source":"https://example.com/report.git"}
]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	plugins, err := loadRecommendedPlugins(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []recommendedPlugin{
		{Name: "autoshutdown", Description: "Safely stop an office after a configurable idle period", Official: true, Source: "https://github.com/scolastico-dev/one-man-office.git", Subpath: "plugins/autoshutdown", Branch: "release"},
		{Name: "pushover", Description: "Private notification fork", Source: "https://example.com/pushover.git", Subpath: "plugins/custom-pushover", Branch: "testing"},
		{Name: "report", Description: "Generate reports", Source: "https://example.com/report.git"},
	}
	if !reflect.DeepEqual(plugins, want) {
		t.Fatalf("recommended plugins = %#v, want %#v", plugins, want)
	}
}

func TestLoadRecommendedPluginsDefaultsOfficialToFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_plugins.json")
	if err := os.WriteFile(path, []byte(`[
  {"name":"report","description":"Generate reports","source":"https://github.com/example/report.git"},
  {"name":"status","description":"Show status","source":"https://github.com/example/status.git","official":false}
]`), 0o600); err != nil {
		t.Fatal(err)
	}
	plugins, err := loadRecommendedPlugins(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 4 {
		t.Fatalf("recommended plugin count = %d, want embedded defaults plus two user entries", len(plugins))
	}
	for _, plugin := range plugins {
		if (plugin.Name == "report" || plugin.Name == "status") && plugin.Official {
			t.Fatalf("user plugin unexpectedly official: %#v", plugin)
		}
	}
}

func TestLoadRecommendedPluginsSortsOfficialFirstThenByName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_plugins.json")
	raw := `[
  {"name":"zebra","description":"Zebra","source":"https://example.com/zebra.git"},
  {"name":"official-zebra","description":"Official zebra","source":"https://example.com/official-zebra.git","official":true},
  {"name":"official-alpha","description":"Official alpha","source":"https://example.com/official-alpha.git","official":true},
  {"name":"alpha","description":"Alpha","source":"https://example.com/alpha.git"}
]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	plugins, err := loadRecommendedPlugins(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"autoshutdown", "official-alpha", "official-zebra", "pushover", "alpha", "zebra"}
	got := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		got = append(got, plugin.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recommended plugin order = %v, want %v", got, want)
	}
}

func TestRecommendedPluginLabelMarksOnlyOfficialEntries(t *testing.T) {
	official := recommendedPlugin{Name: "pushover", Description: "Send notifications", Official: true}
	if got, want := recommendedPluginLabel(official), "[official] pushover — Send notifications"; got != want {
		t.Fatalf("official plugin label = %q, want %q", got, want)
	}
	ordinary := recommendedPlugin{Name: "report", Description: "Generate reports"}
	if got, want := recommendedPluginLabel(ordinary), "report — Generate reports"; got != want {
		t.Fatalf("ordinary plugin label = %q, want %q", got, want)
	}
}

func TestInteractiveSetupAppliesWizardChoices(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	dir := t.TempDir()
	oldTerminal, oldDetect, oldWizard := inputIsTerminal, detectSetupAgents, setupWizard
	t.Cleanup(func() {
		inputIsTerminal, detectSetupAgents, setupWizard = oldTerminal, oldDetect, oldWizard
	})
	inputIsTerminal = func(io.Reader) bool { return true }
	detectSetupAgents = func() []agentcli.Provider { return []agentcli.Provider{agentcli.Claude, agentcli.Codex} }
	called := false
	setupWizard = func(_ io.Reader, _ io.Writer, choices setupChoices, askGlobal bool) (setupChoices, error) {
		called = true
		if !askGlobal {
			t.Fatal("fresh global home did not offer reusable defaults")
		}
		for _, role := range config.AllRoles {
			choices.Roles[role] = config.RoleModels{Models: []string{"codex-sol"}, Assignment: config.AssignmentFailover}
		}
		choices.SelectedPlugins["nudge"] = false
		choices.SaveTemplate = true
		return choices, nil
	}
	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"setup", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("interactive setup skipped the wizard")
	}
	cfg, err := config.Load(filepath.Join(dir, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Roles["developer"].First() != "codex-sol" || cfg.Plugins.Installed["nudge"].Enabled {
		t.Fatalf("wizard choices not applied: role=%+v plugins=%+v", cfg.Roles["developer"], cfg.Plugins.Installed)
	}
	if _, err := os.Stat(filepath.Join(home, "template", ".omo", "omo.yaml")); err != nil {
		t.Fatalf("model choices were not saved globally: %v", err)
	}
}

func TestGlobalPluginFailureDoesNotMarkFreshOfficeComplete(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("OMO_HOME", homeDir)
	home, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	catalog := `[{
  "name": "report",
  "description": "Generate reports",
  "source": "https://example.com/report.git"
}]`
	if err := os.WriteFile(filepath.Join(home.Dir, "known_plugins.json"), []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	oldTerminal, oldWizard, oldGlobal := inputIsTerminal, setupWizard, setupGlobalSync
	t.Cleanup(func() { inputIsTerminal, setupWizard, setupGlobalSync = oldTerminal, oldWizard, oldGlobal })
	inputIsTerminal = func(io.Reader) bool { return true }
	setupWizard = func(_ io.Reader, _ io.Writer, choices setupChoices, _ bool) (setupChoices, error) {
		choices.SelectedPlugins["report"] = true
		choices.InstallGlobal = true
		choices.RemoveLocal = true
		return choices, nil
	}
	setupGlobalSync = func(context.Context, string, string, string, config.Plugin) (pluginmanager.Result, error) {
		return pluginmanager.Result{}, errors.New("network unavailable")
	}
	cmd := Root("test")
	cmd.SetArgs([]string{"setup", dir})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "network unavailable") {
		t.Fatalf("setup error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, office.ConfigPath)); !os.IsNotExist(err) {
		t.Fatalf("failed global install left completed office marker: %v", err)
	}
}

func TestInteractiveSetupSyncsSelectedOfficialPlugin(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("OMO_HOME", homeDir)
	home, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	catalog := `[{"name":"pushover","description":"Send Pushover notifications","official":true,"source":"https://github.com/scolastico-dev/one-man-office.git","subpath":"plugins/pushover","branch":"main"}]`
	if err := os.WriteFile(filepath.Join(home.Dir, "known_plugins.json"), []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	oldTerminal, oldWizard, oldSync := inputIsTerminal, setupWizard, setupPluginSync
	t.Cleanup(func() { inputIsTerminal, setupWizard, setupPluginSync = oldTerminal, oldWizard, oldSync })
	inputIsTerminal = func(io.Reader) bool { return true }
	setupWizard = func(_ io.Reader, _ io.Writer, choices setupChoices, _ bool) (setupChoices, error) {
		var pushover recommendedPlugin
		for _, plugin := range choices.Recommended {
			if plugin.Name == "pushover" {
				pushover = plugin
			}
		}
		if len(choices.Recommended) != 2 || !pushover.Official || pushover.Branch != "main" {
			t.Fatalf("official recommendation was not offered: %#v", choices.Recommended)
		}
		choices.SelectedPlugins["pushover"] = true
		return choices, nil
	}
	called := false
	setupPluginSync = func(_ context.Context, gotDir, name string, plugin config.Plugin) (pluginmanager.Result, error) {
		called = true
		if gotDir != dir || name != "pushover" {
			t.Fatalf("unexpected plugin identity: dir=%q name=%q", gotDir, name)
		}
		want := config.Plugin{Source: "https://github.com/scolastico-dev/one-man-office.git", Subpath: "plugins/pushover", Branch: "main", Enabled: true}
		if !reflect.DeepEqual(plugin, want) {
			t.Fatalf("plugin sync config = %#v, want %#v", plugin, want)
		}
		return pluginmanager.Result{Name: name}, nil
	}
	cmd := Root("test")
	cmd.SetArgs([]string{"setup", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("selected official plugin was not synced")
	}
}

func TestNonInteractiveSetupRetainsHistoricalBehavior(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	oldTerminal, oldWizard := inputIsTerminal, setupWizard
	t.Cleanup(func() { inputIsTerminal, setupWizard = oldTerminal, oldWizard })
	inputIsTerminal = func(io.Reader) bool { return true }
	setupWizard = func(io.Reader, io.Writer, setupChoices, bool) (setupChoices, error) {
		t.Fatal("--non-interactive invoked the wizard")
		return setupChoices{}, nil
	}
	cmd := Root("test")
	cmd.SetArgs([]string{"setup", "--non-interactive", "--agent-cli", "codex", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Roles["developer"].First() != "codex" {
		t.Fatalf("historical Codex default changed: %+v", cfg.Roles["developer"])
	}
}

func TestGlobalPluginsMissingLocallyReturnsOnlyEnabledPortablePlugins(t *testing.T) {
	global := config.Plugins{Installed: map[string]config.Plugin{
		"already-local": {Source: "https://example.com/local.git", Enabled: true},
		"disabled":      {Source: "https://example.com/disabled.git", Enabled: false},
		"shared":        {Source: "https://example.com/shared.git", Enabled: true},
	}}
	local := config.Plugins{Installed: map[string]config.Plugin{
		"already-local": {Source: "https://example.com/local.git", Enabled: true},
	}}
	got := globalPluginsMissingLocally(global, local)
	if len(got) != 1 || got[0].Name != "shared" || got[0].Plugin.Source != "https://example.com/shared.git" {
		t.Fatalf("missing global plugins = %#v", got)
	}
}

func TestGitHandoffDoesNotReofferPluginsJustRemovedFromLocalSetup(t *testing.T) {
	available := []globalPluginChoice{
		{Name: "kept-global", Plugin: config.Plugin{Source: "https://example.com/kept.git", Enabled: true}},
		{Name: "other-global", Plugin: config.Plugin{Source: "https://example.com/other.git", Enabled: true}},
	}
	choices := setupChoices{
		Recommended:     []recommendedPlugin{{Name: "kept-global"}},
		SelectedPlugins: map[string]bool{"kept-global": true},
		InstallGlobal:   true,
		RemoveLocal:     true,
	}
	got := omitRemovedSetupPlugins(available, choices)
	if len(got) != 1 || got[0].Name != "other-global" {
		t.Fatalf("Git handoff choices = %#v", got)
	}
}

func TestWithGitOffersMissingGlobalPluginsForLocalHandoff(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("OMO_HOME", homeDir)
	dir := t.TempDir()
	runGitCommand(t, dir, "init")
	if _, err := office.Setup(dir); err != nil {
		t.Fatal(err)
	}
	globalConfig := `trusted_offices: []
template: {enabled: false, auto_sync: false, setup_never_ask: false}
plugins:
  update_on_start: true
  installed:
    shared:
      source: https://example.com/shared.git
      enabled: true
`
	if err := os.WriteFile(filepath.Join(homeDir, "config.yaml"), []byte(globalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTerminal, oldWizard, oldSync := inputIsTerminal, setupGitPlugins, setupPluginSync
	t.Cleanup(func() { inputIsTerminal, setupGitPlugins, setupPluginSync = oldTerminal, oldWizard, oldSync })
	inputIsTerminal = func(io.Reader) bool { return true }
	offered := false
	setupGitPlugins = func(_ io.Reader, _ io.Writer, plugins []globalPluginChoice) ([]globalPluginChoice, error) {
		offered = len(plugins) == 1 && plugins[0].Name == "shared"
		return plugins, nil
	}
	setupPluginSync = func(_ context.Context, gotDir, name string, plugin config.Plugin) (pluginmanager.Result, error) {
		if gotDir != dir || name != "shared" || plugin.Source != "https://example.com/shared.git" {
			t.Fatalf("unexpected plugin handoff: dir=%q name=%q plugin=%+v", gotDir, name, plugin)
		}
		return pluginmanager.Result{Name: name}, nil
	}
	cmd := Root("test")
	cmd.SetArgs([]string{"setup", "--with-git", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !offered {
		t.Fatal("missing global plugin was not offered for repository handoff")
	}
}

func TestDefaultSetupChoicesPreselectCurrentRolesAndBundledPlugins(t *testing.T) {
	choices, err := defaultSetupChoices(agentcli.Claude, []agentcli.Provider{agentcli.Claude, agentcli.Codex}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(choices.Roles["ceo"].Models, []string{"claude-fable", "codex-astra"}) || choices.Roles["ceo"].Assignment != config.AssignmentFailover {
		t.Fatalf("CEO choices = %+v", choices.Roles["ceo"])
	}
	if !choices.SelectedPlugins["nudge"] || !choices.SelectedPlugins["tools"] {
		t.Fatalf("bundled defaults not selected: %#v", choices.SelectedPlugins)
	}
}

func TestSavedSetupChoicesBecomeWizardDefaultsWhenProfilesRemainAvailable(t *testing.T) {
	choices, err := defaultSetupChoices(agentcli.Claude, []agentcli.Provider{agentcli.Claude, agentcli.Codex}, nil)
	if err != nil {
		t.Fatal(err)
	}
	savedRoles := map[string]config.RoleModels{
		"developer": {Models: []string{"codex-sol", "missing"}, Assignment: config.AssignmentRandom},
	}
	applySavedSetupChoices(&choices, map[string]config.Profile{
		"custom-codex": {Provider: agentcli.Codex, Cmd: "codex-wrapper", Args: []string{"--custom"}},
	}, savedRoles)
	if !reflect.DeepEqual(choices.Roles["developer"].Models, []string{"codex-sol"}) || choices.Roles["developer"].Assignment != config.AssignmentRandom {
		t.Fatalf("saved wizard defaults = %+v", choices.Roles["developer"])
	}
	if choices.Models["custom-codex"].Cmd != "codex-wrapper" {
		t.Fatalf("saved custom profile was lost: %#v", choices.Models)
	}
}

func TestSetupWizardSkipsAllTemplateRolesWhenConfirmed(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	t.Setenv("ACCESSIBLE", "1")
	choices := scriptedWizardChoices(config.AllRoles)
	var input strings.Builder
	input.WriteString("\n")  // accept the default yes
	input.WriteString("0\n") // keep the default plugin selection

	var output bytes.Buffer
	got, err := runModernSetupWizard(strings.NewReader(input.String()), &output, choices, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Your global template defines all roles. Skip the role questions? [Y/n]") {
		t.Fatalf("all-role skip confirmation was not shown:\n%s", output.String())
	}
	if !reflect.DeepEqual(got.Roles, choices.Roles) {
		t.Fatalf("template role choices changed: got %#v, want %#v", got.Roles, choices.Roles)
	}
}

func TestSetupWizardAsksAllTemplateRolesWhenSkipDeclined(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	t.Setenv("ACCESSIBLE", "1")
	choices := scriptedWizardChoices(config.AllRoles)
	var input strings.Builder
	input.WriteString("n\n")
	input.WriteString(strings.Repeat("0\n\n", len(config.AllRoles)))
	input.WriteString("0\n") // finish the plugin selection

	var output bytes.Buffer
	got, err := runModernSetupWizard(strings.NewReader(input.String()), &output, choices, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range config.AllRoles {
		if !strings.Contains(output.String(), "Profiles for "+strings.ReplaceAll(role, "_", " ")) {
			t.Fatalf("declining skip did not ask about %s; output:\n%s", role, output.String())
		}
	}
	if !reflect.DeepEqual(got.Roles, choices.Roles) {
		t.Fatalf("declining skip changed role choices: got %#v, want %#v", got.Roles, choices.Roles)
	}
}

func TestSetupWizardSkipsOnlyDefinedTemplateRolesWhenConfirmed(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	t.Setenv("ACCESSIBLE", "1")
	defined := config.AllRoles[:3]
	choices := scriptedWizardChoices(defined)
	var input strings.Builder
	input.WriteString("\n") // accept the default yes
	input.WriteString(strings.Repeat("0\n\n", len(config.AllRoles)-len(defined)))
	input.WriteString("0\n") // finish the plugin selection

	var output bytes.Buffer
	got, err := runModernSetupWizard(strings.NewReader(input.String()), &output, choices, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Your global template defines roles ceo, product_manager, developer. Skip the questions for those roles? [Y/n]") {
		t.Fatalf("partial-role skip confirmation was not shown:\n%s", output.String())
	}
	for _, role := range defined {
		if strings.Contains(output.String(), "Profiles for "+strings.ReplaceAll(role, "_", " ")) {
			t.Fatalf("defined role %s was still asked about; output:\n%s", role, output.String())
		}
	}
	for _, role := range config.AllRoles[len(defined):] {
		if !strings.Contains(output.String(), "Profiles for "+strings.ReplaceAll(role, "_", " ")) {
			t.Fatalf("undefined role %s was not asked about; output:\n%s", role, output.String())
		}
	}
	if !reflect.DeepEqual(got.Roles, choices.Roles) {
		t.Fatalf("partial template changed role choices: got %#v, want %#v", got.Roles, choices.Roles)
	}
}

func TestSetupWizardWithInactiveTemplateAsksAllRoles(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	t.Setenv("ACCESSIBLE", "1")
	choices := scriptedWizardChoices(nil)
	var input strings.Builder
	input.WriteString(strings.Repeat("0\n\n", len(config.AllRoles)))
	input.WriteString("0\n") // finish the plugin selection

	var output bytes.Buffer
	if _, err := runModernSetupWizard(strings.NewReader(input.String()), &output, choices, false); err != nil {
		t.Fatal(err)
	}
	for _, role := range config.AllRoles {
		if !strings.Contains(output.String(), "Profiles for "+strings.ReplaceAll(role, "_", " ")) {
			t.Fatalf("inactive template did not preserve role question for %s; output:\n%s", role, output.String())
		}
	}
}

func scriptedWizardChoices(templateRoles []string) setupChoices {
	roles := make(map[string]config.RoleModels, len(config.AllRoles))
	for _, role := range config.AllRoles {
		roles[role] = config.RoleModels{Models: []string{"test"}, Assignment: config.AssignmentRoundRobin}
	}
	return setupChoices{
		Models:        map[string]config.Profile{"test": {Provider: agentcli.Codex, Cmd: "codex"}},
		Roles:         roles,
		TemplateRoles: append([]string(nil), templateRoles...),
		SelectedPlugins: map[string]bool{
			"nudge": false,
			"tools": false,
		},
	}
}

func TestAvailableSetupTemplateRolesDropsUnavailableProfiles(t *testing.T) {
	saved := map[string]config.RoleModels{
		"ceo":       {Models: []string{"available"}},
		"developer": {Models: []string{"missing"}},
		"unknown":   {Models: []string{"available"}},
	}
	got := availableSetupTemplateRoles(saved, map[string]config.Profile{
		"available": {Provider: agentcli.Codex, Cmd: "codex"},
	})
	if !reflect.DeepEqual(got, []string{"ceo"}) {
		t.Fatalf("available template roles = %v, want [ceo]", got)
	}
}

func TestInteractiveSetupOnlyOffersTemplateRoleSkippingWhenTemplateActive(t *testing.T) {
	for _, test := range []struct {
		name     string
		enabled  bool
		autoSync bool
		want     []string
	}{
		{name: "inactive"},
		{name: "enabled", enabled: true, want: config.AllRoles},
		{name: "auto-sync", autoSync: true, want: config.AllRoles},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OMO_HOME", t.TempDir())
			home, err := globalhome.Open()
			if err != nil {
				t.Fatal(err)
			}
			globalConfig := "trusted_offices: []\ntemplate:\n  enabled: " + fmt.Sprint(test.enabled) + "\n  auto_sync: " + fmt.Sprint(test.autoSync) + "\n  setup_never_ask: false\nplugins:\n  update_on_start: true\n  installed: {}\n"
			if err := os.WriteFile(filepath.Join(home.Dir, "config.yaml"), []byte(globalConfig), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := writeSetupTemplateRoles(home.Dir, config.AllRoles); err != nil {
				t.Fatal(err)
			}

			dir := t.TempDir()
			oldTerminal, oldDetect, oldWizard := inputIsTerminal, detectSetupAgents, setupWizard
			t.Cleanup(func() { inputIsTerminal, detectSetupAgents, setupWizard = oldTerminal, oldDetect, oldWizard })
			inputIsTerminal = func(io.Reader) bool { return true }
			detectSetupAgents = func() []agentcli.Provider { return []agentcli.Provider{agentcli.Codex} }
			var got []string
			setupWizard = func(_ io.Reader, _ io.Writer, choices setupChoices, askGlobal bool) (setupChoices, error) {
				if askGlobal {
					t.Fatal("existing template unexpectedly offered global-save questions")
				}
				got = choices.TemplateRoles
				return choices, nil
			}
			cmd := Root("test")
			cmd.SetArgs([]string{"setup", dir})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("template roles offered to wizard = %v, want %v", got, test.want)
			}
		})
	}
}

func writeSetupTemplateRoles(homeDir string, roles []string) error {
	path := filepath.Join(homeDir, "template", ".omo")
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	var content strings.Builder
	content.WriteString("models:\n  codex: {provider: codex, cmd: codex}\nroles:\n")
	for _, role := range roles {
		content.WriteString("  ")
		content.WriteString(role)
		content.WriteString(": {models: [codex], assignment: round_robin}\n")
	}
	return os.WriteFile(filepath.Join(path, "omo.yaml"), []byte(content.String()), 0o600)
}

func TestOrderedModelNamesPreservesFailoverPriority(t *testing.T) {
	got := orderedModelNames([]string{"claude-sonnet", "codex-luna", "codex-astra"}, []string{"codex-luna", "claude-sonnet"})
	want := []string{"codex-luna", "claude-sonnet", "codex-astra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered profiles = %v, want %v", got, want)
	}
}

func TestOfficeOptionsLeavesEnabledToolsOwnershipToSetup(t *testing.T) {
	choices, err := defaultSetupChoices(agentcli.Claude, []agentcli.Provider{agentcli.Claude}, nil)
	if err != nil {
		t.Fatal(err)
	}
	options := choices.officeOptions(agentcli.Claude)
	if _, configured := options.Plugins["tools"]; configured {
		t.Fatalf("enabled tools was forced local: %#v", options.Plugins["tools"])
	}
	choices.SelectedPlugins["tools"] = false
	options = choices.officeOptions(agentcli.Claude)
	if plugin, configured := options.Plugins["tools"]; !configured || plugin.Enabled {
		t.Fatalf("disabled tools choice was not persisted: %#v", options.Plugins)
	}
}

func TestSmartAssignmentRejectsGeminiProfiles(t *testing.T) {
	models := map[string]config.Profile{"gemini": {Provider: agentcli.Gemini, Cmd: "gemini"}}
	if err := validateRoleAssignment(config.AssignmentSmart, []string{"gemini"}, models); err == nil {
		t.Fatal("smart assignment accepted a Gemini profile")
	}
	if err := validateRoleAssignment(config.AssignmentRandom, []string{"gemini"}, models); err != nil {
		t.Fatalf("random assignment rejected Gemini: %v", err)
	}
}
