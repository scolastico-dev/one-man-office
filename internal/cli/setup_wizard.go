package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
)

var (
	detectSetupAgents = agentcli.DetectAllInstalled
	setupWizard       = runModernSetupWizard
	setupGitPlugins   = runGitPluginWizard
	setupPluginSync   = pluginmanager.Sync
	setupGlobalSync   = pluginmanager.SyncAt
)

type recommendedPlugin struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Official    bool   `json:"official"`
	Version     string `json:"version,omitempty"`
	Source      string `json:"source"`
	Subpath     string `json:"subpath,omitempty"`
	Branch      string `json:"branch,omitempty"`
}

type setupChoices struct {
	Models          map[string]config.Profile
	Roles           map[string]config.RoleModels
	TemplateRoles   []string
	Recommended     []recommendedPlugin
	SelectedPlugins map[string]bool
	SaveTemplate    bool
	InstallGlobal   bool
	RemoveLocal     bool
	NeverAsk        bool
}

type globalPluginChoice struct {
	Name   string
	Plugin config.Plugin
}

var embeddedOfficialPlugins = []recommendedPlugin{
	{
		Name: "pushover", Description: "Send Pushover notifications for stable unread user mail and manual alerts",
		Official: true, Version: "1.0.0", Source: "https://github.com/scolastico-dev/one-man-office.git", Subpath: "plugins/pushover", Branch: "release",
	},
	{
		Name: "autoshutdown", Description: "Safely stop an office after a configurable idle period",
		Official: true, Version: "1.0.0", Source: "https://github.com/scolastico-dev/one-man-office.git", Subpath: "plugins/autoshutdown", Branch: "release",
	},
	{
		Name: "pullrequest", Description: "Create idempotent pull requests or merge requests for as-is jobs",
		Official: true, Version: "1.0.0", Source: "https://github.com/scolastico-dev/one-man-office.git", Subpath: "plugins/pullrequest", Branch: "release",
	},
}

func loadRecommendedPlugins(path string) ([]recommendedPlugin, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	var plugins []recommendedPlugin
	if err := decoder.Decode(&plugins); err != nil {
		return nil, fmt.Errorf("read recommended plugins: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("read recommended plugins: %w", err)
	}
	seen := map[string]bool{}
	for i := range plugins {
		plugin := &plugins[i]
		if err := pluginmanager.ValidateName(plugin.Name); err != nil {
			return nil, fmt.Errorf("recommended plugin %d: %w", i+1, err)
		}
		if seen[plugin.Name] || plugin.Name == "nudge" || plugin.Name == "tools" {
			return nil, fmt.Errorf("recommended plugin %q is duplicated or reserved", plugin.Name)
		}
		seen[plugin.Name] = true
		plugin.Description = strings.TrimSpace(plugin.Description)
		if plugin.Description == "" {
			return nil, fmt.Errorf("recommended plugin %q requires a description", plugin.Name)
		}
		plugin.Source, err = pluginmanager.NormalizeSource(plugin.Source)
		if err != nil {
			return nil, fmt.Errorf("recommended plugin %q: %w", plugin.Name, err)
		}
		plugin.Subpath, err = pluginmanager.NormalizeSubpath(plugin.Subpath)
		if err != nil {
			return nil, fmt.Errorf("recommended plugin %q: %w", plugin.Name, err)
		}
		plugin.Branch, err = pluginmanager.NormalizeBranch(plugin.Branch)
		if err != nil {
			return nil, fmt.Errorf("recommended plugin %q: %w", plugin.Name, err)
		}
	}
	merged := make(map[string]recommendedPlugin, len(embeddedOfficialPlugins)+len(plugins))
	for _, plugin := range embeddedOfficialPlugins {
		merged[plugin.Name] = plugin
	}
	for _, plugin := range plugins {
		merged[plugin.Name] = plugin
	}
	plugins = make([]recommendedPlugin, 0, len(merged))
	for _, plugin := range merged {
		plugins = append(plugins, plugin)
	}
	sort.Slice(plugins, func(i, j int) bool {
		if plugins[i].Official != plugins[j].Official {
			return plugins[i].Official
		}
		return plugins[i].Name < plugins[j].Name
	})
	return plugins, nil
}

func recommendedPluginLabel(plugin recommendedPlugin) string {
	label := plugin.Name + " — " + plugin.Description
	if plugin.Official {
		return "[official] " + label
	}
	return label
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("expected one JSON document")
		}
		return err
	}
	return nil
}

