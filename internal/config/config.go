// Package config loads and validates omo.yaml.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/yamlformat"
	"gopkg.in/yaml.v3"
)

var AllRoles = []string{
	"ceo", "product_manager", "developer", "reviewer",
	"freelancer", "smokealarm", "firefighter",
}

func IsRole(s string) bool {
	for _, r := range AllRoles {
		if r == s {
			return true
		}
	}
	return false
}

// Duration is a time.Duration that unmarshals from YAML strings like "5m".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

type Profile struct {
	Cmd              string            `yaml:"cmd"`
	Args             []string          `yaml:"args"`
	Env              map[string]string `yaml:"env"`
	Selectable       *bool             `yaml:"selectable"`
	Provider         agentcli.Provider `yaml:"provider,omitempty"`
	PromptDelay      *Duration         `yaml:"prompt_delay,omitempty"`
	InjectPrompt     *bool             `yaml:"inject_prompt,omitempty"`
	PromptRetryCount *int              `yaml:"prompt_retry_count,omitempty"`
	PromptRetryWait  *Duration         `yaml:"prompt_retry_wait,omitempty"`
}

func (p Profile) IsSelectable() bool { return p.Selectable == nil || *p.Selectable }

func (p Profile) ShouldInjectPrompt() bool { return p.InjectPrompt == nil || *p.InjectPrompt }

func (p Profile) InitialPromptDelay(fallback time.Duration) time.Duration {
	if p.PromptDelay == nil {
		return fallback
	}
	return time.Duration(*p.PromptDelay)
}

func (p Profile) InitialPromptRetryCount() int {
	if p.PromptRetryCount == nil {
		return 3
	}
	return *p.PromptRetryCount
}

func (p Profile) InitialPromptRetryWait() time.Duration {
	if p.PromptRetryWait == nil {
		return 30 * time.Second
	}
	return time.Duration(*p.PromptRetryWait)
}

type Assignment string

const (
	AssignmentRoundRobin Assignment = "round_robin"
	AssignmentRandom     Assignment = "random"
	AssignmentFailover   Assignment = "failover"
	AssignmentSmart      Assignment = "smart"
)

// RoleModels names the profiles available to a role and how default profile
// assignments are selected. A role may still use the legacy scalar YAML form,
// or a sequence (which defaults to round-robin assignment).
type RoleModels struct {
	Models     []string   `yaml:"models"`
	Assignment Assignment `yaml:"assignment"`
}

func (r *RoleModels) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		var model string
		if err := n.Decode(&model); err != nil {
			return err
		}
		r.Models = []string{model}
		r.Assignment = AssignmentRoundRobin
		return nil
	case yaml.SequenceNode:
		if err := n.Decode(&r.Models); err != nil {
			return err
		}
		r.Assignment = AssignmentRoundRobin
		return nil
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			if key := n.Content[i].Value; key != "models" && key != "assignment" {
				return fmt.Errorf("field %s not found in type config.RoleModels", key)
			}
		}
		type plain RoleModels
		if err := n.Decode((*plain)(r)); err != nil {
			return err
		}
		if r.Assignment == "" {
			r.Assignment = AssignmentRoundRobin
		}
		return nil
	default:
		return fmt.Errorf("role must be a model name, a model list, or a models/assignment mapping")
	}
}

func (r RoleModels) First() string {
	if len(r.Models) == 0 {
		return ""
	}
	return r.Models[0]
}

type Limits struct {
	MaxDevelopers  int `yaml:"max_developers"`
	MaxFreelancers int `yaml:"max_freelancers"`
}

type Branches struct {
	Prefix string `yaml:"prefix"`
	Naming string `yaml:"naming"`
}

type Usage struct {
	Enabled             bool     `yaml:"enabled"`
	WeeklyLimitPercent  float64  `yaml:"weekly_limit_percent"`
	SafeShutdownPercent float64  `yaml:"safe_shutdown_percent"`
	RefreshInterval     Duration `yaml:"refresh_interval"`
	ClaudeConfigDirs    []string `yaml:"claude_config_dirs"`
	CodexHomes          []string `yaml:"codex_homes"`
}

type SmokeAlarm struct {
	Enabled          bool     `yaml:"enabled"`
	RunOnStart       bool     `yaml:"run_on_start"`
	Mode             string   `yaml:"mode"`
	Interval         Duration `yaml:"interval"`
	Timeout          Duration `yaml:"timeout"`
	TailLines        int      `yaml:"tail_lines"`
	HistoryRuns      int      `yaml:"history_runs"`
	IncludeEvents    bool     `yaml:"include_events"`
	IncludePMChatter bool     `yaml:"include_pm_chatter"`
}

