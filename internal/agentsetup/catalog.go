// Package agentsetup holds the catalog of the agent programs omo knows and
// plans the profiles and role assignments for the ones found on the machine.
// It is pure: it reads no files and no environment.
package agentsetup

import (
	"maps"
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
// windows (fable has its own weekly window, fable_weekly). An account may lack
// a listed window (a plan without it): that window is then not counted, and
// the list only keeps other models' windows and extra_usage out.
func claudeModel(name, description string, selectable bool, extra ...string) catalogModel {
	return catalogModel{
		name:        name,
		args:        []string{"--model", name, "--dangerously-skip-permissions"},
		selectable:  selectable,
		description: description,
		windows:     append([]string{"session", "weekly"}, extra...),
	}
}

// codexModel is a model of the codex profile: its model id and the
// account's session and weekly windows (a plan may report only weekly). The
// list is not empty because the script also reports the weekly window of each
// additional limit, which is no limit of every model.
func codexModel(name, id, description string, selectable bool) catalogModel {
	return catalogModel{
		name:        name,
		args:        []string{"--model", id, "--dangerously-bypass-approvals-and-sandbox"},
		selectable:  selectable,
		description: description,
		windows:     []string{"session", "weekly"},
	}
}

// geminiModel is a model of the gemini profile: its model id.
func geminiModel(name, id, description string, selectable bool) catalogModel {
	return catalogModel{
		name:        name,
		args:        []string{"--model", id, "--yolo"},
		selectable:  selectable,
		description: description,
	}
}

// agyModel is a model of the agy (Antigravity CLI) profile: its model id as
// `agy models` lists it and the weekly window of its quota group. Antigravity
// meters its models in groups that share one weekly limit: the Gemini models
// count gemini-weekly, the Claude and GPT-OSS models 3p-weekly (the bucket
// ids its /usage command reports).
func agyModel(name, id, window, description string, selectable bool) catalogModel {
	return catalogModel{
		name:        name,
		args:        []string{"--model", id, "--dangerously-skip-permissions"},
		selectable:  selectable,
		description: description,
		windows:     []string{window},
	}
}

var catalog = []catalogEntry{
	{cli: api.CLIClaude, command: "claude", usageScript: "claude", models: []catalogModel{
		claudeModel("fable", "Strongest; for the CEO.", false, "fable_weekly"),
		claudeModel("opus", "Strong; for planning and review.", true),
		claudeModel("sonnet", "Balanced; for everyday development.", true),
		claudeModel("haiku", "Fast and light; for small tasks.", true),
	}},
	// Codex mirrors the Claude tiers: astra is the fable tier, sol the
	// opus and sonnet tiers, luna the haiku tier.
	{cli: api.CLICodex, command: "codex", usageScript: "codex", models: []catalogModel{
		codexModel("astra", "gpt-6-astra", "Strongest; for the CEO.", false),
		codexModel("sol", "gpt-6.1-sol", "Strong; for planning, review and everyday development.", true),
		codexModel("luna", "gpt-5.6-luna", "Fast and light; for small tasks.", true),
	}},
	// Antigravity serves models of two quota groups. Opus is the fable
	// tier (only for the CEO), sonnet the opus tier; the developer's
	// middle pick is Gemini Flash (High), so the work spreads over both
	// groups' weekly limits.
	{cli: api.CLIAgy, command: "agy", usageScript: "agy", models: []catalogModel{
		agyModel("opus", "claude-opus-4-6-thinking", "3p-weekly", "Claude Opus 4.6 (Thinking); strongest, for the CEO.", false),
		agyModel("sonnet", "claude-sonnet-4-6", "3p-weekly", "Claude Sonnet 4.6 (Thinking); strong, for planning and review.", true),
		agyModel("flash-high", "gemini-3.8-flash-high", "gemini-weekly", "Gemini 3.8 Flash (High); for everyday development.", true),
		agyModel("flash-low", "gemini-3.8-flash-low", "gemini-weekly", "Gemini 3.8 Flash (Low); fast, for small tasks.", true),
		agyModel("gpt-oss", "gpt-oss-120b-medium", "3p-weekly", "GPT-OSS 120B (Medium); open model, for small tasks.", true),
	}},
	// Gemini mirrors the Claude tiers too: carbon is the fable tier,
	// argon the opus and sonnet tiers, hydrogen and flash the light ones.
	{cli: api.CLIGemini, command: "gemini", models: []catalogModel{
		geminiModel("carbon", "gemini-4-carbon", "Strongest; for the CEO.", false),
		geminiModel("argon", "gemini-4-argon", "Strong; for planning and review.", true),
		geminiModel("hydrogen", "gemini-4-hydrogen", "Efficient and fast; for everyday development.", true),
		geminiModel("flash", "gemini-3.8-flash", "Lightest; for small tasks.", true),
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

// AddMissingModels returns a copy of p with the catalog's models for p's CLI
// that p does not have (by model name) added, and the names added, strongest
// first. Everything p has, its models, command, arguments and environment,
// stays as it is. ok is false for a CLI that is not in the catalog.
func AddMissingModels(p api.Profile) (out api.Profile, added []string, ok bool) {
	cat, ok := Catalog(p.CLI)
	if !ok {
		return api.Profile{}, nil, false
	}
	out = p
	out.Models = maps.Clone(p.Models)
	if out.Models == nil {
		out.Models = map[string]api.ProfileModel{}
	}
	added = []string{}
	for _, name := range Strength(p.CLI) {
		if _, has := out.Models[name]; has {
			continue
		}
		out.Models[name] = cat.Models[name]
		added = append(added, name)
	}
	return out, added, true
}
