package api

import "time"

// UsageSettings is the usage part of the general settings: when omo keeps
// agents off a model that is near a limit of its account and when it stops
// them. Percentages are of a usage window, 0 to 100.
type UsageSettings struct {
	// SoftPercent is the fill from which a model is used last.
	SoftPercent float64 `yaml:"soft_percent" json:"soft_percent"`
	// HardPercent is the fill from which a model is not used and its
	// running agents are stopped.
	HardPercent float64 `yaml:"hard_percent" json:"hard_percent"`
	// RefreshInterval is the time between two runs of a usage script.
	RefreshInterval Duration `yaml:"refresh_interval" json:"refresh_interval"`
	// Windows holds the thresholds of single windows by window name. A PATCH
	// replaces the whole map.
	Windows map[string]UsageThreshold `yaml:"windows" json:"windows"`
}

// UsageThreshold overrides the general thresholds for one window. A missing
// value means the general one.
type UsageThreshold struct {
	SoftPercent *float64 `yaml:"soft_percent,omitempty" json:"soft_percent,omitempty"`
	HardPercent *float64 `yaml:"hard_percent,omitempty" json:"hard_percent,omitempty"`
}

// UsageWindow is one usage window a script reported.
type UsageWindow struct {
	Name        string  `json:"name"`
	UsedPercent float64 `json:"used_percent"`
	// ResetsAt is null when the script did not say.
	ResetsAt *time.Time `json:"resets_at"`
}

// UsageReading is what the last run of a script found for one account.
type UsageReading struct {
	// Key names the account: 16 hex digits over the script, the account
	// folder and the environment.
	Key    string `json:"key"`
	Script string `json:"script"`
	// Profiles are the profiles that share the account, sorted.
	Profiles []string      `json:"profiles"`
	Windows  []UsageWindow `json:"windows"`
	// FetchedAt is null before the first successful run.
	FetchedAt *time.Time `json:"fetched_at"`
	// NextAt is when the next run is due; null when none is planned.
	NextAt  *time.Time `json:"next_at"`
	Running bool       `json:"running"`
	// Error is "" or the redacted message of the last failed run.
	Error string `json:"error"`
	// StaleSince is when the first failed run after the last successful
	// one ended: the windows are then those of FetchedAt and keep counting
	// for usage.MaxStaleAfterFailure from this time. Null while the last
	// run succeeded.
	StaleSince *time.Time `json:"stale_since"`
}

// Values of ModelUsage.State.
const (
	// UsageUnmetered is a model without usage windows.
	UsageUnmetered = "unmetered"
	// UsageUnknown is a model whose windows have no reading.
	UsageUnknown = "unknown"
	// UsageOK is a model below every soft threshold.
	UsageOK = "ok"
	// UsageSoft is a model at or above a soft threshold.
	UsageSoft = "soft"
	// UsageHard is a model at or above a hard threshold.
	UsageHard = "hard"
)

// ModelUsage is how close one model is to its limits.
type ModelUsage struct {
	Model string `json:"model"`
	State string `json:"state"`
	// Window is the fullest applicable window; "" when there is none.
	Window      string     `json:"window"`
	UsedPercent *float64   `json:"used_percent"`
	ResetsAt    *time.Time `json:"resets_at"`
	// Note is "" or one sentence on what the reading lacks for the model:
	// "claude/fable counts the window fable_weekly, which the script did
	// not report".
	Note string `json:"note"`
}

// UsageState is the answer of GET /api/usage and the data of usage.changed.
type UsageState struct {
	Readings []UsageReading `json:"readings"`
	// Models lists every model of every profile, sorted by id.
	Models []ModelUsage `json:"models"`
}

// UsageScriptInfo is an item of GET /api/usage/scripts.
type UsageScriptInfo struct {
	Name       string `json:"name"`
	HasDefault bool   `json:"has_default"`
	Customized bool   `json:"customized"`
	// UsedBy lists the profiles that name the script.
	UsedBy   []string `json:"used_by"`
	Revision string   `json:"revision"`
}

// UsageScript is the answer of GET and PUT /api/usage/scripts/{name}.
type UsageScript struct {
	Name       string `json:"name"`
	Text       string `json:"text"`
	Default    string `json:"default"`
	HasDefault bool   `json:"has_default"`
	Customized bool   `json:"customized"`
	Revision   string `json:"revision"`
}

// UsageScriptUpdate is the body of PUT /api/usage/scripts/{name}.
type UsageScriptUpdate struct {
	Text string `json:"text"`
}

// UsageTest is the body of POST /api/usage/scripts/{name}/test.
type UsageTest struct {
	// Text is the draft to run.
	Text string `json:"text"`
	// Profile is the profile whose account the draft runs as.
	Profile string `json:"profile"`
}

// UsageTestResult is the answer of a test run. A failing script is not an
// HTTP error: Error says why.
type UsageTestResult struct {
	Windows []UsageWindow `json:"windows"`
	Error   string        `json:"error"`
	Log     []string      `json:"log"`
	Millis  int           `json:"millis"`
}

// StartAnyway is the body of POST /api/tickets/{n}/start-anyway: {}.
type StartAnyway struct{}
