package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

type commandField int

const (
	commandIdentity commandField = iota
	commandLine
)

type commandScreen int

const (
	commandBrowse commandScreen = iota
	commandForm
	commandConfirm
	commandRaw
)

type commandLog struct {
	at       time.Time
	identity string
	command  string
	output   string
	err      string
}

type commandConsole struct {
	identities []string
	selected   int
	field      commandField
	screen     commandScreen
	catalog    []commandSpec
	item       int
	input      int
	values     []string
	showHelp   bool
	confirm    string
	line       string
	running    bool
	status     string
	logs       []commandLog
}

type commandResultMsg struct {
	at       time.Time
	identity string
	command  string
	output   string
	err      string
}

type commandExecutor func(executable, socket, officeDir, identity, line string) commandResultMsg

func (m *model) openCommandConsole() {
	agents := m.o.Sup.Mail.Dir.LivingAgents()
	identities := []string{"user"}
	identities = append(identities, agents...)
	m.commands.identities = identities
	if m.commands.selected >= len(identities) {
		m.commands.selected = 0
	}
	if strings.TrimSpace(m.commands.line) == "" {
		m.commands.line = "omo "
	}
	m.commands.catalog = commandCatalog()
	m.commands.screen = commandBrowse
	m.commands.field = commandLine
	m.commands.input = 0
	m.commands.values = nil
	m.commands.confirm = ""
	m.commands.showHelp = false
	m.commands.status = ""
	m.mode = modeCommandConsole
	m.o.Sup.SetInteraction("", false)
}

func (m model) updateCommandConsole(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "?" {
		m.commands.showHelp = !m.commands.showHelp
		return m, nil
	}
	switch m.commands.screen {
	case commandBrowse:
		return m.updateCommandBrowser(msg)
	case commandForm:
		return m.updateCommandForm(msg)
	case commandConfirm:
		return m.updateCommandConfirm(msg)
	default:
		return m.updateRawCommand(msg)
	}
}

func (m model) updateCommandBrowser(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeOverview
		return m, nil
	case tea.KeyUp:
		m.cycleCommandItem(-1)
	case tea.KeyDown:
		m.cycleCommandItem(1)
	case tea.KeyLeft:
		m.cycleCommandIdentity(-1)
	case tea.KeyRight:
		m.cycleCommandIdentity(1)
	case tea.KeyEnter:
		return m.openGuidedCommand()
	}
	return m, nil
}

func (m model) updateCommandForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.commands.catalog) == 0 || m.commands.item >= len(m.commands.catalog) {
		m.commands.screen = commandBrowse
		return m, nil
	}
	spec := m.commands.catalog[m.commands.item]
	switch msg.Type {
	case tea.KeyEsc:
		m.commands.screen = commandBrowse
		m.commands.status = ""
	case tea.KeyTab, tea.KeyDown:
		if len(spec.Inputs) > 0 {
			m.commands.input = (m.commands.input + 1) % len(spec.Inputs)
		}
	case tea.KeyShiftTab, tea.KeyUp:
		if len(spec.Inputs) > 0 {
			m.commands.input = (m.commands.input - 1 + len(spec.Inputs)) % len(spec.Inputs)
		}
	case tea.KeyLeft:
		m.cycleCommandSuggestion(spec, -1)
	case tea.KeyRight:
		m.cycleCommandSuggestion(spec, 1)
	case tea.KeyEnter, tea.KeyCtrlR:
		return m.prepareGuidedCommand(spec)
	case tea.KeyBackspace, tea.KeyDelete:
		if !m.commands.running && m.commands.input < len(m.commands.values) {
			m.commands.values[m.commands.input] = dropLastRune(m.commands.values[m.commands.input])
		}
	case tea.KeyRunes:
		if !m.commands.running && m.commands.input < len(m.commands.values) {
			m.commands.values[m.commands.input] += string(msg.Runes)
		}
	case tea.KeySpace:
		if !m.commands.running && m.commands.input < len(m.commands.values) {
			m.commands.values[m.commands.input] += " "
		}
	}
	return m, nil
}

func (m model) updateCommandConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.commands.screen = commandForm
		if len(m.commands.values) == 0 {
			m.commands.screen = commandBrowse
		}
		m.commands.confirm = ""
	case tea.KeyBackspace, tea.KeyDelete:
		m.commands.confirm = dropLastRune(m.commands.confirm)
	case tea.KeyRunes:
		m.commands.confirm += string(msg.Runes)
	case tea.KeyEnter:
		if strings.EqualFold(strings.TrimSpace(m.commands.confirm), "yes") {
			m.commands.screen = commandBrowse
			m.commands.confirm = ""
			return m.startCommand()
		}
		m.commands.status = "type yes to confirm"
	}
	return m, nil
}

