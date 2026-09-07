package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

type manualPluginInput struct {
	name, input, status string
	editing, running    bool
}

type manualPluginResultMsg struct {
	name string
	err  error
}

func (m model) manualPluginCapability() (bool, bool) {
	if m.observer || m.detail.plugin == "" || m.o == nil || m.o.Sup == nil {
		return false, false
	}
	return m.o.Sup.Plugins.ManualCapability(m.detail.plugin)
}

func (m model) openManualPlugin() (tea.Model, tea.Cmd) {
	subscribed, acceptsArgs := m.manualPluginCapability()
	if !subscribed || m.manual.running {
		return m, nil
	}
	m.manual = manualPluginInput{name: m.detail.plugin, editing: acceptsArgs}
	if acceptsArgs {
		m.detail.offset = m.detailMaxOffset()
		return m, nil
	}
	return m.startManualPlugin(nil)
}

func (m model) updateManualPluginInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.manual.editing = false
		m.manual.input, m.manual.status = "", ""
		m.clampDetailOffset()
		return m, nil
	case tea.KeyEnter:
		// Reuse the command console's quote parser without invoking a shell.
		args, err := splitCommandLine("plugin " + m.manual.input)
		if err != nil {
			m.manual.status = err.Error()
			break
		}
		return m.startManualPlugin(args[1:])
	case tea.KeyBackspace, tea.KeyDelete:
		m.manual.input = dropLastRune(m.manual.input)
	case tea.KeyRunes:
		m.manual.input += string(msg.Runes)
	case tea.KeySpace:
		m.manual.input += " "
	}
	m.detail.offset = m.detailMaxOffset()
	return m, nil
}

func (m model) startManualPlugin(args []string) (tea.Model, tea.Cmd) {
	if subscribed, _ := m.manualPluginCapability(); !subscribed || m.manual.running {
		return m, nil
	}
	m.manual.editing, m.manual.running = false, true
	m.manual.input = ""
	m.manual.status = "Running plugin " + m.manual.name + "…"
	m.detail.offset = m.detailMaxOffset()
	sup, name := m.o.Sup, m.manual.name
	return m, func() tea.Msg {
		return manualPluginResultMsg{name: name, err: sup.TriggerPlugin("user", name, args)}
	}
}

func (m *model) finishManualPlugin(result manualPluginResultMsg) {
	m.manual.running = false
	m.manual.status = fmt.Sprintf("Plugin %s completed", result.name)
	if result.err != nil {
		m.manual.status = result.err.Error()
	}
	if m.mode == modeDetail && m.detail.plugin == result.name {
		m.cache = &viewCache{}
		if detail, ok := m.selectedDetail(); ok && detail.plugin == result.name {
			m.detail = detail
		}
		m.detail.offset = m.detailMaxOffset()
	}
}
