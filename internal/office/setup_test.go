package office

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/pluginfiles"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

func TestSetupCreatesAWorkingOffice(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	created, err := Setup(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) == 0 {
		t.Fatal("Setup reported nothing created")
	}
	for _, p := range []string{
		ConfigPath, ".omo/.gitignore", ".omo/extensions", ".omo/omo.db", ".omo/logs", ".omo/plugins", ".omo/storage", ".omo/worktrees", TemplatesVersionPath,
		filepath.Join(messages.Dir, "start_prompt.txt"),
		filepath.Join(prompts.Dir, "common.md"),
		filepath.Join(prompts.Dir, "reviewer.md"),
		filepath.Join(".omo/plugins/nudge", "plugin.json"),
		filepath.Join(".omo/plugins/nudge", "nudge.lua"),
		filepath.Join(".omo/plugins/tools", "plugin.json"),
		filepath.Join(".omo/plugins/tools", ".omo-bundled"),
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	if outdated, err := TemplatesOutdated(dir); err != nil || outdated {
		t.Fatalf("fresh setup templates reported outdated=%v, err=%v", outdated, err)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, ".omo", ".gitignore")); err != nil || string(raw) != officeGitignore {
		t.Fatalf(".omo/.gitignore = %q, err %v", raw, err)
	}
	// The generated config must actually load and validate.
	if _, err := config.Load(filepath.Join(dir, ConfigPath)); err != nil {
		t.Fatalf("generated config does not validate: %v", err)
	}
	configRaw, err := os.ReadFile(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"claude_config_dirs: []", "codex_homes: []"} {
		if !strings.Contains(string(configRaw), field) {
			t.Errorf("generated config missing usage home field %q", field)
		}
	}
	// The database must be a real, migrated omo database.
	d, err := sql.Open("sqlite", filepath.Join(dir, ".omo", "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, table := range []string{"agents", "jobs", "messages", "events", "incidents"} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Errorf("table %s not initialised: %v", table, err)
		}
	}
}

func TestSetupInstallsToolsPluginAndPreservesLocalEdits(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, ".omo", "plugins", "tools", "plugin.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("tools plugin was not installed: %v", err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"tools","hooks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	o, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	o.Close()
	if got, err := os.ReadFile(manifest); err != nil || string(got) != `{"name":"tools","hooks":[]}` {
		t.Fatalf("ordinary setup or startup overwrote local tools plugin edits: %q, err=%v", got, err)
	}
}

func TestSetupRecordsOwnershipWhenInstallingMissingToolsPlugin(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, ConfigPath)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "    tools:\n      source: builtin:tools\n      enabled: true\n", "", 1))
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".omo", "plugins", "tools")); err != nil {
		t.Fatal(err)
	}

	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "source: builtin:tools") {
		t.Fatalf("setup installed tools without recording bundled ownership:\n%s", raw)
	}
}

func TestSetupMalformedConfigDoesNotCreateUnownedToolsPlugin(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigPath), []byte("plugins: ["), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Setup(dir); err == nil {
		t.Fatal("setup accepted malformed config")
	}
	toolsDir := filepath.Join(dir, ".omo", "plugins", "tools")
	if _, err := os.Stat(toolsDir); !os.IsNotExist(err) {
		t.Fatalf("setup left an unowned tools plugin after config failure: %v", err)
	}
}

func TestSetupAndOpenRejectSchemaInvalidConfigWithoutMutation(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "unknown field",
			content: "plugins:\n  installed: {}\nunknown_top_level: true\n",
		},
		{
			name:    "plugins type conflict",
			content: "plugins: not-a-mapping\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OMO_HOME", t.TempDir())
			dir := t.TempDir()
			configPath := filepath.Join(dir, ConfigPath)
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := Setup(dir); err == nil {
				t.Fatal("setup accepted a schema-invalid config")
			}
			if _, err := Open(dir, true); err == nil {
				t.Fatal("open accepted a schema-invalid config")
			}

			raw, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tt.content {
				t.Fatalf("schema-invalid config was rewritten:\n%s", raw)
			}
			for _, path := range []string{
				prompts.ExtensionsDir,
				filepath.Join(".omo", "plugins", "nudge"),
				filepath.Join(".omo", "plugins", "tools"),
			} {
				if _, err := os.Lstat(filepath.Join(dir, path)); !os.IsNotExist(err) {
					t.Fatalf("schema-invalid config created %s: %v", path, err)
				}
			}
		})
	}
}