func (m model) updateRawCommand(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.commands.screen = commandBrowse
		m.commands.field = commandLine
		return m, nil
	case tea.KeyTab, tea.KeyShiftTab:
		if m.commands.field == commandIdentity {
			m.commands.field = commandLine
		} else {
			m.commands.field = commandIdentity
		}
		return m, nil
	case tea.KeyUp, tea.KeyLeft:
		if m.commands.field == commandIdentity {
			m.cycleCommandIdentity(-1)
		}
		return m, nil
	case tea.KeyDown, tea.KeyRight:
		if m.commands.field == commandIdentity {
			m.cycleCommandIdentity(1)
		}
		return m, nil
	case tea.KeyCtrlR, tea.KeyEnter:
		return m.startCommand()
	case tea.KeyBackspace, tea.KeyDelete:
		if m.commands.field == commandLine && !m.commands.running {
			m.commands.line = dropLastRune(m.commands.line)
		}
	case tea.KeyRunes:
		if m.commands.field == commandLine && !m.commands.running {
			m.commands.line += string(msg.Runes)
		}
	case tea.KeySpace:
		if m.commands.field == commandLine && !m.commands.running {
			m.commands.line += " "
		}
	}
	return m, nil
}

func (m *model) cycleCommandItem(delta int) {
	n := len(m.commands.catalog)
	if n > 0 {
		m.commands.item = (m.commands.item + delta + n) % n
	}
}

func (m model) openGuidedCommand() (tea.Model, tea.Cmd) {
	if len(m.commands.catalog) == 0 || m.commands.item >= len(m.commands.catalog) {
		return m, nil
	}
	spec := m.commands.catalog[m.commands.item]
	m.commands.status = ""
	if spec.Raw {
		m.commands.screen = commandRaw
		m.commands.field = commandLine
		return m, nil
	}
	m.commands.values = make([]string, len(spec.Inputs))
	for i, field := range spec.Inputs {
		if len(field.Suggestions) > 0 && !field.Required {
			m.commands.values[i] = field.Suggestions[0]
		}
	}
	m.commands.input = 0
	if len(spec.Inputs) > 0 {
		m.commands.screen = commandForm
		return m, nil
	}
	return m.prepareGuidedCommand(spec)
}

func (m model) prepareGuidedCommand(spec commandSpec) (tea.Model, tea.Cmd) {
	line, err := buildGuidedCommand(spec, m.commands.values)
	if err != nil {
		m.commands.status = err.Error()
		return m, nil
	}
	m.commands.line = line
	if spec.Destructive {
		m.commands.screen = commandConfirm
		m.commands.confirm = ""
		m.commands.status = ""
		return m, nil
	}
	m.commands.screen = commandBrowse
	return m.startCommand()
}

func (m *model) cycleCommandSuggestion(spec commandSpec, delta int) {
	if m.commands.input >= len(spec.Inputs) || m.commands.input >= len(m.commands.values) {
		return
	}
	choices := spec.Inputs[m.commands.input].Suggestions
	if len(choices) == 0 {
		return
	}
	current := -1
	for i, choice := range choices {
		if choice == m.commands.values[m.commands.input] {
			current = i
			break
		}
	}
	if current < 0 {
		if delta < 0 {
			current = 0
		} else {
			current = -1
		}
	}
	m.commands.values[m.commands.input] = choices[(current+delta+len(choices))%len(choices)]
}

func (m *model) cycleCommandIdentity(delta int) {
	n := len(m.commands.identities)
	if n == 0 {
		return
	}
	m.commands.selected = (m.commands.selected + delta + n) % n
}

func (m model) commandIdentity() string {
	if len(m.commands.identities) == 0 || m.commands.selected >= len(m.commands.identities) {
		return "user"
	}
	return m.commands.identities[m.commands.selected]
}