type Startup struct {
	CheckSelfUpdate bool `yaml:"check_self_update"`
	// CheckTemplates retains its original configuration name but covers every
	// editable asset embedded in omo, including bundled plugins.
	CheckTemplates   bool     `yaml:"check_templates"`
	CheckSuperpowers bool     `yaml:"check_superpowers,omitempty"` // accepted for compatibility; ignored
	CheckTimeout     Duration `yaml:"check_timeout"`
}

type Agents struct {
	ReadyTimeout     Duration `yaml:"ready_timeout"`
	StartPromptDelay Duration `yaml:"start_prompt_delay"`
	MaxSpawnRetries  int      `yaml:"max_spawn_retries"`
	MaxJobRetries    int      `yaml:"max_job_retries"`
	LowerPriority    bool     `yaml:"lower_priority"`
	NiceIncrement    int      `yaml:"nice_increment"`
}

type CEO struct {
	MaxRestarts    int      `yaml:"max_restarts"`
	RestartWindow  Duration `yaml:"restart_window"`
	RestartBackoff Duration `yaml:"restart_backoff"`
}

// Logs bounds transcripts. MaxSizeKB rotates a live session into additional
// segments. Keep is the number of completed session groups retained; living
// sessions do not count toward it. Zero removes completed logs and -1 disables
// inactive-log pruning.
type Logs struct {
	MaxSizeKB int `yaml:"max_size_kb"`
	Keep      int `yaml:"keep"`
}

type Reviews struct {
	EscalateAfter int `yaml:"escalate_after"`
}

type Notifications struct {
	// DeprecatedRepeatInterval only accepts old configuration files long
	// enough for Load to remove the obsolete core reminder setting.
	DeprecatedRepeatInterval Duration `yaml:"repeat_interval,omitempty"`
	InputDebounce            Duration `yaml:"input_debounce"`
}

type Plugin struct {
	Source  string         `yaml:"source"`
	Subpath string         `yaml:"subpath,omitempty"`
	Branch  string         `yaml:"branch,omitempty"`
	Enabled bool           `yaml:"enabled"`
	Config  map[string]any `yaml:"config,omitempty"`
}

type Plugins struct {
	UpdateOnStart bool              `yaml:"update_on_start"`
	LogLines      int               `yaml:"log_lines"`
	Installed     map[string]Plugin `yaml:"installed"`
}

// Cleanup bounds durable SQLite history and shared storage. Storage age is
// measured in distinct days with office events, not elapsed wall-clock days.
// Zero retention disables that rule.
type Cleanup struct {
	Interval          Duration          `yaml:"interval"`
	ReadMessagesAfter Duration          `yaml:"read_messages_after"`
	TerminalJobsAfter Duration          `yaml:"terminal_jobs_after"`
	StorageActiveDays int               `yaml:"storage_active_days"`
	MaxEntries        CleanupMaxEntries `yaml:"max_entries"`
}

func (c Cleanup) Enabled() bool {
	return c.ReadMessagesAfter > 0 || c.TerminalJobsAfter > 0 || c.StorageActiveDays > 0 || c.MaxEntries.Enabled()
}

// CleanupMaxEntries caps historical rows per table. A zero field disables
// the cap for that table. Live orchestration rows remain protected.
type CleanupMaxEntries struct {
	Agents              int `yaml:"agents"`
	Jobs                int `yaml:"jobs"`
	Messages            int `yaml:"messages"`
	Events              int `yaml:"events"`
	Incidents           int `yaml:"incidents"`
	OverallStatistics   int `yaml:"overall_statistics"`
	ShutdownContexts    int `yaml:"shutdown_contexts"`
	ModelUsageSnapshots int `yaml:"model_usage_snapshots"`
}

func (m CleanupMaxEntries) Enabled() bool {
	return m.Agents > 0 || m.Jobs > 0 || m.Messages > 0 || m.Events > 0 ||
		m.Incidents > 0 || m.OverallStatistics > 0 || m.ShutdownContexts > 0 || m.ModelUsageSnapshots > 0
}

