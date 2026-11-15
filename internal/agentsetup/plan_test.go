package agentsetup

import (
	"errors"
	"reflect"
	"strings"
	"testing"

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
	s, err := settings.Open(home, settings.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, cli := range []string{api.CLIClaude, api.CLICodex, api.CLIGemini} {
		p, ok := Catalog(cli)
		if !ok {
			t.Fatalf("no catalog entry for %s", cli)
		}
		if err := s.PutProfile(p); err != nil {
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
	if got := roleByName(t, p, "ceo"); !reflect.DeepEqual(got.Models, []string{"claude/fable", "gemini/default"}) || got.Assignment != api.AssignmentFailover {
		t.Fatalf("ceo = %+v", got)
	}
	if got := roleByName(t, p, "pm"); !reflect.DeepEqual(got.Models, []string{"claude/opus", "gemini/default"}) {
		t.Fatalf("pm = %+v", got)
	}
	if got := roleByName(t, p, "developer"); !reflect.DeepEqual(got.Models, []string{"claude/sonnet", "gemini/default"}) {
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
	if got := roleByName(t, p, "developer"); !reflect.DeepEqual(got.Models, []string{"gemini/default"}) {
		t.Fatalf("developer = %+v", got)
	}
}
