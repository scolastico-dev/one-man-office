package api

import "time"

// Values of SkillSet.Update.
const (
	// SkillSetAtStart updates the checkout at every start of omo.
	SkillSetAtStart = "at_start"
	// SkillSetPinned does not follow the branch: the checkout is fetched
	// again only on Update now or when the repository or ref changes.
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
	// Paths limit what agents are given to these folders and files,
	// relative to the repository's root with / between the parts (e.g.
	// skills/grill-me or docs/style.md); empty gives the whole repository.
	Paths []string `yaml:"paths,omitempty" json:"paths"`
	// Roles are the roles (ceo, pm, developer, reviewer, export) whose
	// agents get the set; empty gives it to every role.
	Roles []string `yaml:"roles,omitempty" json:"roles"`
	// Instructions maps a role that gets the set to a text of its own that
	// comes with the set's skills.
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
	// MissingPaths are the set's paths that its checkout does not have (a
	// folder or a regular file each); never null, empty without checkout.
	MissingPaths []string `json:"missing_paths"`
}

// SkillSetsResponse is the answer of GET /api/skillsets.
type SkillSetsResponse struct {
	SkillSets []SkillSet      `json:"skill_sets"`
	States    []SkillSetState `json:"states"`
	Errors    []FileError     `json:"errors"`
}

// SkillSearchResult is one skill skills.sh found: an item of GET
// /api/skillsets/search. Name, SkillID and Source come from skills.sh
// (someone else's text, checked and cut by omo); Repository and URL are
// made by omo from Source and SkillID.
type SkillSearchResult struct {
	// ID is Source/SkillID.
	ID      string `json:"id"`
	Name    string `json:"name"`
	SkillID string `json:"skill_id"`
	// Source is the GitHub repository as owner/repo.
	Source   string `json:"source"`
	Installs int64  `json:"installs"`
	// Repository is the git address of Source on GitHub.
	Repository string `json:"repository"`
	// URL is the skill's page on skills.sh.
	URL string `json:"url"`
}

// SkillSearchResponse is the answer of GET /api/skillsets/search?q=.
type SkillSearchResponse struct {
	Query   string              `json:"query"`
	Results []SkillSearchResult `json:"results"`
}

// SkillSearchChoice is the payload of POST /api/skillsets/search/resolve:
// a result the user chose.
type SkillSearchChoice struct {
	Source  string `json:"source"`
	SkillID string `json:"skill_id"`
	Name    string `json:"name"`
}

// SkillSearchResolved is the answer of POST /api/skillsets/search/resolve:
// what a new skill set for the chosen result starts with.
type SkillSearchResolved struct {
	Repository string `json:"repository"`
	// Ref is "": the repository's default branch.
	Ref string `json:"ref"`
	// Paths is the skill's folder when Found ([] when the skill is the
	// whole repository or was not found).
	Paths []string `json:"paths"`
	// Found says whether a skill of that name is in the repository.
	Found bool `json:"found"`
	// Commit is the commit that was looked at.
	Commit string `json:"commit"`
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
	// Hidden suppresses automatically generated controls for a manual hook.
	// Explicit controls and direct invocation remain available.
	Hidden bool `json:"hidden,omitempty"`
	// Move and Failure are set for gate hooks only: the move the hook gates
	// (release or start) and what a failing hook does (go_ahead or hold).
	Move    string `json:"move,omitempty"`
	Failure string `json:"failure,omitempty"`
	// Interval is set for timer hooks only.
	Interval string `json:"interval"`
}

// PluginButton declares a manual action at a board location.
type PluginButton struct {
	Label       string `json:"label"`
	Place       string `json:"place"`
	Action      string `json:"action"`
	PerWorktree bool   `json:"per_worktree,omitempty"`
}

// PluginFrontend names public files relative to the installed plugin.
// The zero value serializes to {}, for a plugin without frontend code.
type PluginFrontend struct {
	Script string `json:"script,omitempty"`
	Assets string `json:"assets,omitempty"`
}

// PluginConfigChoice is a stable select value and its display label.
type PluginConfigChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// PluginConfigField describes a top-level config key; defaults come from
// default_config. Unlisted configuration keys remain editable as JSON.
type PluginConfigField struct {
	Key     string               `json:"key"`
	Label   string               `json:"label"`
	Type    string               `json:"type"`
	Choices []PluginConfigChoice `json:"choices,omitempty"`
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
	Commit       string              `json:"commit"`
	LastEvent    string              `json:"last_event"`
	LastRunAt    *time.Time          `json:"last_run_at"`
	Hooks        []PluginHook        `json:"hooks"`
	Buttons      []PluginButton      `json:"buttons"`
	Frontend     PluginFrontend      `json:"frontend"`
	ConfigFields []PluginConfigField `json:"config_fields"`
	// ContentID hashes the manifest and public frontend files on load.
	ContentID string `json:"content_id"`
}

