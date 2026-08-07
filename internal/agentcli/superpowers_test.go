package agentcli

import "testing"

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
