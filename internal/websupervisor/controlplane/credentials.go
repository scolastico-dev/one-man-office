package controlplane

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
)

// normalizeCredentialRoots resolves file credentials against the child cwd.
func normalizeCredentialRoots(profile config.Profile, officeDir, goos string) error {
	if goos == "darwin" && agentcli.Resolve(profile.Provider, profile.Cmd) == agentcli.Claude {
		// Claude hashes the raw configured directory into the Keychain service
		// name. Absolutizing a relative value would select a different account.
		for _, variable := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR"} {
			if root := profile.Env[variable]; root != "" && !filepath.IsAbs(root) {
				return fmt.Errorf("%s must be an absolute path for supervised Claude profiles on macOS; relative paths change the Keychain account namespace", variable)
			}
		}
	}
	for _, variable := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR", "HOME"} {
		root := profile.Env[variable]
		if variable == "CODEX_HOME" || variable == "HOME" {
			root = strings.TrimSpace(root)
		}
		if root != "" && !filepath.IsAbs(root) {
			profile.Env[variable] = filepath.Join(officeDir, root)
		}
	}
	return nil
}
