package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestOpenWritesDefaultSettings(t *testing.T) {
	s, home, _ := openTest(t)
	if !reflect.DeepEqual(s.General(), Defaults()) {
		t.Fatalf("General() = %+v, want defaults", s.General())
	}
	if _, err := os.Stat(filepath.Join(home, "settings.yaml")); err != nil {
		t.Fatal(err)
	}
	if got := Defaults().AgentEnv["GIT_AUTHOR_EMAIL"]; got != "omo@scolasti.co" {
		t.Fatalf("GIT_AUTHOR_EMAIL = %q", got)
	}
	// The written file reads back to the same values.
	s2, err := Open(home, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s2.General(), Defaults()) || len(s2.Errors()) != 0 {
		t.Fatalf("reopened = %+v errors %+v", s2.General(), s2.Errors())
	}
	raw, _ := os.ReadFile(filepath.Join(home, "settings.yaml"))
	if !strings.Contains(string(raw), "silent_after: 15m\n") {
		t.Fatalf("durations not written as text:\n%s", raw)
	}
}

func TestMalformedHandEditKeepsLastValidAndReportsError(t *testing.T) {
	s, home, log := openTest(t)
	g := s.General()
	g.MaxRunningAgents = 6
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	log.take()
	path := filepath.Join(home, "settings.yaml")
	writeFile(t, path, "max_running_agents: [")
	s.Reload()
	if got := s.General().MaxRunningAgents; got != 6 {
		t.Fatalf("max_running_agents = %d, want last valid 6", got)
	}
	errs := s.Errors()
	if len(errs) != 1 || !strings.HasSuffix(errs[0].File, "settings.yaml") {
		t.Fatalf("errors = %+v", errs)
	}
	changes := log.take()
	if len(changes) != 1 || changes[0].Section != SectionGeneral || changes[0].Err == nil {
		t.Fatalf("changes = %+v", changes)
	}
	// An unchanged broken file is not reported again.
	if ch := s.Reload(); len(ch) != 0 {
		t.Fatalf("second reload changes = %+v", ch)
	}
	writeFile(t, path, "max_running_agents: 2")
	s.Reload()
	if got := s.General().MaxRunningAgents; got != 2 {
		t.Fatalf("max_running_agents = %d, want 2", got)
	}
	if len(s.Errors()) != 0 {
		t.Fatalf("errors = %+v, want none", s.Errors())
	}
	changes = log.take()
	if len(changes) != 1 || changes[0].Section != SectionGeneral || changes[0].Err != nil {
		t.Fatalf("changes = %+v", changes)
	}
	// Keys missing from a hand-written file keep their defaults.
	if s.General().AgentEnv["GIT_AUTHOR_EMAIL"] != "omo@scolasti.co" || !s.General().CEO.Enabled {
		t.Fatalf("defaults lost: %+v", s.General())
	}
}

func TestInvalidValueInHandEditIsAnError(t *testing.T) {
	s, home, _ := openTest(t)
	writeFile(t, filepath.Join(home, "settings.yaml"), "review_defaults:\n  story: agnet\n")
	s.Reload()
	errs := s.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "agnet") {
		t.Fatalf("errors = %+v", errs)
	}
	if s.General().ReviewDefaults.Story != api.ReviewAgent {
		t.Fatalf("story review = %q", s.General().ReviewDefaults.Story)
	}
}

func TestUnknownKeyIsAnError(t *testing.T) {
	s, home, _ := openTest(t)
	writeFile(t, filepath.Join(home, "settings.yaml"), "max_agents: 3\n")
	s.Reload()
	errs := s.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "max_agents") {
		t.Fatalf("errors = %+v", errs)
	}
}

func TestSetGeneralValidates(t *testing.T) {
	s, _, log := openTest(t)
	cases := map[string]func(*General){
		"max_running_agents":          func(g *General) { g.MaxRunningAgents = 0 },
		"review_defaults.task":        func(g *General) { g.ReviewDefaults.Task = "maybe" },
		"when_approved_defaults":      func(g *General) { g.WhenApprovedDefaults.Story = "ship" },
		"timeouts_before_investigate": func(g *General) { g.Waiting.TimeoutsBeforeInvestigate = 0 },
		"silent_after":                func(g *General) { g.Health.SilentAfter = 0 },
		"handoff_timeout":             func(g *General) { g.Stop.HandoffTimeout = -1 },
		"rescan_interval":             func(g *General) { g.RescanInterval = 0 },
		"max_depth": func(g *General) {
			g.WorkspaceRoots = []WorkspaceRoot{{Path: "/ws", MaxDepth: 17}}
		},
		"workspace_roots[0].path": func(g *General) {
			g.WorkspaceRoots = []WorkspaceRoot{{Path: "relative/ws", MaxDepth: 4}}
		},
	}
	for field, mutate := range cases {
		g := Defaults()
		mutate(&g)
		err := s.SetGeneral(g)
		var ae *api.Error
		if !asAPIError(err, &ae) || ae.Code != api.CodeInvalidSettings || !strings.Contains(ae.Message, field) {
			t.Errorf("%s: err = %v", field, err)
		}
	}
	if len(log.take()) != 0 || s.General().MaxRunningAgents != 4 {
		t.Fatal("rejected settings were applied")
	}
	g := Defaults()
	g.WorkspaceRoots = []WorkspaceRoot{{Path: "~/workspace", MaxDepth: 4, Ignore: []string{"node_modules"}}}
	g.Health.SilentAfter = Duration(time.Hour)
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.General(), g) {
		t.Fatalf("General() = %+v", s.General())
	}
	if ch := log.take(); len(ch) != 1 || ch[0].Section != SectionGeneral {
		t.Fatalf("changes = %+v", ch)
	}
	if ch := s.Reload(); len(ch) != 0 {
		t.Fatalf("own write reported by reload: %+v", ch)
	}
}

func TestGeneralJSONMatchesYAMLKeys(t *testing.T) {
	b, err := json.Marshal(api.General(Defaults()))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"max_running_agents":4`, `"may_approve_stories":false`, `"silent_after":"15m"`, `"rescan_interval":"10m"`, `"handoff_timeout":"2m"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("json lacks %s: %s", key, b)
		}
	}
	var g api.General
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(General(g), Defaults()) {
		t.Fatalf("round trip = %+v", g)
	}
}
