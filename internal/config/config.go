// Package config loads and validates omo.yaml.
package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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
	Cmd        string            `yaml:"cmd"`
	Args       []string          `yaml:"args"`
	Env        map[string]string `yaml:"env"`
	Selectable *bool             `yaml:"selectable"`
}

func (p Profile) IsSelectable() bool { return p.Selectable == nil || *p.Selectable }

type Limits struct {
	MaxDevelopers  int `yaml:"max_developers"`
	MaxFreelancers int `yaml:"max_freelancers"`
}

type SmokeAlarm struct {
	Enabled          bool     `yaml:"enabled"`
	RunOnStart       bool     `yaml:"run_on_start"`
	Mode             string   `yaml:"mode"`
	Interval         Duration `yaml:"interval"`
	TailLines        int      `yaml:"tail_lines"`
	HistoryRuns      int      `yaml:"history_runs"`
	IncludeEvents    bool     `yaml:"include_events"`
	IncludePMChatter bool     `yaml:"include_pm_chatter"`
}

type Startup struct {
	CheckSelfUpdate bool     `yaml:"check_self_update"`
	CheckTemplates  bool     `yaml:"check_templates"`
	CheckTimeout    Duration `yaml:"check_timeout"`
}

type Agents struct {
	ReadyTimeout     Duration `yaml:"ready_timeout"`
	StartPromptDelay Duration `yaml:"start_prompt_delay"`
	MaxSpawnRetries  int      `yaml:"max_spawn_retries"`
	MaxJobRetries    int      `yaml:"max_job_retries"`
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

type Config struct {
	Repos         map[string]string  `yaml:"repos"`
	Models        map[string]Profile `yaml:"models"`
	Roles         map[string]string  `yaml:"roles"`
	Startup       Startup            `yaml:"startup"`
	Agents        Agents             `yaml:"agents"`
	CEO           CEO                `yaml:"ceo"`
	Limits        Limits             `yaml:"limits"`
	SmokeAlarm    SmokeAlarm         `yaml:"smokealarm"`
	Logs          Logs               `yaml:"logs"`
	Reviews       Reviews            `yaml:"reviews"`
	Notifications Notifications      `yaml:"notifications"`

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
			CheckSelfUpdate: true,
			CheckTemplates:  true,
			CheckTimeout:    Duration(5 * time.Second),
		},
		Agents: Agents{
			ReadyTimeout:     Duration(2 * time.Minute),
			StartPromptDelay: Duration(2 * time.Second),
			MaxSpawnRetries:  2,
			MaxJobRetries:    3,
		},
		CEO: CEO{
			MaxRestarts:    3,
			RestartWindow:  Duration(30 * time.Second),
			RestartBackoff: Duration(500 * time.Millisecond),
		},
		Limits: Limits{MaxDevelopers: 4, MaxFreelancers: 2},
		SmokeAlarm: SmokeAlarm{
			Enabled:          true,
			RunOnStart:       false,
			Mode:             "all",
			Interval:         Duration(5 * time.Minute),
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

# CEO crash-loop protection.
ceo:
  max_restarts: 3
  restart_window: 30s
  restart_backoff: 500ms

limits:
  max_developers: 4
  max_freelancers: 2

smokealarm:
  enabled: true
  run_on_start: false
  mode: all
  interval: 5m
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

trust_workdirs: true
`

func Load(path string) (*Config, error) {
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
	if err := writeBackMissing(path, raw); err != nil {
		return nil, fmt.Errorf("%s: write missing defaults: %w", path, err)
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
	}
	for role, profile := range c.Roles {
		if !IsRole(role) {
			return fmt.Errorf("roles.%s: unknown role", role)
		}
		if _, ok := c.Models[profile]; !ok {
			return fmt.Errorf("roles.%s: unknown profile %q", role, profile)
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
	if c.CEO.MaxRestarts < 1 || c.CEO.RestartWindow <= 0 || c.CEO.RestartBackoff < 0 {
		return fmt.Errorf("ceo: max_restarts and restart_window must be positive; restart_backoff must not be negative")
	}
	if c.Limits.MaxDevelopers < 1 || c.Limits.MaxFreelancers < 1 {
		return fmt.Errorf("limits must be at least 1")
	}
	if c.SmokeAlarm.Mode != "all" && c.SmokeAlarm.Mode != "per_agent" {
		return fmt.Errorf("smokealarm.mode must be all or per_agent, got %q", c.SmokeAlarm.Mode)
	}
	if c.SmokeAlarm.Interval <= 0 || c.SmokeAlarm.TailLines < 1 || c.SmokeAlarm.HistoryRuns < 0 {
		return fmt.Errorf("smokealarm: interval and tail_lines must be positive; history_runs must not be negative")
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
	if dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		return false
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

// ProfileForJob resolves the runner profile for a job: empty override means
// the role default; an explicit override must exist and be selectable.
func (c *Config) ProfileForJob(role, override string) (string, Profile, error) {
	if override == "" {
		key := c.Roles[role]
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