// Tags of the plugins omo's list names: how far omo's maintainers vouch
// for a plugin.
const (
	// PluginOfficial is a plugin kept in omo's repository by its
	// maintainers; it installs without a question.
	PluginOfficial = "official"
	// PluginRecommended is a plugin of someone else that the maintainers
	// looked at.
	PluginRecommended = "recommended"
	// PluginCommunity is a plugin that is listed but not reviewed. A source
	// the list does not name counts as community too.
	PluginCommunity = "community"
)

// CatalogPlugin is one plugin of the list omo provides or of an additional
// list the user named (General.PluginLists), with the source it installs
// from (an official plugin's is the list's own repository and branch).
type CatalogPlugin struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Tag is how far omo vouches for the plugin. A plugin of an additional
	// list is always community, whatever that list says.
	Tag     string `json:"tag"`
	Source  string `json:"source"`
	Ref     string `json:"ref"`
	Subpath string `json:"subpath"`
	// List is the address of the additional list the plugin comes from; ""
	// for omo's own list. Origin is that list's host.
	List   string `json:"list"`
	Origin string `json:"origin"`
}

// PluginListState is how reading one additional plugin list went.
type PluginListState struct {
	// URL is the list's address as the settings name it; Origin its host.
	URL    string `json:"url"`
	Origin string `json:"origin"`
	// Available is false when the list could not be fetched or read;
	// Message then says why. Its plugins are in PluginCatalog.Plugins.
	Available bool   `json:"available"`
	Message   string `json:"message"`
	// Notes name the entries of the list that are not shown, and why.
	Notes []string `json:"notes"`
	// FetchedAt is when the list (or the failure) was read.
	FetchedAt *time.Time `json:"fetched_at"`
}

// PluginCatalog is the answer of GET /api/plugins/catalog: the plugin list
// omo provides, or why it is not available, and the plugins of the
// additional lists with the state of each list.
type PluginCatalog struct {
	// Available is false when omo's own list could not be fetched or read;
	// Message then says why.
	Available bool   `json:"available"`
	Message   string `json:"message"`
	// Source and Ref are where the list is read from.
	Source string `json:"source"`
	Ref    string `json:"ref"`
	// FetchedAt is when the list (or the failure) was read.
	FetchedAt *time.Time `json:"fetched_at"`
	// Plugins are the plugins of omo's own list (when it is available),
	// then those of the additional lists, in the order of the settings.
	Plugins []CatalogPlugin `json:"plugins"`
	// Lists is the state of every additional list, in the order of the
	// settings.
	Lists []PluginListState `json:"lists"`
}

// PluginInstall is the body of POST /api/plugins and PUT
// /api/plugins/{name}. Acknowledge is the warning the user confirmed for a
// source that is not official: "recommended" or "community" (which covers
// both). The server decides the source's tag itself, from its list.
type PluginInstall struct {
	PluginSettings
	Acknowledge string `json:"acknowledge,omitempty"`
}

// PluginUpdate is the body of POST /api/plugins/{name}/update. An update
// from the source the plugin was installed from needs no acknowledgement.
type PluginUpdate struct {
	Acknowledge string `json:"acknowledge,omitempty"`
}

// PluginConfirmation is the details of a refused install, change or update
// that needs the user's acknowledgement: the tag the server gave the source.
type PluginConfirmation struct {
	Field string `json:"field"`
	Tag   string `json:"tag"`
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
// is required for an action of ticket scope. Project optionally selects one
// registered project; with Ticket it must be one of that ticket's projects.
type PluginRun struct {
	Ticket  int    `json:"ticket,omitempty"`
	Project string `json:"project,omitempty"`
}

// PluginRunResult is the answer of a manual plugin action. Message can accompany
// either URL or ForwardID; URL and ForwardID are mutually exclusive. ForwardID
// consumers must use the authenticated proxy Open flow before navigation.
type PluginRunResult struct {
	Message   string `json:"message"`
	URL       string `json:"url,omitempty"`
	ForwardID string `json:"forward_id,omitempty"`
}