type Config struct {
	Repos         map[string]string     `yaml:"repos"`
	Models        map[string]Profile    `yaml:"models"`
	Roles         map[string]RoleModels `yaml:"roles"`
	Startup       Startup               `yaml:"startup"`
	Agents        Agents                `yaml:"agents"`
	CEO           CEO                   `yaml:"ceo"`
	Limits        Limits                `yaml:"limits"`
	Branches      Branches              `yaml:"branches"`
	Usage         Usage                 `yaml:"usage"`
	SmokeAlarm    SmokeAlarm            `yaml:"smokealarm"`
	Logs          Logs                  `yaml:"logs"`
	Reviews       Reviews               `yaml:"reviews"`
	Notifications Notifications         `yaml:"notifications"`
	Plugins       Plugins               `yaml:"plugins"`
	Cleanup       Cleanup               `yaml:"cleanup"`

	// TrustWorkdirs pre-accepts Claude Code's "do you trust this folder?"
	// dialog for each agent's working directory. Without it a fresh worktree
	// blocks on a prompt no agent can answer. Defaults to true.
	TrustWorkdirs *bool `yaml:"trust_workdirs"`
}

func (c *Config) ShouldTrustWorkdirs() bool {
	return c.TrustWorkdirs == nil || *c.TrustWorkdirs
}

// Defaults returns a complete set of behavioral defaults. Repository, model,
// and role maps are intentionally left empty because Setup discovers or
// supplies those office-specific values.
func Defaults() Config {
	trust := true
	return Config{
		Startup: Startup{
			CheckSelfUpdate:  true,
			CheckTemplates:   true,
			CheckSuperpowers: false,
			CheckTimeout:     Duration(5 * time.Second),
		},
		Agents: Agents{
			ReadyTimeout:     Duration(2 * time.Minute),
			StartPromptDelay: Duration(2 * time.Second),
			MaxSpawnRetries:  2,
			MaxJobRetries:    3,
			LowerPriority:    true,
			NiceIncrement:    10,
		},
		CEO: CEO{
			MaxRestarts:    3,
			RestartWindow:  Duration(30 * time.Second),
			RestartBackoff: Duration(500 * time.Millisecond),
		},
		Limits:   Limits{MaxDevelopers: 4, MaxFreelancers: 2},
		Branches: Branches{Prefix: "omo/job-", Naming: "generated"},
		Usage: Usage{
			Enabled: true, WeeklyLimitPercent: 90, SafeShutdownPercent: 85,
			RefreshInterval: Duration(10 * time.Minute),
		},
		SmokeAlarm: SmokeAlarm{
			Enabled:          true,
			RunOnStart:       false,
			Mode:             "all",
			Interval:         Duration(5 * time.Minute),
			Timeout:          Duration(2 * time.Minute),
			TailLines:        120,
			HistoryRuns:      3,
			IncludeEvents:    true,
			IncludePMChatter: true,
		},
		Logs:    Logs{MaxSizeKB: 2048, Keep: 50},
		Reviews: Reviews{EscalateAfter: 2},
		Notifications: Notifications{
			InputDebounce: Duration(30 * time.Second),
		},
		Plugins: Plugins{UpdateOnStart: true, LogLines: 500, Installed: map[string]Plugin{
			"nudge": {Source: "builtin:nudge", Enabled: true, Config: defaultNudgeConfig()},
		}},
		Cleanup: Cleanup{
			Interval: Duration(time.Hour), StorageActiveDays: 60,
			MaxEntries: CleanupMaxEntries{
				Agents: 10000, Jobs: 10000, Messages: 50000, Events: 100000,
				Incidents: 10000, OverallStatistics: 1000, ShutdownContexts: 1000, ModelUsageSnapshots: 100,
			},
		},
		TrustWorkdirs: &trust,
	}
}

func defaultNudgeConfig() map[string]any {
	return map[string]any{
		"check_interval":           "1m",
		"activity_sample_interval": "30s",
		"reminders": map[string]any{
			"inbox":           map[string]any{"after": "5m", "repeat": "15m"},
			"smokealarm_done": map[string]any{"after": "5m", "repeat": "10m"},
			"park_completed":  map[string]any{"after": "2m", "repeat": "15m"},
			"reviewer_wait":   map[string]any{"after": "5m", "repeat": "15m"},
			"no_job_wait":     map[string]any{"after": "15m", "repeat": "30m"},
			"stale_work":      map[string]any{"after": "15m", "repeat": "30m"},
		},
	}
}

