package office

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/pluginfiles"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/yamlformat"
	bundledplugins "github.com/scolastico-dev/one-man-office/plugins"
	"gopkg.in/yaml.v3"
)

// DefaultConfig is the omo.yaml written by Setup. It is deliberately
// complete rather than minimal: every knob is present and commented, so the
// file doubles as the configuration reference.
const DefaultConfig = `# one-man-office configuration.
# Run 'omo' in this directory to start the office.

# Repositories agents may work in, as <key>: <absolute or office-relative path>.
# Developer jobs name one of these keys; omo creates a git worktree per job.
# An office is either one repository, or a directory holding several of them
# (a microservice landscape) — both are supported, and 'omo setup' fills this
# in from what it found.
%s

# Runner profiles. A profile is just a command line. 'provider' enables the
# startup adapter for an officially supported CLI; omit it for custom runners.
# 'selectable: false' hides a profile from the CEO's --model flag while still
# allowing a role to run on it.
# Any args entry may contain %%prompt%%, which is replaced with the initial
# prompt. Per-profile prompt_delay overrides agents.start_prompt_delay for PTY
# injection. inject_prompt controls automatic provider/PTY injection; prompt
# retries default to 3 additional sends with prompt_retry_wait: 30s. Set
# prompt_retry_count: 0 for the legacy one-shot behavior.
%s

# Optional release/embedded-asset checks performed before the office starts.
# The separate Claude/Codex weekly-usage preflight is strict when enabled.
startup:
  check_self_update: true
  check_templates: true
  check_timeout: 5s

# Agent process lifecycle and retry behavior.
agents:
  ready_timeout: 2m
  start_prompt_delay: 2s
  max_spawn_retries: 2
  max_job_retries: 3
  lower_priority: true          # Linux: lower agent process priority
  nice_increment: 10            # added to inherited nice value, capped at 19
  env:                           # injected into every agent PTY
    GIT_AUTHOR_NAME: "OMO - AI Orchestrator"
    GIT_AUTHOR_EMAIL: "omo@scolasti.co"
    GIT_COMMITTER_NAME: "${GIT_COMMITTER_NAME:$GIT_AUTHOR_NAME}"
    GIT_COMMITTER_EMAIL: "${GIT_COMMITTER_EMAIL:$GIT_AUTHOR_EMAIL}"
    GIT_CONFIG_PARAMETERS: "'commit.gpgSign=false' ${GIT_CONFIG_PARAMETERS:-}"

# CEO crash-loop protection.
ceo:
  max_restarts: 3
  restart_window: 30s
  restart_backoff: 500ms

# Concurrency caps.
limits:
  max_developers: 4
  max_freelancers: 2

# Developer branches are named by a short-lived agent using the same profile
# selection as the smoke-alarm role. Set naming: generated for numeric names.
branches:
  prefix: omo/job-
  naming: ai

# Metered Claude/Codex profiles at or above this weekly use cannot spawn.
usage:
  enabled: true                 # false disables usage API calls and limits
  weekly_limit_percent: 90
  safe_shutdown_percent: 85
  refresh_interval: 10m
  claude_config_dirs: []        # absolute CLAUDE_CONFIG_DIR values
  codex_homes: []               # absolute CODEX_HOME values

# The smoke alarm can inspect all agents in one context, or start one alarm
# per agent. Previous-run tails make stalls and loops easier to compare.
smokealarm:
  enabled: true
  run_on_start: false
  mode: all                    # all | per_agent
  interval: 5m
  timeout: 2m                  # restart a round that does not finish
  tail_lines: 120
  history_runs: 3
  include_events: true
  include_pm_chatter: true

# Session transcripts in .omo/logs. Live sessions rotate at max_size_kb.
# keep counts completed session groups; living agents never count. Set -1
# to disable inactive-log pruning, or 0 to remove completed logs.
logs:
  max_size_kb: 2048
  keep: 50

# After this many consecutive rejected reviews, ask the PM to decide whether
# the findings have become nitpicking or moved out of scope.
reviews:
  escalate_after: 2

# Never inject automated mail or omo type input while the user is actively
# typing into that agent's terminal. Set to 0s to disable this protection.
notifications:
  input_debounce: 30s

# Git-backed office plugins managed by omo plugin commands.
plugins:
  update_on_start: true
  log_lines: 500                # retained lines per plugin
  installed:
    nudge:
      source: builtin:nudge
      enabled: true
      config:
        check_interval: 1m
        activity_sample_interval: 30s
        reminders:
          inbox: {after: 5m, repeat: 15m}
          smokealarm_done: {after: 5m, repeat: 10m}
          park_completed: {after: 2m, repeat: 15m}
          reviewer_wait: {after: 5m, repeat: 15m}
          no_job_wait: {after: 15m, repeat: 30m}
          stale_work: {after: 15m, repeat: 30m}
%s

# Storage files expire after 60 distinct days on which the office records
# activity since their last edit. Set storage_active_days to 0 to disable.
# SQLite duration rules remain off until their values are non-zero.
cleanup:
  interval: 1h
  read_messages_after: 0s
  terminal_jobs_after: 0s
  storage_active_days: 60
  max_entries:                  # 0 disables a table cap
    agents: 10000
    jobs: 10000
    messages: 50000
    events: 100000
    incidents: 10000
    overall_statistics: 1000
    shutdown_contexts: 1000
    model_usage_snapshots: 100

# Satisfy supported CLIs' workspace-trust gates for each agent workdir.
# Claude trust is persisted in ~/.claude.json; Gemini trust is session-only.
trust_workdirs: true
`

