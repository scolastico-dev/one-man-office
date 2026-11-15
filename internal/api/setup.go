package api

import "time"

// ScanReport is the result of one scan of the workspace roots: the data of the
// projects.scanned event and the answer of POST /api/projects/rescan.
type ScanReport struct {
	// Trigger is what started the scan: start, timer, roots_changed or button.
	Trigger    string     `json:"trigger"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt time.Time  `json:"finished_at"`
	Roots      []RootScan `json:"roots"`
	// Added lists the ids of the projects created, over all roots.
	Added []string `json:"added"`
	// Missing counts the projects whose folder is missing after the scan.
	Missing int `json:"missing"`
}

// RootScan is the part of a ScanReport that concerns one workspace root.
type RootScan struct {
	// Path is the root as written in the settings.
	Path string `json:"path"`
	// State is ok, missing, not_folder, failed or cut (a bound was reached).
	State   string `json:"state"`
	Message string `json:"message"`
	Visited int    `json:"visited"`
	// Found counts the repositories seen, known or new.
	Found int      `json:"found"`
	Added []string `json:"added"`
	// Skipped names folders that could not be read in time (at most 20).
	Skipped []string `json:"skipped"`
	// Links counts links to folders that were not followed.
	Links int `json:"links"`
}

// ScanState is the answer of GET /api/projects/scan. Last is null before the
// first scan has finished.
type ScanState struct {
	Running bool        `json:"running"`
	Last    *ScanReport `json:"last"`
}

// FolderListing is the answer of GET /api/folders: the sub-folders of one
// folder, never files.
type FolderListing struct {
	// Path is the cleaned absolute path that was listed.
	Path string `json:"path"`
	// Parent is "" at the top of the file system.
	Parent string `json:"parent"`
	// Home is the user's home folder.
	Home    string        `json:"home"`
	Folders []FolderEntry `json:"folders"`
	// More is true when the folder holds more than 2,000 entries; the rest are
	// left out.
	More bool `json:"more"`
}

// FolderEntry is one sub-folder of a FolderListing.
type FolderEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Repository is true when the folder holds a .git folder.
	Repository bool `json:"repository"`
	Hidden     bool `json:"hidden"`
	Link       bool `json:"link"`
}

// AgentEnvCheck is the body of POST /api/settings/agent-env/check.
type AgentEnvCheck struct {
	// Env is the draft environment; HiddenValue resolves to the stored value.
	Env map[string]string `json:"env"`
}

// AgentEnvReport is the answer of POST /api/settings/agent-env/check.
type AgentEnvReport struct {
	// Variables are sorted by name.
	Variables []EnvVarCheck `json:"variables"`
}

// EnvVarCheck is the result of expanding one variable of the agent
// environment. It never carries a value.
type EnvVarCheck struct {
	Name string `json:"name"`
	// State is set, empty or failed.
	State string `json:"state"`
	// Message says why a variable failed.
	Message string `json:"message"`
}

// PromptPreview is the answer of POST /api/prompts/{name}/preview.
type PromptPreview struct {
	// Text is the rendered prompt, "" when Error is set.
	Text string `json:"text"`
	// Error is the template's message.
	Error string `json:"error"`
}

// DetectResult is the answer of POST /api/agent-setup/detect.
type DetectResult struct {
	Found []DetectedCLI `json:"found"`
	// Created lists the names of the profiles written.
	Created []string `json:"created"`
	// Assigned lists the roles that got models.
	Assigned []string `json:"assigned"`
	// Skipped gives one sentence per reason, for example "codex: the profile codex exists".
	Skipped []string `json:"skipped"`
}

// DetectedCLI is an agent program found on PATH.
type DetectedCLI struct {
	CLI     string `json:"cli"`
	Command string `json:"command"`
	Path    string `json:"path"`
}
