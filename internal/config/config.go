// Package config loads and validates omo.yaml.
package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
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

type Usage struct {
	Enabled            bool    `yaml:"enabled"`
	WeeklyLimitPercent float64 `yaml:"weekly_limit_percent"`
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
	CheckSelfUpdate  bool     `yaml:"check_self_update"`
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
	RepeatInterval Duration `yaml:"repeat_interval"`
	InputDebounce  Duration `yaml:"input_debounce"`
}

// Cleanup bounds durable SQLite history. Zero retention disables that rule.
type Cleanup struct {
	Interval          Duration `yaml:"interval"`
	ReadMessagesAfter Duration `yaml:"read_messages_after"`
	TerminalJobsAfter Duration `yaml:"terminal_jobs_after"`
}

func (c Cleanup) Enabled() bool {
	return c.ReadMessagesAfter > 0 || c.TerminalJobsAfter > 0
}

type Config struct {
	Repos         map[string]string     `yaml:"repos"`
	Models        map[string]Profile    `yaml:"models"`
	Roles         map[string]RoleModels `yaml:"roles"`
	Startup       Startup               `yaml:"startup"`
	Agents        Agents                `yaml:"agents"`
	CEO           CEO                   `yaml:"ceo"`
	Limits        Limits                `yaml:"limits"`
	Usage         Usage                 `yaml:"usage"`
	SmokeAlarm    SmokeAlarm            `yaml:"smokealarm"`
	Logs          Logs                  `yaml:"logs"`
	Reviews       Reviews               `yaml:"reviews"`
	Notifications Notifications         `yaml:"notifications"`
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
		Limits: Limits{MaxDevelopers: 4, MaxFreelancers: 2},
		Usage:  Usage{Enabled: true, WeeklyLimitPercent: 90},
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
			RepeatInterval: Duration(3 * time.Minute),
			InputDebounce:  Duration(30 * time.Second),
		},
		Cleanup:       Cleanup{Interval: Duration(time.Hour)},
		TrustWorkdirs: &trust,
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

# Prevent spawning a metered Claude/Codex profile at or above this weekly use.
usage:
  enabled: true
  weekly_limit_percent: 90

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
  repeat_interval: 3m
  input_debounce: 30s

# SQLite retention. A zero duration disables that cleanup rule.
cleanup:
  interval: 1h
  read_messages_after: 0s
  terminal_jobs_after: 0s

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

func load(path string, writeMissing bool) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Defaults are applied before decoding so explicit zero and false values
	// remain meaningful. In particular logs.keep: 0 removes inactive
	// transcripts, while -1 disables inactive-log pruning entirely.
	c := Defaults()
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
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
		seen := make(map[string]bool, len(configured.Models))
		for _, profile := range configured.Models {
			if _, ok := c.Models[profile]; !ok {
				return fmt.Errorf("roles.%s: unknown profile %q", role, profile)
			}
			if seen[profile] {
				return fmt.Errorf("roles.%s: profile %q is repeated", role, profile)
			}
			seen[profile] = true
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
	if c.Usage.WeeklyLimitPercent <= 0 || c.Usage.WeeklyLimitPercent > 100 {
		return fmt.Errorf("usage.weekly_limit_percent must be greater than 0 and no greater than 100")
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
	if c.Notifications.RepeatInterval < 0 || c.Notifications.InputDebounce < 0 {
		return fmt.Errorf("notifications durations must not be negative")
	}
	if c.Cleanup.ReadMessagesAfter < 0 || c.Cleanup.TerminalJobsAfter < 0 {
		return fmt.Errorf("cleanup retention durations must not be negative")
	}
	if c.Cleanup.Enabled() && c.Cleanup.Interval <= 0 {
		return fmt.Errorf("cleanup.interval must be positive when cleanup is enabled")
	}
	return nil
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
	if !mergeMissing(current.Content[0], defaults.Content[0]) {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(current.Content[0]); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
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
	if _, err := io.Copy(tmp, &out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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
