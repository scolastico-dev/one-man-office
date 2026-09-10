// Package tui is omo's built-in viewer: a tabbed office overview and a
// full-screen live peek into any session. The CEO peek is the home screen.
package tui

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
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
	modeSafeShutdownConfirm
	modeActionMenu
	modeCommandConsole
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
	tabPlugins
	tabCommands
	tabPreview
	tabCount
)

var tabNames = []string{"Agents", "Messages", "Jobs", "Incidents", "Events", "Statistics", "Plugins", "Commands", "Preview"}

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
	title      string
	body       string
	offset     int
	plugin     string
	returnMode mode
}

type promptInput struct {
	role   string
	goal   string
	status string
}

type jobFilter int

const (
	jobFilterAll jobFilter = iota
	jobFilterActive
	jobFilterCompleted
	jobFilterFailed
	jobFilterThisOffice
	jobFilterExternal
)

var jobFilterNames = []string{"all", "active", "completed", "failed", "this office", "other office"}

type tickMsg time.Time

func tick(current mode) tea.Cmd {
	return tea.Tick(refreshInterval(current), func(t time.Time) tea.Msg { return tickMsg(t) })
}

func refreshInterval(current mode) time.Duration {
	if current == modePeek {
		return 100 * time.Millisecond
	}
	return 500 * time.Millisecond
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
var pluginStateColor = map[string]lipgloss.Style{
	"ready":    lipgloss.NewStyle().Foreground(lipgloss.Color("78")),
	"running":  lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
	"error":    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203")),
	"disabled": lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	"missing":  lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
}

func styled(m map[string]lipgloss.Style, key, value string) string {
	if st, ok := m[key]; ok {
		return st.Render(value)
	}
	return value
}

type model struct {
	o            *office.Office
	mode         mode
	returnMode   mode
	tab          overviewTab
	sel          [tabCount]int
	peek         string
	readOnly     bool
	observer     bool
	compose      messageComposer
	detail       detailView
	manual       map[string]manualPluginInput
	safeStatus   string
	action       actionMenu
	actionStatus string
	commands     commandConsole
	commandExec  commandExecutor
	peekMouse    peekMouseInput
	preview      promptInput
	jobFilter    jobFilter
	jobSearch    string
	jobSearching bool
	statsOffset  int
	cache        *viewCache
	hitMap       *hitMap
	w, h         int
}

func defaultReadOnly(agent, ceo string) bool { return ceo == "" || agent != ceo }

func Run(o *office.Office) error {
	ceo := o.Sup.CEOName()
	m := model{o: o, mode: modePeek, peek: ceo, readOnly: defaultReadOnly(ceo, ceo), cache: &viewCache{}, hitMap: &hitMap{}}
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

// RunReadOnly starts an observer that never owns sessions or office lifecycle.
func RunReadOnly(o *office.Office) error {
	m := model{o: o, mode: modeOverview, observer: true, cache: &viewCache{}, hitMap: &hitMap{}}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m model) Init() tea.Cmd { return tick(m.mode) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case manualPluginResultMsg:
		m.finishManualPlugin(msg)
		return m, nil
	case commandResultMsg:
		m.finishCommand(msg)
		return m, nil
	case tickMsg:
		return m, tick(m.mode)
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.resizePeek()
		m.clampDetailOffset()
		m.clampStatsOffset()
		return m, nil
	case tea.MouseMsg:
		// Preserve existing wheel routing before considering clickable regions.
		switch m.mode {
		case modePeek:
			m.forwardMouse(msg)
		case modeDetail:
			m.scrollDetailMouse(msg)
		case modeOverview:
			m.scrollStatsMouse(msg)
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if action, ok := m.hitMap.at(msg.X, msg.Y); ok {
				return m.updateClick(action)
			}
		}
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case modeSafeShutdownConfirm:
		return m.updateSafeShutdownConfirm(msg)
	case modeActionMenu:
		return m.updateActionMenu(msg)
	case modeCommandConsole:
		return m.updateCommandConsole(msg)
	case modePromptInput:
		return m.updatePromptInput(msg)
	default:
		return m.updateOverview(msg)
	}
}

func (m model) updateClick(action clickAction) (tea.Model, tea.Cmd) {
	switch action := action.(type) {
	case keyAction:
		return m.updateKey(action.key)
	case tabAction:
		if m.mode == modeOverview {
			m.selectOverviewTab(action.tab)
		}
	case rowAction:
		if m.mode == modeCommandConsole {
			return m.updateCommandRowClick(action)
		}
		if m.mode != modeOverview || action.tab != m.tab || action.row < 0 || action.row >= m.overviewItemCount(action.tab) {
			return m, nil
		}
		if m.sel[action.tab] != action.row {
			m.sel[action.tab] = action.row
			return m, nil
		}
		return m.openSelectedOverview()
	case inputAction:
		return m.updateCommandInputClick(action)
	case commandIdentityAction:
		return m.updateCommandIdentityClick(action)
	case suggestionAction:
		return m.updateCommandSuggestionClick(action)
	case pluginActionAction:
		if m.mode == modeDetail {
			return m.clickManualPluginAction(action.action)
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
	if m.readOnly && msg.String() == "p" {
		m.openReadyPrompt()
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
		if keyToBytes(msg) != nil {
			if keyCountsAsTyping(msg) {
				m.o.Sup.RecordUserInput(m.peek)
			}
			_ = sendPeekInput(sess, msg)
		}
	}
	return m, nil
}

type peekInput interface {
	SendText(string) error
	SendSubmit() error
}

func sendPeekInput(sess peekInput, msg tea.KeyMsg) error {
	if msg.Type == tea.KeyEnter {
		return sess.SendSubmit()
	}
	if b := keyToBytes(msg); b != nil {
		return sess.SendText(string(b))
	}
	return nil
}

func keyCountsAsTyping(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace, tea.KeyDelete, tea.KeyEnter, tea.KeyTab:
		return true
	}
	return false
}

type peekMouseInput interface {
	SendText(string) error
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
	if m.peekMouse != nil {
		_ = m.peekMouse.SendText(fmt.Sprintf("\x1b[<%d;%d;%dM", code, msg.X+1, msg.Y+1))
		return
	}
	if sess, ok := m.o.Sup.Session(m.peek); ok {
		// Forward SGR mouse wheel events to the nested CLI. Capturing the mouse
		// in omo prevents terminals from translating wheel motion into arrows.
		_ = sess.SendText(fmt.Sprintf("\x1b[<%d;%d;%dM", code, msg.X+1, msg.Y+1))
	}
}

func (m model) itemCount() int {
	return m.overviewItemCount(m.tab)
}

func (m model) overviewItemCount(tab overviewTab) int {
	switch tab {
	case tabAgents:
		return len(m.overviewRows())
	case tabMessages:
		return len(m.messageHistory())
	case tabJobs:
		return len(m.overviewJobs())
	case tabIncidents:
		return len(m.incidentHistory())
	case tabEvents:
		return m.eventHistoryCount()
	case tabPlugins:
		return len(m.pluginRuntimes())
	case tabCommands:
		return 0
	case tabPreview:
		return len(config.AllRoles)
	}
	return 0
}

// overviewJobs returns newest jobs first, matching every other durable-history
// table in the overview. Queue.Store.List remains oldest first because dispatch
// order depends on it.
func (m model) overviewJobs() []*queue.Job {
	jobs := m.cachedOverviewJobs()
	filtered := make([]*queue.Job, 0, len(jobs))
	for _, job := range jobs {
		if !jobMatchesFilter(job, m.jobFilter) {
			continue
		}
		if query := strings.ToLower(strings.TrimSpace(m.jobSearch)); query != "" && !strings.Contains(strings.ToLower(job.Title+" "+job.Goal+" "+string(job.State)), query) {
			continue
		}
		filtered = append(filtered, job)
	}
	return filtered
}

func jobMatchesFilter(job *queue.Job, filter jobFilter) bool {
	switch filter {
	case jobFilterActive:
		return job.State != queue.StateDone && job.State != queue.StateFailed && job.State != queue.StateCancelled
	case jobFilterCompleted:
		return job.State == queue.StateDone
	case jobFilterFailed:
		return job.State == queue.StateFailed || job.State == queue.StateCancelled
	case jobFilterThisOffice:
		return job.ID > 0
	case jobFilterExternal:
		return job.ID < 0
	default:
		return true
	}
}

func (m model) updateOverview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.observer {
		return m.updateReadOnlyOverview(msg)
	}
	if m.tab == tabJobs && m.jobSearching {
		switch msg.Type {
		case tea.KeyEsc, tea.KeyEnter:
			m.jobSearching = false
		case tea.KeyBackspace, tea.KeyDelete:
			m.jobSearch = dropLastRune(m.jobSearch)
		case tea.KeyRunes:
			m.jobSearch += string(msg.Runes)
		case tea.KeySpace:
			m.jobSearch += " "
		}
		m.sel[m.tab] = 0
		return m, nil
	}
	switch msg.String() {
	case "q":
		m.returnMode, m.mode = modeOverview, modeQuitConfirm
	case "tab", "right":
		m.switchOverviewTab(1)
	case "shift+tab", "left":
		m.switchOverviewTab(-1)
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
			msgs := m.messageHistory()
			if i := m.sel[m.tab]; i < len(msgs) && msgs[i].To == "user" && !msgs[i].Read {
				_, _ = m.o.Sup.Mail.Read("user", msgs[i].ID)
			}
		} else {
			m.openActionMenu()
		}
	case "/":
		if m.tab == tabJobs {
			m.jobSearching = true
		}
	case "f":
		if m.tab == tabJobs {
			m.jobFilter = (m.jobFilter + 1) % jobFilter(len(jobFilterNames))
			m.sel[m.tab] = 0
		}
	case "m":
		if target := m.overviewMessageTarget(); target != "" {
			m.openComposer(target, modeOverview)
		}
	case "enter":
		return m.openSelectedOverview()
	}
	return m, nil
}