func defaultSetupChoices(provider agentcli.Provider, installed []agentcli.Provider, recommended []recommendedPlugin) (setupChoices, error) {
	catalog, err := office.SetupCatalogFor(provider, installed)
	if err != nil {
		return setupChoices{}, err
	}
	selected := map[string]bool{"nudge": true, "tools": true}
	for _, plugin := range recommended {
		selected[plugin.Name] = false
	}
	return setupChoices{
		Models:          catalog.Models,
		Roles:           catalog.Roles,
		Recommended:     recommended,
		SelectedPlugins: selected,
	}, nil
}

func applySavedSetupChoices(choices *setupChoices, savedModels map[string]config.Profile, savedRoles map[string]config.RoleModels) {
	providers := map[agentcli.Provider]bool{}
	for _, profile := range choices.Models {
		providers[agentcli.Resolve(profile.Provider, profile.Cmd)] = true
	}
	for name, profile := range savedModels {
		if providers[agentcli.Resolve(profile.Provider, profile.Cmd)] {
			choices.Models[name] = profile
		}
	}
	for role, configured := range savedRoles {
		if !config.IsRole(role) {
			continue
		}
		available := make([]string, 0, len(configured.Models))
		for _, name := range configured.Models {
			if _, ok := choices.Models[name]; ok {
				available = append(available, name)
			}
		}
		if len(available) == 0 {
			continue
		}
		configured.Models = available
		choices.Roles[role] = configured
	}
}

func availableSetupTemplateRoles(savedRoles map[string]config.RoleModels, models map[string]config.Profile) []string {
	defined := make(map[string]bool)
	for role, configured := range savedRoles {
		if !config.IsRole(role) {
			continue
		}
		for _, name := range configured.Models {
			if _, ok := models[name]; ok {
				defined[role] = true
				break
			}
		}
	}
	roles := make([]string, 0, len(defined))
	for _, role := range config.AllRoles {
		if defined[role] {
			roles = append(roles, role)
		}
	}
	return roles
}

