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
	m.commands.field = commandLine
	m.commands.status = ""
	m.mode = modeCommandConsole
	m.o.Sup.SetInteraction("", false)
}

func (m model) updateCommandConsole(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeOverview
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
	if len(args) == 0 {
		result.err = "enter an omo subcommand"
		return result
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
	b.WriteString(dimStyle.Render(" Run any omo subcommand through a real second CLI process connected to this office.\n Select an agent identity in the console to impersonate it under normal server-side permissions.\n\n"))
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
	identity := m.commandIdentity()
	identityLine := identity
	commandLine := m.commands.line
	if m.commands.field == commandIdentity {
		identityLine = selStyle.Render("← " + identity + " →")
	} else if !m.commands.running {
		commandLine += "█"
	}
	var b strings.Builder
	b.WriteString(m.fullWidth(headerStyle, " omo command console") + "\n")
	b.WriteString(" Identity: " + identityLine + "\n")
	b.WriteString(" Command:  " + commandLine + "\n")
	if m.commands.status != "" {
		b.WriteString(" Status:   " + m.commands.status + "\n")
	}
	b.WriteString("\n" + headerStyle.Render(" Command log") + "\n")
	var logLines []string
	for _, entry := range m.commands.logs {
		line := fmt.Sprintf("[%s] %s $ %s", entry.at.Format("15:04:05"), entry.identity, entry.command)
		if entry.err != "" {
			line += "  ERROR: " + entry.err
		}
		logLines = append(logLines, line)
		for _, outputLine := range strings.Split(strings.TrimRight(entry.output, "\n"), "\n") {
			logLines = append(logLines, "  "+outputLine)
		}
	}
	available := max(1, m.h-7)
	if len(logLines) > available {
		logLines = logLines[len(logLines)-available:]
	}
	b.WriteString(strings.Join(logLines, "\n"))
	actions := []string{"Tab field", "←/→ or ↑/↓ identity", "Enter/Ctrl+R run", "Esc overview"}
	return placeFooter(b.String(), m.agentFooter(actions), m.w, m.h)
}
