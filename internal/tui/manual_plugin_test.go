package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

func manualPluginModel(t *testing.T, acceptsArgs bool, script string) model {
	t.Helper()
	m := testModel(t)
	dir := filepath.Join(m.o.Sup.OfficeDir, plugins.Dir, "report")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"name":"report","manual_args":%t,"hooks":[{"event":"manual","lua":"hook.lua"}]}`, acceptsArgs)
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	m.o.Sup.Plugins, err = plugins.Load(m.o.Sup.OfficeDir, m.o.DB)
	if err != nil {
		t.Fatal(err)
	}
	m.tab = tabPlugins
	m.openSelectedDetail()
	return m
}

func TestPluginDetailTriggersWithoutArgumentsAsynchronously(t *testing.T) {
	m := manualPluginModel(t, false, `assert(#event.data.args == 0); omo.local_set("ran", true)`)
	if !strings.Contains(m.viewDetail(), "r trigger") {
		t.Fatal("missing trigger action")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("manual trigger did not start asynchronously")
	}
	if _, duplicate := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}); duplicate != nil {
		t.Fatal("duplicate trigger started")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if !strings.Contains(m.viewDetail(), "completed") {
		t.Fatalf("completion not shown: %s", m.viewDetail())
	}
	var value string
	if err := m.o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='report' AND key='ran'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "true" {
		t.Fatalf("hook value = %q", value)
	}
}

func TestPluginDetailArgumentEntryPreservesQuotesAndSurfacesErrors(t *testing.T) {
	m := manualPluginModel(t, true, `assert(event.data.args[1] == "two words" and event.data.args[2] == "--flag" and event.data.args[3] == ""); error("report failed")`)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(model)
	if cmd != nil || !strings.Contains(m.viewDetail(), "Arguments") {
		t.Fatal("argument entry did not open")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`"two words" --flag ""`)})
	m = updated.(model)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("arguments not submitted")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if !strings.Contains(m.viewDetail(), "report failed") {
		t.Fatalf("hook error not shown: %s", m.viewDetail())
	}
}

func TestReadOnlyPluginDetailCannotTrigger(t *testing.T) {
	m := manualPluginModel(t, true, `omo.local_set("ran", true)`)
	m.observer = true
	if strings.Contains(m.viewDetail(), "r trigger") {
		t.Fatal("read-only detail offers trigger")
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil || strings.Contains(updated.(model).viewDetail(), "Arguments") {
		t.Fatal("read-only detail accepted trigger")
	}
}

func TestPluginArgumentEntryRejectsMalformedQuotesAndCanCancel(t *testing.T) {
	m := manualPluginModel(t, true, "")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(`"unfinished`)})
	m = updated.(model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil || !strings.Contains(m.viewDetail(), "unterminated quote") {
		t.Fatal("invalid arguments not rejected")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.mode != modeDetail || strings.Contains(m.viewDetail(), "Arguments") {
		t.Fatal("cancel did not return to detail")
	}
}