// missingDefaultsYAML is merged into existing files after a successful load.
// Keeping it human-authored gives newly added keys useful comments and keeps
// omo.yaml a self-updating configuration reference.
const missingDefaultsYAML = `
# Checks performed before the office starts. Failures warn and continue.
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
  lower_priority: true
  nice_increment: 10

# CEO crash-loop protection.
ceo:
  max_restarts: 3
  restart_window: 30s
  restart_backoff: 500ms

limits:
  max_developers: 4
  max_freelancers: 2

branches:
  prefix: omo/job-
  naming: generated

# Prevent spawning a metered Claude/Codex profile at or above this weekly use.
usage:
  enabled: true
  weekly_limit_percent: 90
  safe_shutdown_percent: 85
  refresh_interval: 10m
  claude_config_dirs: []
  codex_homes: []

smokealarm:
  enabled: true
  run_on_start: false
  mode: all
  interval: 5m
  timeout: 2m
  tail_lines: 120
  history_runs: 3
  include_events: true
  include_pm_chatter: true

logs:
  max_size_kb: 2048
  keep: 50

reviews:
  escalate_after: 2

notifications:
  input_debounce: 30s

# Git-backed office plugins. Use omo plugin install to manage this map.
plugins:
  update_on_start: true
  log_lines: 500
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
# Retention. Zero disables an individual cleanup rule.
cleanup:
  interval: 1h
  read_messages_after: 0s
  terminal_jobs_after: 0s
  storage_active_days: 60
  max_entries:
    agents: 10000
    jobs: 10000
    messages: 50000
    events: 100000
    incidents: 10000
    overall_statistics: 1000
    shutdown_contexts: 1000
    model_usage_snapshots: 100

trust_workdirs: true
`

func Load(path string) (*Config, error) {
	return load(path, true)
}

// LoadReadOnly applies defaults and validation without rewriting the source
// file. It is used by the observer dashboard, which must not mutate an office.
func LoadReadOnly(path string) (*Config, error) {
	return load(path, false)
}

// ValidateSchemaReadOnly rejects malformed or structurally invalid YAML without
// applying migrations or requiring every runtime setting. Setup uses it before
// touching an existing, potentially partial template-provided configuration.
func ValidateSchemaReadOnly(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = decodeSchema(path, raw)
	return err
}

func load(path string, writeMissing bool) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := decodeSchema(path, raw)
	if err != nil {
		return nil, err
	}
	if err := applyUsageHomes(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	applyBuiltinPluginDefaults(&c, document.Content[0])
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if writeMissing {
		if err := writeBackMissing(path, raw); err != nil {
			return nil, fmt.Errorf("%s: write missing defaults: %w", path, err)
		}
	}
	return &c, nil
}