func (m model) startCommand() (tea.Model, tea.Cmd) {
	if m.commands.running {
		return m, nil
	}
	line := strings.TrimSpace(m.commands.line)
	if _, err := splitCommandLine(line); err != nil {
		m.commands.status = err.Error()
		return m, nil
	}
	identity := m.commandIdentity()
	executable, err := os.Executable()
	if err != nil {
		m.commands.status = err.Error()
		return m, nil
	}
	m.commands.running = true
	m.commands.status = fmt.Sprintf("running as %s…", identity)
	runner := m.commandExec
	if runner == nil {
		runner = spawnOMOCommand
	}
	socket, officeDir := m.o.Sup.SocketPath, m.o.Sup.OfficeDir
	return m, func() tea.Msg { return runner(executable, socket, officeDir, identity, line) }
}

func (m *model) finishCommand(result commandResultMsg) {
	m.commands.running = false
	m.commands.status = "completed"
	if result.err != "" {
		m.commands.status = "failed: " + result.err
	}
	if strings.TrimSpace(result.output) == "" {
		result.output = "(no output)"
	}
	m.commands.logs = append(m.commands.logs, commandLog{
		at: result.at, identity: result.identity, command: result.command, output: result.output, err: result.err,
	})
	if len(m.commands.logs) > 50 {
		m.commands.logs = m.commands.logs[len(m.commands.logs)-50:]
	}
}

func spawnOMOCommand(executable, socket, officeDir, identity, line string) commandResultMsg {
	result := commandResultMsg{at: time.Now(), identity: identity, command: line}
	args, err := splitCommandLine(line)
	if err != nil {
		result.err = err.Error()
		return result
	}
	if len(args) > 0 && args[0] == "omo" {
		args = args[1:]
	}
	cmd := exec.Command(executable, args...)
	cmd.Dir = officeDir
	cmd.Env = commandEnvironment(os.Environ(), socket, identity)
	output, err := cmd.CombinedOutput()
	result.output = string(output)
	if err != nil {
		result.err = err.Error()
	}
	return result
}

func commandEnvironment(base []string, socket, identity string) []string {
	out := make([]string, 0, len(base)+2)
	for _, value := range base {
		if strings.HasPrefix(value, "OMO_SOCKET=") || strings.HasPrefix(value, "OMO_AGENT_ID=") {
			continue
		}
		out = append(out, value)
	}
	return append(out, "OMO_SOCKET="+socket, "OMO_AGENT_ID="+identity)
}

func splitCommandLine(line string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false
	started := false
	flush := func() {
		if started {
			args = append(args, current.String())
			current.Reset()
			started = false
		}
	}
	for _, r := range line {
		if escaped {
			current.WriteRune(r)
			started = true
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			started = true
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
			started = true
		case unicode.IsSpace(r):
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("unfinished escape in command")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in command")
	}
	flush()
	if len(args) == 0 || (len(args) == 1 && args[0] == "omo") {
		return nil, fmt.Errorf("enter an omo subcommand")
	}
	return args, nil
}

func (m model) renderCommandTab(b *strings.Builder) {
	b.WriteString(dimStyle.Render(" Browse guided omo operations, see required inputs and help, then run as an authorized identity."))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(" Destructive operations require explicit confirmation; Advanced keeps the raw command runner."))
	b.WriteString("\n\n")
	if len(m.commands.logs) == 0 {
		b.WriteString(dimStyle.Render(" No commands run from this TUI yet.\n"))
		return
	}
	b.WriteString(headerStyle.Render(" Recent command log") + "\n")
	start := max(0, len(m.commands.logs)-5)
	for _, entry := range m.commands.logs[start:] {
		status := "ok"
		if entry.err != "" {
			status = "error"
		}
		fmt.Fprintf(b, " %s  %-20s %-5s %s\n", entry.at.Format("15:04:05"), entry.identity, status, entry.command)
		first := strings.Split(strings.TrimSpace(entry.output), "\n")[0]
		fmt.Fprintf(b, "   %s\n", truncate(first, max(20, m.w-4)))
	}
}

