package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

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
	return model{o: &office.Office{DB: d, Sup: sup}, mode: modeOverview, w: 160, h: 30}
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

func TestSwitchingFromPeekClearsScreen(t *testing.T) {
	m := testModel(t)
	m.mode = modePeek
	updated, cmd := m.updatePeek(tea.KeyMsg{Type: tea.KeyCtrlO})
	if updated.(model).mode != modeOverview || cmd == nil {
		t.Fatalf("peek switch = mode %v, cmd %v", updated.(model).mode, cmd)
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