func runModernSetupWizard(input io.Reader, output io.Writer, choices setupChoices, askGlobal bool) (setupChoices, error) {
	type roleFields struct {
		role       string
		models     []string
		assignment config.Assignment
	}
	modelNames := make([]string, 0, len(choices.Models))
	for name := range choices.Models {
		modelNames = append(modelNames, name)
	}
	sort.Strings(modelNames)
	modelOptions := func(selected []string) []huh.Option[string] {
		selectedSet := map[string]bool{}
		for _, name := range selected {
			selectedSet[name] = true
		}
		options := make([]huh.Option[string], 0, len(modelNames))
		for _, name := range orderedModelNames(modelNames, selected) {
			profile := choices.Models[name]
			provider := agentcli.Resolve(profile.Provider, profile.Cmd)
			label := name
			if provider != "" {
				label += " (" + string(provider) + ")"
			}
			options = append(options, huh.NewOption(label, name).Selected(selectedSet[name]))
		}
		return options
	}
	assignmentOptions := []huh.Option[config.Assignment]{
		huh.NewOption("Round robin", config.AssignmentRoundRobin),
		huh.NewOption("Random", config.AssignmentRandom),
		huh.NewOption("Failover", config.AssignmentFailover),
		huh.NewOption("Smart usage-aware", config.AssignmentSmart),
	}
	skipRoles := make(map[string]bool, len(choices.TemplateRoles))
	for _, role := range choices.TemplateRoles {
		if config.IsRole(role) {
			skipRoles[role] = true
		}
	}
	if len(skipRoles) > 0 {
		skip := true
		title := "Your global template defines roles " + strings.Join(choices.TemplateRoles, ", ") + ". Skip the questions for those roles?"
		if len(skipRoles) == len(config.AllRoles) {
			title = "Your global template defines all roles. Skip the role questions?"
		}
		if err := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().Title(title).Value(&skip),
		)).WithInput(input).WithOutput(output).WithAccessible(os.Getenv("ACCESSIBLE") != "").Run(); err != nil {
			return choices, err
		}
		if !skip {
			skipRoles = map[string]bool{}
		}
	}
	roles := make([]roleFields, 0, len(config.AllRoles)-len(skipRoles))
	groups := make([]*huh.Group, 0, len(config.AllRoles)+1)
	for _, role := range config.AllRoles {
		if skipRoles[role] {
			continue
		}
		configured := choices.Roles[role]
		roles = append(roles, roleFields{role: role, models: append([]string(nil), configured.Models...), assignment: configured.Assignment})
		fields := &roles[len(roles)-1]
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Profiles for "+strings.ReplaceAll(role, "_", " ")).
				Description("Space toggles profiles; the current defaults are preselected.").
				Options(modelOptions(fields.models)...).
				Value(&fields.models).
				Validate(func(selected []string) error {
					if len(selected) == 0 {
						return fmt.Errorf("select at least one profile")
					}
					return nil
				}),
			huh.NewSelect[config.Assignment]().
				Title("Assignment method").
				Options(assignmentOptions...).
				Value(&fields.assignment).
				Validate(func(assignment config.Assignment) error {
					return validateRoleAssignment(assignment, fields.models, choices.Models)
				}),
		))
	}
	selectedPluginNames := make([]string, 0, len(choices.SelectedPlugins))
	pluginOptions := []huh.Option[string]{
		huh.NewOption("nudge — bundled workflow reminders", "nudge").Selected(choices.SelectedPlugins["nudge"]),
		huh.NewOption("tools — bundled maintenance actions", "tools").Selected(choices.SelectedPlugins["tools"]),
	}
	for name, selected := range choices.SelectedPlugins {
		if selected {
			selectedPluginNames = append(selectedPluginNames, name)
		}
	}
	for _, plugin := range choices.Recommended {
		pluginOptions = append(pluginOptions, huh.NewOption(recommendedPluginLabel(plugin), plugin.Name).Selected(choices.SelectedPlugins[plugin.Name]))
	}
	groups = append(groups, huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Enabled plugins").
			Description("Official plugins are maintained by the OMO project; other recommendations come from your catalog. Review each source before enabling a plugin. Plugins can use CLI access.").
			Options(pluginOptions...).
			Value(&selectedPluginNames),
	))
	form := huh.NewForm(groups...).WithInput(input).WithOutput(output).WithAccessible(os.Getenv("ACCESSIBLE") != "")
	if err := form.Run(); err != nil {
		return choices, err
	}
	for _, fields := range roles {
		choices.Roles[fields.role] = config.RoleModels{Models: fields.models, Assignment: fields.assignment}
	}
	for name := range choices.SelectedPlugins {
		choices.SelectedPlugins[name] = false
	}
	for _, name := range selectedPluginNames {
		choices.SelectedPlugins[name] = true
	}
	if !askGlobal {
		return choices, nil
	}
	selectedRecommended := false
	for _, plugin := range choices.Recommended {
		selectedRecommended = selectedRecommended || choices.SelectedPlugins[plugin.Name]
	}
	globalFields := []huh.Field{
		huh.NewConfirm().Title("Save these model and role choices for future omo setup runs?").Value(&choices.SaveTemplate),
	}
	if selectedRecommended {
		globalFields = append(globalFields,
			huh.NewConfirm().Title("Install the selected recommended plugins globally too?").Value(&choices.InstallGlobal))
	}
	if err := huh.NewForm(huh.NewGroup(globalFields...)).WithInput(input).WithOutput(output).WithAccessible(os.Getenv("ACCESSIBLE") != "").Run(); err != nil {
		return choices, err
	}
	if choices.InstallGlobal {
		if err := huh.NewConfirm().
			Title("Remove those recommended plugins from this office and use the global copies?").
			Value(&choices.RemoveLocal).
			RunAccessible(output, input); err != nil {
			return choices, err
		}
	}
	if !choices.SaveTemplate && !choices.InstallGlobal {
		if err := huh.NewConfirm().
			Title("Do not ask about global setup defaults again?").
			Value(&choices.NeverAsk).
			RunAccessible(output, input); err != nil {
			return choices, err
		}
	}
	return choices, nil
}

func orderedModelNames(all, selected []string) []string {
	available := make(map[string]bool, len(all))
	for _, name := range all {
		available[name] = true
	}
	seen := make(map[string]bool, len(all))
	ordered := make([]string, 0, len(all))
	for _, name := range selected {
		if available[name] && !seen[name] {
			seen[name] = true
			ordered = append(ordered, name)
		}
	}
	for _, name := range all {
		if !seen[name] {
			ordered = append(ordered, name)
		}
	}
	return ordered
}

func validateRoleAssignment(assignment config.Assignment, profiles []string, models map[string]config.Profile) error {
	if assignment != config.AssignmentSmart {
		return nil
	}
	for _, name := range profiles {
		profile := models[name]
		provider := agentcli.Resolve(profile.Provider, profile.Cmd)
		if provider != agentcli.Claude && provider != agentcli.Codex {
			return fmt.Errorf("smart assignment supports only Claude and Codex profiles; change %s or choose another method", name)
		}
	}
	return nil
}