func (m model) viewCommandConsole() string {
	footerRows := 1
	bodyRows := m.h - footerRows
	if bodyRows < 2 {
		bodyRows = 2
	}
	historyRows := bodyRows / 2
	commandRows := bodyRows - historyRows

	var controls strings.Builder
	controls.WriteString(m.fullWidth(headerStyle, " omo guided commands") + "\n")
	controls.WriteString(fmt.Sprintf(" Identity: ← %s →\n", m.commandIdentity()))
	if m.commands.status != "" {
		controls.WriteString(" Status: " + m.commands.status + "\n")
	}
	actions := []string{"? help"}
	switch m.commands.screen {
	case commandBrowse:
		controls.WriteString("\n" + headerStyle.Render(" Choose an operation") + "\n")
		start, end := visibleRange(len(m.commands.catalog), m.commands.item, max(1, commandRows-5))
		for i := start; i < end; i++ {
			line := " " + m.commands.catalog[i].Title
			if i == m.commands.item {
				line = selStyle.Render(line)
			}
			controls.WriteString(line + "\n")
		}
		if len(m.commands.catalog) > 0 && m.commands.showHelp {
			controls.WriteString("\n " + dimStyle.Render(m.commands.catalog[m.commands.item].Help) + "\n")
		}
		actions = append(actions, "↑/↓ choose", "←/→ identity", "Enter open", "Esc overview")
	case commandForm:
		spec := m.commands.catalog[m.commands.item]
		controls.WriteString("\n" + headerStyle.Render(" "+spec.Title) + "\n")
		controls.WriteString(" " + dimStyle.Render(spec.Help) + "\n\n")
		for i, field := range spec.Inputs {
			value := m.commands.values[i]
			if value == "" {
				value = dimStyle.Render("<empty>")
			}
			line := fmt.Sprintf(" %-18s %s", field.Name+requiredMark(field.Required), value)
			if i == m.commands.input {
				line = selStyle.Render(line + "█")
			}
			controls.WriteString(line + "\n")
			if i == m.commands.input && (m.commands.showHelp || len(field.Suggestions) > 0) {
				controls.WriteString("   " + dimStyle.Render(field.Help) + "\n")
				if len(field.Suggestions) > 0 {
					controls.WriteString("   choices: " + strings.Join(field.Suggestions, " | ") + "\n")
				}
			}
		}
		actions = append(actions, "Tab/↑/↓ field", "←/→ choice", "Enter run", "Esc back")
	case commandConfirm:
		controls.WriteString("\n" + alertStyle.Render(" Destructive operation") + "\n")
		controls.WriteString(" Command: " + m.commands.line + "\n")
		controls.WriteString(" Type yes to confirm: " + m.commands.confirm + "█\n")
		actions = append(actions, "Enter confirm", "Esc cancel")
	case commandRaw:
		identityLine := m.commandIdentity()
		inputLine := m.commands.line
		if m.commands.field == commandIdentity {
			identityLine = selStyle.Render("← " + identityLine + " →")
		} else if !m.commands.running {
			inputLine += "█"
		}
		controls.WriteString("\n" + headerStyle.Render(" Advanced: raw command") + "\n")
		controls.WriteString(" Identity: " + identityLine + "\n")
		controls.WriteString(" Command:  " + inputLine + "\n")
		actions = append(actions, "Tab field", "←/→ identity", "Enter run", "Esc back")
	}

	lines := fixedPaneLines(controls.String(), commandRows)
	if historyRows > 0 {
		lines = append(lines, headerStyle.Render(" Command history"))
		lines = append(lines, m.commandHistoryLines(historyRows-1)...)
		for len(lines) < bodyRows {
			lines = append(lines, "")
		}
	}
	return placeFooter(strings.Join(lines, "\n"), m.agentFooter(actions), m.w, m.h)
}

func fixedPaneLines(content string, rows int) []string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > rows {
		lines = lines[:rows]
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines
}

func (m model) commandHistoryLines(rows int) []string {
	if rows <= 0 {
		return nil
	}
	if len(m.commands.logs) == 0 {
		return fixedPaneLines(dimStyle.Render(" No commands run from this TUI yet."), rows)
	}
	remaining := rows
	var selected []string
	for i := len(m.commands.logs) - 1; i >= 0 && remaining > 0; i-- {
		entry := m.commands.logs[i]
		header := fmt.Sprintf("[%s] %s $ %s", entry.at.Format("15:04:05"), entry.identity, entry.command)
		if entry.err != "" {
			header += "  ERROR: " + entry.err
		}
		chunk := []string{header}
		for _, outputLine := range strings.Split(strings.TrimRight(entry.output, "\n"), "\n") {
			chunk = append(chunk, "  "+outputLine)
		}
		if len(chunk) > remaining {
			if len(selected) == 0 {
				chunk = append([]string{chunk[0]}, chunk[len(chunk)-(remaining-1):]...)
				selected = chunk
			}
			break
		}
		selected = append(chunk, selected...)
		remaining -= len(chunk)
	}
	return fixedPaneLines(strings.Join(selected, "\n"), rows)
}

func requiredMark(required bool) string {
	if required {
		return " *"
	}
	return ""
}
