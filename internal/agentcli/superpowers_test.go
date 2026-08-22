package agentcli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSuperpowersList(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"claude enabled", `[{"id":"superpowers@claude-plugins-official","enabled":true}]`, true},
		{"claude disabled", `[{"id":"superpowers@claude-plugins-official","enabled":false}]`, false},
		{"codex installed", `{"installed":[{"pluginId":"superpowers@openai-curated","installed":true,"enabled":true}]}`, true},
		{"codex only available", `{"installed":[],"available":[{"name":"superpowers","installed":false}]}`, false},
		{"gemini extension", `[{"name":"superpowers","version":"6.2.0"}]`, true},
		{"unrelated", `[{"name":"other"}]`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSuperpowersList([]byte(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("installed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseSuperpowersListRejectsInvalidJSON(t *testing.T) {
	if _, err := ParseSuperpowersList([]byte("not json")); err == nil {
		t.Fatal("invalid provider output was accepted")
	}
}

func TestSuperpowersInstallHintsCoverSupportedProviders(t *testing.T) {
	for _, provider := range Supported {
		if hint := SuperpowersInstallHint(provider); hint == "" {
			t.Errorf("empty install hint for %s", provider)
		}
	}
}

func TestSuperpowersInstalledFallsBackToCodexPluginCache(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "plugins", "cache", "openai-curated-remote", "superpowers", "6.3.0", ".codex-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"superpowers","version":"6.3.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	installed, err := SuperpowersInstalled(t.Context(), Codex, superpowersTestCLI(t), map[string]string{
		"CODEX_HOME": root, "OMO_SUPERPOWERS_HELPER": `{"installed":[],"available":[]}`,
	})
	if err != nil || !installed {
		t.Fatalf("installed=%v err=%v", installed, err)
	}
}

func TestSuperpowersInstalledReadsClaudeEnabledRegistry(t *testing.T) {
	root := t.TempDir()
	plugins := filepath.Join(root, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	installedJSON := `{"version":2,"plugins":{"superpowers@claude-plugins-official":[{"scope":"user","version":"6.3.0"}]}}`
	settingsJSON := `{"enabledPlugins":{"superpowers@claude-plugins-official":true}}`
	if err := os.WriteFile(filepath.Join(plugins, "installed_plugins.json"), []byte(installedJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(settingsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	installed, err := SuperpowersInstalled(t.Context(), Claude, superpowersTestCLI(t), map[string]string{
		"CLAUDE_CONFIG_DIR": root, "OMO_SUPERPOWERS_HELPER": `[]`,
	})
	if err != nil || !installed {
		t.Fatalf("installed=%v err=%v", installed, err)
	}
}

func TestSuperpowersInstalledHonorsExplicitDisabledCLIResult(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "plugins", "cache", "source", "superpowers", "1.0.0", ".codex-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"superpowers"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	installed, err := SuperpowersInstalled(t.Context(), Codex, superpowersTestCLI(t), map[string]string{
		"CODEX_HOME":             root,
		"OMO_SUPERPOWERS_HELPER": `{"installed":[{"name":"superpowers","enabled":false}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("explicit disabled result was overridden by filesystem fallback")
	}
}

func superpowersTestCLI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-cli")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' \"$OMO_SUPERPOWERS_HELPER\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
