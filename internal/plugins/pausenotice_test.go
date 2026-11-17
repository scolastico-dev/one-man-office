package plugins

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// shippedPlugin copies the repository's plugin name into the env's root.
func (e *env) shippedPlugin(name string) Manifest {
	e.t.Helper()
	src := filepath.Join("..", "..", "plugins", name)
	entries, err := os.ReadDir(src)
	if err != nil {
		e.t.Fatal(err)
	}
	dir := filepath.Join(e.root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		e.t.Fatal(err)
	}
	for _, en := range entries {
		raw, err := os.ReadFile(filepath.Join(src, en.Name()))
		if err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, en.Name()), raw, 0o600); err != nil {
			e.t.Fatal(err)
		}
	}
	m, err := ReadManifest(dir)
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

// TestPauseNoticePlugin runs plugins/pause-notice against fake agents: a
// pause tells every running agent to halt (the CEO with its own text), a
// resume tells the ones that were told and still run to continue, and an
// agent started during the pause gets neither.
func TestPauseNoticePlugin(t *testing.T) {
	desk := &fakeDesk{}
	ceo := RunningAgent{Terminal: "term-ceo", Name: "Ada", Role: "ceo", Model: "fake/a", Purpose: "work"}
	pm := RunningAgent{Terminal: "term-pm", Name: "Pia", Role: "pm", Model: "fake/a", Purpose: "work", Ticket: 3}
	dev := RunningAgent{Terminal: "term-dev", Name: "Bob", Role: "developer", Model: "fake/a", Purpose: "work", Ticket: 7}
	rev := RunningAgent{Terminal: "term-rev", Name: "Rex", Role: "reviewer", Model: "fake/a", Purpose: "review", Ticket: 7}
	late := RunningAgent{Terminal: "term-late", Name: "Lea", Role: "ceo", Model: "fake/a", Purpose: "work"}
	desk.set(ceo, pm, dev, rev)

	e := newEnvWithDesk(t, desk)
	m := e.shippedPlugin("pause-notice")
	halt, _ := m.DefaultConfig["halt_message"].(string)
	ceoHalt, _ := m.DefaultConfig["ceo_halt_message"].(string)
	resume, _ := m.DefaultConfig["resume_message"].(string)
	if halt == "" || ceoHalt == "" || resume == "" || halt == ceoHalt {
		t.Fatalf("default_config %v", m.DefaultConfig)
	}
	// The script's own fallbacks are the manifest's defaults.
	src, err := os.ReadFile(filepath.Join(e.root, "pause-notice", "pause.lua"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{halt, ceoHalt, resume} {
		if !strings.Contains(string(src), `"`+s+`"`) {
			t.Fatalf("pause.lua does not fall back to %q", s)
		}
	}
	// As installed: the defaults are the configuration.
	e.load(enabled("pause-notice", map[string]any(m.DefaultConfig)))

	e.m.Dispatch(Event{Name: EventPaused, At: time.Now(), Data: map[string]any{}})
	eventually(t, "the halt texts", func() bool { return desk.total() == 4 })
	for term, want := range map[string]string{"term-ceo": ceoHalt, "term-pm": halt, "term-dev": halt, "term-rev": halt} {
		if got := desk.texts(term); !slices.Equal(got, []string{want}) {
			t.Fatalf("%s got %q", term, got)
		}
	}

	// During the pause the reviewer ends and the user starts a CEO.
	desk.set(ceo, pm, dev, late)
	e.m.Dispatch(Event{Name: EventResumed, At: time.Now(), Data: map[string]any{}})
	eventually(t, "the resume texts", func() bool { return desk.total() == 7 })
	for term, want := range map[string][]string{
		"term-ceo": {ceoHalt, resume}, "term-pm": {halt, resume}, "term-dev": {halt, resume},
		"term-rev": {halt}, "term-late": nil,
	} {
		if got := desk.texts(term); !slices.Equal(got, want) {
			t.Fatalf("%s got %q, want %q", term, got, want)
		}
	}
	eventually(t, "the log", func() bool { return strings.Contains(e.logText("pause-notice"), "told 3 agents to continue") })
	if got := e.stored("pause-notice", "halted"); got != "" {
		t.Fatalf("halted kept after the resume: %s", got)
	}

	// A second resume (nothing was halted) sends nothing.
	e.m.Dispatch(Event{Name: EventResumed, At: time.Now()})
	e.m.Dispatch(Event{Name: EventTicketCreated, At: time.Now()})
	time.Sleep(200 * time.Millisecond)
	if desk.total() != 7 {
		t.Fatalf("%d texts after a second resume", desk.total())
	}
}

// TestPauseNoticePluginConfiguredTexts: the configured texts are sent; an
// empty one falls back to the default.
func TestPauseNoticePluginConfiguredTexts(t *testing.T) {
	desk := &fakeDesk{}
	desk.set(RunningAgent{Terminal: "term-ceo", Role: "ceo"}, RunningAgent{Terminal: "term-dev", Role: "developer", Ticket: 1})
	e := newEnvWithDesk(t, desk)
	m := e.shippedPlugin("pause-notice")
	e.load(enabled("pause-notice", map[string]any{"halt_message": "Hold still.", "ceo_halt_message": "  ", "resume_message": "Go on."}))
	e.m.Dispatch(Event{Name: EventPaused, At: time.Now()})
	e.m.Dispatch(Event{Name: EventResumed, At: time.Now()})
	eventually(t, "the texts", func() bool { return desk.total() == 4 })
	if got := desk.texts("term-dev"); !slices.Equal(got, []string{"Hold still.", "Go on."}) {
		t.Fatalf("developer got %q", got)
	}
	if got := desk.texts("term-ceo"); !slices.Equal(got, []string{m.DefaultConfig["ceo_halt_message"].(string), "Go on."}) {
		t.Fatalf("CEO got %q", got)
	}
}
