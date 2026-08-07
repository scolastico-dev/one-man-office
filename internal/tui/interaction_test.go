package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/office"
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
