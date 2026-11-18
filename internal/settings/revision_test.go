package settings

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// fieldOf returns the field named by a settings error, or "".
func fieldOf(err error) string {
	var ae *api.Error
	if errors.As(err, &ae) {
		if d, ok := ae.Details.(api.FieldDetail); ok {
			return d.Field
		}
	}
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}

func fakeProfile(name string) Profile {
	return Profile{Name: name, CLI: api.CLICustom, Command: "fake", Models: map[string]Model{"a": {}}}
}

func TestRevisionsFollowFiles(t *testing.T) {
	s, home, _ := openTest(t)
	general := s.Revision(SectionGeneral, "")
	if general == NoRevision || len(general) != 16 {
		t.Fatalf("general revision = %q", general)
	}
	if got := s.Revision(SectionProfiles, "x"); got != NoRevision {
		t.Fatalf("revision without a file = %q", got)
	}
	if got := s.Revision(SectionPrompts, "developer"); got != NoRevision {
		t.Fatalf("prompt revision = %q", got)
	}
	if got := s.Revision(SectionProfiles, "../settings"); got != NoRevision {
		t.Fatalf("revision of a bad name = %q", got)
	}

	// A write changes it.
	if err := s.PutProfileIf("", fakeProfile("x")); err != nil {
		t.Fatal(err)
	}
	first := s.Revision(SectionProfiles, "x")
	if first == NoRevision {
		t.Fatal("no revision after a write")
	}
	p := fakeProfile("x")
	p.Command = "other"
	if err := s.PutProfileIf("", p); err != nil {
		t.Fatal(err)
	}
	second := s.Revision(SectionProfiles, "x")
	if second == first {
		t.Fatal("revision unchanged by a write")
	}

	// A hand edit, once found, changes it; the same bytes give the same one.
	writeFile(t, filepath.Join(home, "settings.yaml"), "limits:\n  tasks: 3\n")
	s.Reload()
	if got := s.Revision(SectionGeneral, ""); got == general {
		t.Fatal("revision unchanged by a hand edit")
	}
	g := s.General()
	g.Limits.Tasks = 4
	if err := s.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "profiles", "x.yaml"), "cli: custom\ncommand: fake\nmodels:\n  a: {}\n")
	s.Reload()
	data, _ := os.ReadFile(filepath.Join(home, "profiles", "x.yaml"))
	want := revisionOf(sha256.Sum256(data))
	if got := s.Revision(SectionProfiles, "x"); got != want {
		t.Fatalf("revision = %q, want %q", got, want)
	}

	// A broken file has a revision too, so it can be saved over.
	writeFile(t, filepath.Join(home, "profiles", "x.yaml"), "cli: [")
	s.Reload()
	broken := s.Revision(SectionProfiles, "x")
	if broken == NoRevision || broken == want {
		t.Fatalf("broken revision = %q", broken)
	}
	if err := s.PutProfileIf(broken, fakeProfile("x")); err != nil {
		t.Fatal(err)
	}

	// Removed: no revision.
	if err := s.DeleteProfileIf("", "x"); err != nil {
		t.Fatal(err)
	}
	if got := s.Revision(SectionProfiles, "x"); got != NoRevision {
		t.Fatalf("revision after delete = %q", got)
	}
}

func TestWriteRefusedAfterUnpolledHandEdit(t *testing.T) {
	s, home, log := openTest(t)
	if err := s.PutProfileIf("", fakeProfile("x")); err != nil {
		t.Fatal(err)
	}
	rev := s.Revision(SectionProfiles, "x")
	log.take()
	// Same size and modification time as the store's own file (a coarse
	// file system clock): only the bytes tell it apart.
	path := filepath.Join(home, "profiles", "x.yaml")
	data, _ := os.ReadFile(path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), "command: fake", "command: fakx", 1)
	if edited == string(data) {
		t.Fatalf("unexpected file: %s", data)
	}
	writeFile(t, path, edited)
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	p := fakeProfile("x")
	p.Command = "mine"
	err = s.PutProfileIf(rev, p)
	wantCode(t, err, api.CodeStaleSettings)
	if !strings.Contains(err.Error(), "profiles/x.yaml changed since you loaded it. Load it again to see the change.") {
		t.Fatalf("message = %v", err)
	}
	if got, _ := s.Profile("x"); got.Command != "fakx" {
		t.Fatalf("hand edit not loaded: command = %q", got.Command)
	}
	if raw, _ := os.ReadFile(path); string(raw) != edited {
		t.Fatalf("file overwritten: %s", raw)
	}
	changes := log.take()
	if len(changes) != 1 || !changes[0].FromDisk || changes[0].Name != "x" || changes[0].Section != SectionProfiles {
		t.Fatalf("changes = %+v", changes)
	}
	// The poll finds nothing new afterwards.
	if ch := s.Reload(); len(ch) != 0 {
		t.Fatalf("reload = %+v", ch)
	}
}

