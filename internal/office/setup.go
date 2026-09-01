package office

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	bundledplugins "github.com/scolastico-dev/one-man-office/plugins"
)

// DefaultConfig is the omo.yaml written by Setup. It is deliberately
// complete rather than minimal: every knob is present and commented, so the
// file doubles as the configuration reference.
const DefaultConfig = `# one-man-office configuration.
# Run 'omo' in this directory to start the office.

# Repositories agents may work in, as <key>: <absolute path>.
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

# Optional release/template checks performed before the office starts.
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

# CEO crash-loop protection.
ceo:
  max_restarts: 3
  restart_window: 30s
  restart_backoff: 500ms

# Concurrency caps.
limits:
  max_developers: 4
  max_freelancers: 2

# Metered Claude/Codex profiles at or above this weekly use cannot spawn.
usage:
  enabled: true                 # false disables usage API calls and limits
  weekly_limit_percent: 90

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

# Repeat unread-mail nudges, but never inject one while the user is actively
# typing into that agent's terminal.
notifications:
  repeat_interval: 3m
  input_debounce: 30s

# Git-backed office plugins managed by omo plugin commands.
plugins:
  update_on_start: true
  installed:
    nudge:
      source: builtin:nudge
      enabled: true

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
  ceo: claude-fable
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

const TemplatesVersionPath = ".omo/templates.sha256"

func templateDigest() (string, error) {
	messagesDigest, err := messages.DefaultsDigest()
	if err != nil {
		return "", err
	}
	promptsDigest, err := prompts.DefaultsDigest()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(messagesDigest + "\x00" + promptsDigest))
	return fmt.Sprintf("%x", sum[:]), nil
}

func writeTemplateVersion(dir string) error {
	digest, err := templateDigest()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, TemplatesVersionPath), []byte(digest+"\n"), 0o644)
}

// TemplatesOutdated reports whether the editable templates were generated by
// this binary. The marker records the embedded defaults, not the edited file
// contents, so offices remain free to customize their copies.
func TemplatesOutdated(dir string) (bool, error) {
	want, err := templateDigest()
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

// Setup scaffolds a new office in dir: the config, the ignored .omo layout, an
// initialised (empty) database and the editable message and prompt templates.
// The config is the initialization marker: if it already exists, Setup does
// nothing at all. Use UpdateTemplates to deliberately refresh the templates.
func Setup(dir string) ([]string, error) {
	return SetupWithAgentCLI(dir, agentcli.Claude)
}

// SetupWithAgentCLI scaffolds an office whose default profiles target one of
// the officially supported interactive agent CLIs.
func SetupWithAgentCLI(dir string, provider agentcli.Provider) ([]string, error) {
	if !provider.Valid() {
		return nil, fmt.Errorf("unsupported agent CLI %q", provider)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(abs, ConfigPath)
	if _, err := os.Stat(cfgPath); err == nil {
		created, err := ensureExtensionsDir(abs)
		if err != nil {
			return nil, err
		}
		var ensured []string
		if created {
			ensured = append(ensured, prompts.ExtensionsDir+"/")
		}
		if installed, err := bundledplugins.EnsureNudge(abs); err != nil {
			return nil, err
		} else if installed {
			ensured = append(ensured, ".omo/plugins/nudge/")
		}
		return ensured, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
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
	if err := os.WriteFile(cfgPath, []byte(renderConfig(repos, provider)), 0o644); err != nil {
		return nil, err
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
	if err := writeTemplateVersion(abs); err != nil {
		return nil, fmt.Errorf("write template version: %w", err)
	}
	if installed, err := bundledplugins.EnsureNudge(abs); err != nil {
		return nil, err
	} else if installed {
		created = append(created, ".omo/plugins/nudge/")
	}
	return created, nil
}

// UpdateTemplates replaces .omo/messages and .omo/prompts with the defaults
// embedded in this binary and restores the bundled nudge plugin only when it
// is missing. Both template replacements are staged before the first live
// directory is moved, and a failed swap restores the old folders.
func UpdateTemplates(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, ConfigPath)); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("office is not set up in %s; run 'omo setup' first", abs)
		}
		return nil, err
	}

	omoDir := filepath.Join(abs, ".omo")
	if _, err := ensureExtensionsDir(abs); err != nil {
		return nil, err
	}
	nudgeInstalled, err := bundledplugins.EnsureNudge(abs)
	if err != nil {
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

	type replacement struct {
		target, fresh, backup string
		oldMoved              bool
		installed             bool
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
		}
		return errors.Join(errs...)
	}

	for i := range replacements {
		r := &replacements[i]
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
	if err := writeTemplateVersion(abs); err != nil {
		return nil, fmt.Errorf("write template version: %w", err)
	}
	replaced := []string{
		fmt.Sprintf("%s/ (%d templates)", messages.Dir, len(messages.Names)),
		fmt.Sprintf("%s/ (%d prompts)", prompts.Dir, len(prompts.Roles)+1),
	}
	if nudgeInstalled {
		replaced = append(replaced, ".omo/plugins/nudge/")
	}
	return replaced, nil
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
func renderConfig(repos map[string]string, provider agentcli.Provider) string {
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
	return fmt.Sprintf(DefaultConfig, block, profiles)
}
