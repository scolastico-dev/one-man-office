package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/supervisor"
)

func testModel(t *testing.T) model {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	cfg := config.Defaults()
	sup := supervisor.New(&cfg, d, gitops.New(), t.TempDir(), nil)
	return model{o: &office.Office{DB: d, Sup: sup}, mode: modeOverview, w: 160, h: 30, hitMap: &hitMap{}}
}

func addLivingAgent(t *testing.T, m model, name, role string) {
	t.Helper()
	if err := db.InsertAgent(m.o.DB, db.Agent{Name: name, Role: role, Profile: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(m.o.DB, name, "working"); err != nil {
		t.Fatal(err)
	}
}

type recordingPeekInput struct {
	text    string
	submits int
}

func findRenderedCell(view string, row int, text string) (int, bool) {
	lines := strings.Split(ansi.Strip(view), "\n")
	if row < 0 || row >= len(lines) {
		return 0, false
	}
	x := strings.Index(lines[row], text)
	if x < 0 {
		return 0, false
	}
	return ansi.StringWidth(lines[row][:x]), true
}

func findRenderedTextCell(view, text string) (int, int, bool) {
	for row := range strings.Split(ansi.Strip(view), "\n") {
		if x, ok := findRenderedCell(view, row, text); ok {
			return x, row, true
		}
	}
	return 0, 0, false
}

func updateMouse(m model, x, y int, button tea.MouseButton, action tea.MouseAction) model {
	updated, _ := m.Update(tea.MouseMsg{X: x, Y: y, Button: button, Action: action})
	return updated.(model)
}

func (r *recordingPeekInput) SendText(text string) error { r.text += text; return nil }
func (r *recordingPeekInput) SendSubmit() error          { r.submits++; return nil }

func TestPeekEnterUsesDelayedSubmitPath(t *testing.T) {
	input := &recordingPeekInput{}
	if err := sendPeekInput(input, tea.KeyMsg{Type: tea.KeyEnter}); err != nil {
		t.Fatal(err)
	}
	if input.submits != 1 || input.text != "" {
		t.Fatalf("submit calls=%d raw text=%q", input.submits, input.text)
	}
}

func TestReadActionOnlyAppearsForUnreadUserMail(t *testing.T) {
	m := testModel(t)
	m.tab = tabMessages
	if _, err := m.o.DB.Exec(`INSERT INTO messages
		(from_agent, to_target, subject, body) VALUES ('ceo-ada', 'user', 'question', 'answer me')`); err != nil {
		t.Fatal(err)
	}
	if view := m.viewOverview(); !strings.Contains(view, "x read") {
		t.Fatalf("unread user mail should offer x read:\n%s", view)
	}
	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = updated.(model)
	if view := m.viewOverview(); strings.Contains(view, "x read") {
		t.Fatalf("read mail should not offer x read:\n%s", view)
	}
}

func TestFooterShowsUnreadUserMail(t *testing.T) {
	m := testModel(t)
	if _, err := m.o.DB.Exec(`INSERT INTO messages
		(from_agent, to_target, subject, body) VALUES ('ceo-ada', 'user', 'question', 'answer me')`); err != nil {
		t.Fatal(err)
	}
	footer := m.agentFooter([]string{"q quit"})
	if !strings.Contains(footer, "✉ USER 1 unread") {
		t.Fatalf("footer has no unread-user indicator: %q", footer)
	}
}

func TestOverviewHeaderShowsSafeMode(t *testing.T) {
	m := testModel(t)
	m.o.Sup.EnterSafeMode()
	if view := m.viewOverview(); !strings.Contains(view, "SAFE MODE") {
		t.Fatalf("safe-mode indicator missing:\n%s", view)
	}
}

func TestAgentOverviewShowsLastCheckedUsageAsASCIIBars(t *testing.T) {
	m := testModel(t)
	checked := time.Date(2026, 8, 23, 10, 5, 0, 0, time.Local)
	if err := db.UpsertModelUsageSnapshot(m.o.DB, db.ModelUsageSnapshot{Provider: "codex", UsedPercent: 60, FetchedAt: checked}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertModelUsageSnapshot(m.o.DB, db.ModelUsageSnapshot{Provider: "claude", UsedPercent: 40, HasSession: true, SessionUsedPercent: 75, FetchedAt: checked}); err != nil {
		t.Fatal(err)
	}
	view := ansi.Strip(m.viewOverview())
	for _, want := range []string{"Usage — last successful check", "claude weekly", "claude session", "codex weekly", "[############--------] 60.0%", "[###############-----] 75.0%"} {
		if !strings.Contains(view, want) {
			t.Errorf("agent overview missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"claude-opus", "codex-luna"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("agent overview contains individual profile %q:\n%s", unwanted, view)
		}
	}
}

func TestAgentOverviewShowsLatestSuccessfulUsageCheckTime(t *testing.T) {
	m := testModel(t)
	firstCheck := time.Date(2026, 8, 23, 8, 5, 0, 0, time.UTC)
	latestCheck := time.Date(2026, 8, 23, 10, 5, 0, 0, time.UTC)
	for _, snapshot := range []db.ModelUsageSnapshot{
		{Provider: "codex", UsedPercent: 60, FetchedAt: firstCheck},
		{Provider: "claude", UsedPercent: 40, FetchedAt: latestCheck},
	} {
		if err := db.UpsertModelUsageSnapshot(m.o.DB, snapshot); err != nil {
			t.Fatal(err)
		}
	}

	want := "Usage — last successful check: " + latestCheck.Local().Format("2006-01-02 15:04:05")
	if view := ansi.Strip(m.viewOverview()); !strings.Contains(view, want) {
		t.Fatalf("agent overview missing latest usage check time %q:\n%s", want, view)
	}
}

func TestAgentOverviewDistinguishesUsageCredentialScopes(t *testing.T) {
	m := testModel(t)
	checked := time.Date(2026, 8, 23, 10, 5, 0, 0, time.Local)
	for _, snapshot := range []db.ModelUsageSnapshot{
		{Provider: "claude", Scope: "claude:/accounts/work/.credentials.json", UsedPercent: 20, FetchedAt: checked},
		{Provider: "claude", Scope: "claude:/accounts/home/.credentials.json", UsedPercent: 60, FetchedAt: checked},
	} {
		if err := db.UpsertModelUsageSnapshot(m.o.DB, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	view := ansi.Strip(m.viewOverview())
	for _, want := range []string{"claude (work) weekly", "claude (home) weekly"} {
		if !strings.Contains(view, want) {
			t.Errorf("agent overview missing %q:\n%s", want, view)
		}
	}
}

func TestAgentOverviewKeepsWindowAndDisambiguatesEqualAccountNames(t *testing.T) {
	m := testModel(t)
	checked := time.Date(2026, 8, 23, 10, 5, 0, 0, time.Local)
	for _, snapshot := range []db.ModelUsageSnapshot{
		{Provider: "claude", Scope: "claude:/accounts/a/work/.credentials.json", UsedPercent: 20, FetchedAt: checked},
		{Provider: "claude", Scope: "claude:/accounts/b/work/.credentials.json", UsedPercent: 60, FetchedAt: checked},
	} {
		if err := db.UpsertModelUsageSnapshot(m.o.DB, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	view := ansi.Strip(m.viewOverview())
	labels := map[string]bool{}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "20.0%") || strings.Contains(line, "60.0%") {
			if !strings.Contains(line, "weekly") || !strings.Contains(line, "#") {
				t.Fatalf("ambiguous or truncated usage label: %q\n%s", line, view)
			}
			labels[strings.TrimSpace(strings.Split(line, "[")[0])] = true
		}
	}
	if len(labels) != 2 {
		t.Fatalf("usage labels are not distinct: %#v\n%s", labels, view)
	}
}

func TestAgentOverviewDisambiguatesLongAccountNamesAfterTruncation(t *testing.T) {
	m := testModel(t)
	checked := time.Date(2026, 8, 23, 10, 5, 0, 0, time.Local)
	for _, snapshot := range []db.ModelUsageSnapshot{
		{Provider: "claude", Scope: "claude:/accounts/.claude-work/.credentials.json", UsedPercent: 20, FetchedAt: checked},
		{Provider: "claude", Scope: "claude:/accounts/.claude-personal/.credentials.json", UsedPercent: 60, FetchedAt: checked},
	} {
		if err := db.UpsertModelUsageSnapshot(m.o.DB, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	view := ansi.Strip(m.viewOverview())
	labels := map[string]bool{}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "20.0%") || strings.Contains(line, "60.0%") {
			if !strings.Contains(line, "weekly") || !strings.Contains(line, "#") {
				t.Fatalf("long usage label was not safely disambiguated: %q\n%s", line, view)
			}
			labels[strings.TrimSpace(strings.Split(line, "[")[0])] = true
		}
	}
	if len(labels) != 2 {
		t.Fatalf("long usage labels are not distinct: %#v\n%s", labels, view)
	}
}

func TestPluginsTabShowsStateAndLastLogOutput(t *testing.T) {
	m := testModel(t)
	checked := time.Date(2026, 9, 1, 10, 5, 0, 0, time.UTC)
	if err := db.SyncPluginRuntimes(m.o.DB, []db.PluginRuntime{
		{Name: "disabled-plugin", State: "disabled", HookCount: 1},
		{Name: "nudge", Version: "1.0.0", State: "ready", HookCount: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.o.DB.Exec(`UPDATE plugin_runtime SET last_log=?, last_log_at=? WHERE name='nudge'`, "first log line\nsent inbox reminder to developer-ada", checked.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	m.tab = tabPlugins
	m.sel[m.tab] = 1
	view := ansi.Strip(m.viewOverview())
	for _, want := range []string{"Plugins", "disabled-plugin", "disabled", "nudge", "ready", "Last log", "sent inbox reminder to developer-ada"} {
		if !strings.Contains(view, want) {
			t.Errorf("plugins tab missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "first log line") {
		t.Fatalf("plugin overview rendered more than the latest log line:\n%s", view)
	}
	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	opened := updated.(model)
	if opened.mode != modeDetail || opened.detail.title != "Plugin — nudge" || !strings.Contains(opened.detail.body, "sent inbox reminder") {
		t.Fatalf("plugin detail = mode %v detail %+v", opened.mode, opened.detail)
	}
}

func TestPluginDetailShowsScrollableLogHistory(t *testing.T) {
	m := testModel(t)
	m.w, m.h = 48, 9
	if err := db.SyncPluginRuntimes(m.o.DB, []db.PluginRuntime{{Name: "logger", State: "ready", HookCount: 1}}); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		message := fmt.Sprintf("history line %02d", i)
		if err := db.AppendPluginRuntimeLog(m.o.DB, "logger", message, time.Date(2026, 9, 1, 10, i, 0, 0, time.UTC), 500); err != nil {
			t.Fatal(err)
		}
	}
	m.tab = tabPlugins
	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.mode != modeDetail || !strings.Contains(m.detail.body, "history line 00") || !strings.Contains(m.detail.body, "history line 11") {
		t.Fatalf("plugin detail omitted retained history: mode=%v\n%s", m.mode, m.detail.body)
	}
	if m.detailMaxOffset() == 0 {
		t.Fatal("plugin log history is not scrollable")
	}
	updated, _ = m.updateDetail(tea.KeyMsg{Type: tea.KeyEnd})
	if view := ansi.Strip(updated.(model).viewDetail()); !strings.Contains(view, "history line 11") {
		t.Fatalf("End did not reveal latest plugin log line:\n%s", view)
	}
}

func TestSwitchingFromPeekClearsScreen(t *testing.T) {
	m := testModel(t)
	m.mode = modePeek
	updated, cmd := m.updatePeek(tea.KeyMsg{Type: tea.KeyCtrlO})
	if updated.(model).mode != modeOverview || cmd == nil {
		t.Fatalf("peek switch = mode %v, cmd %v", updated.(model).mode, cmd)
	}
}

func TestSafeShutdownIsOfferedFromQuitConfirmation(t *testing.T) {
	m := testModel(t)
	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(model)
	if m.mode != modeOverview {
		t.Fatalf("overview safe shutdown key opened mode %v", m.mode)
	}
	if view := m.viewOverview(); strings.Contains(view, "s safe shutdown") {
		t.Fatalf("overview footer still advertises safe shutdown:\n%s", view)
	}

	updated, _ = m.updateOverview(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(model)
	if m.mode != modeQuitConfirm {
		t.Fatalf("quit key opened mode %v", m.mode)
	}
	if view := m.viewQuitConfirm(); !strings.Contains(view, "s safe shutdown") {
		t.Fatalf("quit confirmation does not offer safe shutdown:\n%s", view)
	}

	updated, _ = m.updateQuitConfirm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(model)
	if m.mode != modeSafeShutdownConfirm {
		t.Fatalf("quit safe shutdown option opened mode %v", m.mode)
	}
	updated, _ = m.updateSafeShutdownConfirm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if got := updated.(model).mode; got != modeOverview {
		t.Fatalf("cancel returned to mode %v", got)
	}
}

func TestComposerSendsAndReturnsToReadOnlyPeek(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "developer-jason", "developer")
	m.mode, m.peek, m.readOnly = modePeek, "developer-jason", true

	updated, _ := m.updatePeek(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = updated.(model)
	if m.mode != modeComposeMessage || m.compose.target != "developer-jason" {
		t.Fatalf("message composer not opened for peek target: %+v", m.compose)
	}
	m.compose.subject = "Status"
	m.compose.body = "Please report back."
	updated, _ = m.updateComposer(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = updated.(model)
	if m.mode != modePeek || m.peek != "developer-jason" || !m.readOnly {
		t.Fatalf("send returned to mode=%v peek=%q readOnly=%v", m.mode, m.peek, m.readOnly)
	}
	history, err := m.o.Sup.Mail.History()
	if err != nil || len(history) != 1 || history[0].From != "user" || history[0].To != "developer-jason" {
		t.Fatalf("sent history = %+v err %v", history, err)
	}
}

func TestReadOnlyPeekShowsRecordedReadyPrompt(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "developer-jason", "developer")
	if err := db.SetAgentReadyPrompt(m.o.DB, "developer-jason", "exact prompt from omo ready"); err != nil {
		t.Fatal(err)
	}
	m.mode, m.peek, m.readOnly = modePeek, "developer-jason", true

	if view := m.viewPeek(); !strings.Contains(view, "p prompt") {
		t.Fatalf("read-only peek footer has no prompt action: %s", view)
	}
	updated, _ := m.updatePeek(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(model)
	if m.mode != modeDetail || m.detail.title != "Ready prompt — developer-jason" || m.detail.body != "exact prompt from omo ready" {
		t.Fatalf("ready prompt detail = mode %v detail %+v", m.mode, m.detail)
	}
	updated, _ = m.updateDetail(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.mode != modePeek || m.peek != "developer-jason" || !m.readOnly {
		t.Fatalf("prompt detail returned to mode=%v peek=%q readOnly=%v", m.mode, m.peek, m.readOnly)
	}
}

func TestWritablePeekDoesNotOfferReadyPromptAction(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "ceo-ada", "ceo")
	m.mode, m.peek, m.readOnly = modePeek, "ceo-ada", false

	if view := m.viewPeek(); strings.Contains(view, "p prompt") {
		t.Fatalf("writable peek footer offers read-only prompt action: %s", view)
	}
}

func TestOverviewFooterClickUsesEquivalentKeyRouting(t *testing.T) {
	m := testModel(t)
	view := m.View()
	x, ok := findRenderedCell(view, m.h-1, "q quit")
	if !ok {
		t.Fatalf("quit footer action missing:\n%s", ansi.Strip(view))
	}

	clicked := updateMouse(m, x, m.h-1, tea.MouseButtonLeft, tea.MouseActionPress)
	keyed, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil || clicked.mode != keyed.(model).mode || clicked.returnMode != keyed.(model).returnMode {
		t.Fatalf("footer click state=(mode %v return %v), key state=(mode %v return %v), cmds=%v", clicked.mode, clicked.returnMode, keyed.(model).mode, keyed.(model).returnMode, cmd)
	}
}

func TestOverviewTabClickUsesEquivalentTabSwitching(t *testing.T) {
	m := testModel(t)
	m.sel[tabMessages] = 1
	view := m.View()
	x, ok := findRenderedCell(view, 1, "Messages")
	if !ok {
		t.Fatalf("Messages tab missing:\n%s", ansi.Strip(view))
	}

	clicked := updateMouse(m, x, 1, tea.MouseButtonLeft, tea.MouseActionPress)
	keyed, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd != nil || clicked.tab != keyed.(model).tab || clicked.sel != keyed.(model).sel {
		t.Fatalf("tab click state=(tab %v sel %v), key state=(tab %v sel %v), cmd=%v", clicked.tab, clicked.sel, keyed.(model).tab, keyed.(model).sel, cmd)
	}
}

func TestOverviewRowClickSelectsThenUsesEnterBehavior(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "developer-first", "developer")
	addLivingAgent(t, m, "developer-second", "developer")
	m.tab = tabAgents

	view := m.View()
	x, y, ok := findRenderedTextCell(view, "developer-second")
	if !ok {
		t.Fatalf("second agent row missing:\n%s", ansi.Strip(view))
	}
	selected := updateMouse(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if selected.mode != modeOverview || selected.sel[tabAgents] != 1 {
		t.Fatalf("different-row click state = mode %v selection %d", selected.mode, selected.sel[tabAgents])
	}

	selectedView := selected.View()
	x, y, ok = findRenderedTextCell(selectedView, "developer-second")
	if !ok {
		t.Fatalf("selected agent row missing:\n%s", ansi.Strip(selectedView))
	}
	clicked, clickCmd := updateMouseWithCmd(selected, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	keyed, keyCmd := selected.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	keyedModel := keyed.(model)
	if clickCmd != nil || keyCmd != nil || clicked.mode != keyedModel.mode || clicked.peek != keyedModel.peek {
		t.Fatalf("selected-row click = mode %v peek %q cmd %v; Enter = mode %v peek %q cmd %v", clicked.mode, clicked.peek, clickCmd, keyedModel.mode, keyedModel.peek, keyCmd)
	}
}

func updateMouseWithCmd(m model, x, y int, button tea.MouseButton, action tea.MouseAction) (model, tea.Cmd) {
	updated, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Button: button, Action: action})
	return updated.(model), cmd
}

func TestDetailBackFooterClickUsesEnter(t *testing.T) {
	m := testModel(t)
	job := &queue.Job{Title: "detail back", Goal: "test", Role: "freelancer"}
	if err := m.o.Sup.Jobs.Create(job); err != nil {
		t.Fatal(err)
	}
	m.tab = tabJobs
	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	view := m.View()
	x, y, ok := findRenderedTextCell(view, "Enter/Esc back")
	if !ok {
		t.Fatalf("detail back footer missing:\n%s", ansi.Strip(view))
	}
	clicked, clickCmd := updateMouseWithCmd(m, x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	keyed, keyCmd := m.updateDetail(tea.KeyMsg{Type: tea.KeyEnter})
	keyedModel := keyed.(model)
	if clickCmd != nil || keyCmd != nil || clicked.mode != keyedModel.mode || clicked.detail != keyedModel.detail {
		t.Fatalf("detail back click = mode %v detail %+v cmd %v; Enter = mode %v detail %+v cmd %v", clicked.mode, clicked.detail, clickCmd, keyedModel.mode, keyedModel.detail, keyCmd)
	}
}

func TestOverviewRowHitsUseAbsoluteIndicesAfterPagination(t *testing.T) {
	m := testModel(t)
	m.tab = tabAgents
	m.h = 10
	for i := 0; i < 6; i++ {
		addLivingAgent(t, m, fmt.Sprintf("developer-%02d", i), "developer")
	}
	m.sel[tabAgents] = 5
	m.View()
	var got []int
	for _, rect := range m.hitMap.rects {
		if action, ok := rect.action.(rowAction); ok && action.tab == tabAgents {
			got = append(got, action.row)
		}
	}
	if fmt.Sprint(got) != "[3 4 5]" {
		t.Fatalf("agent row hit indices = %v, want [3 4 5]", got)
	}
	if _, ok := m.hitMap.at(0, 3); !ok {
		t.Fatal("first visible paginated agent row has no hit")
	}
	if action, _ := m.hitMap.at(0, 3); action.(rowAction).row != 3 {
		t.Fatalf("first visible agent row action = %#v, want absolute index 3", action)
	}

	m.tab = tabEvents
	for i := 0; i < 6; i++ {
		if err := db.AppendEvent(m.o.DB, fmt.Sprintf("event-%02d", i), "ceo-test", 0, "detail"); err != nil {
			t.Fatal(err)
		}
	}
	m.sel[tabEvents] = 5
	m.View()
	got = nil
	for _, rect := range m.hitMap.rects {
		if action, ok := rect.action.(rowAction); ok && action.tab == tabEvents {
			got = append(got, action.row)
		}
	}
	if fmt.Sprint(got) != "[3 4 5]" {
		t.Fatalf("event row hit indices = %v, want [3 4 5]", got)
	}
}

func TestEveryOverviewListRegistersRenderedRows(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "developer-first", "developer")
	addLivingAgent(t, m, "developer-second", "developer")
	if _, err := m.o.DB.Exec(`INSERT INTO messages(from_agent,to_target,subject,body) VALUES
		('ceo-test','developer-first','message-first','body'), ('ceo-test','developer-second','message-second','body')`); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"job-first", "job-second"} {
		if err := m.o.Sup.Jobs.Create(&queue.Job{Title: title, Goal: "goal", Role: "freelancer"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, detail := range []string{"incident-first", "incident-second"} {
		if _, err := m.o.DB.Exec(`INSERT INTO incidents(agent,class,detail) VALUES('developer-first','test',?)`, detail); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"event-first", "event-second"} {
		if err := db.AppendEvent(m.o.DB, kind, "ceo-test", 0, "detail"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SyncPluginRuntimes(m.o.DB, []db.PluginRuntime{{Name: "plugin-first", State: "ready"}, {Name: "plugin-second", State: "ready"}}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		tab    overviewTab
		needle string
	}{
		{tabAgents, "developer-second"},
		{tabMessages, "message-second"},
		{tabJobs, "job-second"},
		{tabIncidents, "incident-second"},
		{tabEvents, "event-second"},
		{tabPlugins, "plugin-second"},
		{tabPreview, "developer"},
	} {
		m.mode, m.tab = modeOverview, tc.tab
		m.sel[tc.tab] = 0
		view := m.View()
		x, y, ok := findRenderedTextCell(view, tc.needle)
		if !ok {
			t.Fatalf("tab %v row %q missing:\n%s", tc.tab, tc.needle, ansi.Strip(view))
		}
		action, ok := m.hitMap.at(x, y)
		if !ok {
			t.Fatalf("tab %v row %q has no hit at %d,%d", tc.tab, tc.needle, x, y)
		}
		row, ok := action.(rowAction)
		if !ok || row.tab != tc.tab {
			t.Fatalf("tab %v row %q hit = %#v, want row action for tab", tc.tab, tc.needle, action)
		}
	}
}

func TestObserverTabClickOnlyTargetsVisibleTabs(t *testing.T) {
	m := testModel(t)
	m.observer = true
	view := m.View()
	stripped := ansi.Strip(view)
	if strings.Contains(stripped, "Commands") || strings.Contains(stripped, "Preview") {
		t.Fatalf("observer rendered hidden tabs:\n%s", stripped)
	}
	x, ok := findRenderedCell(view, 1, "Plugins")
	if !ok {
		t.Fatalf("Plugins tab missing:\n%s", stripped)
	}
	clicked := updateMouse(m, x, 1, tea.MouseButtonLeft, tea.MouseActionPress)
	if clicked.tab != tabPlugins {
		t.Fatalf("observer tab click selected %v, want Plugins", clicked.tab)
	}
}

func TestMouseOnlyLeftPressesResolveRenderedHits(t *testing.T) {
	m := testModel(t)
	view := m.View()
	x, ok := findRenderedCell(view, m.h-1, "q quit")
	if !ok {
		t.Fatalf("quit footer action missing:\n%s", ansi.Strip(view))
	}
	for _, tc := range []struct {
		name   string
		button tea.MouseButton
		action tea.MouseAction
	}{
		{name: "release", button: tea.MouseButtonLeft, action: tea.MouseActionRelease},
		{name: "motion", button: tea.MouseButtonLeft, action: tea.MouseActionMotion},
		{name: "right press", button: tea.MouseButtonRight, action: tea.MouseActionPress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := updateMouse(m, x, m.h-1, tc.button, tc.action)
			if got.mode != modeOverview {
				t.Fatalf("disallowed mouse event opened mode %v", got.mode)
			}
		})
	}
	if got := updateMouse(m, 0, 0, tea.MouseButtonLeft, tea.MouseActionPress); got.mode != modeOverview {
		t.Fatalf("empty body cell opened mode %v", got.mode)
	}
}

func TestNarrowOverviewRenderDropsFooterPartsAndBoundsHits(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "ceo-ada", "ceo")
	addLivingAgent(t, m, "pm-alex", "product_manager")
	addLivingAgent(t, m, "developer-jason", "developer")
	m.w = 24
	view := ansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	if width := ansi.StringWidth(lines[m.h-1]); width > m.w {
		t.Fatalf("footer width = %d, want <= %d: %q", width, m.w, lines[m.h-1])
	}
	for i, rect := range m.hitMap.rects {
		if rect.x < 0 || rect.y < 0 || rect.x+rect.w > m.w || rect.y+rect.h > m.h {
			t.Fatalf("hit rectangle %d out of window bounds: %+v in %dx%d", i, rect, m.w, m.h)
		}
	}
}

func TestPeekFooterClickOpensReadyPromptButBodyClickIsIgnored(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "developer-jason", "developer")
	if err := db.SetAgentReadyPrompt(m.o.DB, "developer-jason", "prompt body"); err != nil {
		t.Fatal(err)
	}
	m.mode, m.peek, m.readOnly = modePeek, "developer-jason", true
	view := m.View()
	x, ok := findRenderedCell(view, 1, "p prompt")
	if !ok {
		t.Fatalf("peek prompt action missing:\n%s", ansi.Strip(view))
	}
	clicked := updateMouse(m, x, 1, tea.MouseButtonLeft, tea.MouseActionPress)
	if clicked.mode != modeDetail || clicked.detail.title != "Ready prompt — developer-jason" {
		t.Fatalf("peek footer click opened mode=%v detail=%+v", clicked.mode, clicked.detail)
	}

	m.View()
	bodyClick := updateMouse(m, 0, 0, tea.MouseButtonLeft, tea.MouseActionPress)
	if bodyClick.mode != modePeek || bodyClick.peek != m.peek {
		t.Fatalf("peek body click changed mode=%v peek=%q", bodyClick.mode, bodyClick.peek)
	}
}

func TestPeekWheelAtFooterDoesNotActivateFooterAction(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "developer-jason", "developer")
	if err := db.SetAgentReadyPrompt(m.o.DB, "developer-jason", "prompt body"); err != nil {
		t.Fatal(err)
	}
	m.mode, m.peek, m.readOnly = modePeek, "developer-jason", true
	view := m.View()
	x, ok := findRenderedCell(view, 1, "p prompt")
	if !ok {
		t.Fatalf("peek prompt action missing:\n%s", ansi.Strip(view))
	}
	got := updateMouse(m, x, 1, tea.MouseButtonWheelDown, tea.MouseActionPress)
	if got.mode != modePeek {
		t.Fatalf("wheel event activated footer action, mode=%v", got.mode)
	}
}

func TestOverviewComposerTargetsSelectedAgentAndMessageSender(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "developer-jason", "developer")
	m.tab = tabAgents
	if target := m.overviewMessageTarget(); target != "developer-jason" {
		t.Fatalf("agent target = %q", target)
	}
	if _, err := m.o.DB.Exec(`INSERT INTO messages
		(from_agent, to_target, subject, body) VALUES ('developer-jason', 'user', 'done', 'ready')`); err != nil {
		t.Fatal(err)
	}
	m.tab = tabMessages
	if target := m.overviewMessageTarget(); target != "developer-jason" {
		t.Fatalf("message target = %q", target)
	}
}

func TestAgentFooterAddsOnlyRoleCountsThatFit(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "ceo-ada", "ceo")
	addLivingAgent(t, m, "pm-alex", "product_manager")
	addLivingAgent(t, m, "developer-jason", "developer")
	m.w = 34
	footer := m.agentFooter([]string{"q quit"})
	if ansi.StringWidth(footer) > m.w {
		t.Fatalf("footer width = %d, want <= %d: %q", ansi.StringWidth(footer), m.w, footer)
	}
	if !strings.Contains(footer, "CEO 1/1") || !strings.Contains(footer, "PM 1/1") {
		t.Fatalf("footer omitted role counts that fit: %q", footer)
	}
}

func TestOverviewJobsAreNewestFirst(t *testing.T) {
	m := testModel(t)
	for _, title := range []string{"older", "newer"} {
		job := &queue.Job{Title: title, Goal: "test", Role: "freelancer"}
		if err := m.o.Sup.Jobs.Create(job); err != nil {
			t.Fatal(err)
		}
	}
	jobs := m.overviewJobs()
	if len(jobs) != 2 || jobs[0].Title != "newer" || jobs[1].Title != "older" {
		t.Fatalf("overview jobs = %+v, want newest first", jobs)
	}
}

func TestJobActionMenuCancelsAndThenOffersRequeue(t *testing.T) {
	m := testModel(t)
	job := &queue.Job{Title: "stop me", Goal: "test", Role: "freelancer"}
	if err := m.o.Sup.Jobs.Create(job); err != nil {
		t.Fatal(err)
	}
	m.tab = tabJobs

	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = updated.(model)
	if m.mode != modeActionMenu || len(m.action.items) != 1 || m.action.items[0].kind != actionCancelJob {
		t.Fatalf("cancel menu = %+v, mode=%v", m.action, m.mode)
	}
	updated, _ = m.updateActionMenu(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	got, err := m.o.Sup.Jobs.Get(job.ID)
	if err != nil || got.State != queue.StateCancelled {
		t.Fatalf("cancelled job = %+v, err=%v", got, err)
	}
	if m.mode != modeOverview || !strings.Contains(m.actionStatus, "completed") {
		t.Fatalf("post-action mode=%v status=%q", m.mode, m.actionStatus)
	}

	updated, _ = m.updateOverview(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = updated.(model)
	if len(m.action.items) != 1 || m.action.items[0].kind != actionRequeueJob {
		t.Fatalf("requeue menu = %+v", m.action)
	}
	updated, _ = m.updateActionMenu(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	got, err = m.o.Sup.Jobs.Get(job.ID)
	if err != nil || got.State != queue.StateQueued {
		t.Fatalf("requeued job = %+v, err=%v", got, err)
	}
}

func TestAgentActionMenuOffersKillAndRestart(t *testing.T) {
	m := testModel(t)
	addLivingAgent(t, m, "developer-ada", "developer")
	m.tab = tabAgents
	actions := m.selectedActions()
	if len(actions) != 2 || actions[0].kind != actionKillAgent || actions[1].kind != actionRestartAgent {
		t.Fatalf("agent actions = %+v", actions)
	}
	if view := m.viewOverview(); !strings.Contains(view, "x actions") {
		t.Fatalf("agent footer missing action hint:\n%s", view)
	}
}

func TestEnterOpensAndScrollsFullMessage(t *testing.T) {
	m := testModel(t)
	m.tab, m.w, m.h = tabMessages, 44, 9
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "body line"
	}
	lines[len(lines)-1] = "tail marker"
	if _, err := m.o.DB.Exec(`INSERT INTO messages
		(from_agent, to_target, subject, body) VALUES ('ceo-ada', 'user', 'long question', ?)`, strings.Join(lines, "\n")); err != nil {
		t.Fatal(err)
	}

	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.mode != modeDetail || !strings.Contains(m.detail.title, "long question") {
		t.Fatalf("Enter opened mode=%v detail=%+v", m.mode, m.detail)
	}
	if unread, err := m.o.Sup.Mail.UnreadCount("user"); err != nil || unread != 0 {
		t.Fatalf("opening message left unread count %d, err=%v", unread, err)
	}
	if m.detailMaxOffset() == 0 {
		t.Fatal("long message detail is not scrollable")
	}
	updated, _ = m.updateDetail(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(model)
	if view := m.viewDetail(); !strings.Contains(view, "tail marker") {
		t.Fatalf("End did not reveal final message content:\n%s", view)
	}
	updated, _ = m.updateDetail(tea.KeyMsg{Type: tea.KeyEnter})
	if got := updated.(model).mode; got != modeOverview {
		t.Fatalf("Enter from detail returned to mode %v", got)
	}
}

func TestEnterOpensEveryDatabaseOverviewTable(t *testing.T) {
	m := testModel(t)
	job := &queue.Job{Title: "ship it", Goal: "test", Role: "freelancer"}
	if err := m.o.Sup.Jobs.Create(job); err != nil {
		t.Fatal(err)
	}
	if _, err := m.o.DB.Exec(`INSERT INTO incidents (agent, class, detail) VALUES ('developer-ada', 'stuck', 'needs help')`); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendEvent(m.o.DB, "custom_event", "ceo-ada", job.ID, "event detail"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		tab  overviewTab
		want string
	}{
		{tabJobs, "Job #"},
		{tabIncidents, "Incident #"},
		{tabEvents, "Event #"},
	} {
		m.mode, m.tab = modeOverview, tc.tab
		updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
		opened := updated.(model)
		if opened.mode != modeDetail || !strings.Contains(opened.detail.title, tc.want) {
			t.Errorf("tab %v opened mode=%v detail=%+v", tc.tab, opened.mode, opened.detail)
		}
	}
}

func TestStatisticsViewHasOverallAndSessionSections(t *testing.T) {
	m := testModel(t)
	m.tab = tabStatistics
	if err := db.UpsertOverallStatistics(m.o.DB, []db.ModelStatistics{{Model: "test", AgentsStarted: 2}}); err != nil {
		t.Fatal(err)
	}
	view := m.viewOverview()
	for _, want := range []string{"Overall statistics", "Current session", "test", "agents 2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("statistics view missing %q:\n%s", want, view)
		}
	}
}

func TestStatisticsViewScrollsToModelsBelowTheViewport(t *testing.T) {
	m := testModel(t)
	m.tab, m.h = tabStatistics, 10
	var stats []db.ModelStatistics
	for i := 0; i < 20; i++ {
		stats = append(stats, db.ModelStatistics{Model: fmt.Sprintf("model-%02d", i), AgentsStarted: i + 1})
	}
	if err := db.UpsertOverallStatistics(m.o.DB, stats); err != nil {
		t.Fatal(err)
	}
	if view := m.viewOverview(); strings.Contains(view, "Current session") {
		t.Fatalf("current-session section unexpectedly visible before scrolling:\n%s", view)
	}
	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(model)
	if m.statsOffset == 0 {
		t.Fatal("End did not move the statistics viewport")
	}
	if view := m.viewOverview(); !strings.Contains(view, "Current session") {
		t.Fatalf("current-session section missing after scrolling to End:\n%s", view)
	}
}

func TestPreviewTabCollectsInputAndRendersRolePrompt(t *testing.T) {
	m := testModel(t)
	m.tab = tabPreview
	m.sel[m.tab] = 2 // developer
	if view := m.viewOverview(); !strings.Contains(view, "Select a role") || !strings.Contains(view, "developer") {
		t.Fatalf("preview role list missing:\n%s", view)
	}

	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.mode != modePromptInput || m.preview.role != "developer" {
		t.Fatalf("preview input opened mode=%v state=%+v", m.mode, m.preview)
	}
	updated, _ = m.updatePromptInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("build the API")})
	m = updated.(model)
	updated, _ = m.updatePromptInput(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = updated.(model)
	if m.mode != modeDetail || !strings.Contains(m.detail.title, "developer") {
		t.Fatalf("preview rendered mode=%v detail=%+v", m.mode, m.detail)
	}
	for _, want := range []string{"developer-preview", "build the API", "executing-plans"} {
		if !strings.Contains(m.detail.body, want) {
			t.Errorf("rendered prompt missing %q", want)
		}
	}
}

func TestReadOnlyOverviewShowsOnlyObservationTabsAndOverallStats(t *testing.T) {
	m := testModel(t)
	m.observer = true
	m.tab = tabStatistics
	view := ansi.Strip(m.viewOverview())
	for _, want := range []string{"READ ONLY", "Agents", "Messages", "Jobs", "Incidents", "Events", "Statistics", "Plugins", "Overall statistics"} {
		if !strings.Contains(view, want) {
			t.Errorf("read-only view missing %q:\n%s", want, view)
		}
	}
	for _, forbidden := range []string{"Commands", "Preview", "Current session", "safe shutdown"} {
		if strings.Contains(view, forbidden) {
			t.Errorf("read-only view contains %q:\n%s", forbidden, view)
		}
	}
}

func TestReadOnlyMessageDetailDoesNotMarkRead(t *testing.T) {
	m := testModel(t)
	m.observer = true
	m.tab = tabMessages
	if _, err := m.o.DB.Exec(`INSERT INTO messages(from_agent,to_target,subject,body) VALUES('ceo-test','user','status','still unread')`); err != nil {
		t.Fatal(err)
	}
	updated, _ := m.updateOverview(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.mode != modeDetail {
		t.Fatalf("message did not open in detail mode: %v", m.mode)
	}
	var readAt any
	if err := m.o.DB.QueryRow(`SELECT read_at FROM messages LIMIT 1`).Scan(&readAt); err != nil {
		t.Fatal(err)
	}
	if readAt != nil {
		t.Fatalf("read-only detail marked message read: %#v", readAt)
	}
}

func TestReadOnlyAgentAndMutationKeysAreInert(t *testing.T) {
	m := testModel(t)
	m.observer = true
	m.tab = tabAgents
	addLivingAgent(t, m, "ceo-test", "ceo")
	for _, key := range []string{"enter", "x", "m", "s"} {
		updated, cmd := m.updateOverview(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		m = updated.(model)
		if cmd != nil || m.mode != modeOverview || m.peek != "" {
			t.Fatalf("key %q mutated observer state: mode=%v peek=%q cmd=%v", key, m.mode, m.peek, cmd)
		}
	}
}

func TestPreviewRoleRowsStartAtLeftEdge(t *testing.T) {
	m := testModel(t)
	m.tab = tabPreview
	m.sel[m.tab] = 0

	view := ansi.Strip(m.viewOverview())
	for _, line := range strings.Split(view, "\n") {
		if column := strings.Index(line, "ceo"); column >= 0 {
			if column != 1 {
				t.Fatalf("selected CEO starts at column %d, want 1:\n%q", column, line)
			}
			return
		}
	}
	t.Fatalf("selected CEO row missing:\n%s", view)
}
