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

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/supervisor"
)

type mode int

const (
	modeOverview mode = iota
	modePeek
	modeQuitConfirm
)

type overviewTab int

const (
	tabAgents overviewTab = iota
	tabMessages
	tabJobs
	tabStatistics
)

var tabNames = []string{"Agents", "Messages", "Jobs", "Statistics"}

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
	o          *office.Office
	mode       mode
	returnMode mode
	tab        overviewTab
	sel        [4]int
	peek       string
	readOnly   bool
	w, h       int
}

func defaultReadOnly(agent, ceo string) bool { return ceo == "" || agent != ceo }

func Run(o *office.Office) error {
	ceo := o.Sup.CEOName()
	m := model{o: o, mode: modePeek, peek: ceo, readOnly: defaultReadOnly(ceo, ceo)}
	if m.peek == "" {
		m.mode = modeOverview
	}
	o.Sup.SetInteraction(m.peek, m.mode == modePeek && !m.readOnly)
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
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
		return m, nil
	case tea.MouseMsg:
		if m.mode == modePeek {
			m.forwardMouse(msg)
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
	switch msg.Type {
	case tea.KeyCtrlQ, tea.KeyCtrlO:
		m.mode = modeOverview
		m.o.Sup.SetInteraction("", false)
		return m, nil
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
	}
	return 0
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
		if m.sel[m.tab] > 0 {
			m.sel[m.tab]--
		}
	case "down":
		if m.sel[m.tab] < m.itemCount()-1 {
			m.sel[m.tab]++
		}
	case "x":
		if m.tab == tabMessages {
			msgs, _ := m.o.Sup.Mail.History()
			if i := m.sel[m.tab]; i < len(msgs) && msgs[i].To == "user" {
				_, _ = m.o.Sup.Mail.Read("user", msgs[i].ID)
			}
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

func (m model) View() string {
	switch m.mode {
	case modePeek:
		return m.viewPeek()
	case modeQuitConfirm:
		return m.viewQuitConfirm()
	default:
		return m.viewOverview()
	}
}

func (m model) viewPeek() string {
	screen := dimStyle.Render("(session ended)")
	if sess, ok := m.o.Sup.Session(m.peek); ok {
		screen = sess.ScreenANSI()
	}
	state := "typing goes to this agent"
	if m.readOnly {
		state = "READ-ONLY"
	}
	if m.o.Sup.NudgePending(m.peek) {
		state += " • ✉ mail nudge waiting for typing pause"
	}
	footer := m.fullWidth(footerStyle, fmt.Sprintf(" %s • Ctrl+O/Ctrl+Q overview • Ctrl+T read-only • %s", m.peek, state))
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
	case tabStatistics:
		m.renderStats(&b)
	}
	footer := " Tab/←/→ switch • ↑/↓ select • Enter inspect • x read • q quit"
	roleInfo := m.roleFooter()
	if roleInfo != "" {
		footer += "  " + roleInfo
	}
	return placeFooter(b.String(), m.fullWidth(footerStyle, footer), m.w, m.h)
}

func (m model) renderAgents(b *strings.Builder) {
	rows := m.o.Sup.Overview()
	start, end := visibleRange(len(rows), m.sel[m.tab], max(3, m.h-7))
	for i := start; i < end; i++ {
		r := rows[i]
		status := r.Step
		if status == "" {
			status = dimStyle.Render("(" + r.LastEvent + ")")
		}
		line := fmt.Sprintf(" %-24s %-16s %-24s %-9s %s", r.Name, r.Role, truncate(r.JobTitle, 24), r.State, status)
		if i == m.sel[m.tab] {
			b.WriteString(selStyle.Render(line) + "\n")
			continue
		}
		b.WriteString(fmt.Sprintf(" %-24s %-16s %-24s %-9s %s\n",
			styled(roleColor, r.Role, fmt.Sprintf("%-24s", r.Name)), styled(roleColor, r.Role, fmt.Sprintf("%-16s", r.Role)),
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
	jobs, _ := m.o.Sup.Jobs.List()
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

func (m model) renderStats(b *strings.Builder) {
	s := m.o.Sup.SessionStats()
	b.WriteString(fmt.Sprintf(" Session duration: %s\n Messages: %d\n Reviews: %d started • %d rejected • %d merged • %d overridden\n\n",
		time.Since(s.Started).Round(time.Second), s.Messages, s.ReviewsStarted, s.ReviewsRejected, s.ReviewsMerged, s.ReviewsOverridden))
	b.WriteString(fmt.Sprintf(" CEO time (estimated from CLI output): active %s • idle %s\n\n",
		s.CEO.Active.Round(time.Second), s.CEO.Idle.Round(time.Second)))
	b.WriteString(headerStyle.Render(" Agents started by role") + "\n")
	for _, role := range supervisor.SortedRoleNames(s.AgentsByRole) {
		b.WriteString(fmt.Sprintf(" %-20s %d\n", role, s.AgentsByRole[role]))
	}
	b.WriteString("\n" + headerStyle.Render(" Worker time by model (CEO excluded)") + "\n")
	models := make([]string, 0, len(s.Models))
	for name := range s.Models {
		models = append(models, name)
	}
	sort.Strings(models)
	for _, name := range models {
		mt := s.Models[name]
		b.WriteString(fmt.Sprintf(" %-20s active %-10s idle %s\n", name, mt.Active.Round(time.Second), mt.Idle.Round(time.Second)))
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

func (m model) roleFooter() string {
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
	return strings.Join(parts, " • ")
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