func TestEnsureBundledToolsRollsBackWhenOwnershipRecordingFails(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ConfigPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("plugins:\n  installed: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalRecord := recordBuiltinTools
	recordBuiltinTools = func(string) error { return errors.New("recording failed") }
	t.Cleanup(func() { recordBuiltinTools = originalRecord })

	if _, err := ensureBundledPlugins(dir, configPath, nil); err == nil {
		t.Fatal("bundled tools installation succeeded despite ownership-recording failure")
	}
	toolsDir := filepath.Join(dir, ".omo", "plugins", "tools")
	if _, err := os.Stat(toolsDir); !os.IsNotExist(err) {
		t.Fatalf("ownership failure left tools installed: %v", err)
	}

	recordBuiltinTools = originalRecord
	installed, err := ensureBundledPlugins(dir, configPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(installed, "tools") {
		t.Fatalf("clean retry did not install tools: %v", installed)
	}
	source, configured, err := configuredPluginSource(configPath, "tools")
	if err != nil || !configured || source != "builtin:tools" {
		t.Fatalf("clean retry did not record bundled ownership: source=%q configured=%v err=%v", source, configured, err)
	}
}

func TestEnsureBundledToolsPreservesReplacementWhenOwnershipRecordingFails(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ConfigPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("plugins:\n  installed: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	toolsDir := filepath.Join(dir, ".omo", "plugins", "tools")
	originalRecord := recordBuiltinTools
	recordBuiltinTools = func(string) error {
		if err := os.RemoveAll(toolsDir); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(toolsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(toolsDir, "plugin.json"), []byte(`{"name":"tools","description":"replacement","hooks":[]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		return errors.New("recording failed")
	}
	t.Cleanup(func() { recordBuiltinTools = originalRecord })

	if _, err := ensureBundledPlugins(dir, configPath, nil); err == nil {
		t.Fatal("bundled tools installation succeeded despite ownership-recording failure")
	}
	got, err := os.ReadFile(filepath.Join(toolsDir, "plugin.json"))
	if err != nil || string(got) != `{"name":"tools","description":"replacement","hooks":[]}` {
		t.Fatalf("ownership rollback removed replacement plugin: %q, err=%v", got, err)
	}
}

func TestEnsureBundledToolsHoldsPluginRootLockDuringOwnershipRecording(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ConfigPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("plugins:\n  installed: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalRecord := recordBuiltinTools
	recordBuiltinTools = func(string) error {
		cmd := exec.Command(os.Args[0], "-test.run=^TestEnsureBundledToolsLockChild$")
		cmd.Env = append(os.Environ(), "OMO_PLUGIN_ROOT_LOCK_TEST_PATH="+filepath.Join(dir, ".omo", "plugins"))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("plugin-root lock child: %v\n%s", err, output)
		}
		return errors.New("recording failed")
	}
	t.Cleanup(func() { recordBuiltinTools = originalRecord })

	if _, err := ensureBundledPlugins(dir, configPath, nil); err == nil {
		t.Fatal("bundled tools installation succeeded despite ownership-recording failure")
	}
}

func TestEnsureBundledToolsLockChild(t *testing.T) {
	root := os.Getenv("OMO_PLUGIN_ROOT_LOCK_TEST_PATH")
	if root == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	lock, err := pluginfiles.Lock(ctx, root)
	if err == nil {
		_ = lock.Close()
		t.Fatal("plugin-root lock was not held during ownership recording")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("plugin-root lock error = %v, want deadline", err)
	}
}

func TestSetupDoesNotInstallBundledToolsOverLocalManifestAlias(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	aliasDir := filepath.Join(dir, ".omo", "plugins", "maintenance")
	if err := os.MkdirAll(aliasDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(aliasDir, "plugin.json"), []byte(`{"name":"tools","description":"local alias","hooks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".omo", "plugins", "tools")); !os.IsNotExist(err) {
		t.Fatalf("setup installed bundled tools over a local manifest alias: %v", err)
	}
	o, err := Open(dir, true)
	if err != nil {
		t.Fatalf("opening an office with a local tools manifest alias: %v", err)
	}
	defer o.Close()
}

func TestSetupAndOpenDoNotInstallBundledToolsForConfiguredThirdPartyPlugin(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, ConfigPath)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "source: builtin:tools", "source: https://example.test/tools.git", 1))
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	toolsDir := filepath.Join(dir, ".omo", "plugins", "tools")
	if err := os.RemoveAll(toolsDir); err != nil {
		t.Fatal(err)
	}

	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(toolsDir); !os.IsNotExist(err) {
		t.Fatalf("ordinary setup installed bundled tools for a third-party config: %v", err)
	}
	o, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if _, err := os.Stat(toolsDir); !os.IsNotExist(err) {
		t.Fatalf("office open installed bundled tools for a third-party config: %v", err)
	}
}

func TestFreshSetupDoesNotClaimPreexistingUnmanagedToolsPlugin(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, ".omo", "plugins", "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(toolsDir, "plugin.json")
	const thirdPartyManifest = `{"name":"tools","description":"third party","hooks":[]}`
	if err := os.WriteFile(manifest, []byte(thirdPartyManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed := cfg.Plugins.Installed["tools"]; claimed {
		t.Fatalf("fresh setup claimed an unmanaged tools plugin: %+v", cfg.Plugins.Installed["tools"])
	}
	if got, err := os.ReadFile(manifest); err != nil || string(got) != thirdPartyManifest {
		t.Fatalf("fresh setup changed unmanaged tools: %q, err %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(toolsDir, ".omo-bundled")); !os.IsNotExist(err) {
		t.Fatalf("fresh setup transiently populated unmanaged tools with bundled files: %v", err)
	}
}

func TestSetupSupportsEachOfficialAgentCLI(t *testing.T) {
	tests := []struct {
		provider agentcli.Provider
		profile  string
		wantArg  string
	}{
		{agentcli.Claude, "claude-fable", "--dangerously-skip-permissions"},
		{agentcli.Codex, "codex", "--dangerously-bypass-approvals-and-sandbox"},
		{agentcli.Gemini, "gemini", "--yolo"},
	}
	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			dir := t.TempDir()
			if _, err := SetupWithAgentCLI(dir, tt.provider); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(filepath.Join(dir, ConfigPath))
			if err != nil {
				t.Fatal(err)
			}
			profile := cfg.Models[tt.profile]
			if profile.Provider != tt.provider {
				t.Fatalf("profile provider = %q, want %q", profile.Provider, tt.provider)
			}
			if !contains(profile.Args, tt.wantArg) {
				t.Fatalf("profile args = %v, want %q", profile.Args, tt.wantArg)
			}
			for _, role := range config.AllRoles {
				for _, model := range cfg.Roles[role].Models {
					assigned := cfg.Models[model]
					if tt.provider != agentcli.Claude && assigned.Provider != tt.provider {
						t.Errorf("role %s uses provider %q, want %q", role, assigned.Provider, tt.provider)
					}
				}
			}
		})
	}
}

func TestClaudeSetupConfigDefinesCodexAstraProfile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}

	astra, ok := cfg.Models["codex-astra"]
	if !ok {
		t.Fatal("Claude setup config is missing the codex-astra profile")
	}
	if astra.Provider != agentcli.Codex || astra.Cmd != "codex" || !slices.Equal(astra.Args, []string{"--model", "gpt-6-astra", "--dangerously-bypass-approvals-and-sandbox"}) {
		t.Fatalf("codex-astra profile = %+v", astra)
	}
}

func TestClaudeSetupConfigUsesClaudeFableThenAstraForCEOFailover(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.Models["fable"]; exists {
		t.Fatal("Claude setup config must not define a legacy bare fable profile")
	}

	ceo := cfg.Roles["ceo"]
	if ceo.Assignment != config.AssignmentFailover || !slices.Equal(ceo.Models, []string{"claude-fable", "codex-astra"}) {
		t.Fatalf("CEO profile assignment = %+v, want claude-fable first with Astra failover", ceo)
	}
}

func TestGeneratedConfigIncludesConcreteCommentedModelExamples(t *testing.T) {
	tests := []struct {
		provider agentcli.Provider
		want     []string
	}{
		{
			provider: agentcli.Claude,
			want: []string{
				`codex-sol:`, `args: ["--model", "gpt-5.6-sol"`,
				`codex-luna:`, `args: ["--model", "gpt-5.6-luna"`,
				`codex-mini:`, `args: ["--model", "gpt-5.4-mini"`,
				`# gemini-auto:`, `#   args: ["--model", "auto"`,
				`# gemini-pro:`, `#   args: ["--model", "pro"`,
				`# gemini-fast:`, `#   args: ["--model", "flash"`,
				`# gemini-light:`, `#   args: ["--model", "flash-lite"`,
			},
		},
		{
			provider: agentcli.Codex,
			want: []string{
				`# codex-capable:`, `#   args: ["--model", "gpt-5.3-codex"`,
				`# codex-fast:`, `#   args: ["--model", "codex-mini-latest"`,
			},
		},
		{
			provider: agentcli.Gemini,
			want: []string{
				`# gemini-auto:`, `#   args: ["--model", "auto"`,
				`# gemini-pro:`, `#   args: ["--model", "pro"`,
				`# gemini-fast:`, `#   args: ["--model", "flash"`,
				`# gemini-light:`, `#   args: ["--model", "flash-lite"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			dir := t.TempDir()
			if _, err := SetupWithAgentCLI(dir, tt.provider); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, ConfigPath))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(raw), want) {
					t.Errorf("generated config missing %q", want)
				}
			}
			for _, want := range []string{"%prompt%", "prompt_retry_count: 0", "prompt_retry_wait"} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("generated config missing prompt-delivery documentation %q", want)
				}
			}
			if strings.Contains(string(raw), "YOUR_") {
				t.Error("generated config contains a placeholder model identifier")
			}
		})
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSetupIsIdempotentAndPreservesEdits(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, ConfigPath)
	os.WriteFile(cfg, []byte("# my config\nmodels:\n  x:\n    cmd: claude\nroles:\n  ceo: x\n  product_manager: x\n  developer: x\n  reviewer: x\n  freelancer: x\n  smokealarm: x\n  firefighter: x\n"), 0o644)
	nudge := filepath.Join(dir, messages.Dir, "mail_nudge.txt")
	os.WriteFile(nudge, []byte("MINE"), 0o644)
	ignore := filepath.Join(dir, ".omo", ".gitignore")
	os.WriteFile(ignore, []byte("# MINE\n"), 0o644)
	missing := filepath.Join(dir, prompts.Dir, "reviewer.md")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}

	created, err := Setup(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 0 {
		t.Fatalf("second Setup should create nothing, reported %v", created)
	}
	raw, _ := os.ReadFile(cfg)
	if string(raw[:11]) != "# my config" {
		t.Fatal("Setup overwrote an existing config")
	}
	if raw, _ := os.ReadFile(nudge); string(raw) != "MINE" {
		t.Fatalf("Setup overwrote an edited template: %q", raw)
	}
	if raw, _ := os.ReadFile(ignore); string(raw) != "# MINE\n" {
		t.Fatalf("Setup overwrote an edited .gitignore: %q", raw)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("Setup changed an initialized office; missing prompt stat error = %v", err)
	}
}

func TestSetupAddsMissingExtensionsDirectoryToExistingOffice(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	extensions := filepath.Join(dir, prompts.ExtensionsDir)
	if err := os.RemoveAll(extensions); err != nil {
		t.Fatal(err)
	}
	created, err := Setup(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0] != prompts.ExtensionsDir+"/" {
		t.Fatalf("created = %v", created)
	}
	if info, err := os.Stat(extensions); err != nil || !info.IsDir() {
		t.Fatalf("extensions directory stat = %v, err %v", info, err)
	}
}

func TestUpdateTemplatesReplacesEmbeddedAssets(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}

	messagePath := filepath.Join(dir, messages.Dir, "mail_nudge.txt")
	promptPath := filepath.Join(dir, prompts.Dir, "common.md")
	pluginPath := filepath.Join(dir, ".omo", "plugins", "nudge", "nudge.lua")
	toolsPath := filepath.Join(dir, ".omo", "plugins", "tools", "plugin.json")
	wantMessage, err := os.ReadFile(messagePath)
	if err != nil {
		t.Fatal(err)
	}
	wantPrompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	wantPlugin, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(messagePath, []byte("CUSTOM MESSAGE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte("CUSTOM PROMPT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginPath, []byte("-- CUSTOM PLUGIN"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toolsPath, []byte(`{"name":"tools","hooks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	messageExtra := filepath.Join(dir, messages.Dir, "obsolete.txt")
	promptExtra := filepath.Join(dir, prompts.Dir, "obsolete.md")
	pluginExtra := filepath.Join(dir, ".omo", "plugins", "nudge", "obsolete.lua")
	toolsExtra := filepath.Join(dir, ".omo", "plugins", "tools", "obsolete.json")
	if err := os.WriteFile(messageExtra, []byte("obsolete"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptExtra, []byte("obsolete"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginExtra, []byte("obsolete"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toolsExtra, []byte("obsolete"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outdated, err := TemplatesOutdated(dir); err != nil || outdated {
		t.Fatalf("local embedded-asset edits reported outdated=%v, err=%v", outdated, err)
	}
	if err := os.WriteFile(filepath.Join(dir, TemplatesVersionPath), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outdated, err := TemplatesOutdated(dir); err != nil || !outdated {
		t.Fatalf("old marker reported outdated=%v, err=%v", outdated, err)
	}

	untouched := map[string]string{
		ConfigPath:                         "# custom config\n",
		".omo/.gitignore":                  "# custom ignore\n",
		".omo/logs/keep.log":               "log data\n",
		".omo/worktrees/keep/sentinel.txt": "worktree data\n",
		".omo/other-state":                 "other data\n",
		".omo/extensions/developer.md":     "custom extension\n",
		".omo/plugins/custom/plugin.json":  "custom plugin\n",
	}
	for path, content := range untouched {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := sql.Open("sqlite", filepath.Join(dir, ".omo", "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO events(kind, detail) VALUES ('sentinel', 'keep me')`); err != nil {
		d.Close()
		t.Fatal(err)
	}
	d.Close()

	replaced, err := UpdateTemplates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 3 {
		t.Fatalf("UpdateTemplates reported %v, want three replaced directories", replaced)
	}
	if outdated, err := TemplatesOutdated(dir); err != nil || outdated {
		t.Fatalf("updated templates reported outdated=%v, err=%v", outdated, err)
	}
	if got, _ := os.ReadFile(messagePath); string(got) != string(wantMessage) {
		t.Fatalf("message was not reset to embedded default: %q", got)
	}
	if got, _ := os.ReadFile(promptPath); string(got) != string(wantPrompt) {
		t.Fatalf("prompt was not reset to embedded default: %q", got)
	}
	if got, _ := os.ReadFile(pluginPath); string(got) != string(wantPlugin) {
		t.Fatalf("bundled plugin was not reset to embedded default: %q", got)
	}
	if got, _ := os.ReadFile(toolsPath); string(got) != `{"name":"tools","hooks":[]}` {
		t.Fatalf("config-less tools plugin was reset: %q", got)
	}
	for _, extra := range []string{messageExtra, promptExtra, pluginExtra} {
		if _, err := os.Stat(extra); !os.IsNotExist(err) {
			t.Fatalf("obsolete template survived replacement: %s (err %v)", extra, err)
		}
	}
	if _, err := os.Stat(toolsExtra); err != nil {
		t.Fatalf("config-less tools plugin was changed: %v", err)
	}
	for path, want := range untouched {
		if got, err := os.ReadFile(filepath.Join(dir, path)); err != nil || string(got) != want {
			t.Errorf("update touched %s: got %q, err %v", path, got, err)
		}
	}
	d, err = sql.Open("sqlite", filepath.Join(dir, ".omo", "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'sentinel' AND detail = 'keep me'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("update changed database state: count=%d, err=%v", count, err)
	}
}

func TestUpdateTemplatesPreservesThirdPartyToolsPlugin(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, ConfigPath)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "source: builtin:tools", "source: https://example.test/tools.git", 1))
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	toolsPath := filepath.Join(dir, ".omo", "plugins", "tools", "plugin.json")
	const thirdPartyManifest = `{"name":"tools","description":"third party","hooks":[]}`
	if err := os.WriteFile(toolsPath, []byte(thirdPartyManifest), 0o644); err != nil {
		t.Fatal(err)
	}

	replaced, err := UpdateTemplates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(replaced, ".omo/plugins/tools/") {
		t.Fatalf("third-party tools plugin was reported as bundled: %v", replaced)
	}
	if got, err := os.ReadFile(toolsPath); err != nil || string(got) != thirdPartyManifest {
		t.Fatalf("third-party tools plugin was replaced: %q, err %v", got, err)
	}
}

func TestUpdateTemplatesPreservesConfiglessToolsPluginWithBundledMarker(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, ConfigPath)
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "    tools:\n      source: builtin:tools\n      enabled: true\n", "", 1))
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	toolsPath := filepath.Join(dir, ".omo", "plugins", "tools", "plugin.json")
	const thirdPartyManifest = `{"name":"tools","description":"third party","hooks":[]}`
	if err := os.WriteFile(toolsPath, []byte(thirdPartyManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".omo", "plugins", "tools", ".omo-bundled"), []byte("builtin:tools\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed := cfg.Plugins.Installed["tools"]; claimed {
		t.Fatalf("config migration claimed the config-less tools plugin: %+v", cfg.Plugins.Installed["tools"])
	}
	replaced, err := UpdateTemplates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(replaced, ".omo/plugins/tools/") {
		t.Fatalf("config-less tools plugin was reported as bundled: %v", replaced)
	}
	if got, err := os.ReadFile(toolsPath); err != nil || string(got) != thirdPartyManifest {
		t.Fatalf("config-less tools plugin was replaced: %q, err %v", got, err)
	}
}

func TestUpdateTemplatesRequiresExistingOffice(t *testing.T) {
	if _, err := UpdateTemplates(t.TempDir()); err == nil {
		t.Fatal("UpdateTemplates should reject a directory without an initialized office")
	}
}

func TestUpdateTemplatesMigratesLegacyOfficeWithoutPluginsDirectory(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	pluginsDir := filepath.Join(dir, ".omo", "plugins")
	if err := os.RemoveAll(pluginsDir); err != nil {
		t.Fatal(err)
	}
	replaced, err := UpdateTemplates(dir)
	if err != nil {
		t.Fatal(err)
	}
	pluginDir := filepath.Join(pluginsDir, "nudge")
	if _, err := os.Stat(filepath.Join(pluginDir, "plugin.json")); err != nil {
		t.Fatalf("default plugin was not installed into legacy office: %v", err)
	}
	if !slices.Contains(replaced, ".omo/plugins/nudge/") {
		t.Fatalf("restored plugin not reported: %v", replaced)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "tools", "plugin.json")); err != nil {
		t.Fatalf("tools plugin was not installed into legacy office: %v", err)
	}
	if !slices.Contains(replaced, ".omo/plugins/tools/") {
		t.Fatalf("restored tools plugin not reported: %v", replaced)
	}
}

// A fresh office must be immediately runnable, with no manual steps.
func TestSetupThenOpenWorks(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	o, err := Open(dir, true)
	if err != nil {
		t.Fatalf("a freshly set-up office must open: %v", err)
	}
	o.Close()
}

func TestSetupAllowsDeepOfficeWithShortRuntimeSocket(t *testing.T) {
	deep := filepath.Join(t.TempDir(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"cccccccccccccccccccccccccccccc", "dddddddddddddddddddddddddddddd")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Skipf("cannot create deep path: %v", err)
	}
	if _, err := Setup(deep); err != nil {
		t.Fatalf("deep office should use a short temporary socket: %v", err)
	}
}