const claudeProfiles = `models:
  claude-fable:
    provider: claude
    cmd: claude
    args: ["--model", "fable", "--dangerously-skip-permissions"]
    selectable: false
  codex-astra:
    provider: codex
    cmd: codex
    args: ["--model", "gpt-6-astra", "--dangerously-bypass-approvals-and-sandbox"]
  claude-opus:
    provider: claude
    cmd: claude
    args: ["--model", "opus", "--dangerously-skip-permissions"]
  claude-sonnet:
    provider: claude
    cmd: claude
    args: ["--model", "sonnet", "--dangerously-skip-permissions"]
  claude-haiku:
    provider: claude
    cmd: claude
    args: ["--model", "haiku", "--dangerously-skip-permissions"]
  codex-sol:
    provider: codex
    cmd: codex
    args: ["--model", "gpt-5.6-sol", "--dangerously-bypass-approvals-and-sandbox"]
  codex-luna:
    provider: codex
    cmd: codex
    args: ["--model", "gpt-5.6-luna", "--dangerously-bypass-approvals-and-sandbox"]
  codex-mini:
    provider: codex
    cmd: codex
    args: ["--model", "gpt-5.4-mini", "--dangerously-bypass-approvals-and-sandbox"]

  # Gemini examples are intentionally inactive. We recommend Claude and
  # Codex for omo; enable Gemini only after reviewing the tradeoffs in README.
  # gemini-auto:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "auto", "--yolo"]
  # gemini-pro:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "pro", "--yolo"]
  # gemini-fast:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "flash", "--yolo"]
  # gemini-light:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "flash-lite", "--yolo"]

# Profiles per role. A role may instead use [profile-a, profile-b], or a
# {models: [...], assignment: round_robin|random|failover|smart} mapping.
# All seven roles are required.
roles:
  ceo:
    models: [claude-fable, codex-astra]
    assignment: failover
  product_manager:
    models: [claude-opus, codex-sol]
    assignment: round_robin
  developer:
    models: [claude-sonnet, codex-luna]
    assignment: round_robin
  reviewer:
    models: [claude-opus, codex-sol]
    assignment: random
  freelancer:
    models: [codex-luna, claude-sonnet]
    assignment: failover
  smokealarm:
    models: [claude-haiku, codex-mini]
    assignment: failover
  firefighter: claude-opus`

