// Package tui is omo's built-in viewer: a tabbed office overview and a
// full-screen live peek into any session. The CEO peek is the home screen.
package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/supervisor"
)

type mode int

const (
	modeOverview mode = iota
	modePeek
	modeQuitConfirm
	modeComposeMessage
	modeDetail
	modePromptInput
)

type overviewTab int

const (
	tabAgents overviewTab = iota
	tabMessages
	tabJobs
	tabIncidents
	tabEvents
	tabStatistics
	tabPreview
	tabCount
)

var tabNames = []string{"Agents", "Messages", "Jobs", "Incidents", "Events", "Statistics", "Preview"}

type composeField int

const (
	composeSubject composeField = iota
	composeBody
)

type messageComposer struct {
	target  string
	subject string
	body    string
	status  string
	field   composeField
}

type detailView struct {
	title  string
	body   string
	offset int
}

type promptInput struct {
	role   string
	goal   string
	status string
}

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

var (
	headerStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	footerStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24"))
	selStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("60"))
	tabStyle       = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("252"))
	activeTabStyle = tabStyle.Copy().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24"))
	noteStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	alertStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
)

var roleColor = map[string]lipgloss.Style{
	"ceo":             lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213")),
	"product_manager": lipgloss.NewStyle().Foreground(lipgloss.Color("117")),
	"developer":       lipgloss.NewStyle().Foreground(lipgloss.Color("114")),
	"reviewer":        lipgloss.NewStyle().Foreground(lipgloss.Color("222")),
	"freelancer":      lipgloss.NewStyle().Foreground(lipgloss.Color("180")),
	"smokealarm":      lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	"firefighter":     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203")),
}
var stateColor = map[string]lipgloss.Style{
	"spawning": lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	"working":  lipgloss.NewStyle().Foreground(lipgloss.Color("78")),
	"waiting":  lipgloss.NewStyle().Foreground(lipgloss.Color("179")),
	"done":     lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	"dead":     lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
}

func styled(m map[string]lipgloss.Style, key, value string) string {
	if st, ok := m[key]; ok {
		return st.Render(value)
	}
	return value
}

type model struct {
	o           *office.Office
	mode        mode
	returnMode  mode
	tab         overviewTab
	sel         [tabCount]int
	peek        string
	readOnly    bool
	compose     messageComposer
	detail      detailView
	preview     promptInput
	statsOffset int
	w, h        int
}

func defaultReadOnly(agent, ceo string) bool { return ceo == "" || agent != ceo }

func Run(o *office.Office) error {
	ceo := o.Sup.CEOName()
	m := model{o: o, mode: modePeek, peek: ceo, readOnly: defaultReadOnly(ceo, ceo)}
	if m.peek == "" {
		m.mode = modeOverview
	}
	o.Sup.SetInteraction(m.peek, m.mode == modePeek && !m.readOnly)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	done := make(chan struct{})
	go func() {
		select {
		case <-o.Sup.EmergencyStop():
			p.Quit()
		case <-done:
		}
	}()
	_, err := p.Run()
	close(done)
	return err
}

func (m model) Init() tea.Cmd { return tick() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		return m, tick()
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.resizePeek()
		m.clampDetailOffset()
		m.clampStatsOffset()
		return m, nil
	case tea.MouseMsg:
		switch m.mode {
		case modePeek:
			m.forwardMouse(msg)
		case modeDetail:
			m.scrollDetailMouse(msg)
		case modeOverview:
			m.scrollStatsMouse(msg)
		}
		return m, nil
	case tea.KeyMsg:
		// Ctrl+C is the emergency stop everywhere, including a writable peek.
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		switch m.mode {
		case modePeek:
			return m.updatePeek(msg)
		case modeQuitConfirm:
			return m.updateQuitConfirm(msg)
		case modeComposeMessage:
			return m.updateComposer(msg)
		case modeDetail:
			return m.updateDetail(msg)
		case modePromptInput:
			return m.updatePromptInput(msg)
		default:
			return m.updateOverview(msg)
		}
	}
	return m, nil
}

func (m *model) resizePeek() {
	if m.mode != modePeek || m.h <= 1 || m.w <= 0 {
		return
	}
	if sess, ok := m.o.Sup.Session(m.peek); ok {
		_ = sess.Resize(uint16(m.h-1), uint16(m.w))
	}
}

func (m model) updatePeek(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.readOnly && msg.String() == "m" && m.canMessage(m.peek) {
		m.openComposer(m.peek, modePeek)
		return m, nil
	}
	switch msg.Type {
	case tea.KeyCtrlQ, tea.KeyCtrlO:
		m.mode = modeOverview
		m.o.Sup.SetInteraction("", false)
		return m, tea.ClearScreen
	case tea.KeyCtrlT:
		m.readOnly = !m.readOnly
		m.o.Sup.SetInteraction(m.peek, !m.readOnly)
		return m, nil
	}
	if m.readOnly {
		return m, nil
	}
	if sess, ok := m.o.Sup.Session(m.peek); ok {
		if b := keyToBytes(msg); b != nil {
			if keyCountsAsTyping(msg) {
				m.o.Sup.RecordUserInput(m.peek)
			}
			_ = sess.SendText(string(b))
		}
	}
	return m, nil
}

