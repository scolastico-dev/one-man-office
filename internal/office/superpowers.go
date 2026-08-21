package office

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
)

// SuperpowersWarnings verifies the installed state reported by each configured
// supported CLI. Custom runners remain outside this provider-specific check.
func SuperpowersWarnings(ctx context.Context, cfg *config.Config) []string {
	type check struct {
		provider agentcli.Provider
		command  string
		env      map[string]string
		label    string
	}
	checks := map[string]check{}
	for _, profile := range cfg.Models {
		provider := agentcli.Resolve(profile.Provider, profile.Cmd)
		if provider.Valid() {
			key := string(provider) + "\x00" + profile.Cmd + "\x00" + normalizedEnvironment(profile.Env)
			checks[key] = check{provider: provider, command: profile.Cmd, env: profile.Env, label: string(provider)}
		}
	}
	keys := make([]string, 0, len(checks))
	for key := range checks {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var warnings []string
	for _, key := range keys {
		check := checks[key]
		installed, err := agentcli.SuperpowersInstalled(ctx, check.provider, check.command, check.env)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"could not check Superpowers for %s (%v) — %s",
				check.label, err, agentcli.SuperpowersInstallHint(check.provider)))
			continue
		}
		if !installed {
			warnings = append(warnings, fmt.Sprintf(
				"Superpowers is not installed and enabled for %s — role prompts require it; %s",
				check.label, agentcli.SuperpowersInstallHint(check.provider)))
		}
	}
	return warnings
}

func normalizedEnvironment(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		parts = append(parts, key+"="+env[key])
	}
	return strings.Join(parts, "\x00")
}