const codexProfiles = `models:
  codex:
    provider: codex
    cmd: codex
    args: ["--dangerously-bypass-approvals-and-sandbox"]

  # Concrete opt-in examples. The unqualified profile above follows the
  # account's current default, so fresh offices do not assume model access.
  # codex-capable:
  #   provider: codex
  #   cmd: codex
  #   args: ["--model", "gpt-5.3-codex", "--dangerously-bypass-approvals-and-sandbox"]
  # codex-fast:
  #   provider: codex
  #   cmd: codex
  #   args: ["--model", "codex-mini-latest", "--dangerously-bypass-approvals-and-sandbox"]

# Profiles per role also accept a list or a models/assignment mapping.
roles:
  ceo: codex
  product_manager: codex
  developer: codex
  reviewer: codex
  freelancer: codex
  smokealarm: codex
  firefighter: codex`

const geminiProfiles = `models:
  gemini:
    provider: gemini
    cmd: gemini
    args: ["--yolo"]

  # Gemini CLI aliases are portable across preview rollouts. Uncomment the
  # profiles you want and assign their keys to roles below.
  # gemini-auto:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "auto", "--yolo"]
  # gemini-pro:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "pro", "--yolo"]
  # gemini-fast:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "flash", "--yolo"]
  # gemini-light:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "flash-lite", "--yolo"]

# Profiles per role also accept a list or a models/assignment mapping.
roles:
  ceo: gemini
  product_manager: gemini
  developer: gemini
  reviewer: gemini
  freelancer: gemini
  smokealarm: gemini
  firefighter: gemini`

const officeGitignore = `*
!.gitignore
`

var recordBuiltinTools = config.EnsureBuiltinTools

// TemplatesVersionPath is retained for compatibility with existing offices.
// Its marker now covers every editable asset embedded in the binary: messages,
// prompts, and bundled plugins.
const TemplatesVersionPath = ".omo/templates.sha256"

func embeddedAssetsDigest() (string, error) {
	messagesDigest, err := messages.DefaultsDigest()
	if err != nil {
		return "", err
	}
	promptsDigest, err := prompts.DefaultsDigest()
	if err != nil {
		return "", err
	}
	pluginsDigest, err := bundledplugins.DefaultsDigest()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(messagesDigest + "\x00" + promptsDigest + "\x00" + pluginsDigest))
	return fmt.Sprintf("%x", sum[:]), nil
}

func writeEmbeddedAssetsVersion(dir string) error {
	digest, err := embeddedAssetsDigest()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, TemplatesVersionPath), []byte(digest+"\n"), 0o644)
}

// TemplatesOutdated reports whether the editable messages, prompts, and
// bundled plugins were generated by this binary. The historical function name
// and marker path are retained for compatibility. The marker records embedded
// defaults, not edited file contents, so offices may customize their copies.
func TemplatesOutdated(dir string) (bool, error) {
	want, err := embeddedAssetsDigest()
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, TemplatesVersionPath))
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(raw)) != want, nil
}