func (m model) updateReadOnlyOverview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.returnMode, m.mode = modeOverview, modeQuitConfirm
	case "tab", "right":
		m.switchOverviewTab(1)
	case "shift+tab", "left":
		m.switchOverviewTab(-1)
	case "up":
		if m.tab == tabStatistics {
			m.scrollStats(-1)
		} else if m.sel[m.tab] > 0 {
			m.sel[m.tab]--
		}
	case "down":
		if m.tab == tabStatistics {
			m.scrollStats(1)
		} else if m.sel[m.tab] < m.itemCount()-1 {
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
	case "enter":
		if m.tab != tabAgents && m.tab != tabStatistics {
			return m.openSelectedOverview()
		}
	}
	return m, nil
}

func (m *model) switchOverviewTab(delta int) {
	count := overviewTab(len(tabNames))
	if m.observer {
		count = tabPlugins + 1
	}
	m.selectOverviewTab((m.tab + overviewTab(count) + overviewTab(delta)) % overviewTab(count))
}

func (m *model) selectOverviewTab(tab overviewTab) {
	count := overviewTab(len(tabNames))
	if m.observer {
		count = tabPlugins + 1
	}
	if tab < 0 || tab >= count {
		return
	}
	m.tab = tab
	m.clampSelection()
}

func (m *model) clampSelection() {
	if n := m.itemCount(); n == 0 {
		m.sel[m.tab] = 0
	} else if m.sel[m.tab] < 0 {
		m.sel[m.tab] = 0
	} else if m.sel[m.tab] >= n {
		m.sel[m.tab] = n - 1
	}
}

