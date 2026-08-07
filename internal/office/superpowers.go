package office

import (
	"context"
	"fmt"
	"sort"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
)

// SuperpowersWarnings verifies the installed state reported by each configured
// supported CLI. Custom runners remain outside this provider-specific check.
func SuperpowersWarnings(ctx context.Context, cfg *config.Config) []string {
	commands := map[agentcli.Provider]string{}
	for _, profile := range cfg.Models {
		provider := agentcli.Resolve(profile.Provider, profile.Cmd)
		if provider.Valid() {
			if _, exists := commands[provider]; !exists {
				commands[provider] = profile.Cmd
			}
		}
	}
	providers := make([]string, 0, len(commands))
	for provider := range commands {
		providers = append(providers, string(provider))
	}
	sort.Strings(providers)

	var warnings []string
	for _, value := range providers {
		provider := agentcli.Provider(value)
		installed, err := agentcli.SuperpowersInstalled(ctx, provider, commands[provider])
		if err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"could not check Superpowers for %s (%v) — %s",
				provider, err, agentcli.SuperpowersInstallHint(provider)))
			continue
		}
		if !installed {
			warnings = append(warnings, fmt.Sprintf(
				"Superpowers is not installed and enabled for %s — role prompts require it; %s",
				provider, agentcli.SuperpowersInstallHint(provider)))
		}
	}
	return warnings
}