func TestWriteWithCurrentRevision(t *testing.T) {
	s, _, log := openTest(t)
	if err := s.PutProfileIf("", fakeProfile("x")); err != nil {
		t.Fatal(err)
	}
	log.take()
	p := fakeProfile("x")
	p.Command = "mine"
	if err := s.PutProfileIf(s.Revision(SectionProfiles, "x"), p); err != nil {
		t.Fatal(err)
	}
	changes := log.take()
	if len(changes) != 1 || changes[0].FromDisk {
		t.Fatalf("changes = %+v", changes)
	}
	if got, _ := s.Profile("x"); got.Command != "mine" {
		t.Fatalf("command = %q", got.Command)
	}
}

func TestCreateOnlyRevision(t *testing.T) {
	s, _, _ := openTest(t)
	if err := s.PutProfileIf(NoRevision, fakeProfile("x")); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.PutProfileIf(NoRevision, fakeProfile("x")), api.CodeStaleSettings)
}

// TestEveryGuardedWriteChecksTheRevision runs each guarded write with a
// revision that does not match: refused, nothing written.
func TestEveryGuardedWriteChecksTheRevision(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	s, err := Open(home, Options{
		PromptDefaults: map[string]string{"developer": "D", "developer-instructions": ""},
		UsageDefaults:  map[string]string{"claude": "c"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutProfileIf("", fakeProfile("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPromptIf("", "developer", "mine"); err != nil {
		t.Fatal(err)
	}
	for _, write := range []error{
		s.PutUsageScriptIf("", "claude", "mine"),
		s.PutUsageScriptIf("", "own", "mine"),
		s.PutSkillSetIf("", superpowers()),
		s.PutPluginIf("", pushover()),
	} {
		if write != nil {
			t.Fatal(write)
		}
	}
	snapshot := func() map[string]string {
		out := map[string]string{}
		filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := os.ReadFile(p)
				out[p] = string(b)
			}
			return nil
		})
		return out
	}
	before := snapshot()
	const stale = "0123456789abcdef"
	writes := map[string]func() error{
		"SetGeneralIf":    func() error { return s.SetGeneralIf(stale, s.General()) },
		"UpdateGeneral":   func() error { _, err := s.UpdateGeneral(stale, func(*General) error { return nil }); return err },
		"PutProfileIf":    func() error { return s.PutProfileIf(stale, fakeProfile("x")) },
		"DeleteProfileIf": func() error { return s.DeleteProfileIf(stale, "x") },
		"PutRoleIf": func() error {
			return s.PutRoleIf(stale, Role{Name: "developer", Models: []string{"x/a"}})
		},
		"PutPromptIf":   func() error { return s.PutPromptIf(stale, "developer", "other") },
		"ResetPromptIf": func() error { return s.ResetPromptIf(stale, "developer") },
		"blank instructions": func() error {
			return s.PutPromptIf(stale, "developer-instructions", " ")
		},
		"PutUsageScriptIf":         func() error { return s.PutUsageScriptIf(stale, "claude", "other") },
		"ResetUsageScriptIf":       func() error { return s.ResetUsageScriptIf(stale, "claude") },
		"ResetUsageScriptIf (own)": func() error { return s.ResetUsageScriptIf(stale, "own") },
		"PutSkillSetIf":            func() error { return s.PutSkillSetIf(stale, superpowers()) },
		"DeleteSkillSetIf":         func() error { return s.DeleteSkillSetIf(stale, "superpowers") },
		"PutPluginIf":              func() error { return s.PutPluginIf(stale, pushover()) },
		"DeletePluginIf":           func() error { return s.DeletePluginIf(stale, "pushover") },
	}
	for name, write := range writes {
		var ae *api.Error
		if err := write(); !errors.As(err, &ae) || ae.Code != api.CodeStaleSettings {
			t.Errorf("%s: err = %v, want stale_settings", name, err)
		}
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatal("a refused write changed a file")
	}
}

func TestUpdateGeneralChangesOnlyItsKeys(t *testing.T) {
	s, home, log := openTest(t)
	g := s.General()
	g.WorkspaceRoots = []WorkspaceRoot{{Path: abs("/ws"), MaxDepth: 3, Ignore: []string{"node_modules"}}}
	g.AgentEnv = map[string]string{"A": "1"}
	if err := s.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}
	log.take()
	got, err := s.UpdateGeneral(s.Revision(SectionGeneral, ""), func(g *General) error {
		g.Health.SilentAfter = Duration(time.Hour)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := g
	want.Health.SilentAfter = Duration(time.Hour)
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(s.General(), want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if ch := log.take(); len(ch) != 1 || ch[0].Section != SectionGeneral || ch[0].FromDisk {
		t.Fatalf("changes = %+v", ch)
	}

	// The change runs on the settings on disk, read just before.
	writeFile(t, filepath.Join(home, "settings.yaml"), "limits:\n  tasks: 9\n")
	got, err = s.UpdateGeneral("", func(g *General) error {
		g.Health.SilentAfter = Duration(2 * time.Hour)
		return nil
	})
	if err != nil || got.Limits.Tasks != 9 || time.Duration(got.Health.SilentAfter) != 2*time.Hour {
		t.Fatalf("got %+v, %v", got, err)
	}
	if ch := log.take(); len(ch) != 2 || !ch[0].FromDisk || ch[1].FromDisk {
		t.Fatalf("changes = %+v", ch)
	}

	// An error of the change or an invalid result writes nothing.
	raw, _ := os.ReadFile(filepath.Join(home, "settings.yaml"))
	boom := errors.New("boom")
	if _, err := s.UpdateGeneral("", func(g *General) error {
		g.Limits.Tasks = 2
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	_, err = s.UpdateGeneral("", func(g *General) error {
		g.Limits.Tasks = 0
		return nil
	})
	wantCode(t, err, api.CodeInvalidSettings)
	if fieldOf(err) != "limits.tasks" {
		t.Fatalf("field = %q", fieldOf(err))
	}
	if after, _ := os.ReadFile(filepath.Join(home, "settings.yaml")); string(after) != string(raw) {
		t.Fatal("a failed update wrote the file")
	}
	if s.General().Limits.Tasks != 9 || len(log.take()) != 0 {
		t.Fatal("a failed update changed the settings")
	}
}

func TestValidationNamesTheField(t *testing.T) {
	s, _, _ := openTest(t)
	general := map[string]func(*General){
		"limits.tasks":                        func(g *General) { g.Limits.Tasks = 65 },
		"limits.stories":                      func(g *General) { g.Limits.Stories = 0 },
		"review_defaults.story":               func(g *General) { g.ReviewDefaults.Story = "x" },
		"when_approved_defaults.task":         func(g *General) { g.WhenApprovedDefaults.Task = "x" },
		"waiting.timeouts_before_investigate": func(g *General) { g.Waiting.TimeoutsBeforeInvestigate = 101 },
		"health.silent_after":                 func(g *General) { g.Health.SilentAfter = Duration(59 * time.Second) },
		"health.question_unanswered_after":    func(g *General) { g.Health.QuestionUnansweredAfter = Duration(8 * 24 * time.Hour) },
		"stop.handoff_timeout":                func(g *General) { g.Stop.HandoffTimeout = Duration(4 * time.Second) },
		"rescan_interval":                     func(g *General) { g.RescanInterval = Duration(time.Second) },
		"workspace_roots": func(g *General) {
			for range 33 {
				g.WorkspaceRoots = append(g.WorkspaceRoots, WorkspaceRoot{Path: abs("/ws")})
			}
		},
		"workspace_roots.1.max_depth": func(g *General) {
			g.WorkspaceRoots = []WorkspaceRoot{{Path: abs("/a")}, {Path: abs("/b"), MaxDepth: 17}}
		},
		"workspace_roots.0.path": func(g *General) { g.WorkspaceRoots = []WorkspaceRoot{{Path: abs("/a\u202e")}} },
		"workspace_roots.0.ignore": func(g *General) {
			g.WorkspaceRoots = []WorkspaceRoot{{Path: abs("/a"), Ignore: make([]string, 101)}}
			for i := range g.WorkspaceRoots[0].Ignore {
				g.WorkspaceRoots[0].Ignore[i] = "x"
			}
		},
		"workspace_roots.0.ignore.1": func(g *General) {
			g.WorkspaceRoots = []WorkspaceRoot{{Path: abs("/a"), Ignore: []string{"ok", "a/**"}}}
		},
		"agent_env": func(g *General) {
			g.AgentEnv = map[string]string{}
			for i := range 201 {
				g.AgentEnv[strings.Repeat("V", i+1)] = "x"
			}
		},
		"agent_env.BIG": func(g *General) { g.AgentEnv = map[string]string{"BIG": strings.Repeat("x", 32<<10+1)} },
	}
	for field, mutate := range general {
		g := Defaults()
		mutate(&g)
		err := s.SetGeneralIf("", g)
		wantCode(t, err, api.CodeInvalidSettings)
		if got := fieldOf(err); got != field {
			t.Errorf("%s: field = %q (%v)", field, got, err)
		}
	}
	// The bounds themselves are allowed.
	g := Defaults()
	g.Limits.Tasks = 64
	g.Waiting.TimeoutsBeforeInvestigate = 100
	g.Health.SilentAfter = Duration(time.Minute)
	g.Health.QuestionUnansweredAfter = Duration(7 * 24 * time.Hour)
	g.Stop.HandoffTimeout = Duration(5 * time.Second)
	g.RescanInterval = Duration(time.Minute)
	if err := s.SetGeneralIf("", g); err != nil {
		t.Fatal(err)
	}

	many := make([]string, 101)
	profiles := map[string]func(*Profile){
		"name":                    func(p *Profile) { p.Name = "Bad Name" },
		"cli":                     func(p *Profile) { p.CLI = "x" },
		"command":                 func(p *Profile) { p.Command = " " },
		"args":                    func(p *Profile) { p.Args = many },
		"env.A":                   func(p *Profile) { p.Env = map[string]string{"A": strings.Repeat("x", 32<<10+1)} },
		"account_dir":             func(p *Profile) { p.AccountDir = abs("/a\x1b") },
		"usage_script":            func(p *Profile) { p.UsageScript = "Bad" },
		"prompt_delivery.retries": func(p *Profile) { n := -1; p.PromptDelivery.Retries = &n },
		"models":                  func(p *Profile) { p.Models = nil },
		"models.a.args":           func(p *Profile) { p.Models = map[string]Model{"a": {Args: many}} },
		"models.a.env.B": func(p *Profile) {
			p.Models = map[string]Model{"a": {Env: map[string]string{"B": strings.Repeat("x", 32<<10+1)}}}
		},
		"models.a.description":       func(p *Profile) { p.Models = map[string]Model{"a": {Description: strings.Repeat("é", 201)}} },
		"models.b.description":       func(p *Profile) { p.Models = map[string]Model{"a": {}, "b": {Description: "two\nlines"}} },
		"models.c.description":       func(p *Profile) { p.Models = map[string]Model{"c": {Description: "a\u200bb"}} },
		"prompt_delivery.delay":      func(p *Profile) { d := Duration(-1); p.PromptDelivery.Delay = &d },
		"prompt_delivery.retry_wait": func(p *Profile) { d := Duration(-1); p.PromptDelivery.RetryWait = &d },
	}
	for field, mutate := range profiles {
		p := fakeProfile("x")
		mutate(&p)
		err := s.PutProfileIf("", p)
		wantCode(t, err, api.CodeValidation)
		if got := fieldOf(err); got != field {
			t.Errorf("%s: field = %q (%v)", field, got, err)
		}
	}
	p := fakeProfile("x")
	p.Models = map[string]Model{"a": {Description: strings.Repeat("é", 200)}}
	if err := s.PutProfileIf("", p); err != nil {
		t.Fatal(err)
	}

	err := s.PutRoleIf("", Role{Name: "developer", Models: []string{"x/a", "x/nope"}})
	wantCode(t, err, api.CodeValidation)
	if fieldOf(err) != "models.1" {
		t.Errorf("role field = %q", fieldOf(err))
	}
	err = s.PutRoleIf("", Role{Name: "developer", Assignment: "x"})
	wantCode(t, err, api.CodeValidation)
	if fieldOf(err) != "assignment" {
		t.Errorf("role field = %q", fieldOf(err))
	}
}

// TestFileErrorsQuoteNoValues is the guard for settings errors: no message
// of a file that cannot be loaded, and no refusal of a write, ever quotes a
// value. Each case puts a secret where a value belongs.
func TestFileErrorsQuoteNoValues(t *testing.T) {
	const secret = "sk-ant-SECRET"
	files := map[string]struct{ file, content, want string }{
		"profile type":          {"profiles/x.yaml", "cli: custom\ncommand: x\nmodels: \"" + secret + "\"\n", "line 3"},
		"general int":           {"settings.yaml", "limits:\n  tasks: " + secret + "\n", "line 2"},
		"general duration":      {"settings.yaml", "health:\n  silent_after: " + secret + "\n", ""},
		"general enum":          {"settings.yaml", "review_defaults:\n  story: " + secret + "\n", "review_defaults.story"},
		"general unclosed":      {"settings.yaml", "limits:\n  tasks: [" + secret + "\n", "line"},
		"general quote":         {"settings.yaml", "agent_env:\n  A: \"" + secret + "\n", "line"},
		"general map key":       {"settings.yaml", "agent_env:\n  ? [" + secret + "]\n  : x\n", ""},
		"general twice":         {"settings.yaml", "limits:\n  tasks: 1\n  tasks: " + secret + "\n", "line 3"},
		"general root":          {"settings.yaml", "workspace_roots:\n  - path: " + secret + "\n", "workspace_roots.0.path"},
		"general ignore":        {"settings.yaml", "workspace_roots:\n  - path: " + abs("/a") + "\n    ignore: [\"**" + secret + "\"]\n", "workspace_roots.0.ignore.0"},
		"general env name":      {"settings.yaml", "agent_env:\n  \"A=" + secret + "\": x\n", "agent_env"},
		"profile cli":           {"profiles/x.yaml", "cli: " + secret + "\ncommand: x\nmodels:\n  a: {}\n", "cli"},
		"profile usage script":  {"profiles/x.yaml", "cli: custom\ncommand: x\nusage_script: " + secret + "\nmodels:\n  a: {}\n", "usage_script"},
		"profile description":   {"profiles/x.yaml", "cli: custom\ncommand: x\nmodels:\n  a:\n    description: \"" + secret + "\\n\"\n", "models.a.description"},
		"profile delay":         {"profiles/x.yaml", "cli: custom\ncommand: x\nprompt_delivery:\n  delay: " + secret + "\nmodels:\n  a: {}\n", ""},
		"role assignment":       {"roles/developer.yaml", "models: []\nassignment: " + secret + "\n", "assignment"},
		"role model":            {"roles/developer.yaml", "models: [\"" + secret + "\"]\n", "models.0"},
		"role type":             {"roles/developer.yaml", "models: " + secret + "\n", "line 1"},
		"project unknown key":   {"projects/p.yaml", "name: p\npath: " + abs("/p") + "\nstory_merge: " + secret + "\n", "story_merge"},
		"project merge mode":    {"projects/p.yaml", "name: p\npath: " + abs("/p") + "\nmerge:\n  task: " + secret + "\n", "merge.task"},
		"general merge default": {"settings.yaml", "merge_defaults:\n  story_task: " + secret + "\n", "merge_defaults.story_task"},
		"project path":          {"projects/p.yaml", "name: p\npath: " + secret + "\n", "path"},
		"project type":          {"projects/p.yaml", "name: [" + secret + "]\npath: " + abs("/p") + "\n", "line 1"},
		"model usage window":    {"profiles/x.yaml", "cli: custom\ncommand: x\nmodels:\n  a:\n    usage_windows: [" + secret + "]\n", "models.a.usage_windows.0"},
		"usage threshold":       {"settings.yaml", "usage:\n  soft_percent: " + secret + "\n", "line 2"},
		"usage window name":     {"settings.yaml", "usage:\n  windows:\n    " + secret + ": {}\n", "usage.windows"},
		"usage script":          {"usage/x.lua", secret + "\u202e\n", "usage script"},
		"skill set repository":  {"skillsets/x.yaml", "repository: ext::" + secret + "\n", "repository"},
		"skill set ref":         {"skillsets/x.yaml", "repository: " + abs("/r") + "\nref: -" + secret + "\n", "ref"},
		"skill set update":      {"skillsets/x.yaml", "repository: " + abs("/r") + "\nupdate: " + secret + "\n", "update"},
		"skill set role":        {"skillsets/x.yaml", "repository: " + abs("/r") + "\ninstructions:\n  " + secret + ": x\n", "instructions"},
		"skill set text":        {"skillsets/x.yaml", "repository: " + abs("/r") + "\ninstructions:\n  pm: \"" + secret + "\\u202e\"\n", "instructions.pm"},
		"plugin source":         {"plugins/x.yaml", "source: fd::" + secret + "\n", "source"},
		"plugin subpath":        {"plugins/x.yaml", "source: " + abs("/p") + "\nsubpath: ../" + secret + "\n", "subpath"},
		"plugin config":         {"plugins/x.yaml", "source: " + abs("/p") + "\nconfig: " + secret + "\n", "line 2"},
	}
	for name, c := range files {
		t.Run(name, func(t *testing.T) {
			s, home, log := openTest(t)
			writeFile(t, filepath.Join(home, filepath.FromSlash(c.file)), c.content)
			s.Reload()
			errs := s.Errors()
			if len(errs) != 1 {
				t.Fatalf("errors = %+v", errs)
			}
			msg := errs[0].Message
			if strings.Contains(msg, "SECRET") {
				t.Fatalf("message quotes the value: %q", msg)
			}
			if !strings.Contains(msg, c.want) {
				t.Fatalf("message = %q, want it to hold %q", msg, c.want)
			}
			for _, ch := range log.take() {
				if ch.Err != nil && strings.Contains(ch.Err.Message, "SECRET") {
					t.Fatalf("change quotes the value: %+v", ch.Err)
				}
			}
		})
	}

	// Refused writes.
	s, _, _ := openTest(t)
	g := Defaults()
	g.ReviewDefaults.Task = secret
	g.WorkspaceRoots = []WorkspaceRoot{{Path: abs("/a"), Ignore: []string{secret + "/"}}}
	writes := map[string]error{}
	writes["general"] = s.SetGeneralIf("", g)
	g = Defaults()
	g.WorkspaceRoots = []WorkspaceRoot{{Path: secret}}
	writes["root"] = s.SetGeneralIf("", g)
	g = Defaults()
	g.AgentEnv = map[string]string{"A": strings.Repeat(secret, 4000)}
	writes["env size"] = s.SetGeneralIf("", g)
	p := fakeProfile("x")
	p.CLI = secret
	writes["cli"] = s.PutProfileIf("", p)
	p = fakeProfile("x")
	p.UsageScript = secret
	writes["usage script"] = s.PutProfileIf("", p)
	writes["assignment"] = s.PutRoleIf("", Role{Name: "developer", Assignment: secret})
	k := superpowers()
	k.Repository = "https://user:" + secret + "@host/x\u202e"
	writes["skill set repository"] = s.PutSkillSetIf("", k)
	k = superpowers()
	k.Ref = secret + ".."
	writes["skill set ref"] = s.PutSkillSetIf("", k)
	k = superpowers()
	k.Instructions = map[string]string{secret: "x"}
	writes["skill set role"] = s.PutSkillSetIf("", k)
	pl := pushover()
	pl.Source = "ext::" + secret
	writes["plugin source"] = s.PutPluginIf("", pl)
	pl = pushover()
	pl.Subpath = "../" + secret
	writes["plugin subpath"] = s.PutPluginIf("", pl)
	writes["usage script"] = s.PutUsageScriptIf("", "x", secret+"\x1b")
	for name, err := range writes {
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: message quotes the value: %v", name, err)
		}
	}
}

// hiddenSamples are characters omo never shows as they are.
var hiddenSamples = []string{"\u202e", "\u200b", "\x1b", "\u2066", "\U000e0041", "\u00ad"}

// TestNamesRefuseHiddenCharacters is the guard for names and labels the
// store accepts: none may be set with a character omo never shows as it is.
// Project names: TestProjectNamesRefuseHiddenCharacters.
func TestNamesRefuseHiddenCharacters(t *testing.T) {
	s, _, _ := openWithPrompts(t, map[string]string{"developer": "D"})
	for _, h := range hiddenSamples {
		name := "a" + h + "b"
		pr := fakeProfile("x")
		pr.Models = map[string]Model{"a": {Description: name}}
		wantCode(t, s.PutProfileIf("", pr), api.CodeValidation)
		pr = fakeProfile("x")
		pr.Models = map[string]Model{name: {}}
		wantCode(t, s.PutProfileIf("", pr), api.CodeValidation)
		pr = fakeProfile("x")
		pr.Env = map[string]string{name: "v"}
		wantCode(t, s.PutProfileIf("", pr), api.CodeValidation)
		pr = fakeProfile("x")
		pr.Models = map[string]Model{"a": {Env: map[string]string{name: "v"}}}
		wantCode(t, s.PutProfileIf("", pr), api.CodeValidation)
		pr = fakeProfile("x")
		pr.AccountDir = abs("/" + name)
		wantCode(t, s.PutProfileIf("", pr), api.CodeValidation)
		wantCode(t, s.PutProfileIf("", fakeProfile(name)), api.CodeValidation)
		wantCode(t, s.PutRoleIf("", Role{Name: "developer", Models: []string{"x/" + name}}), api.CodeValidation)
		wantCode(t, s.PutRoleIf("", Role{Name: name}), api.CodeNotFound)
		wantCode(t, s.PutPromptIf("", name, "t"), api.CodeNotFound)
		wantCode(t, s.PutUsageScriptIf("", name, "t"), api.CodeValidation)
		wantCode(t, s.PutUsageScriptIf("", "x", name), api.CodeValidation)
		k := superpowers()
		k.Name = name
		wantCode(t, s.PutSkillSetIf("", k), api.CodeValidation)
		k = superpowers()
		k.Instructions = map[string]string{"pm": name}
		wantCode(t, s.PutSkillSetIf("", k), api.CodeValidation)
		k = superpowers()
		k.Repository = abs("/" + name)
		wantCode(t, s.PutSkillSetIf("", k), api.CodeValidation)
		pl := pushover()
		pl.Name = name
		wantCode(t, s.PutPluginIf("", pl), api.CodeValidation)
		pl = pushover()
		pl.Subpath = name
		wantCode(t, s.PutPluginIf("", pl), api.CodeValidation)
		pl = pushover()
		pl.Source = abs("/" + name)
		wantCode(t, s.PutPluginIf("", pl), api.CodeValidation)
		pr = fakeProfile("x")
		pr.Models = map[string]Model{"a": {UsageWindows: []string{name}}}
		wantCode(t, s.PutProfileIf("", pr), api.CodeValidation)

		for _, g := range []func(*General){
			func(g *General) { g.WorkspaceRoots = []WorkspaceRoot{{Path: abs("/" + name)}} },
			func(g *General) { g.WorkspaceRoots = []WorkspaceRoot{{Path: abs("/a"), Ignore: []string{name}}} },
			func(g *General) { g.AgentEnv = map[string]string{name: "v"} },
			func(g *General) { g.Usage.Windows = map[string]api.UsageThreshold{name: {}} },
		} {
			gen := Defaults()
			g(&gen)
			wantCode(t, s.SetGeneralIf("", gen), api.CodeInvalidSettings)
		}
	}
}

// TestWritesAreAtomicAndPrivate: every write replaces its file through a
// temporary file in the same folder and leaves it readable by the user only.
func TestWritesAreAtomicAndPrivate(t *testing.T) {
	s, home, _ := openWithPrompts(t, map[string]string{"developer": "D"})
	if err := s.PutProfileIf("", fakeProfile("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutRoleIf("", Role{Name: "developer", Models: []string{"x/a"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPromptIf("", "developer", "mine"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutUsageScriptIf("", "fake", "mine"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSkillSetIf("", superpowers()); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPluginIf("", pushover()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateGeneral("", func(g *General) error { g.AgentEnv["API_KEY"] = "sk"; return nil }); err != nil {
		t.Fatal(err)
	}
	repo := newRepo(t, t.TempDir(), "r")
	if _, err := s.AddProject(repo, "r"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"settings.yaml", "profiles/x.yaml", "roles/developer.yaml", "prompts/developer.md", "projects/r.yaml",
		"usage/fake.lua", "skillsets/superpowers.yaml", "plugins/pushover.yaml"} {
		info, err := os.Stat(filepath.Join(home, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); runtime.GOOS != "windows" && mode != 0o600 {
			t.Errorf("%s: mode %o, want 600", f, mode)
		}
	}
	filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".omo-write-") {
			t.Errorf("temporary file left: %s", p)
		}
		return nil
	})
}
