package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

func manualPluginModel(t *testing.T, acceptsArgs bool, script string) model {
	t.Helper()
	m := testModel(t)
	dir := filepath.Join(m.o.Sup.OfficeDir, plugins.Dir, "report")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"name":"report","hooks":[{"event":"manual","manual_args":%t,"name":"run","description":"Run action","lua":"hook.lua"}]}`, acceptsArgs)
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

func TestManualBusyStateIsSpecificToPlugin(t *testing.T) {
	m := manualPluginModel(t, false, `omo.local_set("ran", true)`)
	dir := filepath.Join(m.o.Sup.OfficeDir, plugins.Dir, "second")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"second","hooks":[{"event":"manual","name":"run","description":"Run action","lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`omo.local_set("ran", true)`), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	m.o.Sup.Plugins, err = plugins.Load(m.o.Sup.OfficeDir, m.o.DB)
	if err != nil {
		t.Fatal(err)
	}
	updated, first := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(model)
	if first == nil {
		t.Fatal("first trigger missing")
	}
	m.sel[tabPlugins] = 1
	m.openSelectedDetail()
	if !strings.Contains(m.viewDetail(), "r trigger") {
		t.Fatal("different plugin incorrectly marked busy")
	}
	updated, second := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(model)
	if second == nil {
		t.Fatal("different plugin could not start")
	}
	updated, _ = m.Update(first())
	m = updated.(model)
	if !strings.Contains(m.viewDetail(), "Running plugin second") || strings.Contains(m.viewDetail(), "r trigger") {
		t.Fatal("first completion cleared second plugin's busy state")
	}
	updated, _ = m.Update(second())
	m = updated.(model)
	if !strings.Contains(m.viewDetail(), "Plugin second action run completed") {
		t.Fatal("second result missing")
	}
}

func TestPluginDetailSelectsNamedActionAndItsArgumentPolicy(t *testing.T) {
	m := manualPluginModel(t, false, `omo.local_set("wrong_action", true)`)
	dir := filepath.Join(m.o.Sup.OfficeDir, plugins.Dir, "report")
	manifest := `{"name":"report","hooks":[{"event":"manual","name":"run","description":"Build report","lua":"hook.lua"},{"event":"manual","name":"send","description":"Send report to a recipient","manual_args":true,"lua":"send.lua"}]}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "send.lua"), []byte(`assert(event.data.action == "send" and event.data.args[1] == "reader"); omo.local_set("sent", true)`), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	m.o.Sup.Plugins, err = plugins.Load(m.o.Sup.OfficeDir, m.o.DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"run", "Build report", "send", "Send report to a recipient"} {
		if !strings.Contains(m.viewDetail(), want) {
			t.Fatalf("detail missing %q: %s", want, m.viewDetail())
		}
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("ran action before selection")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil || !strings.Contains(m.viewDetail(), "Arguments") {
		t.Fatal("selected action did not request arguments")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("reader")})
	m = updated.(model)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("named action did not start")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if !strings.Contains(m.viewDetail(), "action send completed") {
		t.Fatalf("result = %s", m.viewDetail())
	}
	var wrong int
	if err := m.o.DB.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE key='wrong_action'`).Scan(&wrong); err != nil {
		t.Fatal(err)
	}
	if wrong != 0 {
		t.Fatal("ran unselected action")
	}
}

func TestManualActionSelectorKeepsSelectionVisible(t *testing.T) {
	m := manualPluginModel(t, false, "")
	m.h = 10
	manifest := plugins.Manifest{Name: "report"}
	for i := 0; i < 30; i++ {
		manifest.Hooks = append(manifest.Hooks, plugins.Hook{Event: plugins.EventManual, Name: fmt.Sprintf("action-%02d", i), Description: "Run this action", Lua: "hook.lua"})
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.o.Sup.OfficeDir, plugins.Dir, "report", "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	m.o.Sup.Plugins, err = plugins.Load(m.o.Sup.OfficeDir, m.o.DB)
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(model)
	if !strings.Contains(m.viewDetail(), "› action-00") {
		t.Fatal("first selected action is offscreen")
	}
	for i := 0; i < 29; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(model)
	}
	if !strings.Contains(m.viewDetail(), "› action-29") {
		t.Fatal("last selected action is offscreen")
	}
}

func TestManualActionRowsClickSelectAndChooseVisibleActions(t *testing.T) {
	m := manualPluginModel(t, false, `omo.local_set("ran", true)`)
	dir := filepath.Join(m.o.Sup.OfficeDir, plugins.Dir, "report")
	manifest := `{"name":"report","hooks":[{"event":"manual","name":"run","description":"Run action","lua":"hook.lua"},{"event":"manual","name":"send","description":"Send action","manual_args":true,"lua":"send.lua"}]}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "send.lua"), []byte(`omo.local_set("sent", true)`), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	m.o.Sup.Plugins, err = plugins.Load(m.o.Sup.OfficeDir, m.o.DB)
	if err != nil {
		t.Fatal(err)
	}

	view := m.View()
	x, y, ok := findRenderedTextCell(view, "send")
	if !ok {
		t.Fatalf("manual action row missing:\n%s", ansi.Strip(view))
	}
	keyboard, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	keyboard, _ = keyboard.(model).Update(tea.KeyMsg{Type: tea.KeyDown})
	selected, cmd := updateMouseWithCmd(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if cmd != nil || !selected.manual["report"].selecting || selected.manual["report"].selected != 1 {
		t.Fatalf("different action click state = %+v cmd %v", selected.manual["report"], cmd)
	}
	if got := keyboard.(model).manual["report"]; got != selected.manual["report"] {
		t.Fatalf("different action click state = %+v, keyboard selection state = %+v", selected.manual["report"], got)
	}

	view = selected.View()
	x, y, ok = findRenderedTextCell(view, "send")
	if !ok {
		t.Fatalf("selected manual action row missing:\n%s", ansi.Strip(view))
	}
	chosen, cmd := updateMouseWithCmd(selected, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if cmd != nil || !chosen.manual["report"].editing || chosen.manual["report"].action != "send" {
		t.Fatalf("selected argument action click state = %+v cmd %v", chosen.manual["report"], cmd)
	}
	keyboardChosen, _ := keyboard.(model).updateManualPluginInput(tea.KeyMsg{Type: tea.KeyEnter})
	if got := keyboardChosen.(model).manual["report"]; got != chosen.manual["report"] {
		t.Fatalf("selected argument click state = %+v, keyboard choice state = %+v", chosen.manual["report"], got)
	}

	updated, _ := chosen.updateManualPluginInput(tea.KeyMsg{Type: tea.KeyEsc})
	reset := updated.(model)
	view = reset.View()
	x, y, ok = findRenderedTextCell(view, "run — Run action")
	if !ok {
		t.Fatalf("no-argument action row missing:\n%s", ansi.Strip(view))
	}
	runSelected, cmd := updateMouseWithCmd(reset, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if cmd == nil || runSelected.manual["report"].action != "run" || !runSelected.manual["report"].running {
		t.Fatalf("no-argument action click state = %+v cmd %v", runSelected.manual["report"], cmd)
	}
}

func TestManualTriggerFooterClickUsesTriggerKey(t *testing.T) {
	m := manualPluginModel(t, false, `omo.local_set("ran", true)`)
	view := m.View()
	x, y, ok := findRenderedTextCell(view, "r trigger")
	if !ok {
		t.Fatalf("manual trigger footer missing:\n%s", ansi.Strip(view))
	}
	updated, cmd := updateMouseWithCmd(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if cmd == nil || !updated.manual["report"].running {
		t.Fatalf("manual trigger footer click state = %+v cmd %v", updated.manual["report"], cmd)
	}
}

func TestWrappedManualActionNameKeepsVisibleClickRegion(t *testing.T) {
	m := manualPluginModel(t, false, `omo.local_set("ran", true)`)
	dir := filepath.Join(m.o.Sup.OfficeDir, plugins.Dir, "report")
	manifest := `{"name":"report","hooks":[{"event":"manual","name":"an-action-name-longer-than-the-view","description":"Run action","lua":"hook.lua"}]}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	m.o.Sup.Plugins, err = plugins.Load(m.o.Sup.OfficeDir, m.o.DB)
	if err != nil {
		t.Fatal(err)
	}
	m.w, m.h = 18, 40
	view := m.View()
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "an-action-name") || !strings.Contains(stripped, "-view") {
		t.Fatalf("wrapped manual action missing from detail:\n%s", ansi.Strip(view))
	}
	for _, rect := range m.hitMap.rects {
		if _, ok := rect.action.(pluginActionAction); ok {
			return
		}
	}
	t.Fatalf("wrapped manual action has no click region:\n%s", ansi.Strip(view))
}

func TestManualActionHitsFollowScrolledAndClippedRows(t *testing.T) {
	m := manualPluginModel(t, false, "")
	m.w, m.h = 18, 10
	dir := filepath.Join(m.o.Sup.OfficeDir, plugins.Dir, "report")
	manifest := plugins.Manifest{Name: "report"}
	for i := 0; i < 30; i++ {
		manifest.Hooks = append(manifest.Hooks, plugins.Hook{Event: plugins.EventManual, Name: fmt.Sprintf("action-%02d", i), Description: "Run this action", Lua: "hook.lua"})
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	m.o.Sup.Plugins, err = plugins.Load(m.o.Sup.OfficeDir, m.o.DB)
	if err != nil {
		t.Fatal(err)
	}
	m.View()
	for _, rect := range m.hitMap.rects {
		if _, ok := rect.action.(pluginActionAction); ok {
			t.Fatal("manual action outside the initial detail viewport has a hit")
		}
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = updated.(model)
	for i := 0; i < 29; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(model)
	}
	m.View()
	var sawLast, sawFirst bool
	for _, rect := range m.hitMap.rects {
		if action, ok := rect.action.(pluginActionAction); ok {
			sawLast = sawLast || action.action == 29
			sawFirst = sawFirst || action.action == 0
		}
	}
	if !sawLast || sawFirst {
		t.Fatalf("scrolled manual hits = last:%v first:%v offset:%d", sawLast, sawFirst, m.detail.offset)
	}
}
