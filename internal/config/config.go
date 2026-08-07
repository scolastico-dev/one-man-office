// Package config loads and validates omo.yaml.
package config

import (
	"bytes"
	"fmt"
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
	Interval  Duration `yaml:"interval"`
	TailLines int      `yaml:"tail_lines"`
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

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Defaults are applied before decoding so explicit zero values remain
	// meaningful. In particular logs.keep: 0 removes inactive transcripts,
	// while -1 disables inactive-log pruning entirely.
	c := Config{
		Logs:    Logs{MaxSizeKB: 2048, Keep: 50},
		Reviews: Reviews{EscalateAfter: 2},
		Notifications: Notifications{
			RepeatInterval: Duration(3 * time.Minute),
			InputDebounce:  Duration(30 * time.Second),
		},
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
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
	if c.Limits.MaxDevelopers == 0 {
		c.Limits.MaxDevelopers = 4
	}
	if c.Limits.MaxFreelancers == 0 {
		c.Limits.MaxFreelancers = 2
	}
	if c.SmokeAlarm.Interval == 0 {
		c.SmokeAlarm.Interval = Duration(5 * time.Minute)
	}
	if c.SmokeAlarm.TailLines == 0 {
		c.SmokeAlarm.TailLines = 120
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
