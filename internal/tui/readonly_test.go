package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Typing into a worker agent's session by accident derails it, so peeks open
// read-only everywhere except the CEO — whose session is the whole point of
// the home screen. Ctrl+T still toggles either way.
func TestDefaultReadOnlyByAgent(t *testing.T) {
	cases := []struct {
		agent, ceo string
		want       bool
	}{
		{"ceo-ada", "ceo-ada", false},        // the CEO is who you talk to
		{"developer-jason", "ceo-ada", true}, // everyone else is observed
		{"reviewer-sara", "ceo-ada", true},
		{"pm-alex", "ceo-ada", true},
		{"ceo-ada", "", true}, // no known CEO: stay safe
	}
	for _, c := range cases {
		if got := defaultReadOnly(c.agent, c.ceo); got != c.want {
			t.Errorf("defaultReadOnly(%q, ceo=%q) = %v, want %v", c.agent, c.ceo, got, c.want)
		}
	}
}

func TestReadOnlyRowClicksSelectAndOpenWithoutWrites(t *testing.T) {
	m := testModel(t)
	m.observer = true
	addLivingAgent(t, m, "developer-first", "developer")
	addLivingAgent(t, m, "developer-second", "developer")
	m.tab = tabAgents
	view := m.View()
	x, y, ok := findRenderedTextCell(view, "developer-second")
	if !ok {
		t.Fatalf("observer agent row missing:\n%s", ansi.Strip(view))
	}
	selected := updateMouse(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if selected.sel[tabAgents] != 1 || selected.mode != modeOverview {
		t.Fatalf("observer row selection = %d mode %v", selected.sel[tabAgents], selected.mode)
	}

	if _, err := m.o.DB.Exec(`INSERT INTO messages(from_agent,to_target,subject,body) VALUES('ceo-test','user','status','still unread')`); err != nil {
		t.Fatal(err)
	}
	m.tab = tabMessages
	view = m.View()
	x, y, ok = findRenderedTextCell(view, "status")
	if !ok {
		t.Fatalf("observer message row missing:\n%s", ansi.Strip(view))
	}
	opened := updateMouse(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if opened.mode != modeDetail {
		t.Fatalf("observer message click mode = %v", opened.mode)
	}
	var readAt any
	if err := m.o.DB.QueryRow(`SELECT read_at FROM messages LIMIT 1`).Scan(&readAt); err != nil {
		t.Fatal(err)
	}
	if readAt != nil {
		t.Fatalf("observer message click marked message read: %#v", readAt)
	}
}

func TestReadOnlyPluginClickHasNoTriggerHit(t *testing.T) {
	m := manualPluginModel(t, false, `omo.local_set("ran", true)`)
	m.observer = true
	m.mode = modeOverview
	m.tab = tabPlugins
	view := m.View()
	x, y, ok := findRenderedTextCell(view, "report")
	if !ok {
		t.Fatalf("observer plugin row missing:\n%s", ansi.Strip(view))
	}
	opened := updateMouse(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if opened.mode != modeDetail {
		t.Fatalf("observer plugin row click mode = %v", opened.mode)
	}
	view = opened.View()
	for _, rect := range opened.hitMap.rects {
		if _, ok := rect.action.(pluginActionAction); ok {
			t.Fatalf("observer plugin detail registered trigger hit: %+v", rect)
		}
	}
	updated, cmd := opened.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil || updated.(model).mode != modeDetail {
		t.Fatalf("observer plugin trigger mutated mode=%v cmd=%v", updated.(model).mode, cmd)
	}
	var stored int
	if err := m.o.DB.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE plugin='report' AND key='ran'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatalf("observer plugin click wrote plugin storage: %d rows", stored)
	}
}
