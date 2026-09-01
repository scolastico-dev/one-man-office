package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/scolastico-dev/one-man-office/internal/queue"
)

type actionKind int

const (
	actionCancelJob actionKind = iota
	actionRequeueJob
	actionKillAgent
	actionRestartAgent
)

type actionItem struct {
	kind  actionKind
	label string
	jobID int64
	agent string
}

type actionMenu struct {
	title    string
	items    []actionItem
	selected int
	status   string
}

func (m model) selectedActions() []actionItem {
	switch m.tab {
	case tabAgents:
		rows := m.overviewRows()
		i := m.sel[m.tab]
		if i >= len(rows) {
			return nil
		}
		return []actionItem{
			{kind: actionKillAgent, label: "Kill agent and cancel its active work", agent: rows[i].Name},
			{kind: actionRestartAgent, label: "Restart agent without requeueing its work", agent: rows[i].Name},
		}
	case tabJobs:
		jobs := m.overviewJobs()
		i := m.sel[m.tab]
		if i >= len(jobs) {
			return nil
		}
		j := jobs[i]
		if j.State == queue.StateFailed || j.State == queue.StateCancelled {
			return []actionItem{{kind: actionRequeueJob, label: "Requeue job", jobID: j.ID}}
		}
		if j.State != queue.StateDone {
			return []actionItem{{kind: actionCancelJob, label: "Cancel job and stop its agent", jobID: j.ID}}
		}
	}
	return nil
}

func (m *model) openActionMenu() {
	items := m.selectedActions()
	if len(items) == 0 {
		return
	}
	title := "Management actions"
	if m.tab == tabAgents {
		title = "Agent actions — " + items[0].agent
	} else if m.tab == tabJobs {
		title = fmt.Sprintf("Job actions — #%d", items[0].jobID)
	}
	m.action = actionMenu{title: title, items: items}
	m.actionStatus = ""
	m.mode = modeActionMenu
	m.o.Sup.SetInteraction("", false)
}

func (m model) updateActionMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeOverview
		m.action = actionMenu{}
	case tea.KeyUp:
		if m.action.selected > 0 {
			m.action.selected--
		}
	case tea.KeyDown:
		if m.action.selected+1 < len(m.action.items) {
			m.action.selected++
		}
	case tea.KeyEnter:
		if len(m.action.items) == 0 {
			return m, nil
		}
		item := m.action.items[m.action.selected]
		var err error
		switch item.kind {
		case actionCancelJob:
			err = m.o.Sup.CancelJob(item.jobID, "user")
		case actionRequeueJob:
			err = m.o.Sup.RequeueJob(item.jobID, "user")
		case actionKillAgent:
			err = m.o.Sup.StopAgent(item.agent, "user", "kill")
		case actionRestartAgent:
			err = m.o.Sup.StopAgent(item.agent, "user", "restart")
		}
		if err != nil {
			m.action.status = err.Error()
			return m, nil
		}
		m.actionStatus = item.label + " completed"
		m.action = actionMenu{}
		m.mode = modeOverview
		m.clampSelection()
	}
	return m, nil
}

func (m model) viewActionMenu() string {
	var content strings.Builder
	content.WriteString(headerStyle.Render(m.action.title) + "\n\n")
	for i, item := range m.action.items {
		line := "  " + item.label
		if i == m.action.selected {
			line = selStyle.Render("› " + item.label)
		}
		content.WriteString(line + "\n")
	}
	if m.action.status != "" {
		content.WriteString("\n" + alertStyle.Render(m.action.status) + "\n")
	}
	content.WriteString("\n" + dimStyle.Render("↑/↓ select • Enter execute • Esc cancel"))
	box := lipgloss.NewStyle().Padding(1, 2).Width(64).
		Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("39")).Render(content.String())
	if m.w > 0 && m.h > 0 {
		return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}
