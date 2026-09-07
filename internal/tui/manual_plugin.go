package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/scolastico-dev/one-man-office/internal/proto"
)

type manualPluginInput struct {
	action, input, status       string
	editing, running, selecting bool
	selected                    int
}

type manualPluginResultMsg struct {
	name   string
	action string
	err    error
}

func (m model) manualPluginActions() []proto.PluginAction {
	if m.observer || m.detail.plugin == "" || m.o == nil || m.o.Sup == nil {
		return nil
	}
	return m.o.Sup.Plugins.ManualActions(m.detail.plugin)
}

func (m model) openManualPlugin() (tea.Model, tea.Cmd) {
	actions := m.manualPluginActions()
	if len(actions) == 0 || m.manual[m.detail.plugin].running {
		return m, nil
	}
	if m.manual == nil {
		m.manual = make(map[string]manualPluginInput)
	}
	if len(actions) == 1 {
		return m.chooseManualPluginAction(actions[0])
	}
	m.manual[m.detail.plugin] = manualPluginInput{selecting: true}
	m.focusManualSelection()
	return m, nil
}

func (m *model) focusManualSelection() {
	for i, line := range m.detailLines() {
		if strings.HasPrefix(line, "› ") {
			if i < m.detail.offset {
				m.detail.offset = i
			}
			if i >= m.detail.offset+m.detailPageSize() {
				m.detail.offset = i - m.detailPageSize() + 1
			}
			break
		}
	}
	m.clampDetailOffset()
}

func (m model) chooseManualPluginAction(action proto.PluginAction) (tea.Model, tea.Cmd) {
	m.manual[m.detail.plugin] = manualPluginInput{action: action.Name, editing: action.ManualArgs}
	if action.ManualArgs {
		m.detail.offset = m.detailMaxOffset()
		return m, nil
	}
	return m.startManualPlugin(nil)
}

func (m model) updateManualPluginInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	manual := m.manual[m.detail.plugin]
	if manual.selecting {
		actions := m.manualPluginActions()
		switch msg.Type {
		case tea.KeyEsc:
			manual = manualPluginInput{}
		case tea.KeyUp:
			if manual.selected > 0 {
				manual.selected--
			}
		case tea.KeyDown:
			if manual.selected+1 < len(actions) {
				manual.selected++
			}
		case tea.KeyEnter:
			if manual.selected < len(actions) {
				return m.chooseManualPluginAction(actions[manual.selected])
			}
		}
		m.manual[m.detail.plugin] = manual
		m.focusManualSelection()
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.manual[m.detail.plugin] = manualPluginInput{}
		m.clampDetailOffset()
		return m, nil
	case tea.KeyEnter:
		// Reuse the command console's quote parser without invoking a shell.
		args, err := splitCommandLine("plugin " + manual.input)
		if err != nil {
			manual.status = err.Error()
			break
		}
		return m.startManualPlugin(args[1:])
	case tea.KeyBackspace, tea.KeyDelete:
		manual.input = dropLastRune(manual.input)
	case tea.KeyRunes:
		manual.input += string(msg.Runes)
	case tea.KeySpace:
		manual.input += " "
	}
	m.manual[m.detail.plugin] = manual
	m.detail.offset = m.detailMaxOffset()
	return m, nil
}

func (m model) startManualPlugin(args []string) (tea.Model, tea.Cmd) {
	if len(m.manualPluginActions()) == 0 || m.manual[m.detail.plugin].running {
		return m, nil
	}
	action := m.manual[m.detail.plugin].action
	m.manual[m.detail.plugin] = manualPluginInput{action: action, running: true, status: "Running plugin " + m.detail.plugin + " action " + action + "…"}
	m.detail.offset = m.detailMaxOffset()
	sup, name := m.o.Sup, m.detail.plugin
	return m, func() tea.Msg {
		return manualPluginResultMsg{name: name, action: action, err: sup.TriggerPlugin("user", name, action, args)}
	}
}

func (m *model) finishManualPlugin(result manualPluginResultMsg) {
	manual := manualPluginInput{action: result.action, status: fmt.Sprintf("Plugin %s action %s completed", result.name, result.action)}
	if result.err != nil {
		manual.status = result.err.Error()
	}
	m.manual[result.name] = manual
	if m.mode == modeDetail && m.detail.plugin == result.name {
		m.cache = &viewCache{}
		if detail, ok := m.selectedDetail(); ok && detail.plugin == result.name {
			m.detail = detail
		}
		m.detail.offset = m.detailMaxOffset()
	}
}