func decodeSchema(path string, raw []byte) (Config, error) {
	// Defaults are applied before decoding so explicit zero and false values
	// remain meaningful. In particular logs.keep: 0 removes inactive
	// transcripts, while -1 disables inactive-log pruning entirely.
	c := Defaults()
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func applyUsageHomes(c *Config) error {
	type homes struct {
		paths    []string
		variable string
	}
	configured := map[agentcli.Provider]homes{
		agentcli.Claude: {paths: c.Usage.ClaudeConfigDirs, variable: "CLAUDE_CONFIG_DIR"},
		agentcli.Codex:  {paths: c.Usage.CodexHomes, variable: "CODEX_HOME"},
	}
	for provider, item := range configured {
		seen := map[string]bool{}
		for _, path := range item.paths {
			if path == "" || !filepath.IsAbs(path) {
				return fmt.Errorf("usage.%s paths must be absolute, got %q", usageHomesField(provider), path)
			}
			clean := filepath.Clean(path)
			if seen[clean] {
				return fmt.Errorf("usage.%s repeats path %q", usageHomesField(provider), path)
			}
			seen[clean] = true
		}
	}
	for key, profile := range c.Models {
		provider := agentcli.Resolve(profile.Provider, profile.Cmd)
		item, ok := configured[provider]
		if !ok || len(item.paths) == 0 {
			continue
		}
		selected := profile.Env[item.variable]
		if selected == "" {
			if len(item.paths) != 1 {
				return fmt.Errorf("models.%s.env.%s is required when usage.%s contains multiple accounts", key, item.variable, usageHomesField(provider))
			}
			if profile.Env == nil {
				profile.Env = map[string]string{}
			}
			profile.Env[item.variable] = filepath.Clean(item.paths[0])
			if provider == agentcli.Claude {
				profile.Env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = filepath.Clean(item.paths[0])
			}
			c.Models[key] = profile
			continue
		}
		if !filepath.IsAbs(selected) || !containsCleanPath(item.paths, selected) {
			return fmt.Errorf("models.%s.env.%s %q is not listed in usage.%s", key, item.variable, selected, usageHomesField(provider))
		}
		profile.Env[item.variable] = filepath.Clean(selected)
		if provider == agentcli.Claude {
			secure, exists := profile.Env["CLAUDE_SECURESTORAGE_CONFIG_DIR"]
			if exists && filepath.Clean(secure) != filepath.Clean(selected) {
				return fmt.Errorf("models.%s.env.CLAUDE_SECURESTORAGE_CONFIG_DIR must match the selected usage.claude_config_dirs account", key)
			}
			profile.Env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = filepath.Clean(selected)
		}
		c.Models[key] = profile
	}
	return nil
}

func usageHomesField(provider agentcli.Provider) string {
	if provider == agentcli.Claude {
		return "claude_config_dirs"
	}
	return "codex_homes"
}

func containsCleanPath(paths []string, target string) bool {
	target = filepath.Clean(target)
	for _, path := range paths {
		if filepath.Clean(path) == target {
			return true
		}
	}
	return false
}

func applyBuiltinPluginDefaults(c *Config, root *yaml.Node) {
	nudge, ok := c.Plugins.Installed["nudge"]
	if !ok || nudge.Source != "builtin:nudge" {
		return
	}
	entry := mappingValue(mappingValue(mappingValue(root, "plugins"), "installed"), "nudge")
	if value := mappingValue(entry, "config"); value != nil && value.Tag == "!!null" {
		return
	}
	nudge.Config = mergeConfigDefaults(nudge.Config, defaultNudgeConfig())
	c.Plugins.Installed["nudge"] = nudge
}

func mergeConfigDefaults(current, defaults map[string]any) map[string]any {
	if current == nil {
		current = map[string]any{}
	}
	for key, fallback := range defaults {
		value, exists := current[key]
		if !exists {
			current[key] = fallback
			continue
		}
		currentMap, currentOK := value.(map[string]any)
		fallbackMap, fallbackOK := fallback.(map[string]any)
		if currentOK && fallbackOK {
			current[key] = mergeConfigDefaults(currentMap, fallbackMap)
		}
	}
	return current
}

func (c *Config) validate() error {
	if len(c.Models) == 0 {
		return fmt.Errorf("models: at least one profile required")
	}
	for key, p := range c.Models {
		if p.Cmd == "" {
			return fmt.Errorf("models.%s: cmd required", key)
		}
		if p.Provider != "" && !p.Provider.Valid() {
			return fmt.Errorf("models.%s: provider must be claude, codex, or gemini, got %q", key, p.Provider)
		}
		if p.PromptDelay != nil && *p.PromptDelay < 0 {
			return fmt.Errorf("models.%s.prompt_delay must not be negative", key)
		}
		if p.InitialPromptRetryCount() < 0 {
			return fmt.Errorf("models.%s.prompt_retry_count must not be negative", key)
		}
		if p.PromptRetryWait != nil && *p.PromptRetryWait < 0 {
			return fmt.Errorf("models.%s.prompt_retry_wait must not be negative", key)
		}
		if p.InitialPromptRetryCount() > 0 && p.InitialPromptRetryWait() <= 0 {
			return fmt.Errorf("models.%s.prompt_retry_wait must be positive when prompt retries are enabled", key)
		}
	}
	for role, configured := range c.Roles {
		if !IsRole(role) {
			return fmt.Errorf("roles.%s: unknown role", role)
		}
		if len(configured.Models) == 0 {
			return fmt.Errorf("roles.%s: at least one profile required", role)
		}
		switch configured.Assignment {
		case AssignmentRoundRobin, AssignmentRandom, AssignmentFailover, AssignmentSmart:
		default:
			return fmt.Errorf("roles.%s: assignment must be round_robin, random, failover, or smart, got %q", role, configured.Assignment)
		}
		for _, profile := range configured.Models {
			if _, ok := c.Models[profile]; !ok {
				return fmt.Errorf("roles.%s: unknown profile %q", role, profile)
			}
			if configured.Assignment == AssignmentSmart {
				p := c.Models[profile]
				provider := agentcli.Resolve(p.Provider, p.Cmd)
				if provider != agentcli.Claude && provider != agentcli.Codex {
					return fmt.Errorf("roles.%s: smart assignment profile %q must use claude or codex", role, profile)
				}
			}
		}
	}
	for _, role := range AllRoles {
		if _, ok := c.Roles[role]; !ok {
			return fmt.Errorf("roles: missing entry for %q", role)
		}
	}
	for name, p := range c.Repos {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("repos.%s: path must be absolute, got %q", name, p)
		}
	}
	if c.Startup.CheckTimeout < 0 {
		return fmt.Errorf("startup.check_timeout must not be negative")
	}
	if c.Agents.ReadyTimeout <= 0 || c.Agents.StartPromptDelay < 0 || c.Agents.MaxSpawnRetries < 1 || c.Agents.MaxJobRetries < 1 {
		return fmt.Errorf("agents: ready_timeout must be positive, start_prompt_delay must not be negative, and retry counts must be at least 1")
	}
	if c.Agents.NiceIncrement < 1 || c.Agents.NiceIncrement > 19 {
		return fmt.Errorf("agents.nice_increment must be between 1 and 19")
	}
	if c.CEO.MaxRestarts < 1 || c.CEO.RestartWindow <= 0 || c.CEO.RestartBackoff < 0 {
		return fmt.Errorf("ceo: max_restarts and restart_window must be positive; restart_backoff must not be negative")
	}
	if c.Limits.MaxDevelopers < 1 || c.Limits.MaxFreelancers < 1 {
		return fmt.Errorf("limits must be at least 1")
	}
	if !validBranchName(c.Branches.Prefix + "1") {
		return fmt.Errorf("branches.prefix %q does not produce a valid Git branch name", c.Branches.Prefix)
	}
	if c.Branches.Naming != "generated" && c.Branches.Naming != "ai" {
		return fmt.Errorf("branches.naming must be generated or ai, got %q", c.Branches.Naming)
	}
	if c.Usage.WeeklyLimitPercent <= 0 || c.Usage.WeeklyLimitPercent > 100 {
		return fmt.Errorf("usage.weekly_limit_percent must be greater than 0 and no greater than 100")
	}
	if c.Usage.SafeShutdownPercent <= 0 || c.Usage.SafeShutdownPercent >= c.Usage.WeeklyLimitPercent {
		return fmt.Errorf("usage.safe_shutdown_percent must be greater than 0 and lower than weekly_limit_percent")
	}
	if c.Usage.RefreshInterval < 0 {
		return fmt.Errorf("usage.refresh_interval must not be negative")
	}
	if c.SmokeAlarm.Mode != "all" && c.SmokeAlarm.Mode != "per_agent" {
		return fmt.Errorf("smokealarm.mode must be all or per_agent, got %q", c.SmokeAlarm.Mode)
	}
	if c.SmokeAlarm.Interval <= 0 || c.SmokeAlarm.Timeout <= 0 || c.SmokeAlarm.TailLines < 1 || c.SmokeAlarm.HistoryRuns < 0 {
		return fmt.Errorf("smokealarm: interval, timeout, and tail_lines must be positive; history_runs must not be negative")
	}
	if c.Logs.MaxSizeKB < 0 || c.Logs.Keep < -1 {
		return fmt.Errorf("logs: max_size_kb must not be negative and keep must be -1 or greater")
	}
	if c.Reviews.EscalateAfter < 1 {
		return fmt.Errorf("reviews.escalate_after must be at least 1")
	}
	if c.Notifications.InputDebounce < 0 {
		return fmt.Errorf("notifications.input_debounce must not be negative")
	}
	if c.Plugins.LogLines < 1 {
		return fmt.Errorf("plugins.log_lines must be positive")
	}
	for name, plugin := range c.Plugins.Installed {
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
			return fmt.Errorf("plugins.installed: invalid plugin name %q", name)
		}
		if plugin.Source == "" {
			return fmt.Errorf("plugins.installed.%s.source is required", name)
		}
		if plugin.Subpath != "" {
			clean := filepath.Clean(plugin.Subpath)
			if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
				return fmt.Errorf("plugins.installed.%s.subpath must stay inside the repository", name)
			}
		}
		if plugin.Branch != "" && (plugin.Branch == "HEAD" || strings.HasPrefix(plugin.Branch, "refs/") || !validBranchName(plugin.Branch)) {
			return fmt.Errorf("plugins.installed.%s.branch is not a valid Git branch name: %q", name, plugin.Branch)
		}
	}
	if c.Cleanup.ReadMessagesAfter < 0 || c.Cleanup.TerminalJobsAfter < 0 || c.Cleanup.StorageActiveDays < 0 {
		return fmt.Errorf("cleanup retention values must not be negative")
	}
	maxEntries := c.Cleanup.MaxEntries
	if maxEntries.Agents < 0 || maxEntries.Jobs < 0 || maxEntries.Messages < 0 || maxEntries.Events < 0 ||
		maxEntries.Incidents < 0 || maxEntries.OverallStatistics < 0 || maxEntries.ShutdownContexts < 0 || maxEntries.ModelUsageSnapshots < 0 {
		return fmt.Errorf("cleanup.max_entries values must not be negative")
	}
	if maxEntries.Events > 0 && maxEntries.Events < c.Cleanup.StorageActiveDays {
		return fmt.Errorf("cleanup.max_entries.events must be 0 or at least cleanup.storage_active_days")
	}
	if c.Cleanup.Enabled() && c.Cleanup.Interval <= 0 {
		return fmt.Errorf("cleanup.interval must be positive when cleanup is enabled")
	}
	return nil
}