func (m model) openSelectedOverview() (tea.Model, tea.Cmd) {
	if m.tab == tabCommands {
		m.openCommandConsole()
		return m, nil
	}
	if m.tab == tabAgents {
		if m.observer {
			return m, nil
		}
		rows := m.overviewRows()
		i := m.sel[m.tab]
		if i < len(rows) {
			m.mode, m.peek = modePeek, rows[i].Name
			m.readOnly = defaultReadOnly(m.peek, m.o.Sup.CEOName())
			m.o.Sup.SetInteraction(m.peek, !m.readOnly)
			m.resizePeek()
		}
		return m, nil
	}
	if m.tab == tabStatistics {
		return m, nil
	}
	if m.tab == tabPreview {
		m.openPromptInput()
		return m, nil
	}
	m.openSelectedDetail()
	return m, nil
}

func (m model) updateQuitConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		return m, tea.Quit
	case "s":
		if !m.observer {
			m.mode = modeSafeShutdownConfirm
		}
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
	// Every helper participating in this render shares one database snapshot.
	// A new cache per View keeps input-triggered renders immediately fresh.
	if m.cache == nil {
		m.cache = &viewCache{}
	}
	if m.hitMap == nil {
		m.hitMap = &hitMap{}
	}
	m.hitMap.reset()
	m.cache.beginView()
	switch m.mode {
	case modePeek:
		return m.viewPeek()
	case modeQuitConfirm:
		return m.viewQuitConfirm()
	case modeComposeMessage:
		return m.viewComposer()
	case modeDetail:
		return m.viewDetail()
	case modeSafeShutdownConfirm:
		return m.viewSafeShutdownConfirm()
	case modeActionMenu:
		return m.viewActionMenu()
	case modeCommandConsole:
		return m.viewCommandConsole()
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
		actions = append(actions, "p prompt")
		if m.canMessage(m.peek) {
			actions = append(actions, "m message")
		}
	} else {
		actions = append(actions, "Ctrl+T read-only", "WRITABLE")
	}
	if m.o.Sup.InputPending(m.peek) {
		actions = append(actions, "⏳ injected input pending")
	}
	footer := m.agentFooterAt(actions, strings.Count(screen, "\n")+1)
	return screen + "\n\x1b[0m" + footer
}

