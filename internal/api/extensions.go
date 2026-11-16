package api

import "time"

// Values of SkillSet.Update.
const (
	// SkillSetAtStart updates the checkout whenever an agent starts.
	SkillSetAtStart = "at_start"
	// SkillSetPinned never updates the checkout by itself.
	SkillSetPinned = "pinned"
)

// SkillSet is a git repository of instructions with one shared checkout: the
// payload of PUT /api/skillsets/{name} and an item of GET /api/skillsets. Its
// JSON names equal the keys of skillsets/<name>.yaml; the name comes from the
// file name.
type SkillSet struct {
	Name       string `yaml:"-" json:"name"`
	Repository string `yaml:"repository" json:"repository"`
	Ref        string `yaml:"ref" json:"ref"`
	// Update is SkillSetAtStart or SkillSetPinned.
	Update  string `yaml:"update" json:"update"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
	// Instructions maps a role (ceo, pm, developer, reviewer, export) to the
	// text that points the role at the checkout.
	Instructions map[string]string `yaml:"instructions" json:"instructions"`
	// Revision identifies the content of the file for If-Match. It is
	// ignored in PUT bodies.
	Revision string `yaml:"-" json:"revision,omitempty"`
}

// SkillSetState is the state of a skill set's checkout, and the data of
// skillset.changed.
type SkillSetState struct {
	Name string `json:"name"`
	// State is missing, updating, ok or failed.
	State string `json:"state"`
	// Commit is the full hash; "" without a checkout.
	Commit string `json:"commit"`
	Path   string `json:"path"`
	// UpdatedAt is null before the first successful update.
	UpdatedAt *time.Time `json:"updated_at"`
	// Message says why an update failed: git's output, cut and without
	// credentials.
	Message string `json:"message"`
}

// SkillSetsResponse is the answer of GET /api/skillsets.
type SkillSetsResponse struct {
	SkillSets []SkillSet      `json:"skill_sets"`
	States    []SkillSetState `json:"states"`
	Errors    []FileError     `json:"errors"`
}

// PluginSettings is the settings file of one plugin: the payload of PUT
// /api/plugins/{name} and of POST /api/plugins. Its JSON names equal the keys
// of plugins/<name>.yaml; the name comes from the file name.
type PluginSettings struct {
	Name    string `yaml:"-" json:"name"`
	Source  string `yaml:"source" json:"source"`
	Subpath string `yaml:"subpath" json:"subpath"`
	Ref     string `yaml:"ref" json:"ref"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
	// Config is the plugin's own configuration.
	Config map[string]any `yaml:"config" json:"config"`
	// Revision identifies the content of the file for If-Match. It is
	// ignored in PUT bodies.
	Revision string `yaml:"-" json:"revision,omitempty"`
}

// PluginHook is one hook of a plugin's manifest.
type PluginHook struct {
	Event string `json:"event"`
	// Name and Description are set for manual hooks only.
	Name        string `json:"name"`
	Description string `json:"description"`
	// Scope of a manual hook: ticket or global.
	Scope string `json:"scope"`
	// Interval is set for timer hooks only.
	Interval string `json:"interval"`
}

// PluginInfo is what omo knows about an installed plugin, and the data of
// plugin.changed.
type PluginInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	// State is ready, running, error, disabled, not_installed or invalid.
	State string `json:"state"`
	// Message says why the plugin is invalid or what its last error was.
	Message string `json:"message"`
	// Commit is the installed commit; "" when it is not known.
	Commit    string       `json:"commit"`
	LastEvent string       `json:"last_event"`
	LastRunAt *time.Time   `json:"last_run_at"`
	Hooks     []PluginHook `json:"hooks"`
}

// PluginsResponse is the answer of GET /api/plugins.
type PluginsResponse struct {
	Plugins  []PluginInfo     `json:"plugins"`
	Settings []PluginSettings `json:"settings"`
	Errors   []FileError      `json:"errors"`
}

// PluginLogLine is one line of a plugin's log.
type PluginLogLine struct {
	At      time.Time `json:"at"`
	Message string    `json:"message"`
}

// PluginRun is the body of POST /api/plugins/{name}/actions/{action}. Ticket
// is required for an action of ticket scope.
type PluginRun struct {
	Ticket int `json:"ticket,omitempty"`
}

// PluginRunResult is the answer of a manual plugin action.
type PluginRunResult struct {
	Message string `json:"message"`
}
