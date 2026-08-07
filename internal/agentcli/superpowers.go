package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// SuperpowersInstalled asks the configured CLI for its enabled plugins or
// extensions. Listing local installations does not contact a model.
func SuperpowersInstalled(ctx context.Context, provider Provider, command string) (bool, error) {
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
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return false, fmt.Errorf("%s: %w", detail, err)
		}
		return false, err
	}
	return ParseSuperpowersList(stdout.Bytes())
}

// ParseSuperpowersList accepts the array and object shapes emitted by the
// three supported CLIs. It deliberately keys on semantic fields instead of a
// provider version's surrounding JSON envelope.
func ParseSuperpowersList(raw []byte) (bool, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("parse plugin list: %w", err)
	}
	return containsEnabledSuperpowers(value), nil
}

func containsEnabledSuperpowers(value any) bool {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			if containsEnabledSuperpowers(child) {
				return true
			}
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
				return false
			}
			if installed, ok := value["installed"].(bool); ok && !installed {
				return false
			}
			return true
		}
		for _, child := range value {
			if containsEnabledSuperpowers(child) {
				return true
			}
		}
	}
	return false
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