// PlanTemplateUpdate lists every office-relative file replaced by
// UpdateTemplates without touching the office.
func PlanTemplateUpdate(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(abs, ConfigPath)
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("office is not set up in %s; run 'omo setup' first", abs)
		}
		return nil, err
	}
	replaceTools, err := bundledToolsShouldUpdate(configPath)
	if err != nil {
		return nil, err
	}
	pathSet := make(map[string]bool, len(messages.Names)+len(prompts.Roles)+2)
	if info, err := os.Stat(filepath.Join(abs, prompts.ExtensionsDir)); os.IsNotExist(err) {
		pathSet[filepath.ToSlash(prompts.ExtensionsDir)+"/"] = true
	} else if err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%s exists but is not a directory", filepath.Join(abs, prompts.ExtensionsDir))
	}
	for _, name := range messages.Names {
		pathSet[filepath.ToSlash(filepath.Join(messages.Dir, name+".txt"))] = true
	}
	for _, name := range append([]string{"common"}, prompts.Roles...) {
		pathSet[filepath.ToSlash(filepath.Join(prompts.Dir, name+".md"))] = true
	}
	pluginFiles, err := bundledplugins.DefaultFiles()
	if err != nil {
		return nil, err
	}
	for _, path := range pluginFiles {
		if !replaceTools && (path == bundledplugins.ToolsName || strings.HasPrefix(path, bundledplugins.ToolsName+"/")) {
			continue
		}
		pathSet[filepath.ToSlash(filepath.Join(".omo", "plugins", filepath.FromSlash(path)))] = true
	}
	replacementRoots := []string{messages.Dir, prompts.Dir, filepath.Join(".omo", "plugins", bundledplugins.NudgeName)}
	if replaceTools {
		replacementRoots = append(replacementRoots, filepath.Join(".omo", "plugins", bundledplugins.ToolsName))
	}
	for _, root := range replacementRoots {
		fullRoot := filepath.Join(abs, root)
		if err := filepath.WalkDir(fullRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(abs, path)
			if err != nil {
				return err
			}
			pathSet[filepath.ToSlash(rel)] = true
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if info, err := os.Lstat(filepath.Join(abs, TemplatesVersionPath)); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", filepath.Join(abs, TemplatesVersionPath))
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	pathSet[TemplatesVersionPath] = true
	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// Setup scaffolds a new office in dir: the config, the ignored .omo layout, an
// initialised (empty) database and the editable message and prompt templates.
// The config is the initialization marker: if it already exists, Setup does
// nothing at all. Use UpdateTemplates to deliberately refresh the templates.
func Setup(dir string) ([]string, error) {
	return SetupWithAgentCLI(dir, agentcli.Claude)
}

// SetupWithAgentCLI scaffolds an office whose default profiles target one of
// the officially supported interactive agent CLIs.
func SetupWithAgentCLI(dir string, provider agentcli.Provider) (result []string, resultErr error) {
	if !provider.Valid() {
		return nil, fmt.Errorf("unsupported agent CLI %q", provider)
	}
	home, err := globalhome.Open()
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(abs, ConfigPath)
	if _, err := os.Stat(cfgPath); err == nil {
		if err := config.ValidateSchemaReadOnly(cfgPath); err != nil {
			return nil, err
		}
		created, err := ensureExtensionsDir(abs)
		if err != nil {
			return nil, err
		}
		var ensured []string
		if created {
			ensured = append(ensured, prompts.ExtensionsDir+"/")
		}
		installed, err := ensureBundledPlugins(abs, cfgPath, home)
		if err != nil {
			return nil, err
		}
		for _, name := range installed {
			ensured = append(ensured, ".omo/plugins/"+name+"/")
		}
		return ensured, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	template, err := home.PrepareTemplate(abs)
	if err != nil {
		return nil, fmt.Errorf("prepare global template: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			// The config is the initialization marker, including when supplied
			// by the overlay. Leave it absent after any failure so setup retries
			// all remaining work rather than treating a partial copy as complete.
			if err := os.Remove(cfgPath); err != nil && !os.IsNotExist(err) {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove incomplete setup marker: %w", err))
			}
		}
	}()
	var created []string

	for _, sub := range []string{".omo", prompts.ExtensionsDir, ".omo/logs", ".omo/plugins", ".omo/storage", ".omo/worktrees"} {
		if err := os.MkdirAll(filepath.Join(abs, sub), 0o755); err != nil {
			return nil, err
		}
	}
	ignorePath := filepath.Join(abs, ".omo", ".gitignore")
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(ignorePath, []byte(officeGitignore), 0o644); err != nil {
			return nil, err
		}
		created = append(created, ".omo/.gitignore")
	} else if err != nil {
		return nil, err
	}

	repos, layout := DiscoverRepos(abs)
	includeTools, err := bundledToolsShouldInstall(abs, cfgPath, home)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(cfgPath, []byte(renderConfig(repos, provider, includeTools)), 0o644); err != nil {
		return nil, err
	}
	if override, ok := template.ConfigOverride(); ok {
		if err := applyTemplateConfigOverride(cfgPath, override); err != nil {
			return nil, fmt.Errorf("apply global config template: %w", err)
		}
	}
	created = append(created, fmt.Sprintf("%s (%s, %d repo(s) found)", ConfigPath, layout, len(repos)))

	// Creating the database here means a fresh office is immediately
	// inspectable (and any schema problem surfaces now, not at first spawn).
	dbPath := filepath.Join(abs, ".omo", "omo.db")
	_, dbMissing := os.Stat(dbPath)
	d, err := db.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("initialise database: %w", err)
	}
	d.Close()
	if os.IsNotExist(dbMissing) {
		created = append(created, ".omo/omo.db")
	}

	before := countFiles(filepath.Join(abs, messages.Dir))
	if err := messages.WriteDefaults(abs); err != nil {
		return nil, err
	}
	if n := countFiles(filepath.Join(abs, messages.Dir)) - before; n > 0 {
		created = append(created, fmt.Sprintf("%s/ (%d templates)", messages.Dir, n))
	}
	before = countFiles(filepath.Join(abs, prompts.Dir))
	if err := prompts.WriteDefaults(abs); err != nil {
		return nil, err
	}
	if n := countFiles(filepath.Join(abs, prompts.Dir)) - before; n > 0 {
		created = append(created, fmt.Sprintf("%s/ (%d templates)", prompts.Dir, n))
	}
	installed, err := ensureBundledPlugins(abs, cfgPath, home)
	if err != nil {
		return nil, err
	}
	for _, name := range installed {
		created = append(created, ".omo/plugins/"+name+"/")
	}
	if err := writeEmbeddedAssetsVersion(abs); err != nil {
		return nil, fmt.Errorf("write embedded asset version: %w", err)
	}
	overlaid, err := template.Apply(abs)
	if err != nil {
		return nil, fmt.Errorf("apply global template: %w", err)
	}
	created = append(created, overlaid...)
	complete = true
	return created, nil
}

// SyncTemplateConfig reapplies only the reserved global partial config
// template. Other global template assets and office runtime state stay intact.
func SyncTemplateConfig(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(abs, ConfigPath)
	if _, err := os.Stat(cfgPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("office is not set up in %s; run 'omo setup' first", abs)
		}
		return nil, err
	}
	home, err := globalhome.Open()
	if err != nil {
		return nil, err
	}
	template, err := home.PrepareTemplate(abs)
	if err != nil {
		return nil, fmt.Errorf("prepare global template: %w", err)
	}
	override, ok := template.ConfigOverride()
	if !ok {
		return nil, nil
	}
	if err := applyTemplateConfigOverride(cfgPath, override); err != nil {
		return nil, fmt.Errorf("apply global config template: %w", err)
	}
	return []string{ConfigPath}, nil
}

