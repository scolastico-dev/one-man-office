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
	g.Limits.Tasks = 6
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	log.take()
	path := filepath.Join(home, "settings.yaml")
	writeFile(t, path, "limits:\n  tasks: [")
	s.Reload()
	if got := s.General().Limits.Tasks; got != 6 {
		t.Fatalf("limits.tasks = %d, want last valid 6", got)
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
	writeFile(t, path, "limits:\n  tasks: 2")
	s.Reload()
	if got := s.General().Limits.Tasks; got != 2 {
		t.Fatalf("limits.tasks = %d, want 2", got)
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
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "review_defaults.story") || strings.Contains(errs[0].Message, "agnet") {
		t.Fatalf("errors = %+v", errs)
	}
	if s.General().ReviewDefaults.Story != Defaults().ReviewDefaults.Story {
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
		"limits.tasks":                func(g *General) { g.Limits.Tasks = 0 },
		"limits.stories":              func(g *General) { g.Limits.Stories = 65 },
		"review_defaults.task":        func(g *General) { g.ReviewDefaults.Task = "maybe" },
		"when_approved_defaults":      func(g *General) { g.WhenApprovedDefaults.Story = "ship" },
		"timeouts_before_investigate": func(g *General) { g.Waiting.TimeoutsBeforeInvestigate = 0 },
		"silent_after":                func(g *General) { g.Health.SilentAfter = 0 },
		"handoff_timeout":             func(g *General) { g.Stop.HandoffTimeout = -1 },
		"rescan_interval":             func(g *General) { g.RescanInterval = 0 },
		"max_depth": func(g *General) {
			g.WorkspaceRoots = []WorkspaceRoot{{Path: "/ws", MaxDepth: 17}}
		},
		"workspace_roots.0.path": func(g *General) {
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
	if len(log.take()) != 0 || s.General().Limits.Tasks != 4 {
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
	for _, key := range []string{`"limits":{"tasks":4,"stories":2}`, `"may_approve_stories":false`, `"tickets_need_user_approval":true`, `"silent_after":"15m"`, `"rescan_interval":"10m"`, `"handoff_timeout":"2m"`} {
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

func TestLoadGeneralDefaultsOnBrokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yaml")
	if got := LoadGeneral(path); got.Stop.HandoffTimeout != Defaults().Stop.HandoffTimeout {
		t.Fatalf("missing file: %+v", got.Stop)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("LoadGeneral created the file: %v", err)
	}
	for _, text := range []string{"stop: [", "no_such_key: 1\n", "limits:\n  tasks: 0\n"} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LoadGeneral(path); got.Limits.Tasks != Defaults().Limits.Tasks {
			t.Fatalf("%q: %+v", text, got)
		}
	}
	if err := os.WriteFile(path, []byte("stop:\n  handoff_timeout: 5s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadGeneral(path); time.Duration(got.Stop.HandoffTimeout) != 5*time.Second {
		t.Fatalf("valid file: %+v", got.Stop)
	}
}

// TestWorkspaceRootsAndIgnorePatternsDoNotRepeat: a write may not list the
// same folder twice (compared cleaned and with ~ expanded) or the same
// ignore pattern twice in one root; the refusal names the later entry. A
// hand-written file with repeats still loads as it is and can be saved again
// with those repeats, but no new one may be added.
func TestWorkspaceRootsAndIgnorePatternsDoNotRepeat(t *testing.T) {
	s, home, _ := openTest(t)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home folder:", err)
	}
	for _, c := range []struct {
		field string
		roots []WorkspaceRoot
	}{
		{"workspace_roots.1.path", []WorkspaceRoot{{Path: "/ws", MaxDepth: 4}, {Path: "/ws/./x/..", MaxDepth: 2}}},
		{"workspace_roots.2.path", []WorkspaceRoot{{Path: "~/ws", MaxDepth: 4}, {Path: "/other", MaxDepth: 4}, {Path: filepath.Join(homeDir, "ws") + "/", MaxDepth: 4}}},
		{"workspace_roots.0.ignore.2", []WorkspaceRoot{{Path: "/ws", MaxDepth: 4, Ignore: []string{"a", "b", "a"}}}},
	} {
		g := Defaults()
		g.WorkspaceRoots = c.roots
		err := s.SetGeneral(g)
		var ae *api.Error
		if !asAPIError(err, &ae) || ae.Code != api.CodeInvalidSettings || fieldOf(err) != c.field {
			t.Errorf("%s: err = %v", c.field, err)
		}
		if _, err := s.UpdateGeneral("", func(g *General) error { g.WorkspaceRoots = c.roots; return nil }); fieldOf(err) != c.field {
			t.Errorf("%s: UpdateGeneral err = %v", c.field, err)
		}
	}
	// The same pattern in two roots is fine.
	g := Defaults()
	g.WorkspaceRoots = []WorkspaceRoot{{Path: "/a", MaxDepth: 4, Ignore: []string{"x"}}, {Path: "/b", MaxDepth: 4, Ignore: []string{"x"}}}
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}

	// A hand-written file with repeats loads as it is.
	writeFile(t, filepath.Join(home, "settings.yaml"), "workspace_roots:\n  - path: /ws\n    ignore: [a, a]\n  - path: /ws/\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 0 {
		t.Fatalf("errors = %+v", errs)
	}
	if roots := s.General().WorkspaceRoots; len(roots) != 2 || len(roots[0].Ignore) != 2 {
		t.Fatalf("roots = %+v", roots)
	}
	// Saving other values keeps the repeats.
	if _, err := s.UpdateGeneral("", func(g *General) error { g.Limits.Tasks = 5; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGeneral(s.General()); err != nil {
		t.Fatal(err)
	}
	// A new repeat is refused.
	_, err = s.UpdateGeneral("", func(g *General) error {
		g.WorkspaceRoots = append(g.WorkspaceRoots, WorkspaceRoot{Path: "/c", MaxDepth: 4}, WorkspaceRoot{Path: "/c", MaxDepth: 4})
		return nil
	})
	if fieldOf(err) != "workspace_roots.3.path" {
		t.Fatalf("err = %v", err)
	}
}

func TestReviewDefaultsOfAFreshHome(t *testing.T) {
	g := Defaults()
	want := api.ReviewDefaults{Story: api.ReviewAgentThenHuman, Task: api.ReviewAgentThenHuman, StoryTask: api.ReviewAgent}
	if g.ReviewDefaults != want {
		t.Fatalf("review defaults %+v, want %+v", g.ReviewDefaults, want)
	}
	wantApproved := api.WhenApprovedDefaults{Story: api.WhenApprovedMerge, Task: api.WhenApprovedMerge, StoryTask: api.WhenApprovedMerge}
	if g.WhenApprovedDefaults != wantApproved {
		t.Fatalf("when approved defaults %+v, want %+v", g.WhenApprovedDefaults, wantApproved)
	}
	s, home, _ := openTest(t)
	raw, err := os.ReadFile(filepath.Join(home, "settings.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "story_task: agent\n") || !strings.Contains(string(raw), "story_task: merge\n") {
		t.Fatalf("a fresh settings file does not name the defaults of tasks in a story:\n%s", raw)
	}
	if s.General().ReviewDefaults != want {
		t.Fatalf("store review defaults %+v", s.General().ReviewDefaults)
	}
}

// A key a settings file leaves out keeps the default of a fresh home.
func TestMissingReviewDefaultsKeepTheDefaults(t *testing.T) {
	s, home, _ := openTest(t)
	path := filepath.Join(home, "settings.yaml")
	writeFile(t, path, "review_defaults:\n  story: human\n  task: skipped\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 0 {
		t.Fatalf("errors %+v", errs)
	}
	want := api.ReviewDefaults{Story: api.ReviewHuman, Task: api.ReviewSkipped, StoryTask: Defaults().ReviewDefaults.StoryTask}
	if g := s.General(); g.ReviewDefaults != want {
		t.Fatalf("review defaults %+v, want %+v", g.ReviewDefaults, want)
	}
	if got := LoadGeneral(path); got.ReviewDefaults != want {
		t.Fatalf("LoadGeneral review defaults %+v", got.ReviewDefaults)
	}
	// Without any review keys the file keeps the defaults of a fresh home.
	writeFile(t, path, "limits:\n  tasks: 3\n")
	s.Reload()
	if g := s.General(); g.ReviewDefaults != Defaults().ReviewDefaults || g.WhenApprovedDefaults != Defaults().WhenApprovedDefaults {
		t.Fatalf("review defaults %+v / %+v", g.ReviewDefaults, g.WhenApprovedDefaults)
	}
	// A file that names them keeps its own.
	writeFile(t, path, "review_defaults:\n  task: human\n  story_task: skipped\n")
	s.Reload()
	if g := s.General(); g.ReviewDefaults.StoryTask != api.ReviewSkipped || g.ReviewDefaults.Task != api.ReviewHuman {
		t.Fatalf("review defaults %+v", g.ReviewDefaults)
	}
}

func TestStoryTaskDefaultsAreValidated(t *testing.T) {
	s, _, _ := openTest(t)
	for field, mutate := range map[string]func(*General){
		"review_defaults.story_task":        func(g *General) { g.ReviewDefaults.StoryTask = "maybe" },
		"when_approved_defaults.story_task": func(g *General) { g.WhenApprovedDefaults.StoryTask = api.WhenApprovedExport },
	} {
		g := Defaults()
		mutate(&g)
		err := s.SetGeneral(g)
		var ae *api.Error
		if !asAPIError(err, &ae) || ae.Code != api.CodeInvalidSettings || !strings.Contains(ae.Message, field) {
			t.Errorf("%s: err = %v", field, err)
			continue
		}
		if fd, _ := ae.Details.(api.FieldDetail); fd.Field != field {
			t.Errorf("%s: err = %v", field, err)
		}
	}
}

func TestOpenTicketsAsPageIsOffByDefault(t *testing.T) {
	if Defaults().UI.OpenTicketsAsPage {
		t.Fatal("ui.open_tickets_as_page is on in the defaults")
	}
	// A settings file written before the key existed behaves as off.
	s, home, _ := openTest(t)
	path := filepath.Join(home, "settings.yaml")
	writeFile(t, path, "limits:\n  tasks: 2\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 0 || s.General().UI.OpenTicketsAsPage {
		t.Fatalf("old file: errors %+v, on %v", errs, s.General().UI.OpenTicketsAsPage)
	}
	if LoadGeneral(path).UI.OpenTicketsAsPage {
		t.Fatal("LoadGeneral reads an old file as on")
	}
	// On when the file says so, and the write keeps it.
	writeFile(t, path, "ui:\n  open_tickets_as_page: true\n")
	s.Reload()
	if !s.General().UI.OpenTicketsAsPage {
		t.Fatal("the key in the file is not read")
	}
	g := s.General()
	g.UI.OpenTicketsAsPage = false
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "open_tickets_as_page: false") || s.General().UI.OpenTicketsAsPage {
		t.Fatalf("the write did not keep the key:\n%s", raw)
	}
}

func TestCEOTicketsNeedUserApprovalIsOnByDefault(t *testing.T) {
	if !Defaults().CEO.TicketsNeedUserApproval {
		t.Fatal("ceo.tickets_need_user_approval is off in the defaults")
	}
	// A file that names other CEO keys but not this one keeps it on.
	s, home, _ := openTest(t)
	path := filepath.Join(home, "settings.yaml")
	writeFile(t, path, "ceo:\n  enabled: true\n  may_approve_stories: true\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 0 || !s.General().CEO.TicketsNeedUserApproval {
		t.Fatalf("errors %+v, on %v", errs, s.General().CEO.TicketsNeedUserApproval)
	}
	if !LoadGeneral(path).CEO.TicketsNeedUserApproval {
		t.Fatal("LoadGeneral reads the file as off")
	}
	writeFile(t, path, "ceo:\n  tickets_need_user_approval: false\n")
	s.Reload()
	if g := s.General(); g.CEO.TicketsNeedUserApproval || !g.CEO.Enabled {
		t.Fatalf("ceo %+v", g.CEO)
	}
}

func TestOpenTicketsAsPageMustBeABoolean(t *testing.T) {
	s, home, _ := openTest(t)
	writeFile(t, filepath.Join(home, "settings.yaml"), "ui:\n  open_tickets_as_page: sometimes\n")
	s.Reload()
	errs := s.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "wrong type") || strings.Contains(errs[0].Message, "sometimes") {
		t.Fatalf("errors = %+v", errs)
	}
	if s.General().UI.OpenTicketsAsPage {
		t.Fatal("a broken value switched the setting on")
	}
}

func TestBranchTemplate(t *testing.T) {
	if got := Defaults().Branches.Template; got != "omo/{slug}" {
		t.Fatalf("default template %q", got)
	}
	s, home, _ := openTest(t)
	path := filepath.Join(home, "settings.yaml")
	writeFile(t, path, "limits:\n  tasks: 2\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 0 || s.General().Branches.Template != "omo/{slug}" {
		t.Fatalf("file without the key: errors %+v, template %q", errs, s.General().Branches.Template)
	}
	writeFile(t, path, "branches:\n  template: \"{user}/{type}/{slug}\"\n")
	s.Reload()
	if got := s.General().Branches.Template; got != "{user}/{type}/{slug}" {
		t.Fatalf("template %q", got)
	}
	// A file with a template that cannot be used is reported; the last
	// valid settings stay.
	for content, want := range map[string]string{
		"branches:\n  template: \"\"\n":                  "must not be empty",
		"branches:\n  template: main\n":                  "must contain {slug} or {number}",
		"branches:\n  template: \"omo/{title}\"\n":       "unknown placeholder {title}",
		"branches:\n  template: \"omo /{slug}\"\n":       "contains a space",
		"branches:\n  template: \"refs/heads/{slug}\"\n": "starts with refs/",
		"branches:\n  template: \"{slug}.lock\"\n":       "ends with .lock",
		"branches:\n  template: [\"x\"]\n":               "wrong type",
	} {
		writeFile(t, path, content)
		s.Reload()
		errs := s.Errors()
		if len(errs) != 1 || !strings.Contains(errs[0].Message, want) {
			t.Errorf("%q: errors %+v, want %q", content, errs, want)
		}
		if got := s.General().Branches.Template; got != "{user}/{type}/{slug}" {
			t.Errorf("%q: the template became %q", content, got)
		}
	}
}

func TestPluginLists(t *testing.T) {
	if got := Defaults().PluginLists; got == nil || len(got) != 0 {
		t.Fatalf("default plugin_lists %#v", got)
	}
	s, _, _ := openTest(t)
	good := []string{"https://raw.githubusercontent.com/someone/lists/main/omo.yaml", "http://127.0.0.1:8080/list.yaml?x=1"}
	g := Defaults()
	g.PluginLists = good
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	if got := s.General().PluginLists; !reflect.DeepEqual(got, good) {
		t.Fatalf("stored %v", got)
	}
	for _, c := range []struct {
		lists []string
		field string
		want  string
	}{
		{[]string{"file:///etc/passwd"}, "plugin_lists.0", "http:// or https://"},
		{[]string{"https://a.example/x", "ftp://a.example/x"}, "plugin_lists.1", "http:// or https://"},
		{[]string{"git@github.com:a/b.git"}, "plugin_lists.0", "http:// or https://"},
		{[]string{"/home/me/list.yaml"}, "plugin_lists.0", "http:// or https://"},
		{[]string{"https:///x"}, "plugin_lists.0", "host"},
		{[]string{"https://me:secret@a.example/x"}, "plugin_lists.0", "user name or password"},
		{[]string{"https://a.example/x#top"}, "plugin_lists.0", "#"},
		{[]string{""}, "plugin_lists.0", "http:// or https://"},
		{[]string{"https://a.example/‮x"}, "plugin_lists.0", "control, invisible or direction"},
		{[]string{"https://a.example/" + strings.Repeat("x", 2048)}, "plugin_lists.0", "2048"},
		{[]string{"https://a.example/x", "https://a.example/x"}, "plugin_lists.1", "repeats plugin_lists.0"},
		{make([]string, 17), "plugin_lists", "at most 16"},
	} {
		g := Defaults()
		g.PluginLists = c.lists
		err := s.SetGeneral(g)
		var ae *api.Error
		if !asAPIError(err, &ae) || ae.Code != api.CodeInvalidSettings || !strings.Contains(ae.Message, c.want) {
			t.Errorf("%.40q: err = %v, want %q", c.lists, err, c.want)
			continue
		}
		if d, ok := ae.Details.(api.FieldDetail); !ok || d.Field != c.field {
			t.Errorf("%.40q: details %+v, want field %s", c.lists, ae.Details, c.field)
		}
	}
	if got := s.General().PluginLists; !reflect.DeepEqual(got, good) {
		t.Fatalf("a refused write changed the lists: %v", got)
	}
}