func (m model) viewOverview() string {
	if m.cache == nil {
		m.cache = &viewCache{}
	}
	var b strings.Builder
	queued, running := m.cachedQueueStats()
	header := fmt.Sprintf(" omo office — %d queued / %d running", queued, running)
	if m.observer {
		header += "  READ ONLY — unmodified database snapshot"
	}
	if m.o.Sup.SafeMode() {
		header += "  SAFE MODE"
	}
	if n := m.cachedOpenIncidents(); n > 0 {
		header += fmt.Sprintf("  ⚠ %d open", n)
	}
	if m.safeStatus != "" {
		header += "  " + m.safeStatus
	}
	b.WriteString(m.fullWidth(headerStyle, header))
	b.WriteByte('\n')
	if m.actionStatus != "" {
		b.WriteString(noteStyle.Render(" "+m.actionStatus) + "\n")
	}
	visibleTabs := tabNames
	if m.observer {
		visibleTabs = tabNames[:tabPlugins+1]
	}
	tabY := 1
	if m.actionStatus != "" {
		tabY++
	}
	tabX := 0
	for i, name := range visibleTabs {
		st := tabStyle
		if overviewTab(i) == m.tab {
			st = activeTabStyle
		}
		rendered := st.Render(name)
		b.WriteString(rendered)
		tabWidth := ansi.StringWidth(rendered)
		m.addHit(tabX, tabY, tabWidth, 1, tabAction{tab: overviewTab(i)})
		tabX += tabWidth
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
	case tabPlugins:
		m.renderPlugins(&b)
	case tabCommands:
		m.renderCommandTab(&b)
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
	if m.itemCount() > 0 && !m.observer {
		if m.tab == tabAgents {
			actions = append(actions, "Enter inspect")
		} else if m.tab == tabPreview {
			actions = append(actions, "Enter input")
		} else if m.tab != tabStatistics {
			actions = append(actions, "Enter open")
		}
	}
	if m.tab == tabCommands {
		actions = append(actions, "Enter console")
	}
	if !m.observer && m.canReadSelectedMessage() {
		actions = append(actions, "x read")
	} else if !m.observer && len(m.selectedActions()) > 0 {
		actions = append(actions, "x actions")
	}
	if !m.observer && m.overviewMessageTarget() != "" {
		actions = append(actions, "m message")
	}
	if m.observer {
		if m.itemCount() > 0 && m.tab != tabAgents && m.tab != tabStatistics {
			actions = append(actions, "Enter view")
		}
		actions = append(actions, "READ ONLY", "q quit")
	} else {
		actions = append(actions, "q quit")
	}
	return placeFooter(b.String(), m.agentFooter(actions), m.w, m.h)
}

func (m model) renderPreviewRoles(b *strings.Builder) {
	b.WriteString(dimStyle.Render(" Select a role, then enter the goal/input to render exactly what its agent would receive."))
	b.WriteString("\n\n")
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
			line = selStyle.Render(line)
		} else {
			line = styled(roleColor, role, line)
		}
		b.WriteString(line + "\n")
		m.registerOverviewRowHit(b, tabPreview, i, line)
	}
}

func (m model) registerOverviewRowHit(b *strings.Builder, tab overviewTab, index int, line string) {
	y := strings.Count(b.String(), "\n") - 1
	if m.h > 0 && y >= m.h-1 {
		return
	}
	m.addHit(0, y, ansi.StringWidth(line), 1, rowAction{tab: tab, row: index})
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
	return m.placeModal(box,
		renderedTextHit{text: "Enter newline", action: keyAction{key: tea.KeyMsg{Type: tea.KeyEnter}}},
		renderedTextHit{text: "Ctrl+P render preview", action: keyAction{key: tea.KeyMsg{Type: tea.KeyCtrlP}}},
		renderedTextHit{text: "Esc cancel", action: keyAction{key: tea.KeyMsg{Type: tea.KeyEsc}}},
	)
}

func (m model) renderAgents(b *strings.Builder) {
	usageLines := m.renderModelUsage(b)
	rows := m.overviewRows()
	start, end := visibleRange(len(rows), m.sel[m.tab], max(3, m.h-7-usageLines))
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
			line = selStyle.Render(line)
		} else {
			line = fmt.Sprintf(" %-24s %-16s %-24s %-9s %s",
				styled(roleColor, r.Role, fmt.Sprintf("%-24s", truncate(name, 24))), styled(roleColor, r.Role, fmt.Sprintf("%-16s", r.Role)),
				truncate(r.JobTitle, 24), styled(stateColor, r.State, fmt.Sprintf("%-9s", r.State)), status)
		}
		b.WriteString(line + "\n")
		m.registerOverviewRowHit(b, tabAgents, i, line)
	}
}

func (m model) renderModelUsage(b *strings.Builder) int {
	snapshots := m.usageSnapshots()
	if len(snapshots) == 0 {
		return 0
	}
	lastCheck := snapshots[0].FetchedAt
	for _, snapshot := range snapshots[1:] {
		if snapshot.FetchedAt.After(lastCheck) {
			lastCheck = snapshot.FetchedAt
		}
	}
	b.WriteString(dimStyle.Render(" Usage — last successful check: "+formatLocalTime(lastCheck)) + "\n")
	lines := 2
	const providerWidth = 20
	displayCandidates := make([]string, len(snapshots))
	displayCounts := map[string]int{}
	for i, snapshot := range snapshots {
		displayCandidates[i] = usageProviderCandidate(snapshot)
		displayCounts[truncate(displayCandidates[i], providerWidth)]++
	}
	for i, snapshot := range snapshots {
		providerLabel := displayCandidates[i]
		projected := truncate(providerLabel, providerWidth)
		if displayCounts[projected] > 1 || len(providerLabel) > providerWidth {
			providerLabel = usageProviderHashedLabel(snapshot, providerWidth)
		}
		label := providerLabel + " weekly"
		b.WriteString(fmt.Sprintf(" %-28s %s %.1f%%\n", label, usageBar(snapshot.UsedPercent), snapshot.UsedPercent))
		lines++
		if snapshot.HasSession {
			label = providerLabel + " session"
			b.WriteString(fmt.Sprintf(" %-28s %s %.1f%%\n", label, usageBar(snapshot.SessionUsedPercent), snapshot.SessionUsedPercent))
			lines++
		}
	}
	b.WriteByte('\n')
	return lines
}

