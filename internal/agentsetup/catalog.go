// Package agentsetup holds the catalog of the agent programs omo knows and
// plans the profiles and role assignments for the ones found on the machine.
// It is pure: it reads no files and no environment.
package agentsetup

import (
	"slices"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

type catalogModel struct {
	name        string
	args        []string
	selectable  bool
	description string
	// windows are the usage windows of the profile's script that count
	// for the model.
	windows []string
}

type catalogEntry struct {
	cli     string
	command string
	// usageScript is the shipped usage script the profile names; "" for
	// none.
	usageScript string
	// models run from the strongest to the lightest.
	models []catalogModel
}

// claudeModel is a model of the claude profile. Every model counts the
// account's session (five-hour) and weekly windows; extra are the model's own
// windows (fable has its own weekly window, fable_weekly).
func claudeModel(name, description string, selectable bool, extra ...string) catalogModel {
	return catalogModel{
		name:        name,
		args:        []string{"--model", name, "--dangerously-skip-permissions"},
		selectable:  selectable,
		description: description,
		windows:     append([]string{"session", "weekly"}, extra...),
	}
}

var catalog = []catalogEntry{
	{cli: api.CLIClaude, command: "claude", usageScript: "claude", models: []catalogModel{
		claudeModel("fable", "Strongest; for the CEO.", false, "fable_weekly"),
		claudeModel("opus", "Strong; for planning and review.", true),
		claudeModel("sonnet", "Balanced; for everyday development.", true),
		claudeModel("haiku", "Fast and light; for small tasks.", true),
	}},
	{cli: api.CLICodex, command: "codex", usageScript: "codex", models: []catalogModel{
		{name: "default", args: []string{"--dangerously-bypass-approvals-and-sandbox"}, selectable: true,
			description: "The account's current default model.", windows: []string{"session", "weekly"}},
	}},
	{cli: api.CLIGemini, command: "gemini", models: []catalogModel{
		{name: "default", args: []string{"--yolo"}, selectable: true,
			description: "The account's current default model."},
	}},
}

func entry(cli string) (catalogEntry, bool) {
	for _, e := range catalog {
		if e.cli == cli {
			return e, true
		}
	}
	return catalogEntry{}, false
}

// Catalog returns the profile omo writes for a CLI it finds; ok false for an
// unknown CLI. Every call returns a fresh value the caller may change.
func Catalog(cli string) (api.Profile, bool) {
	e, ok := entry(cli)
	if !ok {
		return api.Profile{}, false
	}
	p := api.Profile{
		Name:        e.cli,
		CLI:         e.cli,
		Command:     e.command,
		Args:        []string{},
		Env:         map[string]string{},
		UsageScript: e.usageScript,
		Models:      map[string]api.ProfileModel{},
	}
	for _, m := range e.models {
		selectable := m.selectable
		windows := slices.Clone(m.windows)
		if windows == nil {
			windows = []string{}
		}
		p.Models[m.name] = api.ProfileModel{
			Args:         slices.Clone(m.args),
			Env:          map[string]string{},
			Selectable:   &selectable,
			Description:  m.description,
			UsageWindows: windows,
		}
	}
	return p, true
}

// Strength lists a catalog profile's models from strongest to lightest (for
// role assignment). It is nil for a CLI that is not in the catalog.
func Strength(cli string) []string {
	e, ok := entry(cli)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(e.models))
	for _, m := range e.models {
		names = append(names, m.name)
	}
	return names
}