func validBranchName(name string) bool {
	if name == "" || name == "@" || strings.TrimSpace(name) != name || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") {
		return false
	}
	if strings.Contains(name, "..") || strings.Contains(name, "//") || strings.Contains(name, "@{") || strings.ContainsAny(name, " ~^:?*[\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func writeBackMissing(path string, raw []byte) error {
	var current, defaults yaml.Node
	if err := yaml.Unmarshal(raw, &current); err != nil {
		return err
	}
	if err := yaml.Unmarshal([]byte(missingDefaultsYAML), &defaults); err != nil {
		return err
	}
	if len(current.Content) == 0 || len(defaults.Content) == 0 {
		return nil
	}
	root := current.Content[0]
	// Plugin values are arbitrary user data, so they must not go through the
	// core migration that replaces a scalar when a new mapping is expected.
	defaultEntry := mappingValue(mappingValue(mappingValue(defaults.Content[0], "plugins"), "installed"), "nudge")
	currentEntry := mappingValue(mappingValue(mappingValue(root, "plugins"), "installed"), "nudge")
	changed := false
	if currentEntry != nil {
		source := mappingValue(currentEntry, "source")
		if source == nil || source.Value != "builtin:nudge" {
			removeMappingKey(defaultEntry, "config")
		} else if value := mappingValue(currentEntry, "config"); value != nil {
			changed = MergeMissingPluginDefaultsIn(root, value, mappingValue(defaultEntry, "config"))
			removeMappingKey(defaultEntry, "config")
		}
	}
	changed = mergeMissing(root, defaults.Content[0]) || changed
	if notifications := mappingValue(root, "notifications"); notifications != nil {
		changed = removeMappingKey(notifications, "repeat_interval") || changed
	}
	if !changed {
		return nil
	}
	return writeConfigNode(path, raw, current.Content[0])
}