func usageProviderCandidate(snapshot db.ModelUsageSnapshot) string {
	providerLabel := snapshot.Provider
	if _, credentialFile, ok := strings.Cut(snapshot.Scope, ":"); ok {
		credentialFile, _, _ = strings.Cut(credentialFile, "|")
		providerLabel += " (" + filepath.Base(filepath.Dir(credentialFile)) + ")"
	}
	return providerLabel
}

func usageProviderHashedLabel(snapshot db.ModelUsageSnapshot, width int) string {
	candidate := usageProviderCandidate(snapshot)
	sum := sha256.Sum256([]byte(snapshot.Scope))
	suffix := fmt.Sprintf("#%x", sum[:4])
	room := width - len(suffix)
	return truncate(candidate, max(1, room)) + suffix
}

func (m model) renderPlugins(b *strings.Builder) {
	runtimes := m.pluginRuntimes()
	if len(runtimes) == 0 {
		b.WriteString(dimStyle.Render(" No plugins are installed in this office.\n"))
		return
	}
	b.WriteString(dimStyle.Render(" Plugin               State       Hooks  Last event           Last run") + "\n")
	start, end := visibleRange(len(runtimes), m.sel[m.tab], max(3, (m.h-11)/2))
	for i := start; i < end; i++ {
		runtime := runtimes[i]
		line := fmt.Sprintf(" %-20s %-11s %5d  %-20s %s", truncate(runtime.Name, 20), runtime.State,
			runtime.HookCount, truncate(detailValue(runtime.LastEvent), 20), pluginTime(runtime.LastRunAt))
		if i == m.sel[m.tab] {
			line = selStyle.Render(line)
		} else {
			state := styled(pluginStateColor, runtime.State, fmt.Sprintf("%-11s", runtime.State))
			line = fmt.Sprintf(" %-20s %s %5d  %-20s %s", truncate(runtime.Name, 20), state,
				runtime.HookCount, truncate(detailValue(runtime.LastEvent), 20), pluginTime(runtime.LastRunAt))
		}
		b.WriteString(line + "\n")
		m.registerOverviewRowHit(b, tabPlugins, i, line)
	}
	selected := runtimes[m.sel[m.tab]]
	b.WriteString("\n" + noteStyle.Render("Last log — "+selected.Name) + "  " + dimStyle.Render(pluginTime(selected.LastLogAt)) + "\n")
	if selected.LastLog == "" {
		b.WriteString(dimStyle.Render(" No log output recorded yet.") + "\n")
	} else {
		b.WriteString(wrapSimple(pluginOverviewLogLine(selected.LastLog), max(20, m.w-2)) + "\n")
	}
}

// pluginOverviewLogLine keeps legacy multi-line runtime values from expanding
// the frequently refreshed overview. New records already persist this line.
func pluginOverviewLogLine(message string) string {
	if at := strings.LastIndex(message, "\n"); at >= 0 {
		return message[at+1:]
	}
	return message
}

func pluginTime(value time.Time) string {
	return formatLocalTime(value)
}

func formatLocalTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.Local().Format("2006-01-02 15:04:05")
}

func usageBar(percent float64) string {
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}
	const width = 20
	filled := int(percent*width/100 + 0.5)
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
}

func (m model) renderMessages(b *strings.Builder) {
	msgs := m.messageHistory()
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
		m.registerOverviewRowHit(b, tabMessages, i, line)
	}
	i := m.sel[m.tab]
	if i < len(msgs) {
		b.WriteString("\n" + noteStyle.Render(msgs[i].Subject) + "\n")
		b.WriteString(wrapSimple(msgs[i].Body, max(20, m.w-2)) + "\n")
	}
}