func keyCountsAsTyping(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace, tea.KeyDelete, tea.KeyEnter, tea.KeyTab:
		return true
	}
	return false
}

func (m model) forwardMouse(msg tea.MouseMsg) {
	code := -1
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		code = 64
	case tea.MouseButtonWheelDown:
		code = 65
	}
	if code < 0 {
		return
	}
	if sess, ok := m.o.Sup.Session(m.peek); ok {
		// Forward SGR mouse wheel events to the nested CLI. Capturing the mouse
		// in omo prevents terminals from translating wheel motion into arrows.
		_ = sess.SendText(fmt.Sprintf("\x1b[<%d;%d;%dM", code, msg.X+1, msg.Y+1))
	}
}

func (m model) itemCount() int {
	switch m.tab {
	case tabAgents:
		return len(m.o.Sup.Overview())
	case tabMessages:
		xs, _ := m.o.Sup.Mail.History()
		return len(xs)
	case tabJobs:
		xs, _ := m.o.Sup.Jobs.List()
		return len(xs)
	case tabIncidents:
		xs, _ := db.AllIncidents(m.o.DB)
		return len(xs)
	case tabEvents:
		xs, _ := db.AllEvents(m.o.DB)
		return len(xs)
	case tabPreview:
		return len(config.AllRoles)
	}
	return 0
}

// overviewJobs returns newest jobs first, matching every other durable-history
// table in the overview. Queue.Store.List remains oldest first because dispatch
// order depends on it.
func (m model) overviewJobs() []*queue.Job {
	jobs, _ := m.o.Sup.Jobs.List()
	for left, right := 0, len(jobs)-1; left < right; left, right = left+1, right-1 {
		jobs[left], jobs[right] = jobs[right], jobs[left]
	}
	return jobs
}

func (m model) updateOverview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.returnMode, m.mode = modeOverview, modeQuitConfirm
	case "tab", "right":
		m.tab = (m.tab + 1) % overviewTab(len(tabNames))
		m.clampSelection()
	case "shift+tab", "left":
		m.tab = (m.tab + overviewTab(len(tabNames)) - 1) % overviewTab(len(tabNames))
		m.clampSelection()
	case "up":
		if m.tab == tabStatistics {
			m.scrollStats(-1)
			break
		}
		if m.sel[m.tab] > 0 {
			m.sel[m.tab]--
		}
	case "down":
		if m.tab == tabStatistics {
			m.scrollStats(1)
			break
		}
		if m.sel[m.tab] < m.itemCount()-1 {
			m.sel[m.tab]++
		}
	case "pgup", "ctrl+u":
		if m.tab == tabStatistics {
			m.scrollStats(-m.statsPageSize())
		}
	case "pgdown", "ctrl+d":
		if m.tab == tabStatistics {
			m.scrollStats(m.statsPageSize())
		}
	case "home", "g":
		if m.tab == tabStatistics {
			m.statsOffset = 0
		}
	case "end", "G":
		if m.tab == tabStatistics {
			m.statsOffset = m.statsMaxOffset()
		}
	case "x":
		if m.tab == tabMessages {
			msgs, _ := m.o.Sup.Mail.History()
			if i := m.sel[m.tab]; i < len(msgs) && msgs[i].To == "user" && !msgs[i].Read {
				_, _ = m.o.Sup.Mail.Read("user", msgs[i].ID)
			}
		}
	case "m":
		if target := m.overviewMessageTarget(); target != "" {
			m.openComposer(target, modeOverview)
		}
	case "enter":
		if m.tab == tabAgents {
			rows := m.o.Sup.Overview()
			i := m.sel[m.tab]
			if i < len(rows) {
				m.mode, m.peek = modePeek, rows[i].Name
				m.readOnly = defaultReadOnly(m.peek, m.o.Sup.CEOName())
				m.o.Sup.SetInteraction(m.peek, !m.readOnly)
				m.resizePeek()
			}
		} else if m.tab == tabPreview {
			m.openPromptInput()
		} else {
			m.openSelectedDetail()
		}
	}
	return m, nil
}

func (m *model) clampSelection() {
	if n := m.itemCount(); n == 0 {
		m.sel[m.tab] = 0
	} else if m.sel[m.tab] >= n {
		m.sel[m.tab] = n - 1
	}
}

func (m model) updateQuitConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		return m, tea.Quit
	case "n", "esc":
		m.mode = m.returnMode
	}
	return m, nil
}

func (m *model) openComposer(target string, returnMode mode) {
	m.returnMode = returnMode
	m.mode = modeComposeMessage
	m.compose = messageComposer{target: target}
	m.o.Sup.SetInteraction("", false)
}