func (choices setupChoices) officeOptions(provider agentcli.Provider) office.SetupOptions {
	used := map[string]bool{}
	for _, role := range choices.Roles {
		for _, name := range role.Models {
			used[name] = true
		}
	}
	models := make(map[string]config.Profile, len(used))
	for name := range used {
		models[name] = choices.Models[name]
	}
	plugins := map[string]config.Plugin{
		"nudge": config.Defaults().Plugins.Installed["nudge"],
	}
	nudge := plugins["nudge"]
	nudge.Enabled = choices.SelectedPlugins["nudge"]
	plugins["nudge"] = nudge
	if !choices.SelectedPlugins["tools"] {
		plugins["tools"] = config.Plugin{Source: "builtin:tools", Enabled: false}
	}
	for _, plugin := range choices.Recommended {
		if !choices.SelectedPlugins[plugin.Name] || choices.InstallGlobal && choices.RemoveLocal {
			continue
		}
		plugins[plugin.Name] = config.Plugin{Source: plugin.Source, Subpath: plugin.Subpath, Branch: plugin.Branch, Enabled: true}
	}
	return office.SetupOptions{Provider: provider, Models: models, Roles: choices.Roles, Plugins: plugins}
}

func installGlobalSetupPlugins(ctx context.Context, home *globalhome.Home, choices setupChoices) error {
	if !choices.InstallGlobal {
		return nil
	}
	for _, plugin := range choices.Recommended {
		if !choices.SelectedPlugins[plugin.Name] {
			continue
		}
		entry := config.Plugin{Source: plugin.Source, Subpath: plugin.Subpath, Branch: plugin.Branch, Enabled: true}
		if _, err := setupGlobalSync(ctx, filepath.Join(home.Dir, "plugins"), filepath.Join(home.Dir, "config.yaml"), plugin.Name, entry); err != nil {
			return fmt.Errorf("install plugin %s globally: %w", plugin.Name, err)
		}
	}
	return nil
}

func installLocalSetupPlugins(ctx context.Context, officeDir string, choices setupChoices) error {
	for _, plugin := range choices.Recommended {
		if !choices.SelectedPlugins[plugin.Name] || choices.InstallGlobal && choices.RemoveLocal {
			continue
		}
		entry := config.Plugin{Source: plugin.Source, Subpath: plugin.Subpath, Branch: plugin.Branch, Enabled: true}
		if _, err := setupPluginSync(ctx, officeDir, plugin.Name, entry); err != nil {
			return fmt.Errorf("install plugin %s locally: %w", plugin.Name, err)
		}
	}
	return nil
}

func globalPluginsMissingLocally(global, local config.Plugins) []globalPluginChoice {
	names := make([]string, 0, len(global.Installed))
	for name, plugin := range global.Installed {
		if !plugin.Enabled || strings.HasPrefix(plugin.Source, "builtin:") {
			continue
		}
		if _, exists := local.Installed[name]; exists {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	plugins := make([]globalPluginChoice, 0, len(names))
	for _, name := range names {
		plugins = append(plugins, globalPluginChoice{Name: name, Plugin: global.Installed[name]})
	}
	return plugins
}

func omitRemovedSetupPlugins(available []globalPluginChoice, choices setupChoices) []globalPluginChoice {
	if !choices.InstallGlobal || !choices.RemoveLocal {
		return available
	}
	omit := make(map[string]bool, len(choices.Recommended))
	for _, plugin := range choices.Recommended {
		if choices.SelectedPlugins[plugin.Name] {
			omit[plugin.Name] = true
		}
	}
	filtered := make([]globalPluginChoice, 0, len(available))
	for _, plugin := range available {
		if !omit[plugin.Name] {
			filtered = append(filtered, plugin)
		}
	}
	return filtered
}

func runGitPluginWizard(input io.Reader, output io.Writer, available []globalPluginChoice) ([]globalPluginChoice, error) {
	selected := make([]string, 0, len(available))
	options := make([]huh.Option[string], 0, len(available))
	for _, plugin := range available {
		selected = append(selected, plugin.Name)
		options = append(options, huh.NewOption(plugin.Name+" — "+plugin.Plugin.Source, plugin.Name).Selected(true))
	}
	if err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Global plugins to add to this repository").
			Description("Selected plugins become local, reviewable office configuration and files.").
			Options(options...).
			Value(&selected),
	)).WithInput(input).WithOutput(output).WithAccessible(os.Getenv("ACCESSIBLE") != "").Run(); err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(selected))
	for _, name := range selected {
		wanted[name] = true
	}
	result := make([]globalPluginChoice, 0, len(selected))
	for _, plugin := range available {
		if wanted[plugin.Name] {
			result = append(result, plugin)
		}
	}
	return result, nil
}

func vendorGlobalPlugins(ctx context.Context, officeDir string, plugins []globalPluginChoice) error {
	for _, plugin := range plugins {
		if _, err := setupPluginSync(ctx, officeDir, plugin.Name, plugin.Plugin); err != nil {
			return fmt.Errorf("add global plugin %s to repository: %w", plugin.Name, err)
		}
	}
	return nil
}