func (m model) renderJobs(b *strings.Builder) {
	jobs := m.overviewJobs()
	search := strings.TrimSpace(m.jobSearch)
	filterLine := fmt.Sprintf(" filter: %s", jobFilterNames[m.jobFilter])
	if search != "" || m.jobSearching {
		filterLine += fmt.Sprintf("  search: %s%s", search, func() string {
			if m.jobSearching {
				return "█"
			}
			return ""
		}())
	}
	b.WriteString(dimStyle.Render(filterLine+"  (f cycle filter, / search)") + "\n")
	if len(jobs) == 0 {
		b.WriteString(dimStyle.Render(" No jobs in this office yet.\n"))
		return
	}
	start, end := visibleRange(len(jobs), m.sel[m.tab], max(3, (m.h-9)/2))
	for i := start; i < end; i++ {
		j := jobs[i]
		origin := "local"
		if j.ID < 0 {
			origin = "external"
		}
		line := fmt.Sprintf(" #%-4d %-10s %-16s %-20s %-8s %s", j.ID, j.State, j.Role, truncate(j.Assignee, 20), origin, j.Title)
		if i == m.sel[m.tab] {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
		m.registerOverviewRowHit(b, tabJobs, i, line)
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
	incidents := m.incidentHistory()
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
		m.registerOverviewRowHit(b, tabIncidents, i, line)
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
	count := m.eventHistoryCount()
	if count == 0 {
		b.WriteString(dimStyle.Render(" No events in this office yet.\n"))
		return
	}
	start, end := visibleRange(count, m.sel[m.tab], max(3, (m.h-10)/2))
	events := m.eventHistoryPage(start, end-start)
	for offset, event := range events {
		i := start + offset
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
		m.registerOverviewRowHit(b, tabEvents, i, line)
	}
	i := m.sel[m.tab]
	if i >= start && i-start < len(events) {
		event := events[i-start]
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
	if !m.observer {
		m.o.Sup.SetInteraction("", false)
	}
	m.clampDetailOffset()
}

func (m *model) selectedDetail() (detailView, bool) {
	i := m.sel[m.tab]
	switch m.tab {
	case tabMessages:
		messages := m.messageHistory()
		if i >= len(messages) {
			return detailView{}, false
		}
		message := messages[i]
		if !m.observer && message.To == "user" && !message.Read {
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
		incidents := m.incidentHistory()
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
		event, ok := m.eventAt(i)
		if !ok {
			return detailView{}, false
		}
		job := "—"
		if event.JobID != 0 {
			job = fmt.Sprintf("#%d", event.JobID)
		}
		return detailView{
			title: fmt.Sprintf("Event #%d — %s", event.ID, event.Kind),
			body: fmt.Sprintf("Agent: %s\nJob: %s\nCreated: %s\n\nDetail\n%s",
				detailValue(event.Agent), job, event.CreatedAt, detailValue(event.Detail)),
		}, true
	case tabPlugins:
		runtimes := m.pluginRuntimes()
		if i >= len(runtimes) {
			return detailView{}, false
		}
		runtime := runtimes[i]
		return detailView{
			title:  "Plugin — " + runtime.Name,
			plugin: runtime.Name,
			body:   m.pluginDetailBody(runtime),
		}, true
	}
	return detailView{}, false
}

func (m model) pluginDetailBody(runtime db.PluginRuntime) string {
	var body strings.Builder
	fmt.Fprintf(&body, "State: %s\nVersion: %s\nHooks: %d\nLast event: %s\nLast run: %s\nDescription: %s\n\nLog history",
		runtime.State, detailValue(runtime.Version), runtime.HookCount, detailValue(runtime.LastEvent), pluginTime(runtime.LastRunAt), detailValue(runtime.Description))
	logs := m.pluginLogs(runtime.Name)
	if len(logs) == 0 {
		fmt.Fprintf(&body, "\n%s  %s", pluginTime(runtime.LastLogAt), detailValue(runtime.LastLog))
		return body.String()
	}
	fmt.Fprintf(&body, " (%d retained lines)", len(logs))
	for _, log := range logs {
		fmt.Fprintf(&body, "\n%s  %s", pluginTime(log.CreatedAt), log.Message)
	}
	return body.String()
}

func (m model) detailLines() []string {
	width := m.w - 2
	if width < 1 {
		width = 80
	}
	// Preserve the stored record exactly while ensuring an unusually long URL,
	// hash, or path is still fully reachable instead of being clipped.
	body := m.detail.body
	if actions := m.manualPluginActions(); len(actions) > 0 {
		body += "\n\nManual actions"
		for i := range actions {
			body += "\n" + m.manualActionLine(i)
		}
	}
	if manual, ok := m.manual[m.detail.plugin]; ok {
		if manual.editing {
			body += "\n\nArguments (quotes group words; empty runs without arguments):\n> " + manual.input + "▏"
		}
		if manual.status != "" {
			body += "\n\n" + manual.status
		}
	}
	wrapped := ansi.Hardwrap(body, width, true)
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (m model) manualActionLine(index int) string {
	actions := m.manualPluginActions()
	if index < 0 || index >= len(actions) {
		return ""
	}
	action := actions[index]
	prefix, accepts := "  ", "no"
	if manual := m.manual[m.detail.plugin]; manual.selecting && index == manual.selected {
		prefix = "› "
	}
	if action.ManualArgs {
		accepts = "yes"
	}
	return fmt.Sprintf("%s%s — %s (arguments: %s)", prefix, action.Name, action.Description, accepts)
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
	if manual := m.manual[m.detail.plugin]; (manual.editing || manual.selecting) && !m.observer {
		return m.updateManualPluginInput(msg)
	}
	switch msg.String() {
	case "r":
		return m.openManualPlugin()
	case "esc", "enter", "q", "left":
		m.mode = m.detail.returnMode
		m.detail = detailView{}
		if m.mode == modePeek {
			m.resizePeek()
		}
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

func (m *model) openReadyPrompt() {
	agent, err := db.GetAgent(m.o.DB, m.peek)
	body := "The agent has not completed omo ready, so no prompt has been recorded yet."
	if err != nil {
		body = "The recorded prompt is unavailable: " + err.Error()
	} else if agent.ReadyPrompt != "" {
		body = agent.ReadyPrompt
	}
	m.detail = detailView{title: "Ready prompt — " + m.peek, body: body, returnMode: modePeek}
	m.mode = modeDetail
	m.o.Sup.SetInteraction("", false)
	m.clampDetailOffset()
}

func (m model) viewDetail() string {
	m.clampDetailOffset()
	lines := m.detailLines()
	start := m.detail.offset
	end := min(len(lines), start+m.detailPageSize())
	m.registerManualActionHits(lines, start, end)
	content := m.fullWidth(headerStyle, " "+m.detail.title) + "\n\n" + strings.Join(lines[start:end], "\n")
	actions := []string{fmt.Sprintf("lines %d-%d/%d", start+1, end, len(lines)), "↑/↓ scroll", "PgUp/PgDn page", "Home/End", "Enter/Esc back"}
	if len(m.manualPluginActions()) > 0 && !m.manual[m.detail.plugin].running {
		if m.manual[m.detail.plugin].selecting {
			actions = []string{"↑/↓ select action", "Enter choose", "Esc cancel"}
		} else if m.manual[m.detail.plugin].editing {
			actions = []string{"Enter trigger", "Esc cancel arguments"}
		} else {
			actions = append(actions, "r trigger")
		}
	}
	return placeFooter(content, m.agentFooter(actions), m.w, m.h)
}

func (m model) registerManualActionHits(lines []string, start, end int) {
	actions := m.manualPluginActions()
	manual := m.manual[m.detail.plugin]
	if len(actions) == 0 || manual.editing || manual.running {
		return
	}
	width := m.w - 2
	if width < 1 {
		width = 80
	}
	baseLines := strings.Split(ansi.Hardwrap(m.detail.body, width, true), "\n")
	lineIndex := len(baseLines) + 1 // the blank line before "Manual actions"
	lineIndex += len(strings.Split(ansi.Hardwrap("Manual actions", width, true), "\n"))
	for index := range actions {
		actionLines := strings.Split(ansi.Hardwrap(m.manualActionLine(index), width, true), "\n")
		actionStart := lineIndex
		lineIndex += len(actionLines)
		visibleStart := max(actionStart, start)
		visibleEnd := min(lineIndex, end)
		for physical := visibleStart; physical < visibleEnd; physical++ {
			y := 2 + physical - start
			if m.h > 0 && y >= m.h-1 {
				continue
			}
			line := lines[physical]
			m.addHit(0, y, ansi.StringWidth(line), 1, pluginActionAction{action: index})
		}
	}
}

func (m model) renderStats(b *strings.Builder) {
	overall := m.o.Sup.OverallStats()
	b.WriteString(headerStyle.Render(" Overall statistics") + "\n")
	m.renderStatsSummary(b, overall)
	b.WriteString(fmt.Sprintf(" CEO time (estimated): active %s • idle %s\n",
		overall.CEO.Active.Round(time.Second), overall.CEO.Idle.Round(time.Second)))
	if m.observer {
		return
	}
	b.WriteString("\n" + headerStyle.Render(" Current session") + "\n")
	session := m.o.Sup.SessionStats()
	b.WriteString(fmt.Sprintf(" Duration: %s\n", time.Since(session.Started).Round(time.Second)))
	m.renderStatsSummary(b, session)
	b.WriteString(fmt.Sprintf(" CEO time (estimated): active %s • idle %s\n",
		session.CEO.Active.Round(time.Second), session.CEO.Idle.Round(time.Second)))
}

func (m model) statsLines() []string {
	c := m.activeCache()
	if c.statsLoaded {
		return c.statsLines
	}
	var b strings.Builder
	m.renderStats(&b)
	c.statsLines = strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	c.statsLoaded = true
	return c.statsLines
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
	body := "Quit omo and stop every agent? [y/N]\n\ns safe shutdown • Ctrl+C immediate emergency stop"
	if m.observer {
		body = "Close the read-only observer? [y/N]\n\nThe running office and all agents are unaffected."
	}
	box := lipgloss.NewStyle().Bold(true).Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("203")).Render(body)
	return m.placeModal(box,
		renderedTextHit{text: "s safe shutdown", action: keyAction{key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}}},
		renderedTextHit{text: "Ctrl+C immediate emergency stop", action: keyAction{key: tea.KeyMsg{Type: tea.KeyCtrlC}}},
	)
}

func (m model) updateSafeShutdownConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		if err := m.o.Sup.BeginSafeShutdown("user"); err != nil {
			m.safeStatus = err.Error()
		} else {
			m.safeStatus = "safe shutdown in progress"
		}
		m.mode = modeOverview
	case "n", "esc":
		m.mode = modeOverview
	}
	return m, nil
}

func (m model) viewSafeShutdownConfirm() string {
	body := "Safely shut down omo? [y/N]\n\nNew spawns stop immediately. Every living agent is asked to finish only if near done; otherwise it must save a concise durable handoff. omo stops when all agents respond or the deadline expires."
	box := lipgloss.NewStyle().Bold(true).Padding(1, 2).Width(72).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("214")).Render(body)
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
	return m.placeModal(box,
		renderedTextHit{text: "Tab switch field", action: keyAction{key: tea.KeyMsg{Type: tea.KeyTab}}},
		renderedTextHit{text: "Enter newline", action: keyAction{key: tea.KeyMsg{Type: tea.KeyEnter}}},
		renderedTextHit{text: "Ctrl+S send", action: keyAction{key: tea.KeyMsg{Type: tea.KeyCtrlS}}},
		renderedTextHit{text: "Esc cancel", action: keyAction{key: tea.KeyMsg{Type: tea.KeyEsc}}},
	)
}

func (m model) canReadSelectedMessage() bool {
	if m.tab != tabMessages {
		return false
	}
	messages := m.messageHistory()
	i := m.sel[m.tab]
	return i < len(messages) && messages[i].To == "user" && !messages[i].Read
}

func (m model) overviewMessageTarget() string {
	switch m.tab {
	case tabAgents:
		rows := m.overviewRows()
		i := m.sel[m.tab]
		if i < len(rows) && m.canMessage(rows[i].Name) {
			return rows[i].Name
		}
	case tabMessages:
		messages := m.messageHistory()
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
	_, counts := m.footerSnapshot()
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
	return m.agentFooterAt(actions, m.h-1)
}

func (m model) agentFooterAt(actions []string, footerY int) string {
	parts := make([]string, 0, len(actions)+1)
	if unread, _ := m.footerSnapshot(); unread > 0 {
		parts = append(parts, fmt.Sprintf("✉ USER %d unread", unread))
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
	m.registerFooterActions(parts, footerY)
	return m.fullWidth(footerStyle, value)
}

func (m model) registerFooterActions(parts []string, footerY int) {
	if m.w <= 0 || m.h <= 0 || footerY < 0 || footerY >= m.h {
		return
	}
	x := 1 // agentFooter prefixes the joined parts with one leading space.
	for _, part := range parts {
		width := ansi.StringWidth(part)
		if width <= 0 || x+width > m.w {
			break
		}
		if key, ok := footerKey(part); ok {
			m.addHit(x, footerY, width, 1, keyAction{key: key})
		}
		x += width + ansi.StringWidth(" • ")
	}
}

func footerKey(part string) (tea.KeyMsg, bool) {
	key := func(value string) tea.KeyMsg {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
	}
	switch part {
	case "? help":
		return key("?"), true
	case "Tab/←/→ switch":
		return tea.KeyMsg{Type: tea.KeyTab}, true
	case "↑/↓ select", "↑/↓ scroll", "↑/↓ choose", "↑/↓ select action":
		return tea.KeyMsg{Type: tea.KeyDown}, true
	case "PgUp/PgDn page":
		return tea.KeyMsg{Type: tea.KeyPgDown}, true
	case "Home/End":
		return tea.KeyMsg{Type: tea.KeyEnd}, true
	case "Enter inspect", "Enter input", "Enter open", "Enter console", "Enter view", "Enter run", "Enter confirm", "Enter/Esc back", "Enter choose", "Enter trigger":
		return tea.KeyMsg{Type: tea.KeyEnter}, true
	case "←/→ identity", "←/→ choice":
		return tea.KeyMsg{Type: tea.KeyRight}, true
	case "Tab field", "Tab/↑/↓ field":
		return tea.KeyMsg{Type: tea.KeyTab}, true
	case "Esc overview", "Esc back", "Esc cancel", "Esc cancel arguments":
		return tea.KeyMsg{Type: tea.KeyEsc}, true
	case "Ctrl+O overview":
		return tea.KeyMsg{Type: tea.KeyCtrlO}, true
	case "Ctrl+T writable", "Ctrl+T read-only":
		return tea.KeyMsg{Type: tea.KeyCtrlT}, true
	case "p prompt":
		return key("p"), true
	case "m message":
		return key("m"), true
	case "r trigger":
		return key("r"), true
	case "x read", "x actions":
		return key("x"), true
	case "q quit":
		return key("q"), true
	default:
		return tea.KeyMsg{}, false
	}
}

func (m model) addHit(x, y, width, height int, action clickAction) {
	if m.hitMap == nil || m.w <= 0 || m.h <= 0 || width <= 0 || height <= 0 {
		return
	}
	if x < 0 || y < 0 || x+width > m.w || y+height > m.h {
		return
	}
	m.hitMap.add(x, y, width, height, action)
}

type renderedTextHit struct {
	text   string
	action clickAction
}

func (m model) placeModal(box string, hits ...renderedTextHit) string {
	view := box
	if m.w > 0 && m.h > 0 {
		view = lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, box)
	}
	m.registerRenderedTextHits(view, hits...)
	return view
}

func (m model) registerRenderedTextHits(view string, hits ...renderedTextHit) {
	if m.w <= 0 || m.h <= 0 {
		return
	}
	lines := strings.Split(ansi.Strip(view), "\n")
	for _, hit := range hits {
		if hit.text == "" || hit.action == nil {
			continue
		}
		width := ansi.StringWidth(hit.text)
		if width <= 0 {
			continue
		}
		for y, line := range lines {
			for from := 0; from < len(line); {
				at := strings.Index(line[from:], hit.text)
				if at < 0 {
					break
				}
				at += from
				x := ansi.StringWidth(line[:at])
				if y < m.h && x+width <= m.w {
					m.addHit(x, y, width, 1, hit.action)
				}
				from = at + len(hit.text)
			}
		}
	}
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