func applyTemplateConfigOverride(path string, override []byte) error {
	merged, err := mergedTemplateConfig(path, override)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".omo-config-template-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(merged); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := config.Load(tmpPath); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func mergedTemplateConfig(path string, override []byte) ([]byte, error) {
	base, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var current, partial yaml.Node
	if err := yaml.Unmarshal(base, &current); err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(override, &partial); err != nil {
		return nil, fmt.Errorf("parse partial config: %w", err)
	}
	if len(current.Content) == 0 || len(partial.Content) == 0 || current.Content[0].Kind != yaml.MappingNode || partial.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("partial config must be a YAML mapping")
	}
	if containsYAMLAlias(partial.Content[0]) {
		return nil, fmt.Errorf("partial config must not contain YAML aliases")
	}
	if mappingNodeValue(partial.Content[0], "repos") != nil {
		return nil, fmt.Errorf("partial config may not override repos")
	}
	mergeTemplateConfig(current.Content[0], partial.Content[0])
	return yamlformat.EncodePreservingBlankLines(base, current.Content[0], 2)
}

func mappingNodeValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func mergeTemplateConfig(dst, src *yaml.Node) {
	for i := 0; i+1 < len(src.Content); i += 2 {
		key, value := src.Content[i], src.Content[i+1]
		if existing := mappingNodeValue(dst, key.Value); existing != nil && existing.Kind == yaml.MappingNode && value.Kind == yaml.MappingNode {
			mergeTemplateConfig(existing, value)
			continue
		}
		replaced := false
		for j := 0; j+1 < len(dst.Content); j += 2 {
			if dst.Content[j].Value == key.Value {
				dst.Content[j+1] = cloneTemplateYAMLNode(value)
				replaced = true
				break
			}
		}
		if !replaced {
			dst.Content = append(dst.Content, cloneTemplateYAMLNode(key), cloneTemplateYAMLNode(value))
		}
	}
}

