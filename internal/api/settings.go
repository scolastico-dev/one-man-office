package api

import (
	"strings"
	"time"
)

// Merge modes: how omo merges a ticket's branch into its target branch.
const (
	// MergeModeMerge makes a merge commit, "Merge: <title>", with the
	// target and the ticket's branch as parents.
	MergeModeMerge = "merge"
	// MergeModeSquash makes one commit on top of the target with the
	// ticket's changes; the ticket's branch is not a parent. Its message is
	// "<title>", for a story followed by the list of its completed tasks
	// by title. No message holds a ticket number.
	MergeModeSquash = "squash"
)

// MergeModes holds a merge mode for each kind of merge omo does: Story for
// a story's integration branch into its base branch, Task for the branch of
// a task without a story into its base branch, StoryTask for the branch of
// a task inside a story into the story's integration branch. In the general
// settings every value is set. In a project an empty value means "use the
// general default"; Project.EffectiveMerge holds the values in effect.
type MergeModes struct {
	Story     string `yaml:"story,omitempty" json:"story"`
	Task      string `yaml:"task,omitempty" json:"task"`
	StoryTask string `yaml:"story_task,omitempty" json:"story_task"`
}

// MergeModesUpdate is UpdateProject.Merge: each key given sets the
// project's own mode for that kind of merge, "" going back to the general
// default; keys left out stay unchanged.
type MergeModesUpdate struct {
	Story     Optional[string] `json:"story,omitzero"`
	Task      Optional[string] `json:"task,omitzero"`
	StoryTask Optional[string] `json:"story_task,omitzero"`
}

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
	Limits               LimitSettings        `yaml:"limits" json:"limits"`
	CEO                  CEOSettings          `yaml:"ceo" json:"ceo"`
	Tasks                TaskSettings         `yaml:"tasks" json:"tasks"`
	ReviewDefaults       ReviewDefaults       `yaml:"review_defaults" json:"review_defaults"`
	WhenApprovedDefaults WhenApprovedDefaults `yaml:"when_approved_defaults" json:"when_approved_defaults"`
	MergeDefaults        MergeModes           `yaml:"merge_defaults" json:"merge_defaults"`
	Waiting              WaitingSettings      `yaml:"waiting" json:"waiting"`
	Health               HealthSettings       `yaml:"health" json:"health"`
	Images               ImageSettings        `yaml:"images" json:"images"`
	Stop                 StopSettings         `yaml:"stop" json:"stop"`
	WorkspaceRoots       []WorkspaceRoot      `yaml:"workspace_roots" json:"workspace_roots"`
	RescanInterval       Duration             `yaml:"rescan_interval" json:"rescan_interval"`
	AgentEnv             map[string]string    `yaml:"agent_env" json:"agent_env"`
	Usage                UsageSettings        `yaml:"usage" json:"usage"`
	UI                   UISettings           `yaml:"ui" json:"ui"`
	Branches             BranchSettings       `yaml:"branches" json:"branches"`
}

// BranchSettings holds how omo names the branches it creates for tickets.
// A ticket that already has a branch keeps it whatever these say.
type BranchSettings struct {
	// Template is the name of a new branch, with the placeholders {slug},
	// {number}, {type}, {project}, {host} and {user} (see package
	// branchname); "omo/{slug}" by default. A name that is taken gets -2,
	// -3, ... appended.
	Template string `yaml:"template" json:"template"`
}

