package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SuperpowersInstalled asks the configured CLI for its enabled plugins or
// extensions. Listing local installations does not contact a model.
func SuperpowersInstalled(ctx context.Context, provider Provider, command string, env map[string]string) (bool, error) {
	var args []string
	switch provider {
	case Claude:
		args = []string{"plugin", "list", "--json"}
	case Codex:
		args = []string{"plugin", "list", "--json"}
	case Gemini:
		args = []string{"extensions", "list", "--output-format", "json"}
	default:
		return false, fmt.Errorf("unsupported provider %q", provider)
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = mergedEnvironment(os.Environ(), env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if installedOnDisk(provider, env) {
			return true, nil
		}
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return false, fmt.Errorf("%s: %w", detail, err)
		}
		return false, err
	}
	installed, disabled, err := parseSuperpowersState(stdout.Bytes())
	if err != nil {
		if installedOnDisk(provider, env) {
			return true, nil
		}
		return false, err
	}
	if installed || disabled {
		return installed, nil
	}
	return installedOnDisk(provider, env), nil
}

// ParseSuperpowersList accepts the array and object shapes emitted by the
// three supported CLIs. It deliberately keys on semantic fields instead of a
// provider version's surrounding JSON envelope.
func ParseSuperpowersList(raw []byte) (bool, error) {
	installed, _, err := parseSuperpowersState(raw)
	return installed, err
}

func parseSuperpowersState(raw []byte) (installed, disabled bool, err error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false, fmt.Errorf("parse plugin list: %w", err)
	}
	installed, disabled = superpowersState(value)
	return installed, disabled, nil
}

func containsEnabledSuperpowers(value any) bool {
	installed, _ := superpowersState(value)
	return installed
}

func superpowersState(value any) (installed, disabled bool) {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			childInstalled, childDisabled := superpowersState(child)
			installed = installed || childInstalled
			disabled = disabled || childDisabled
		}
	case map[string]any:
		matches := false
		for _, key := range []string{"id", "pluginId", "name"} {
			if text, ok := value[key].(string); ok && strings.Contains(strings.ToLower(text), "superpowers") {
				matches = true
			}
		}
		if matches {
			if enabled, ok := value["enabled"].(bool); ok && !enabled {
				return false, true
			}
			if installed, ok := value["installed"].(bool); ok && !installed {
				return false, false
			}
			return true, false
		}
		for _, child := range value {
			childInstalled, childDisabled := superpowersState(child)
			installed = installed || childInstalled
			disabled = disabled || childDisabled
		}
	}
	return installed, disabled
}

func mergedEnvironment(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	order := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = item
	}
	for key, value := range overrides {
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = key + "=" + value
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, values[key])
	}
	return out
}

func envValue(overrides map[string]string, key string) string {
	if value, ok := overrides[key]; ok {
		return value
	}
	return os.Getenv(key)
}

func providerHome(overrides map[string]string) string {
	if home := envValue(overrides, "HOME"); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return home
}

func installedOnDisk(provider Provider, env map[string]string) bool {
	switch provider {
	case Codex:
		return codexSuperpowersInstalled(env)
	case Claude:
		return claudeSuperpowersInstalled(env)
	default:
		return false
	}
}

func codexSuperpowersInstalled(env map[string]string) bool {
	root := envValue(env, "CODEX_HOME")
	if root == "" {
		root = filepath.Join(providerHome(env), ".codex")
	}
	for _, path := range []string{
		filepath.Join(root, "skills", "superpowers", "SKILL.md"),
		filepath.Join(providerHome(env), ".agents", "skills", "superpowers", "SKILL.md"),
	} {
		if regularFile(path) {
			return true
		}
	}
	for _, pattern := range []string{
		filepath.Join(root, "plugins", "cache", "*", "superpowers", "*", ".codex-plugin", "plugin.json"),
		filepath.Join(root, "plugins", "cache", "*", "*", "superpowers", "*", ".codex-plugin", "plugin.json"),
	} {
		matches, _ := filepath.Glob(pattern)
		for _, path := range matches {
			if manifestNamesSuperpowers(path) {
				return true
			}
		}
	}
	return false
}

func claudeSuperpowersInstalled(env map[string]string) bool {
	root := envValue(env, "CLAUDE_CONFIG_DIR")
	if root == "" {
		root = filepath.Join(providerHome(env), ".claude")
	}
	installedRaw, err := os.ReadFile(filepath.Join(root, "plugins", "installed_plugins.json"))
	if err != nil {
		return false
	}
	var installed struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	if json.Unmarshal(installedRaw, &installed) != nil {
		return false
	}
	pluginID := ""
	for id, entries := range installed.Plugins {
		if strings.Contains(strings.ToLower(id), "superpowers") && string(entries) != "null" && string(entries) != "[]" {
			pluginID = id
			break
		}
	}
	if pluginID == "" {
		return false
	}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if json.Unmarshal(raw, &settings) == nil && settings.EnabledPlugins[pluginID] {
			return true
		}
	}
	return false
}

func manifestNamesSuperpowers(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var manifest struct {
		Name string `json:"name"`
	}
	return json.Unmarshal(raw, &manifest) == nil && strings.EqualFold(manifest.Name, "superpowers")
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func SuperpowersInstallHint(provider Provider) string {
	switch provider {
	case Claude:
		return "in Claude Code, run /plugin install superpowers@claude-plugins-official"
	case Codex:
		return "in Codex, run /plugins, search for Superpowers, and select Install Plugin"
	case Gemini:
		return "run: gemini extensions install https://github.com/obra/superpowers"
	default:
		return "see https://github.com/obra/superpowers#installation"
	}
}
