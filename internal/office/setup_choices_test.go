package office

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
)

func TestSetupCatalogUsesDetectedProfilesAndCurrentRoleDefaults(t *testing.T) {
	catalog, err := SetupCatalogFor(agentcli.Claude, []agentcli.Provider{agentcli.Claude, agentcli.Codex})
	if err != nil {
		t.Fatal(err)
	}
	ceo := catalog.Roles["ceo"]
	if !reflect.DeepEqual(ceo.Models, []string{"claude-fable", "codex-astra"}) || ceo.Assignment != config.AssignmentFailover {
		t.Fatalf("CEO defaults = %+v", ceo)
	}
	for _, name := range []string{"claude-fable", "claude-opus", "codex-astra", "codex-sol"} {
		if _, ok := catalog.Models[name]; !ok {
			t.Errorf("detected catalog missing %s", name)
		}
	}
	if _, ok := catalog.Models["gemini"]; ok {
		t.Fatal("undetected Gemini profile was offered")
	}
}

func TestSetupSandboxedSmokeProfilesOnUnix(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			catalog, err := setupCatalogForGOOS(agentcli.Claude, []agentcli.Provider{agentcli.Claude, agentcli.Codex}, goos)
			if err != nil {
				t.Fatal(err)
			}
			smoke := catalog.Roles["smokealarm"]
			if !reflect.DeepEqual(smoke.Models, []string{"claude-haiku-sandboxed", "claude-sonnet-sandboxed"}) || smoke.Assignment != config.AssignmentFailover {
				t.Fatalf("smoke defaults = %+v", smoke)
			}
			for _, name := range smoke.Models {
				if p := catalog.Models[name]; p.Sandbox == nil || !p.Sandbox.Enabled {
					t.Errorf("%s must be sandboxed: %+v", name, p)
				}
			}
			if p := catalog.Models["claude-sonnet"]; p.Sandbox != nil && p.Sandbox.Enabled {
				t.Fatalf("write-capable Sonnet became sandboxed: %+v", p)
			}
			if !reflect.DeepEqual(catalog.Roles["developer"].Models, []string{"claude-sonnet", "codex-sol"}) {
				t.Fatalf("developer assignment changed: %+v", catalog.Roles["developer"])
			}
		})
	}
}

func TestSetupCodexOnlySmokeUsesSmallSandboxedProfile(t *testing.T) {
	catalog, err := setupCatalogForGOOS(agentcli.Codex, []agentcli.Provider{agentcli.Codex}, "linux")
	if err != nil {
		t.Fatal(err)
	}
	smoke := catalog.Roles["smokealarm"]
	if !reflect.DeepEqual(smoke.Models, []string{"codex-luna-sandboxed"}) {
		t.Fatalf("Codex smoke = %+v", smoke)
	}
	p := catalog.Models[smoke.Models[0]]
	if p.Sandbox == nil || !p.Sandbox.Enabled || !reflect.DeepEqual(p.Args, []string{"--model", "gpt-6-luna", "--dangerously-bypass-approvals-and-sandbox"}) {
		t.Fatalf("Codex smoke profile = %+v", p)
	}
	if catalog.Roles["developer"].First() != "codex" {
		t.Fatalf("developer assignment changed: %+v", catalog.Roles["developer"])
	}
}

func TestSetupWindowsSmokeUsesStrongerUnsandboxedProfile(t *testing.T) {
	for _, tc := range []struct {
		provider agentcli.Provider
		want     string
	}{{agentcli.Claude, "claude-sonnet"}, {agentcli.Codex, "codex-sol"}} {
		t.Run(string(tc.provider), func(t *testing.T) {
			catalog, err := setupCatalogForGOOS(tc.provider, []agentcli.Provider{tc.provider}, "windows")
			if err != nil {
				t.Fatal(err)
			}
			smoke := catalog.Roles["smokealarm"]
			if !reflect.DeepEqual(smoke.Models, []string{tc.want}) {
				t.Fatalf("Windows smoke = %+v", smoke)
			}
			p := catalog.Models[tc.want]
			if p.Sandbox != nil && p.Sandbox.Enabled {
				t.Fatalf("Windows profile sandboxed: %+v", p)
			}
			for name := range catalog.Models {
				if strings.HasSuffix(name, "-sandboxed") {
					t.Errorf("Windows generated unsupported sandboxed profile %q", name)
				}
			}
		})
	}
}

func TestSetupWithOptionsRejectsInvalidInteractiveChoices(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	catalog, err := SetupCatalogFor(agentcli.Gemini, []agentcli.Provider{agentcli.Gemini})
	if err != nil {
		t.Fatal(err)
	}
	roles := make(map[string]config.RoleModels, len(config.AllRoles))
	for _, role := range config.AllRoles {
		roles[role] = config.RoleModels{Models: []string{"gemini"}, Assignment: config.AssignmentSmart}
	}
	_, err = SetupWithOptions(dir, SetupOptions{Provider: agentcli.Gemini, Models: catalog.Models, Roles: roles})
	if err == nil || !strings.Contains(err.Error(), "smart assignment") {
		t.Fatalf("invalid choices error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ConfigPath)); !os.IsNotExist(statErr) {
		t.Fatalf("invalid setup left config marker behind: %v", statErr)
	}
}

func TestSetupWithOptionsPersistsRoleAssignmentsAndPluginChoices(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	catalog, err := SetupCatalogFor(agentcli.Claude, []agentcli.Provider{agentcli.Claude, agentcli.Codex})
	if err != nil {
		t.Fatal(err)
	}
	roles := make(map[string]config.RoleModels, len(config.AllRoles))
	for _, role := range config.AllRoles {
		roles[role] = config.RoleModels{Models: []string{"codex-sol"}, Assignment: config.AssignmentRandom}
	}
	plugins := map[string]config.Plugin{
		"nudge": {Source: "builtin:nudge", Enabled: false},
		"tools": {Source: "builtin:tools", Enabled: true},
	}
	if _, err := SetupWithOptions(dir, SetupOptions{Provider: agentcli.Claude, Models: catalog.Models, Roles: roles, Plugins: plugins}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir + "/" + ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Roles, roles) {
		t.Fatalf("roles = %#v, want %#v", cfg.Roles, roles)
	}
	if cfg.Plugins.Installed["nudge"].Enabled || !cfg.Plugins.Installed["tools"].Enabled {
		t.Fatalf("plugin choices = %#v", cfg.Plugins.Installed)
	}
}

func TestInteractiveChoicesOverrideSavedTemplateDefaults(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	home, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	gemini, err := SetupCatalogFor(agentcli.Gemini, []agentcli.Provider{agentcli.Gemini})
	if err != nil {
		t.Fatal(err)
	}
	if err := home.SaveSetupTemplate(gemini.Models, gemini.Roles); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(home.Dir, "template", ".omo", "omo.yaml")
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("plugins:\n  installed:\n    template-plugin:\n      source: https://example.com/template.git\n      enabled: false\n")...)
	if err := os.WriteFile(templatePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	codex, err := SetupCatalogFor(agentcli.Codex, []agentcli.Provider{agentcli.Codex})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := SetupWithOptions(dir, SetupOptions{Provider: agentcli.Codex, Models: codex.Models, Roles: codex.Roles}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Roles["developer"].First() != codex.Roles["developer"].First() {
		t.Fatalf("saved template overrode wizard choice: %+v", cfg.Roles["developer"])
	}
	if _, ok := cfg.Plugins.Installed["template-plugin"]; !ok {
		t.Fatalf("template plugin was discarded: %#v", cfg.Plugins.Installed)
	}
}