func cloneTemplateYAMLNode(node *yaml.Node) *yaml.Node {
	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		clone.Content[i] = cloneTemplateYAMLNode(child)
	}
	return &clone
}

func containsYAMLAlias(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode {
		return true
	}
	for _, child := range node.Content {
		if containsYAMLAlias(child) {
			return true
		}
	}
	return false
}

// UpdateTemplates replaces .omo/messages, .omo/prompts, and bundled plugin
// directories with the defaults embedded in this binary. All replacements are
// staged before the first live directory is moved, and a failed swap restores
// the old folders.
func UpdateTemplates(dir string) ([]string, error) {
	return updateTemplatesWithPreview(dir, nil)
}

// UpdateTemplatesWithPreview emits the authoritative replacement list while
// holding the same lock used for the subsequent template/plugin replacement.
func UpdateTemplatesWithPreview(dir string, preview func(string)) ([]string, error) {
	return updateTemplatesWithPreview(dir, preview)
}

func updateTemplatesWithPreview(dir string, preview func(string)) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(abs, ConfigPath)
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("office is not set up in %s; run 'omo setup' first", abs)
		}
		return nil, err
	}
	replaceTools, err := bundledToolsShouldUpdate(configPath)
	if err != nil {
		return nil, err
	}
	omoDir := filepath.Join(abs, ".omo")
	pluginLock, err := pluginfiles.Lock(context.Background(), filepath.Join(omoDir, "plugins"))
	if err != nil {
		return nil, fmt.Errorf("lock office plugins: %w", err)
	}
	defer pluginLock.Close()
	plan, err := PlanTemplateUpdate(abs)
	if err != nil {
		return nil, err
	}
	if preview != nil {
		for _, path := range plan {
			preview(path)
		}
	}
	return updateTemplatesUnlocked(abs, replaceTools)
}

