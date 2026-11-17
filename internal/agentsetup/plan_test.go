package agentsetup

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// lookup returns a lookPath that finds exactly the named programs. It never
// touches the file system or the environment.
func lookup(found ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, f := range found {
			if f == name {
				return "/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

func emptyRoles() []api.RoleSettings {
	return []api.RoleSettings{
		{Name: "ceo", Assignment: api.AssignmentRoundRobin},
		{Name: "pm", Assignment: api.AssignmentRoundRobin},
		{Name: "developer", Assignment: api.AssignmentRoundRobin},
		{Name: "reviewer", Assignment: api.AssignmentRoundRobin},
	}
}

func roleByName(t *testing.T, p Plan, name string) api.RoleSettings {
	t.Helper()
	for _, r := range p.Roles {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("plan has no role %s: %+v", name, p.Roles)
	return api.RoleSettings{}
}

func TestCatalogProfilesValidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	// The store knows the shipped usage scripts, so a profile naming
	// another script would be refused.
	s, err := settings.Open(home, settings.Options{UsageDefaults: map[string]string{"claude": "-- claude", "codex": "-- codex", "agy": "-- agy"}})
	if err != nil {
		t.Fatal(err)
	}
	// Every supported program: the settings accept each one's profile.
	for _, provider := range agentcli.Supported {
		cli := string(provider)
		p, ok := Catalog(cli)
		if !ok {
			t.Fatalf("no catalog entry for %s", cli)
		}
		if err := s.PutProfileIf("", p); err != nil {
			t.Fatalf("%s: %v", cli, err)
		}
		for _, name := range Strength(cli) {
			if _, ok := p.Models[name]; !ok {
				t.Fatalf("%s: strength lists %q, which the profile lacks", cli, name)
			}
		}
		if len(Strength(cli)) != len(p.Models) {
			t.Fatalf("%s: strength %v does not cover the models", cli, Strength(cli))
		}
	}
	if _, ok := Catalog("custom"); ok {
		t.Fatal("custom must not be in the catalog")
	}
	if Strength("custom") != nil {
		t.Fatal("custom has no strength list")
	}
}

func TestCatalogNamesUsageScripts(t *testing.T) {
	claude, _ := Catalog(api.CLIClaude)
	if claude.UsageScript != "claude" {
		t.Fatalf("claude usage script = %q", claude.UsageScript)
	}
	want := map[string][]string{
		"fable":  {"session", "weekly", "fable_weekly"},
		"opus":   {"session", "weekly"},
		"sonnet": {"session", "weekly"},
		"haiku":  {"session", "weekly"},
	}
	for name, windows := range want {
		if got := claude.Models[name].UsageWindows; !reflect.DeepEqual(got, windows) {
			t.Errorf("claude/%s windows = %v, want %v", name, got, windows)
		}
	}
	codex, _ := Catalog(api.CLICodex)
	if codex.UsageScript != "codex" {
		t.Fatalf("codex = %+v", codex)
	}
	for _, name := range []string{"astra", "sol", "luna"} {
		if !reflect.DeepEqual(codex.Models[name].UsageWindows, []string{"session", "weekly"}) {
			t.Fatalf("codex/%s = %+v", name, codex.Models[name])
		}
	}
	gemini, _ := Catalog(api.CLIGemini)
	if gemini.UsageScript != "" {
		t.Fatalf("gemini = %+v", gemini)
	}
	for _, name := range []string{"carbon", "argon", "hydrogen", "flash"} {
		if w := gemini.Models[name].UsageWindows; w == nil || len(w) != 0 {
			t.Fatalf("gemini/%s = %+v", name, gemini.Models[name])
		}
	}
	// Copies are independent.
	claude.Models["opus"].UsageWindows[0] = "changed"
	again, _ := Catalog(api.CLIClaude)
	if again.Models["opus"].UsageWindows[0] != "session" || again.Models["sonnet"].UsageWindows[0] != "session" {
		t.Fatal("catalog windows shared")
	}
}

func TestCatalogReturnsIndependentCopies(t *testing.T) {
	p, _ := Catalog(api.CLIClaude)
	p.Models["opus"].Args[0] = "changed"
	p.Models["x"] = api.ProfileModel{}
	q, _ := Catalog(api.CLIClaude)
	if q.Models["opus"].Args[0] != "--model" || len(q.Models) != 4 {
		t.Fatalf("catalog was mutated through a returned profile: %+v", q)
	}
}

func TestPlanNothingFound(t *testing.T) {
	p := Detect(lookup(), nil, emptyRoles())
	if len(p.Found) != 0 || len(p.Profiles) != 0 || len(p.Roles) != 0 || len(p.Skipped) != 0 {
		t.Fatalf("plan = %+v, want empty", p)
	}
}

func TestPlanClaudeOnly(t *testing.T) {
	p := Detect(lookup("claude"), nil, emptyRoles())
	if want := []Found{{CLI: "claude", Command: "claude", Path: "/bin/claude"}}; !reflect.DeepEqual(p.Found, want) {
		t.Fatalf("found = %+v", p.Found)
	}
	if len(p.Profiles) != 1 || p.Profiles[0].Name != "claude" || p.Profiles[0].CLI != api.CLIClaude {
		t.Fatalf("profiles = %+v", p.Profiles)
	}
	fable := p.Profiles[0].Models["fable"]
	if fable.Selectable == nil || *fable.Selectable {
		t.Fatal("fable must not be selectable")
	}
	want := map[string]struct {
		models     []string
		assignment string
	}{
		"ceo":       {[]string{"claude/fable"}, api.AssignmentFailover},
		"pm":        {[]string{"claude/opus"}, api.AssignmentRoundRobin},
		"developer": {[]string{"claude/sonnet"}, api.AssignmentRoundRobin},
		"reviewer":  {[]string{"claude/opus"}, api.AssignmentRoundRobin},
	}
	if len(p.Roles) != len(want) {
		t.Fatalf("roles = %+v", p.Roles)
	}
	for name, w := range want {
		r := roleByName(t, p, name)
		if !reflect.DeepEqual(r.Models, w.models) || r.Assignment != w.assignment {
			t.Errorf("%s = %+v, want %+v", name, r, w)
		}
	}
}

func TestPlanTwoCLIs(t *testing.T) {
	// Lookup order of the arguments must not matter: Supported order does.
	p := Detect(lookup("gemini", "claude"), nil, emptyRoles())
	var clis []string
	for _, f := range p.Found {
		clis = append(clis, f.CLI)
	}
	if !reflect.DeepEqual(clis, []string{"claude", "gemini"}) {
		t.Fatalf("found = %v", clis)
	}
	if len(p.Profiles) != 2 || p.Profiles[0].Name != "claude" || p.Profiles[1].Name != "gemini" {
		t.Fatalf("profiles = %+v", p.Profiles)
	}
	if got := roleByName(t, p, "ceo"); !reflect.DeepEqual(got.Models, []string{"claude/fable", "gemini/carbon"}) || got.Assignment != api.AssignmentFailover {
		t.Fatalf("ceo = %+v", got)
	}
	if got := roleByName(t, p, "pm"); !reflect.DeepEqual(got.Models, []string{"claude/opus", "gemini/argon"}) {
		t.Fatalf("pm = %+v", got)
	}
	if got := roleByName(t, p, "developer"); !reflect.DeepEqual(got.Models, []string{"claude/sonnet", "gemini/hydrogen"}) {
		t.Fatalf("developer = %+v", got)
	}
}

func TestPlanNeverOverwrites(t *testing.T) {
	existing := []api.Profile{{Name: "claude", CLI: api.CLICustom, Command: "mine"}}
	roles := emptyRoles()
	roles[2].Models = []string{"mine/x"} // developer already has models
	p := Detect(lookup("claude", "codex"), existing, roles)
	if len(p.Profiles) != 1 || p.Profiles[0].Name != "codex" {
		t.Fatalf("profiles = %+v, want only codex", p.Profiles)
	}
	if len(p.Skipped) != 1 || !strings.Contains(p.Skipped[0], "claude") || !strings.HasSuffix(p.Skipped[0], ".") {
		t.Fatalf("skipped = %q", p.Skipped)
	}
	for _, r := range p.Roles {
		if r.Name == "developer" {
			t.Fatal("a role with models must be left untouched")
		}
		for _, m := range r.Models {
			if strings.HasPrefix(m, "claude/") {
				t.Fatalf("role %s refers to the skipped claude profile: %v", r.Name, r.Models)
			}
		}
	}
	if len(p.Found) != 2 {
		t.Fatalf("found = %+v, want both programs listed", p.Found)
	}
}

func TestPlanSkipsCLIThatAlreadyHasAProfile(t *testing.T) {
	existing := []api.Profile{{Name: "work", CLI: api.CLICodex, Command: "codex"}}
	p := Detect(lookup("codex"), existing, emptyRoles())
	if len(p.Profiles) != 0 || len(p.Roles) != 0 || len(p.Skipped) != 1 || !strings.Contains(p.Skipped[0], "work") {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPlanDoesNotSetRolesWithoutNewProfiles(t *testing.T) {
	p := Detect(lookup("gemini"), nil, nil)
	if len(p.Roles) != 4 {
		t.Fatalf("roles = %+v, want all four (absent roles count as empty)", p.Roles)
	}
	if got := roleByName(t, p, "developer"); !reflect.DeepEqual(got.Models, []string{"gemini/hydrogen"}) {
		t.Fatalf("developer = %+v", got)
	}
}

// The Codex profile mirrors the Claude tiers: astra is the strongest and
// only for the CEO (as fable is), sol serves the opus and sonnet tiers,
// luna is the light one. Each passes its model id and skips approvals.
func TestCodexModelsMirrorTheClaudeTiers(t *testing.T) {
	codex, _ := Catalog(api.CLICodex)
	if got := Strength(api.CLICodex); !reflect.DeepEqual(got, []string{"astra", "sol", "luna"}) {
		t.Fatalf("strength %v", got)
	}
	for name, want := range map[string]struct {
		args       []string
		selectable bool
	}{
		"astra": {[]string{"--model", "gpt-6-astra", "--dangerously-bypass-approvals-and-sandbox"}, false},
		"sol":   {[]string{"--model", "gpt-6.1-sol", "--dangerously-bypass-approvals-and-sandbox"}, true},
		"luna":  {[]string{"--model", "gpt-5.6-luna", "--dangerously-bypass-approvals-and-sandbox"}, true},
	} {
		m := codex.Models[name]
		if !reflect.DeepEqual(m.Args, want.args) || m.Selectable == nil || *m.Selectable != want.selectable || m.Description == "" || strings.Contains(m.Description, "\n") {
			t.Fatalf("codex/%s = %+v", name, m)
		}
	}
	p := Detect(lookup("codex"), nil, emptyRoles())
	for role, want := range map[string]string{"ceo": "codex/astra", "pm": "codex/sol", "developer": "codex/sol", "reviewer": "codex/sol"} {
		if got := roleByName(t, p, role); !reflect.DeepEqual(got.Models, []string{want}) {
			t.Fatalf("%s = %v, want %s", role, got.Models, want)
		}
	}
}

// The Gemini profile has fixed model ids, strongest first: carbon is the
// fable tier (only for the CEO), argon the opus and sonnet tiers, hydrogen
// the efficient one and flash the lightest.
func TestGeminiModelsHaveFixedIDs(t *testing.T) {
	gemini, _ := Catalog(api.CLIGemini)
	if got := Strength(api.CLIGemini); !reflect.DeepEqual(got, []string{"carbon", "argon", "hydrogen", "flash"}) {
		t.Fatalf("strength %v", got)
	}
	if len(gemini.Models) != 4 {
		t.Fatalf("models = %+v", gemini.Models)
	}
	for name, want := range map[string]struct {
		id         string
		selectable bool
	}{
		"carbon":   {"gemini-4-carbon", false},
		"argon":    {"gemini-4-argon", true},
		"hydrogen": {"gemini-4-hydrogen", true},
		"flash":    {"gemini-3.8-flash", true},
	} {
		m := gemini.Models[name]
		if !reflect.DeepEqual(m.Args, []string{"--model", want.id, "--yolo"}) || m.Selectable == nil || *m.Selectable != want.selectable || m.Description == "" || strings.Contains(m.Description, "\n") {
			t.Fatalf("gemini/%s = %+v", name, m)
		}
	}
	p := Detect(lookup("gemini"), nil, emptyRoles())
	for role, want := range map[string]string{"ceo": "gemini/carbon", "pm": "gemini/argon", "developer": "gemini/hydrogen", "reviewer": "gemini/argon"} {
		if got := roleByName(t, p, role); !reflect.DeepEqual(got.Models, []string{want}) {
			t.Fatalf("%s = %v, want %s", role, got.Models, want)
		}
	}
}

// AddMissingModels adds the catalog's models a profile lacks, by name, and
// changes nothing else: an existing model stays as it is even where the
// catalog's differs.
func TestAddMissingModels(t *testing.T) {
	own := api.ProfileModel{Args: []string{"--mine"}, Env: map[string]string{"A": "1"}, Description: "mine"}
	p := api.Profile{
		Name: "work", CLI: api.CLIGemini, Command: "my-gemini", Args: []string{"--x"}, Env: map[string]string{"K": "v"},
		Models: map[string]api.ProfileModel{"default": {}, "argon": own},
	}
	out, added, ok := AddMissingModels(p)
	if !ok || !reflect.DeepEqual(added, []string{"carbon", "hydrogen", "flash"}) {
		t.Fatalf("added %v, ok %v", added, ok)
	}
	if len(p.Models) != 2 {
		t.Fatalf("the argument was changed: %+v", p.Models)
	}
	if !reflect.DeepEqual(out.Models["argon"], own) || !reflect.DeepEqual(out.Models["default"], api.ProfileModel{}) {
		t.Fatalf("existing models changed: %+v", out.Models)
	}
	cat, _ := Catalog(api.CLIGemini)
	for _, name := range added {
		if !reflect.DeepEqual(out.Models[name], cat.Models[name]) {
			t.Fatalf("%s = %+v, want the catalog's", name, out.Models[name])
		}
	}
	out.Models = p.Models
	if !reflect.DeepEqual(out, p) {
		t.Fatalf("other fields changed: %+v", out)
	}
	// Nothing missing: nothing added.
	full, _ := Catalog(api.CLICodex)
	if _, added, ok := AddMissingModels(full); !ok || len(added) != 0 {
		t.Fatalf("full profile: added %v", added)
	}
	if _, _, ok := AddMissingModels(api.Profile{CLI: api.CLICustom}); ok {
		t.Fatal("a custom CLI has no catalog")
	}
}

// The Antigravity profile carries models of both of its quota groups: the
// Claude and GPT-OSS models share one weekly window (3p-weekly), the Gemini
// models the other (gemini-weekly). The model ids are those `agy models`
// lists. Opus is for the CEO only, as fable is; the PM and the reviewer get
// sonnet and the developer Gemini Flash, so the two groups share the work.
func TestAgyModelsSpanBothQuotaGroups(t *testing.T) {
	agy, ok := Catalog(api.CLIAgy)
	if !ok || agy.Command != "agy" || agy.UsageScript != "agy" {
		t.Fatalf("agy = %+v", agy)
	}
	if got := Strength(api.CLIAgy); !reflect.DeepEqual(got, []string{"opus", "sonnet", "flash-high", "flash-low", "gpt-oss"}) {
		t.Fatalf("strength %v", got)
	}
	for name, want := range map[string]struct {
		id         string
		selectable bool
		window     string
	}{
		"opus":       {"claude-opus-4-6-thinking", false, "3p-weekly"},
		"sonnet":     {"claude-sonnet-4-6", true, "3p-weekly"},
		"flash-high": {"gemini-3.8-flash-high", true, "gemini-weekly"},
		"flash-low":  {"gemini-3.8-flash-low", true, "gemini-weekly"},
		"gpt-oss":    {"gpt-oss-120b-medium", true, "3p-weekly"},
	} {
		m := agy.Models[name]
		if !reflect.DeepEqual(m.Args, []string{"--model", want.id, "--dangerously-skip-permissions"}) || m.Selectable == nil || *m.Selectable != want.selectable ||
			!reflect.DeepEqual(m.UsageWindows, []string{want.window}) || m.Description == "" || strings.Contains(m.Description, "\n") {
			t.Fatalf("agy/%s = %+v", name, m)
		}
	}
	p := Detect(lookup("agy"), nil, emptyRoles())
	for role, want := range map[string]string{"ceo": "agy/opus", "pm": "agy/sonnet", "developer": "agy/flash-high", "reviewer": "agy/sonnet"} {
		if got := roleByName(t, p, role); !reflect.DeepEqual(got.Models, []string{want}) {
			t.Fatalf("%s = %v, want %s", role, got.Models, want)
		}
	}
}

// Every program omo supports has a catalog entry, so detection, "Add missing
// models" and the first start know it.
func TestEverySupportedCLIHasACatalogEntry(t *testing.T) {
	for _, provider := range agentcli.Supported {
		if _, ok := Catalog(string(provider)); !ok {
			t.Errorf("%s has no catalog entry", provider)
		}
	}
	if len(catalog) != len(agentcli.Supported) {
		t.Errorf("catalog has %d entries for %d supported programs", len(catalog), len(agentcli.Supported))
	}
}