// EnsureBuiltinTools records that this office owns the bundled tools plugin.
// Callers must first install it only after ruling out local and global owners.
func EnsureBuiltinTools(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var current, tools yaml.Node
	if err := yaml.Unmarshal(raw, &current); err != nil {
		return err
	}
	if err := yaml.Unmarshal([]byte("plugins:\n  installed:\n    tools:\n      source: builtin:tools\n      enabled: true\n"), &tools); err != nil {
		return err
	}
	if len(current.Content) == 0 || len(tools.Content) == 0 {
		return nil
	}
	root := current.Content[0]
	installed := mappingValue(mappingValue(root, "plugins"), "installed")
	if mappingValue(installed, "tools") != nil {
		return nil
	}
	if !mergeMissing(root, tools.Content[0]) {
		return nil
	}
	return writeConfigNode(path, raw, root)
}

func writeConfigNode(path string, raw []byte, root *yaml.Node) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	out, err := yamlformat.EncodePreservingBlankLines(raw, root, 2)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".omo-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
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

func removeMappingKey(mapping *yaml.Node, key string) bool {
	if mapping.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
	}
	return false
}

func mergeMissing(dst, src *yaml.Node) bool {
	if src.Kind != yaml.MappingNode {
		return false
	}
	if dst.Kind != yaml.MappingNode {
		*dst = *cloneNode(src)
		return true
	}
	changed := false
	for i := 0; i < len(src.Content); i += 2 {
		srcKey, srcValue := src.Content[i], src.Content[i+1]
		var dstValue *yaml.Node
		for j := 0; j < len(dst.Content); j += 2 {
			if dst.Content[j].Value == srcKey.Value {
				dstValue = dst.Content[j+1]
				break
			}
		}
		if dstValue == nil {
			dst.Content = append(dst.Content, cloneNode(srcKey), cloneNode(srcValue))
			changed = true
			continue
		}
		if mergeMissing(dstValue, srcValue) {
			changed = true
		}
	}
	return changed
}