func (m model) updateComposer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.closeComposer()
		return m, nil
	case tea.KeyCtrlS:
		subject := strings.TrimSpace(m.compose.subject)
		body := strings.TrimSpace(m.compose.body)
		if subject == "" || body == "" {
			m.compose.status = "Subject and body are required."
			return m, nil
		}
		if !m.canMessage(m.compose.target) {
			m.compose.status = "That agent is no longer running."
			return m, nil
		}
		if _, err := m.o.Sup.Mail.Send("user", m.compose.target, subject, body, bus.PrioNormal); err != nil {
			m.compose.status = err.Error()
			return m, nil
		}
		m.closeComposer()
		return m, nil
	case tea.KeyTab, tea.KeyShiftTab:
		if m.compose.field == composeSubject {
			m.compose.field = composeBody
		} else {
			m.compose.field = composeSubject
		}
	case tea.KeyEnter:
		if m.compose.field == composeSubject {
			m.compose.field = composeBody
		} else {
			m.compose.body += "\n"
		}
	case tea.KeyBackspace, tea.KeyDelete:
		if m.compose.field == composeSubject {
			m.compose.subject = dropLastRune(m.compose.subject)
		} else {
			m.compose.body = dropLastRune(m.compose.body)
		}
	case tea.KeyRunes:
		m.appendComposeText(string(msg.Runes))
	case tea.KeySpace:
		m.appendComposeText(" ")
	}
	if msg.Type != tea.KeyCtrlS {
		m.compose.status = ""
	}
	return m, nil
}

func (m *model) appendComposeText(value string) {
	if m.compose.field == composeSubject {
		m.compose.subject += strings.ReplaceAll(value, "\n", " ")
		return
	}
	m.compose.body += value
}

func (m *model) closeComposer() {
	m.mode = m.returnMode
	m.compose = messageComposer{}
	if m.mode == modePeek {
		m.o.Sup.SetInteraction(m.peek, !m.readOnly)
		m.resizePeek()
	}
}

func dropLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	return string(runes[:len(runes)-1])
}

func (m model) View() string {
	switch m.mode {
	case modePeek:
		return m.viewPeek()
	case modeQuitConfirm:
		return m.viewQuitConfirm()
	case modeComposeMessage:
		return m.viewComposer()
	case modeDetail:
		return m.viewDetail()
	case modePromptInput:
		return m.viewPromptInput()
	default:
		return m.viewOverview()
	}
}

func (m model) viewPeek() string {
	screen := dimStyle.Render("(session ended)")
	if sess, ok := m.o.Sup.Session(m.peek); ok {
		screen = sess.ScreenANSI()
	}
	actions := []string{m.peek, "Ctrl+O overview"}
	if m.readOnly {
		actions = append(actions, "Ctrl+T writable")
		if m.canMessage(m.peek) {
			actions = append(actions, "m message")
		}
	} else {
		actions = append(actions, "Ctrl+T read-only", "WRITABLE")
	}
	if m.o.Sup.NudgePending(m.peek) {
		actions = append(actions, "✉ mail nudge pending")
	}
	footer := m.agentFooter(actions)
	return screen + "\n\x1b[0m" + footer
}

func (m model) viewOverview() string {
	var b strings.Builder
	queued, running := m.o.Sup.QueueStats()
	header := fmt.Sprintf(" omo office — %d queued / %d running", queued, running)
	if n := m.o.Sup.OpenIncidents(); n > 0 {
		header += fmt.Sprintf("  ⚠ %d open", n)
	}
	b.WriteString(m.fullWidth(headerStyle, header))
	b.WriteByte('\n')
	for i, name := range tabNames {
		st := tabStyle
		if overviewTab(i) == m.tab {
			st = activeTabStyle
		}
		b.WriteString(st.Render(name))
	}
	b.WriteByte('\n')
	b.WriteByte('\n')
	switch m.tab {
	case tabAgents:
		m.renderAgents(&b)
	case tabMessages:
		m.renderMessages(&b)
	case tabJobs:
		m.renderJobs(&b)
	case tabIncidents:
		m.renderIncidents(&b)
	case tabEvents:
		m.renderEvents(&b)
	case tabStatistics:
		m.renderStatsPage(&b)
	case tabPreview:
		m.renderPreviewRoles(&b)
	}
	actions := []string{"Tab/←/→ switch"}
	if m.tab == tabStatistics && m.statsMaxOffset() > 0 {
		lines := m.statsLines()
		end := min(len(lines), m.statsOffset+m.statsPageSize())
		actions = append(actions, fmt.Sprintf("lines %d-%d/%d", m.statsOffset+1, end, len(lines)), "↑/↓ scroll", "PgUp/PgDn page", "Home/End")
	} else if m.itemCount() > 0 {
		actions = append(actions, "↑/↓ select")
	}
	if m.itemCount() > 0 {
		if m.tab == tabAgents {
			actions = append(actions, "Enter inspect")
		} else if m.tab == tabPreview {
			actions = append(actions, "Enter input")
		} else if m.tab != tabStatistics {
			actions = append(actions, "Enter open")
		}
	}
	if m.canReadSelectedMessage() {
		actions = append(actions, "x read")
	}
	if m.overviewMessageTarget() != "" {
		actions = append(actions, "m message")
	}
	actions = append(actions, "q quit")
	return placeFooter(b.String(), m.agentFooter(actions), m.w, m.h)
}