func updateTemplatesUnlocked(abs string, replaceTools bool) ([]string, error) {
	omoDir := filepath.Join(abs, ".omo")
	if _, err := ensureExtensionsDir(abs); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(omoDir, ".setup-update-")
	if err != nil {
		return nil, fmt.Errorf("stage template update: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := messages.WriteDefaults(stage); err != nil {
		return nil, fmt.Errorf("stage message templates: %w", err)
	}
	if err := prompts.WriteDefaults(stage); err != nil {
		return nil, fmt.Errorf("stage role prompts: %w", err)
	}
	if _, err := bundledplugins.EnsureDefaults(stage); err != nil {
		return nil, fmt.Errorf("stage bundled plugins: %w", err)
	}
	if err := writeEmbeddedAssetsVersion(stage); err != nil {
		return nil, fmt.Errorf("stage embedded asset version: %w", err)
	}

	type replacement struct {
		target, fresh, backup string
		oldMoved              bool
		installed             bool
		parentCreated         bool
	}
	replacements := []replacement{
		{
			target: filepath.Join(abs, messages.Dir),
			fresh:  filepath.Join(stage, messages.Dir),
			backup: filepath.Join(stage, "previous-messages"),
		},
		{
			target: filepath.Join(abs, prompts.Dir),
			fresh:  filepath.Join(stage, prompts.Dir),
			backup: filepath.Join(stage, "previous-prompts"),
		},
		{
			target: filepath.Join(abs, ".omo", "plugins", bundledplugins.NudgeName),
			fresh:  filepath.Join(stage, ".omo", "plugins", bundledplugins.NudgeName),
			backup: filepath.Join(stage, "previous-nudge"),
		},
		{
			target: filepath.Join(abs, TemplatesVersionPath),
			fresh:  filepath.Join(stage, TemplatesVersionPath),
			backup: filepath.Join(stage, "previous-template-version"),
		},
	}
	if replaceTools {
		replacements = append(replacements, replacement{
			target: filepath.Join(abs, ".omo", "plugins", bundledplugins.ToolsName),
			fresh:  filepath.Join(stage, ".omo", "plugins", bundledplugins.ToolsName),
			backup: filepath.Join(stage, "previous-tools"),
		})
	}

	rollback := func(last int) error {
		var errs []error
		for i := last; i >= 0; i-- {
			r := &replacements[i]
			if r.installed {
				if err := os.RemoveAll(r.target); err != nil {
					errs = append(errs, err)
					continue
				}
			}
			if r.oldMoved {
				if err := os.Rename(r.backup, r.target); err != nil {
					errs = append(errs, err)
				}
			}
			if r.parentCreated {
				if err := os.Remove(filepath.Dir(r.target)); err != nil {
					errs = append(errs, err)
				}
			}
		}
		return errors.Join(errs...)
	}

	for i := range replacements {
		r := &replacements[i]
		parent := filepath.Dir(r.target)
		if _, err := os.Stat(parent); os.IsNotExist(err) {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return nil, errors.Join(fmt.Errorf("create parent for %s: %w", r.target, err), rollback(i-1))
			}
			r.parentCreated = true
		} else if err != nil {
			return nil, errors.Join(fmt.Errorf("inspect parent for %s: %w", r.target, err), rollback(i-1))
		}
		if _, err := os.Lstat(r.target); err == nil {
			if err := os.Rename(r.target, r.backup); err != nil {
				return nil, errors.Join(fmt.Errorf("back up %s: %w", r.target, err), rollback(i-1))
			}
			r.oldMoved = true
		} else if !os.IsNotExist(err) {
			return nil, errors.Join(fmt.Errorf("inspect %s: %w", r.target, err), rollback(i-1))
		}
		if err := os.Rename(r.fresh, r.target); err != nil {
			return nil, errors.Join(fmt.Errorf("install %s: %w", r.target, err), rollback(i))
		}
		r.installed = true
	}
	replaced := []string{
		fmt.Sprintf("%s/ (%d templates)", messages.Dir, len(messages.Names)),
		fmt.Sprintf("%s/ (%d prompts)", prompts.Dir, len(prompts.Roles)+1),
		".omo/plugins/nudge/",
	}
	if replaceTools {
		replaced = append(replaced, ".omo/plugins/tools/")
	}
	return replaced, nil
}

func bundledToolsShouldUpdate(configPath string) (bool, error) {
	source, configured, err := configuredPluginSource(configPath, bundledplugins.ToolsName)
	if err != nil {
		return false, err
	}
	return configured && source == "builtin:tools", nil
}

func ensureBundledPlugins(officeDir, configPath string, home *globalhome.Home) ([]string, error) {
	installed := make([]string, 0, 2)
	root := filepath.Join(officeDir, ".omo", "plugins")
	lock, err := pluginfiles.Lock(context.Background(), root)
	if err != nil {
		return nil, fmt.Errorf("lock bundled plugins: %w", err)
	}
	defer lock.Close()
	if created, err := bundledplugins.EnsureNudge(officeDir); err != nil {
		return nil, err
	} else if created {
		installed = append(installed, bundledplugins.NudgeName)
	}
	shouldInstall, err := bundledToolsShouldInstall(officeDir, configPath, home)
	if err != nil {
		return nil, err
	}
	if !shouldInstall {
		return installed, nil
	}
	if created, err := bundledplugins.EnsureTools(officeDir); err != nil {
		return nil, err
	} else if created {
		toolsPath := filepath.Join(root, bundledplugins.ToolsName)
		installedDirectory, err := os.Open(toolsPath)
		if err != nil {
			return nil, fmt.Errorf("inspect newly installed bundled tools: %w", err)
		}
		defer installedDirectory.Close()
		installedInfo, err := installedDirectory.Stat()
		if err != nil {
			return nil, fmt.Errorf("inspect newly installed bundled tools: %w", err)
		}
		if err := recordBuiltinTools(configPath); err != nil {
			rollbackErr := removePluginDirectoryIfUnchanged(toolsPath, installedInfo)
			if rollbackErr != nil {
				return nil, errors.Join(fmt.Errorf("record bundled tools ownership: %w", err), fmt.Errorf("remove unrecorded tools plugin: %w", rollbackErr))
			}
			return nil, fmt.Errorf("record bundled tools ownership: %w", err)
		}
		installed = append(installed, bundledplugins.ToolsName)
	}
	return installed, nil
}

