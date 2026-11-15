package api

import (
	"strings"
	"time"
)

// Values of Project.StoryMerge.
const (
	StoryMergeMerge  = "merge"
	StoryMergeSquash = "squash"
)

// Duration is a time span written as Go duration text ("15m", "1h30m") in
// both JSON and YAML.
type Duration time.Duration

// MarshalText writes the duration in its shortest Go duration form.
func (d Duration) MarshalText() ([]byte, error) {
	s := time.Duration(d).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return []byte(s), nil
}

// UnmarshalText parses Go duration text.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(strings.TrimSpace(string(text)))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// General is the general settings payload of GET and PUT /api/settings. Its
// JSON names equal the keys of settings.yaml.
type General struct {
	MaxRunningAgents     int                  `yaml:"max_running_agents" json:"max_running_agents"`
	CEO                  CEOSettings          `yaml:"ceo" json:"ceo"`
	Tasks                TaskSettings         `yaml:"tasks" json:"tasks"`
	ReviewDefaults       ReviewDefaults       `yaml:"review_defaults" json:"review_defaults"`
	WhenApprovedDefaults WhenApprovedDefaults `yaml:"when_approved_defaults" json:"when_approved_defaults"`
	Waiting              WaitingSettings      `yaml:"waiting" json:"waiting"`
	Health               HealthSettings       `yaml:"health" json:"health"`
	Stop                 StopSettings         `yaml:"stop" json:"stop"`
	WorkspaceRoots       []WorkspaceRoot      `yaml:"workspace_roots" json:"workspace_roots"`
	RescanInterval       Duration             `yaml:"rescan_interval" json:"rescan_interval"`
	AgentEnv             map[string]string    `yaml:"agent_env" json:"agent_env"`
}

// CEOSettings switches the CEO agent and its powers.
type CEOSettings struct {
	Enabled           bool `yaml:"enabled" json:"enabled"`
	MayApproveStories bool `yaml:"may_approve_stories" json:"may_approve_stories"`
}

// TaskSettings holds the defaults for tasks.
type TaskSettings struct {
	RequireUserRelease bool `yaml:"require_user_release" json:"require_user_release"`
}

// ReviewDefaults is the review mode used when a ticket sets none.
type ReviewDefaults struct {
	Story Review `yaml:"story" json:"story"`
	Task  Review `yaml:"task" json:"task"`
}

// WhenApprovedDefaults is the approval effect used when a ticket sets none.
type WhenApprovedDefaults struct {
	Story WhenApproved `yaml:"story" json:"story"`
	Task  WhenApproved `yaml:"task" json:"task"`
}

// WaitingSettings controls how waiting agents are handled.
type WaitingSettings struct {
	TimeoutsBeforeInvestigate int `yaml:"timeouts_before_investigate" json:"timeouts_before_investigate"`
}

// HealthSettings holds the thresholds after which an agent needs attention.
type HealthSettings struct {
	SilentAfter             Duration `yaml:"silent_after" json:"silent_after"`
	QuestionUnansweredAfter Duration `yaml:"question_unanswered_after" json:"question_unanswered_after"`
}

// StopSettings controls how agents are stopped.
type StopSettings struct {
	HandoffTimeout Duration `yaml:"handoff_timeout" json:"handoff_timeout"`
}

// WorkspaceRoot is a folder searched for git repositories.
type WorkspaceRoot struct {
	Path     string   `yaml:"path" json:"path"`
	MaxDepth int      `yaml:"max_depth" json:"max_depth"`
	Ignore   []string `yaml:"ignore" json:"ignore"`
}

// SettingsResponse is the answer of GET and PUT /api/settings.
type SettingsResponse struct {
	Settings General     `json:"settings"`
	Errors   []FileError `json:"errors"`
}

// RemovedProjects is the answer of DELETE /api/projects?missing=true.
type RemovedProjects struct {
	Removed []string `json:"removed"`
}