func (m model) renderPreviewRoles(b *strings.Builder) {
	b.WriteString(dimStyle.Render(" Select a role, then enter the goal/input to render exactly what its agent would receive.\n\n"))
	start, end := visibleRange(len(config.AllRoles), m.sel[m.tab], max(3, m.h-9))
	for i := start; i < end; i++ {
		role := config.AllRoles[i]
		configured := m.o.Sup.Config().Roles[role]
		models := strings.Join(configured.Models, ", ")
		if models == "" {
			models = "not configured"
		}
		line := fmt.Sprintf(" %-20s models: %-30s assignment: %s", role, models, configured.Assignment)
		if i == m.sel[m.tab] {
			b.WriteString(selStyle.Render(line) + "\n")
		} else {
			b.WriteString(styled(roleColor, role, line) + "\n")
		}
	}
}

func (m *model) openPromptInput() {
	i := m.sel[m.tab]
	if i < 0 || i >= len(config.AllRoles) {
		return
	}
	m.preview = promptInput{role: config.AllRoles[i]}
	m.mode = modePromptInput
	m.o.Sup.SetInteraction("", false)
}

func (m model) updatePromptInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeOverview
		m.preview = promptInput{}
	case tea.KeyCtrlP:
		prompt, err := m.o.Sup.PreviewPrompt(m.preview.role, m.preview.goal)
		if err != nil {
			m.preview.status = err.Error()
			return m, nil
		}
		m.detail = detailView{title: "Prompt preview — " + m.preview.role, body: prompt}
		m.mode = modeDetail
		m.clampDetailOffset()
	case tea.KeyEnter:
		m.preview.goal += "\n"
	case tea.KeyBackspace, tea.KeyDelete:
		m.preview.goal = dropLastRune(m.preview.goal)
	case tea.KeyRunes:
		m.preview.goal += string(msg.Runes)
	case tea.KeySpace:
		m.preview.goal += " "
	}
	if msg.Type != tea.KeyCtrlP {
		m.preview.status = ""
	}
	return m, nil
}

func (m model) viewPromptInput() string {
	dialogWidth := 88
	if m.w > 0 && dialogWidth > m.w-4 {
		dialogWidth = max(1, m.w-4)
	}
	inputWidth := max(1, dialogWidth-4)
	goal := m.preview.goal + "█"
	if m.preview.goal == "" {
		goal = dimStyle.Render("Enter the initial goal or generated role input here…") + "█"
	}
	var content strings.Builder
	content.WriteString(headerStyle.Render("Preview input — "+m.preview.role) + "\n\n")
	content.WriteString("Goal / role input\n" + wrapSimple(goal, inputWidth) + "\n")
	if m.preview.status != "" {
		content.WriteString("\n" + alertStyle.Render(m.preview.status) + "\n")
	}
	content.WriteString("\n" + dimStyle.Render("Enter newline • Ctrl+P render preview • Esc cancel"))
	box := lipgloss.NewStyle().Padding(1, 2).Width(dialogWidth).
		Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("39")).Render(content.String())
	if m.w > 0 && m.h > 0 {
		return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}

func (m model) renderAgents(b *strings.Builder) {
	rows := m.o.Sup.Overview()
	start, end := visibleRange(len(rows), m.sel[m.tab], max(3, m.h-7))
	for i := start; i < end; i++ {
		r := rows[i]
		name := r.Name
		if r.Depth > 0 {
			name = strings.Repeat("  ", r.Depth-1) + "└─ " + name
		}
		status := r.Step
		if status == "" {
			status = dimStyle.Render("(" + r.LastEvent + ")")
		}
		line := fmt.Sprintf(" %-24s %-16s %-24s %-9s %s", truncate(name, 24), r.Role, truncate(r.JobTitle, 24), r.State, status)
		if i == m.sel[m.tab] {
			b.WriteString(selStyle.Render(line) + "\n")
			continue
		}
		b.WriteString(fmt.Sprintf(" %-24s %-16s %-24s %-9s %s\n",
			styled(roleColor, r.Role, fmt.Sprintf("%-24s", truncate(name, 24))), styled(roleColor, r.Role, fmt.Sprintf("%-16s", r.Role)),
			truncate(r.JobTitle, 24), styled(stateColor, r.State, fmt.Sprintf("%-9s", r.State)), status))
	}
}