func removePluginDirectoryIfUnchanged(path string, installed os.FileInfo) error {
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(installed, current) {
		return fmt.Errorf("refuse to remove plugin directory replaced during installation")
	}
	return os.RemoveAll(path)
}

func bundledToolsShouldInstall(officeDir, configPath string, home *globalhome.Home) (bool, error) {
	if source, configured, err := configuredPluginSource(configPath, bundledplugins.ToolsName); err != nil {
		return false, err
	} else if configured {
		return source == "builtin:tools", nil
	}
	localRoot := filepath.Join(officeDir, ".omo", "plugins")
	if _, err := os.Stat(filepath.Join(localRoot, bundledplugins.ToolsName)); !os.IsNotExist(err) {
		return false, nil
	}
	if ownsName, err := pluginRootOwnsName(localRoot, bundledplugins.ToolsName); err != nil {
		return false, err
	} else if ownsName {
		return false, nil
	}
	globalOwnsName, err := globalToolsOwnsName(home)
	if err != nil {
		return false, err
	}
	return !globalOwnsName, nil
}

func globalToolsOwnsName(home *globalhome.Home) (bool, error) {
	if home == nil {
		return false, nil
	}
	if _, configured := home.Config.Plugins.Installed[bundledplugins.ToolsName]; configured {
		return true, nil
	}
	return pluginRootOwnsName(filepath.Join(home.Dir, "plugins"), bundledplugins.ToolsName)
}

// pluginRootOwnsName checks loaded plugin identity, which is declared by the
// manifest rather than necessarily matching the installation directory name.
func pluginRootOwnsName(root, name string) (bool, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read plugin root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		manifest, err := plugins.ReadManifest(filepath.Join(root, entry.Name()))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("read plugin manifest %q: %w", entry.Name(), err)
		}
		manifestName := manifest.Name
		if manifestName == "" {
			manifestName = entry.Name()
		}
		if manifestName == name {
			return true, nil
		}
	}
	return false, nil
}

func configuredPluginSource(path, name string) (string, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read plugin configuration: %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return "", false, fmt.Errorf("parse plugin configuration: %w", err)
	}
	if len(document.Content) == 0 {
		return "", false, nil
	}
	installed := yamlMappingValue(yamlMappingValue(document.Content[0], "plugins"), "installed")
	entry := yamlMappingValue(installed, name)
	if entry == nil {
		return "", false, nil
	}
	source := yamlMappingValue(entry, "source")
	if source == nil {
		return "", true, nil
	}
	return source.Value, true, nil
}

func yamlMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func ensureExtensionsDir(abs string) (bool, error) {
	path := filepath.Join(abs, prompts.ExtensionsDir)
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("prompt extensions path %s is not a directory", path)
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false, err
	}
	return true, nil
}

func countFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	return len(entries)
}

// renderConfig fills the repos block of the default config from discovery.
func renderConfig(repos map[string]string, provider agentcli.Provider, includeTools bool) string {
	block := "repos: {}\n  # api: /home/you/workspace/acme/api\n  # ui:  /home/you/workspace/acme/ui"
	if len(repos) > 0 {
		var b strings.Builder
		b.WriteString("repos:")
		for _, k := range SortedKeys(repos) {
			fmt.Fprintf(&b, "\n  %s: %s", k, repos[k])
		}
		block = b.String()
	}
	profiles := map[agentcli.Provider]string{
		agentcli.Claude: claudeProfiles,
		agentcli.Codex:  codexProfiles,
		agentcli.Gemini: geminiProfiles,
	}[provider]
	tools := ""
	if includeTools {
		tools = "    tools:\n      source: builtin:tools\n      enabled: true"
	}
	return fmt.Sprintf(DefaultConfig, block, profiles, tools)
}