func cloneNode(n *yaml.Node) *yaml.Node {
	out := *n
	out.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		out.Content[i] = cloneNode(child)
	}
	return &out
}

// MergeMissingPluginDefaultsIn adds object keys recursively without replacing
// any existing value, including null, false, zero, arrays, or type conflicts.
// Unlike core schema migration, plugin configuration is owned by the user.
// The document root lets it materialize other aliases before extending
// an anchored mapping, so defaults for one plugin cannot affect another alias
// consumer of the same user-owned anchor.
func MergeMissingPluginDefaultsIn(root, dst, src *yaml.Node) bool {
	if dst.Kind == yaml.AliasNode && src.Kind == yaml.MappingNode {
		// Keep shared anchors user-owned. Extend this reference locally using
		// a merge key instead of mutating the anchor's other consumers.
		alias := *dst
		alias.HeadComment = ""
		alias.LineComment = ""
		alias.FootComment = ""
		extended := &yaml.Node{
			Kind:        yaml.MappingNode,
			Tag:         "!!map",
			HeadComment: dst.HeadComment,
			LineComment: dst.LineComment,
			FootComment: dst.FootComment,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!merge", Value: "<<"}, &alias,
			},
		}
		if !MergeMissingPluginDefaultsIn(root, extended, src) {
			return false
		}
		*dst = *extended
		return true
	}
	if dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		return false
	}
	if dst.Anchor != "" && root != dst {
		trial := cloneNode(dst)
		trial.Anchor = ""
		if !MergeMissingPluginDefaultsIn(trial, trial, src) {
			return false
		}
		materializeAliases(root, dst)
	}
	// Decode resolves YAML merge keys and aliases, so an inherited value
	// counts as present even if it has no direct entry in this mapping.
	var effective map[string]yaml.Node
	if err := dst.Decode(&effective); err != nil {
		return false
	}
	changed := false
	for i := 0; i+1 < len(src.Content); i += 2 {
		key, fallback := src.Content[i], src.Content[i+1]
		current := mappingValue(dst, key.Value)
		if current == nil {
			if inherited, exists := effective[key.Value]; exists {
				local := cloneNode(&inherited)
				local.Anchor = ""
				if MergeMissingPluginDefaultsIn(root, local, fallback) {
					dst.Content = append(dst.Content, cloneNode(key), local)
					changed = true
				}
				continue
			}
			dst.Content = append(dst.Content, cloneNode(key), cloneNode(fallback))
			changed = true
		} else {
			changed = MergeMissingPluginDefaultsIn(root, current, fallback) || changed
		}
	}
	return changed
}

func materializeAliases(node, target *yaml.Node) {
	if node == nil {
		return
	}
	for _, child := range node.Content {
		if child.Kind == yaml.AliasNode && child.Alias == target {
			head, line, foot := child.HeadComment, child.LineComment, child.FootComment
			*child = *cloneNode(target)
			child.Anchor = ""
			child.HeadComment = head
			child.LineComment = line
			child.FootComment = foot
			continue
		}
		materializeAliases(child, target)
	}
}

// ProfileForJob validates an explicit job override. With no override it
// returns the role's first configured profile; runtime assignment rules are
// applied by the supervisor when the job is dispatched.
func (c *Config) ProfileForJob(role, override string) (string, Profile, error) {
	if override == "" {
		key := c.Roles[role].First()
		return key, c.Models[key], nil
	}
	p, ok := c.Models[override]
	if !ok {
		return "", Profile{}, fmt.Errorf("unknown model profile %q", override)
	}
	if !p.IsSelectable() {
		return "", Profile{}, fmt.Errorf("model profile %q is not selectable", override)
	}
	return override, p, nil
}
