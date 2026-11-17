package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// A Claude Code or Codex agent gets omo's MCP bridge for its session while
// the MCP settings switch it on for its program; Antigravity, Gemini CLI
// and custom programs have no per-session entry.
func TestAgentsGetTheMCPBridgeAsTheSettingsSay(t *testing.T) {
	const bridge = "the omo executable"
	on := api.MCPSettings{Enabled: true, Inject: api.MCPInject{Claude: true, Codex: true}}
	for _, tc := range []struct {
		name     string
		cli      string
		settings *api.MCPSettings
		want     string
	}{
		{"claude by default", api.CLIClaude, nil, bridge},
		{"codex by default", api.CLICodex, nil, bridge},
		{"claude", api.CLIClaude, &on, bridge},
		{"codex", api.CLICodex, &on, bridge},
		{"agy", api.CLIAgy, &on, ""},
		{"gemini", api.CLIGemini, &on, ""},
		{"custom", api.CLICustom, &on, ""},
		{"claude switched off", api.CLIClaude, &api.MCPSettings{Enabled: true, Inject: api.MCPInject{Codex: true}}, ""},
		{"codex switched off", api.CLICodex, &api.MCPSettings{Enabled: true, Inject: api.MCPInject{Claude: true}}, ""},
		{"endpoint off", api.CLIClaude, &api.MCPSettings{Inject: api.MCPInject{Claude: true, Codex: true}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := []func(*harnessConfig){withProfile(settings.Profile{Name: "fake", CLI: tc.cli, Command: "fake-agent",
				Models: map[string]settings.Model{"a": {}, "b": {}}})}
			if tc.settings != nil {
				s := *tc.settings
				opts = append(opts, withMCP(func() api.MCPSettings { return s }))
			}
			h := newHarness(t, opts...)
			if _, err := h.e.StartCEO(h.ctx, false); err != nil {
				t.Fatal(err)
			}
			want := ""
			if tc.want == bridge {
				want = h.e.omoPath
			}
			specs := h.runner.specList()
			if len(specs) != 1 || specs[0].MCPBridge != want {
				t.Fatalf("bridge %q, want %q", specs[0].MCPBridge, want)
			}
		})
	}
}

// The bridge is the omo executable with its links resolved: `omo update`
// replaces that file in place.
func TestMCPBridgeIsTheResolvedExecutable(t *testing.T) {
	h := newHarness(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(exe); h.e.omoPath != want || want == "" {
		t.Fatalf("omoPath %q, want %q", h.e.omoPath, want)
	}
}