// UISettings holds how the installation's web app behaves. They belong to
// the installation, not to one browser.
type UISettings struct {
	// OpenTicketsAsPage makes a click on a ticket open its own page instead
	// of the panel over the board.
	OpenTicketsAsPage bool `yaml:"open_tickets_as_page" json:"open_tickets_as_page"`
	// ScreenReaderMode gives screen reader software a text copy of the
	// output of every terminal in the web app; nothing changes visibly.
	ScreenReaderMode bool `yaml:"screen_reader_mode" json:"screen_reader_mode"`
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

// ReviewDefaults is the review mode used when a ticket sets none: Story for
// stories, Task for tasks without a story, StoryTask for tasks inside a
// story.
type ReviewDefaults struct {
	Story     Review `yaml:"story" json:"story"`
	Task      Review `yaml:"task" json:"task"`
	StoryTask Review `yaml:"story_task" json:"story_task"`
}

// WhenApprovedDefaults is the approval effect used when a ticket sets none,
// with the same keys as ReviewDefaults. A task inside a story is always
// merged into its story, so StoryTask is always merge.
type WhenApprovedDefaults struct {
	Story     WhenApproved `yaml:"story" json:"story"`
	Task      WhenApproved `yaml:"task" json:"task"`
	StoryTask WhenApproved `yaml:"story_task" json:"story_task"`
}

// LimitSettings holds how many tickets of each kind may be in work at once.
// A ticket is in work from the moment it moves from Ready For Dev to In
// Progress until it is done, so also while it is in review, waits for an
// answer or has no running agent. The limits govern what omo starts by
// itself; the CEO never counts.
type LimitSettings struct {
	// Tasks is how many tasks may be in work at once, whether or not they
	// belong to a story.
	Tasks int `yaml:"tasks" json:"tasks"`
	// Stories is how many stories may be in work at once.
	Stories int `yaml:"stories" json:"stories"`
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

// NoRevision is the revision of a settings item that has no file.
const NoRevision = "0"

// FieldDetail is the Error.Details of validation and invalid_settings errors
// of settings writes: it names the field the message belongs to.
type FieldDetail struct {
	// Field is a dotted key such as "health.silent_after",
	// "workspace_roots.1.ignore.0", "models.sonnet.env.FOO" or "text".
	Field string `json:"field"`
}

// SettingsResponse is the answer of GET, PUT and PATCH /api/settings. Revision
// identifies the content of settings.yaml for If-Match; Home is the omo home
// folder.
type SettingsResponse struct {
	Settings General     `json:"settings"`
	Errors   []FileError `json:"errors"`
	Revision string      `json:"revision"`
	Home     string      `json:"home"`
}

// RemovedProjects is the answer of DELETE /api/projects?missing=true.
type RemovedProjects struct {
	Removed []string `json:"removed"`
}

// Values of ProjectBatch.Action.
const (
	ProjectBatchRemove  = "remove"
	ProjectBatchDisable = "disable"
	ProjectBatchEnable  = "enable"
)

// ProjectBatch is the body of POST /api/projects/batch: one action for
// several projects, each handled as DELETE /api/projects/{id} (remove) or
// PATCH /api/projects/{id} with enabled false or true would handle it.
type ProjectBatch struct {
	IDs    []string `json:"ids"`
	Action string   `json:"action"`
}

// ProjectBatchResult is the answer of POST /api/projects/batch: one item
// per id, in the order of the request.
type ProjectBatchResult struct {
	Results []ProjectBatchItem `json:"results"`
}

// ProjectBatchItem says what happened to one project of a batch: Done, or
// the refusal the single route would have answered (for example in_use
// for a project open tickets use, validation for one a workspace root
// would find again, not_found for an unknown id).
type ProjectBatchItem struct {
	ID    string `json:"id"`
	Done  bool   `json:"done"`
	Error *Error `json:"error,omitempty"`
}

// Values of Profile.CLI.
const (
	CLIClaude = "claude"
	CLICodex  = "codex"
	CLIGemini = "gemini"
	CLIAgy    = "agy" // Antigravity CLI
	CLICustom = "custom"
)

// Values of RoleSettings.Assignment.
const (
	AssignmentRoundRobin = "round_robin"
	AssignmentRandom     = "random"
	AssignmentFailover   = "failover"
	AssignmentSmart      = "smart"
)

// Profile is a CLI with its account: the payload of PUT /api/profiles/{name}
// and an item of GET /api/profiles. Its JSON names equal the keys of
// profiles/<name>.yaml; the name comes from the file name.
type Profile struct {
	Name           string                  `yaml:"-" json:"name"`
	CLI            string                  `yaml:"cli" json:"cli"`
	Command        string                  `yaml:"command" json:"command"`
	Args           []string                `yaml:"args" json:"args"`
	Env            map[string]string       `yaml:"env" json:"env"`
	AccountDir     string                  `yaml:"account_dir" json:"account_dir"`
	UsageScript    string                  `yaml:"usage_script" json:"usage_script"`
	PromptDelivery PromptDelivery          `yaml:"prompt_delivery,omitempty" json:"prompt_delivery"`
	Models         map[string]ProfileModel `yaml:"models" json:"models"`
	// Revision identifies the content of the profile's file for If-Match. It is
	// ignored in PUT bodies.
	Revision string `yaml:"-" json:"revision,omitempty"`
}

// PromptDelivery controls how the start prompt reaches the CLI. A missing
// value means the default of the profile's CLI.
type PromptDelivery struct {
	Inject    *bool     `yaml:"inject,omitempty" json:"inject,omitempty"`
	Delay     *Duration `yaml:"delay,omitempty" json:"delay,omitempty"`
	Retries   *int      `yaml:"retries,omitempty" json:"retries,omitempty"`
	RetryWait *Duration `yaml:"retry_wait,omitempty" json:"retry_wait,omitempty"`
}

// ProfileModel is a variant of a profile that holds only what differs. A
// missing Selectable means true. (Model is the flattened view of it.)
type ProfileModel struct {
	Args         []string          `yaml:"args" json:"args"`
	Env          map[string]string `yaml:"env" json:"env"`
	UsageWindows []string          `yaml:"usage_windows" json:"usage_windows"`
	Selectable   *bool             `yaml:"selectable,omitempty" json:"selectable,omitempty"`
	Description  string            `yaml:"description" json:"description"`
}

// ProfilesResponse is the answer of GET /api/profiles.
type ProfilesResponse struct {
	Profiles []Profile   `json:"profiles"`
	Errors   []FileError `json:"errors"`
}

// RoleSettings is the model assignment of one agent role: the payload of
// PUT /api/roles/{role} and an item of GET /api/roles. (Role is the enum of
// agent roles.) Models are model ids ("<profile>/<model>"); repeats are
// weights for random.
type RoleSettings struct {
	Name       string   `yaml:"-" json:"name"`
	Models     []string `yaml:"models" json:"models"`
	Assignment string   `yaml:"assignment" json:"assignment"`
	// Revision identifies the content of the role's file for If-Match. It is
	// ignored in PUT bodies.
	Revision string `yaml:"-" json:"revision,omitempty"`
}

// Values of PromptInfo.Kind and Prompt.Kind.
const (
	// PromptKindRole is the mechanics prompt of an agent role.
	PromptKindRole = "role"
	// PromptKindInstructions is the user's own instructions for a role, inserted
	// into its role prompt.
	PromptKindInstructions = "instructions"
	// PromptKindMessage is a message omo sends to a running agent.
	PromptKindMessage = "message"
)

// PromptInfo is an item of GET /api/prompts. Revision is NoRevision for a
// prompt that is not customized.
type PromptInfo struct {
	Name       string `json:"name"`
	Customized bool   `json:"customized"`
	Kind       string `json:"kind"`
	Revision   string `json:"revision"`
}

// Prompt is a prompt with its text, the answer of GET and PUT
// /api/prompts/{name}. Default is omo's own text.
type Prompt struct {
	Name       string `json:"name"`
	Text       string `json:"text"`
	Customized bool   `json:"customized"`
	Kind       string `json:"kind"`
	Default    string `json:"default"`
	Revision   string `json:"revision"`
}

// PromptUpdate is the body of PUT /api/prompts/{name}.
type PromptUpdate struct {
	Text string `json:"text"`
}