func (m model) renderMessages(b *strings.Builder) {
	msgs, _ := m.o.Sup.Mail.History()
	if len(msgs) == 0 {
		b.WriteString(dimStyle.Render(" No messages in this office yet.\n"))
		return
	}
	start, end := visibleRange(len(msgs), m.sel[m.tab], max(3, (m.h-9)/2))
	for i := start; i < end; i++ {
		msg := msgs[i]
		mark := "✓"
		if !msg.Read {
			mark = "●"
		}
		line := fmt.Sprintf(" %s #%-4d %-8s %-16s → %-16s %s", mark, msg.ID, msg.Priority, truncate(msg.From, 16), truncate(msg.To, 16), msg.Subject)
		if i == m.sel[m.tab] {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	i := m.sel[m.tab]
	if i < len(msgs) {
		b.WriteString("\n" + noteStyle.Render(msgs[i].Subject) + "\n")
		b.WriteString(wrapSimple(msgs[i].Body, max(20, m.w-2)) + "\n")
	}
}

func (m model) renderJobs(b *strings.Builder) {
	jobs := m.overviewJobs()
	if len(jobs) == 0 {
		b.WriteString(dimStyle.Render(" No jobs in this office yet.\n"))
		return
	}
	start, end := visibleRange(len(jobs), m.sel[m.tab], max(3, (m.h-9)/2))
	for i := start; i < end; i++ {
		j := jobs[i]
		line := fmt.Sprintf(" #%-4d %-10s %-16s %-20s %s", j.ID, j.State, j.Role, truncate(j.Assignee, 20), j.Title)
		if i == m.sel[m.tab] {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	i := m.sel[m.tab]
	if i < len(jobs) {
		j := jobs[i]
		b.WriteString(fmt.Sprintf("\n%s\nrepo: %s  branch: %s  parent: %d  review rejects: %d\ngoal: %s\nnote: %s\nresult: %s\n",
			noteStyle.Render(j.Title), j.Repo, j.Branch, j.ParentJob, j.ReviewRejections,
			wrapSimple(j.Goal, max(20, m.w-8)), wrapSimple(j.Note, max(20, m.w-8)), wrapSimple(j.Result, max(20, m.w-8))))
	}
}

func (m model) renderIncidents(b *strings.Builder) {
	incidents, _ := db.AllIncidents(m.o.DB)
	if len(incidents) == 0 {
		b.WriteString(dimStyle.Render(" No incidents in this office yet.\n"))
		return
	}
	start, end := visibleRange(len(incidents), m.sel[m.tab], max(3, (m.h-10)/2))
	for i := start; i < end; i++ {
		incident := incidents[i]
		line := fmt.Sprintf(" #%-4d %-9s %-16s %-22s %s", incident.ID, incident.State,
			truncate(incident.Class, 16), truncate(incident.Agent, 22), incident.Detail)
		if i == m.sel[m.tab] {
			line = selStyle.Render(line)
		} else if incident.State == "open" {
			line = alertStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	i := m.sel[m.tab]
	if i < len(incidents) {
		incident := incidents[i]
		resolved := "—"
		if incident.ResolvedAt.Valid {
			resolved = incident.ResolvedAt.String
		}
		b.WriteString(fmt.Sprintf("\n%s\nagent: %s  state: %s\ncreated: %s  resolved: %s\n%s\n",
			noteStyle.Render(incident.Class), incident.Agent, incident.State, incident.CreatedAt, resolved,
			wrapSimple(incident.Detail, max(20, m.w-2))))
	}
}

func (m model) renderEvents(b *strings.Builder) {
	events, _ := db.AllEvents(m.o.DB)
	if len(events) == 0 {
		b.WriteString(dimStyle.Render(" No events in this office yet.\n"))
		return
	}
	start, end := visibleRange(len(events), m.sel[m.tab], max(3, (m.h-10)/2))
	for i := start; i < end; i++ {
		event := events[i]
		subject := event.Agent
		if subject == "" {
			subject = "—"
		}
		line := fmt.Sprintf(" #%-5d %-19s %-24s %-22s", event.ID, event.CreatedAt,
			truncate(event.Kind, 24), truncate(subject, 22))
		if event.JobID != 0 {
			line += fmt.Sprintf(" job #%d", event.JobID)
		}
		if i == m.sel[m.tab] {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	i := m.sel[m.tab]
	if i < len(events) {
		event := events[i]
		agent := event.Agent
		if agent == "" {
			agent = "—"
		}
		job := "—"
		if event.JobID != 0 {
			job = fmt.Sprintf("#%d", event.JobID)
		}
		b.WriteString(fmt.Sprintf("\n%s\nagent: %s  job: %s  created: %s\n%s\n",
			noteStyle.Render(event.Kind), agent, job, event.CreatedAt,
			wrapSimple(event.Detail, max(20, m.w-2))))
	}
}

func detailValue(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func (m *model) openSelectedDetail() {
	detail, ok := m.selectedDetail()
	if !ok {
		return
	}
	m.detail = detail
	m.mode = modeDetail
	m.o.Sup.SetInteraction("", false)
	m.clampDetailOffset()
}

func (m *model) selectedDetail() (detailView, bool) {
	i := m.sel[m.tab]
	switch m.tab {
	case tabMessages:
		messages, _ := m.o.Sup.Mail.History()
		if i >= len(messages) {
			return detailView{}, false
		}
		message := messages[i]
		if message.To == "user" && !message.Read {
			if _, err := m.o.Sup.Mail.Read("user", message.ID); err == nil {
				message.Read = true
			}
		}
		read := "no"
		if message.Read {
			read = "yes"
		}
		return detailView{
			title: fmt.Sprintf("Message #%d — %s", message.ID, message.Subject),
			body: fmt.Sprintf("From: %s\nTo: %s\nPriority: %s\nCreated: %s\nRead: %s\n\nSubject: %s\n\n%s",
				message.From, message.To, message.Priority, message.CreatedAt, read, message.Subject, message.Body),
		}, true
	case tabJobs:
		jobs := m.overviewJobs()
		if i >= len(jobs) {
			return detailView{}, false
		}
		job := jobs[i]
		return detailView{
			title: fmt.Sprintf("Job #%d — %s", job.ID, job.Title),
			body: fmt.Sprintf("State: %s\nRole: %s\nModel: %s\nRepository: %s\nAssignee: %s\nParent job: %d\nWorktree: %s\nBranch: %s\nReview rejections: %d\nReview override: %t\n\nGoal\n%s\n\nNote\n%s\n\nResult\n%s",
				job.State, job.Role, detailValue(job.Model), detailValue(job.Repo), detailValue(job.Assignee), job.ParentJob,
				detailValue(job.Worktree), detailValue(job.Branch), job.ReviewRejections, job.ReviewOverride,
				detailValue(job.Goal), detailValue(job.Note), detailValue(job.Result)),
		}, true
	case tabIncidents:
		incidents, _ := db.AllIncidents(m.o.DB)
		if i >= len(incidents) {
			return detailView{}, false
		}
		incident := incidents[i]
		resolved := "—"
		if incident.ResolvedAt.Valid {
			resolved = incident.ResolvedAt.String
		}
		return detailView{
			title: fmt.Sprintf("Incident #%d — %s", incident.ID, incident.Class),
			body: fmt.Sprintf("Agent: %s\nState: %s\nCreated: %s\nResolved: %s\n\nDetail\n%s",
				incident.Agent, incident.State, incident.CreatedAt, resolved, detailValue(incident.Detail)),
		}, true
	case tabEvents:
		events, _ := db.AllEvents(m.o.DB)
		if i >= len(events) {
			return detailView{}, false
		}
		event := events[i]
		job := "—"
		if event.JobID != 0 {
			job = fmt.Sprintf("#%d", event.JobID)
		}
		return detailView{
			title: fmt.Sprintf("Event #%d — %s", event.ID, event.Kind),
			body: fmt.Sprintf("Agent: %s\nJob: %s\nCreated: %s\n\nDetail\n%s",
				detailValue(event.Agent), job, event.CreatedAt, detailValue(event.Detail)),
		}, true
	}
	return detailView{}, false
}

func (m model) detailLines() []string {
	width := m.w - 2
	if width < 1 {
		width = 80
	}
	// Preserve the stored record exactly while ensuring an unusually long URL,
	// hash, or path is still fully reachable instead of being clipped.
	wrapped := ansi.Hardwrap(m.detail.body, width, true)
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (m model) detailPageSize() int {
	if m.h <= 0 {
		return len(m.detailLines())
	}
	// One row each for the title and footer, plus a blank row below the title.
	return max(1, m.h-3)
}

func (m model) detailMaxOffset() int {
	return max(0, len(m.detailLines())-m.detailPageSize())
}

func (m *model) clampDetailOffset() {
	if m.mode != modeDetail {
		return
	}
	if m.detail.offset < 0 {
		m.detail.offset = 0
	}
	if last := m.detailMaxOffset(); m.detail.offset > last {
		m.detail.offset = last
	}
}

func (m *model) scrollDetail(delta int) {
	m.detail.offset += delta
	m.clampDetailOffset()
}

func (m *model) scrollDetailMouse(msg tea.MouseMsg) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollDetail(-3)
	case tea.MouseButtonWheelDown:
		m.scrollDetail(3)
	}
}

func (m model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "q", "left":
		m.mode = modeOverview
		m.detail = detailView{}
	case "up", "k":
		m.scrollDetail(-1)
	case "down", "j":
		m.scrollDetail(1)
	case "pgup", "ctrl+u":
		m.scrollDetail(-m.detailPageSize())
	case "pgdown", "ctrl+d":
		m.scrollDetail(m.detailPageSize())
	case "home", "g":
		m.detail.offset = 0
	case "end", "G":
		m.detail.offset = m.detailMaxOffset()
	}
	return m, nil
}

func (m model) viewDetail() string {
	m.clampDetailOffset()
	lines := m.detailLines()
	start := m.detail.offset
	end := min(len(lines), start+m.detailPageSize())
	content := m.fullWidth(headerStyle, " "+m.detail.title) + "\n\n" + strings.Join(lines[start:end], "\n")
	actions := []string{fmt.Sprintf("lines %d-%d/%d", start+1, end, len(lines)), "↑/↓ scroll", "PgUp/PgDn page", "Home/End", "Enter/Esc back"}
	return placeFooter(content, m.agentFooter(actions), m.w, m.h)
}

func (m model) renderStats(b *strings.Builder) {
	overall := m.o.Sup.OverallStats()
	b.WriteString(headerStyle.Render(" Overall statistics") + "\n")
	m.renderStatsSummary(b, overall)
	b.WriteString(fmt.Sprintf(" CEO time (estimated): active %s • idle %s\n",
		overall.CEO.Active.Round(time.Second), overall.CEO.Idle.Round(time.Second)))
	b.WriteString("\n" + headerStyle.Render(" Current session") + "\n")
	session := m.o.Sup.SessionStats()
	b.WriteString(fmt.Sprintf(" Duration: %s\n", time.Since(session.Started).Round(time.Second)))
	m.renderStatsSummary(b, session)
	b.WriteString(fmt.Sprintf(" CEO time (estimated): active %s • idle %s\n",
		session.CEO.Active.Round(time.Second), session.CEO.Idle.Round(time.Second)))
}

func (m model) statsLines() []string {
	var b strings.Builder
	m.renderStats(&b)
	return strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
}

func (m model) statsPageSize() int {
	if m.h <= 0 {
		return len(m.statsLines())
	}
	// Header, tabs, their spacer, and the footer each consume one row.
	return max(1, m.h-4)
}

func (m model) statsMaxOffset() int {
	return max(0, len(m.statsLines())-m.statsPageSize())
}

func (m *model) clampStatsOffset() {
	if m.statsOffset < 0 {
		m.statsOffset = 0
	}
	if last := m.statsMaxOffset(); m.statsOffset > last {
		m.statsOffset = last
	}
}

func (m *model) scrollStats(delta int) {
	if m.tab != tabStatistics {
		return
	}
	m.statsOffset += delta
	m.clampStatsOffset()
}

func (m *model) scrollStatsMouse(msg tea.MouseMsg) {
	if m.tab != tabStatistics {
		return
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollStats(-3)
	case tea.MouseButtonWheelDown:
		m.scrollStats(3)
	}
}

func (m model) renderStatsPage(b *strings.Builder) {
	lines := m.statsLines()
	start := min(m.statsOffset, m.statsMaxOffset())
	end := min(len(lines), start+m.statsPageSize())
	b.WriteString(strings.Join(lines[start:end], "\n"))
	if end > start {
		b.WriteByte('\n')
	}
}

func (m model) renderStatsSummary(b *strings.Builder, s supervisor.SessionStats) {
	b.WriteString(fmt.Sprintf(" Messages: %d • Reviews: %d started / %d rejected / %d merged / %d overridden\n",
		s.Messages, s.ReviewsStarted, s.ReviewsRejected, s.ReviewsMerged, s.ReviewsOverridden))
	roles := make([]string, 0, len(s.AgentsByRole))
	for _, role := range supervisor.SortedRoleNames(s.AgentsByRole) {
		roles = append(roles, fmt.Sprintf("%s %d", role, s.AgentsByRole[role]))
	}
	b.WriteString(" Agents started: " + detailValue(strings.Join(roles, " • ")) + "\n")
	b.WriteString(" " + dimStyle.Render("Models (time excludes separately reported CEO activity)") + "\n")
	models := make([]string, 0, len(s.Models))
	for name := range s.Models {
		models = append(models, name)
	}
	sort.Strings(models)
	for _, name := range models {
		mt := s.Models[name]
		b.WriteString(fmt.Sprintf(" %-20s agents %-4d active %-10s idle %s\n", name, mt.AgentsStarted, mt.Active.Round(time.Second), mt.Idle.Round(time.Second)))
	}
}

func (m model) viewQuitConfirm() string {
	body := "Quit omo and stop every agent? [y/N]\n\nCtrl+C is always the immediate emergency stop."
	box := lipgloss.NewStyle().Bold(true).Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("203")).Render(body)
	if m.w > 0 && m.h > 0 {
		return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}

func (m model) viewComposer() string {
	dialogWidth := 72
	if m.w > 0 && dialogWidth > m.w-4 {
		dialogWidth = max(1, m.w-4)
	}
	inputWidth := max(1, dialogWidth-4)
	subject := m.compose.subject
	body := m.compose.body
	if m.compose.field == composeSubject {
		subject += "█"
	} else {
		body += "█"
	}
	if body == "" {
		body = dimStyle.Render("(empty)")
	}
	var content strings.Builder
	content.WriteString(headerStyle.Render("Message to "+m.compose.target) + "\n\n")
	content.WriteString("Subject\n" + wrapSimple(subject, inputWidth) + "\n\n")
	content.WriteString("Body\n" + wrapSimple(body, inputWidth) + "\n")
	if m.compose.status != "" {
		content.WriteString("\n" + alertStyle.Render(m.compose.status) + "\n")
	}
	content.WriteString("\n" + dimStyle.Render("Tab switch field • Enter newline • Ctrl+S send • Esc cancel"))
	box := lipgloss.NewStyle().Padding(1, 2).Width(dialogWidth).
		Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("39")).Render(content.String())
	if m.w > 0 && m.h > 0 {
		return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}

func (m model) canReadSelectedMessage() bool {
	if m.tab != tabMessages {
		return false
	}
	messages, _ := m.o.Sup.Mail.History()
	i := m.sel[m.tab]
	return i < len(messages) && messages[i].To == "user" && !messages[i].Read
}

func (m model) overviewMessageTarget() string {
	switch m.tab {
	case tabAgents:
		rows := m.o.Sup.Overview()
		i := m.sel[m.tab]
		if i < len(rows) && m.canMessage(rows[i].Name) {
			return rows[i].Name
		}
	case tabMessages:
		messages, _ := m.o.Sup.Mail.History()
		i := m.sel[m.tab]
		if i >= len(messages) {
			return ""
		}
		// Prefer the non-user side. For inter-agent traffic, replying to the
		// sender is least surprising; fall back to its recipient if it ended.
		candidates := []string{messages[i].From, messages[i].To}
		if messages[i].From == "user" {
			candidates[0], candidates[1] = messages[i].To, messages[i].From
		}
		for _, candidate := range candidates {
			if candidate != "user" && m.canMessage(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func (m model) canMessage(agent string) bool {
	if agent == "" || agent == "user" || m.o == nil || m.o.Sup == nil || m.o.Sup.Mail == nil || m.o.Sup.Mail.Dir == nil {
		return false
	}
	_, ok := m.o.Sup.Mail.Dir.Role(agent)
	return ok
}

func (m model) roleFooterParts() []string {
	counts := m.o.Sup.RoleCounts()
	var parts []string
	for _, role := range config.AllRoles {
		v := counts[role]
		if v.Total == 0 {
			continue
		}
		label := map[string]string{"product_manager": "PM", "developer": "DEV", "reviewer": "REV", "freelancer": "FREE", "smokealarm": "SMOKE", "firefighter": "FIRE", "ceo": "CEO"}[role]
		parts = append(parts, fmt.Sprintf("%s %d/%d", label, v.Active, v.Total))
	}
	return parts
}

// agentFooter keeps contextual controls truthful, then adds role counts in
// org-chart order until the terminal width is exhausted.
func (m model) agentFooter(actions []string) string {
	parts := make([]string, 0, len(actions)+1)
	if m.o != nil && m.o.Sup != nil && m.o.Sup.Mail != nil {
		if unread, err := m.o.Sup.Mail.UnreadCount("user"); err == nil && unread > 0 {
			parts = append(parts, fmt.Sprintf("✉ USER %d unread", unread))
		}
	}
	parts = append(parts, actions...)
	value := " " + strings.Join(parts, " • ")
	for _, part := range m.roleFooterParts() {
		candidate := value + " • " + part
		if m.w > 0 && ansi.StringWidth(candidate) > m.w {
			break
		}
		value = candidate
	}
	return m.fullWidth(footerStyle, value)
}

func (m model) fullWidth(st lipgloss.Style, value string) string {
	if m.w > 0 {
		return st.Copy().Width(m.w).MaxWidth(m.w).Render(truncate(value, m.w))
	}
	return st.Render(value)
}

// placeFooter reserves the final physical terminal row for the footer. It
// also clips body lines horizontally so a narrow terminal cannot soft-wrap a
// table row and push the footer below the viewport.
func placeFooter(content, footer string, width, height int) string {
	if height <= 0 {
		return strings.TrimRight(content, "\n") + "\n" + footer
	}
	if width > 0 {
		footer = ansi.Truncate(footer, width, "")
	}
	if height == 1 {
		return footer
	}

	content = strings.TrimRight(content, "\n")
	var lines []string
	if content != "" {
		lines = strings.Split(content, "\n")
	}
	if width > 0 {
		for i := range lines {
			lines[i] = ansi.Truncate(lines[i], width, "")
		}
	}
	bodyRows := height - 1
	if len(lines) > bodyRows {
		lines = lines[:bodyRows]
	}
	for len(lines) < bodyRows {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n") + "\n" + footer
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return string(r[:1])
	}
	return string(r[:n-1]) + "…"
}

func wrapSimple(s string, width int) string {
	if width < 1 {
		return s
	}
	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(paragraph) {
			if len([]rune(line))+1+len([]rune(word)) > width && line != "" {
				out = append(out, line)
				line = word
			} else if line == "" {
				line = word
			} else {
				line += " " + word
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func visibleRange(total, selected, limit int) (int, int) {
	if total <= limit {
		return 0, total
	}
	start := selected - limit/2
	if start < 0 {
		start = 0
	}
	if start+limit > total {
		start = total - limit
	}
	return start, start + limit
}
