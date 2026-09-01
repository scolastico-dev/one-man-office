package tui

import (
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/supervisor"
)

// viewCache exists for one View call. Bubble Tea may ask several helpers for
// the same active-tab data while building rows, controls, details, and the
// footer; querying once keeps rendering cost predictable as history grows.
type viewCache struct {
	agentsLoaded        bool
	agents              []supervisor.AgentRow
	messagesLoaded      bool
	messages            []bus.Message
	jobsLoaded          bool
	jobs                []*queue.Job
	incidentsLoaded     bool
	incidents           []db.Incident
	eventCountLoaded    bool
	eventCount          int
	eventPageLoaded     bool
	eventPageStart      int
	eventPage           []db.Event
	usageLoaded         bool
	usage               []db.ModelUsageSnapshot
	queueLoaded         bool
	queued              int
	running             int
	openIncidentsLoaded bool
	openIncidents       int
	footerLoaded        bool
	footerAt            time.Time
	userUnread          int
	roleCounts          map[string]supervisor.RoleCount
	statsLoaded         bool
	statsLines          []string
}

func (c *viewCache) beginView() {
	c.agentsLoaded, c.messagesLoaded, c.jobsLoaded = false, false, false
	c.incidentsLoaded, c.eventCountLoaded, c.eventPageLoaded, c.usageLoaded = false, false, false, false
	c.queueLoaded, c.openIncidentsLoaded, c.statsLoaded = false, false, false
	c.agents, c.messages, c.jobs = nil, nil, nil
	c.incidents, c.eventPage, c.usage, c.statsLines = nil, nil, nil, nil
}

func (m model) activeCache() *viewCache {
	if m.cache != nil {
		return m.cache
	}
	// Update helpers can run before the first View in tests. They still get a
	// valid one-shot cache, while normal rendering installs a shared cache at
	// the start of View.
	return &viewCache{}
}

func (m model) overviewRows() []supervisor.AgentRow {
	c := m.activeCache()
	if !c.agentsLoaded {
		c.agents = m.o.Sup.Overview()
		c.agentsLoaded = true
	}
	return c.agents
}

func (m model) messageHistory() []bus.Message {
	c := m.activeCache()
	if !c.messagesLoaded {
		c.messages, _ = m.o.Sup.Mail.History()
		c.messagesLoaded = true
	}
	return c.messages
}

func (m model) cachedOverviewJobs() []*queue.Job {
	c := m.activeCache()
	if !c.jobsLoaded {
		c.jobs, _ = m.o.Sup.Jobs.List()
		for left, right := 0, len(c.jobs)-1; left < right; left, right = left+1, right-1 {
			c.jobs[left], c.jobs[right] = c.jobs[right], c.jobs[left]
		}
		c.jobsLoaded = true
	}
	return c.jobs
}

func (m model) incidentHistory() []db.Incident {
	c := m.activeCache()
	if !c.incidentsLoaded {
		c.incidents, _ = db.AllIncidents(m.o.DB)
		c.incidentsLoaded = true
	}
	return c.incidents
}

func (m model) eventHistoryCount() int {
	c := m.activeCache()
	if !c.eventCountLoaded {
		c.eventCount, _ = db.CountEvents(m.o.DB)
		c.eventCountLoaded = true
	}
	return c.eventCount
}

func (m model) eventHistoryPage(start, limit int) []db.Event {
	c := m.activeCache()
	if !c.eventPageLoaded || c.eventPageStart != start || len(c.eventPage) < limit {
		c.eventPage, _ = db.EventsPage(m.o.DB, start, limit)
		c.eventPageStart = start
		c.eventPageLoaded = true
	}
	return c.eventPage
}

func (m model) eventAt(index int) (db.Event, bool) {
	if index < 0 || index >= m.eventHistoryCount() {
		return db.Event{}, false
	}
	events := m.eventHistoryPage(index, 1)
	if len(events) == 0 {
		return db.Event{}, false
	}
	return events[0], true
}

func (m model) usageSnapshots() []db.ModelUsageSnapshot {
	c := m.activeCache()
	if !c.usageLoaded {
		c.usage, _ = db.ModelUsageSnapshots(m.o.DB)
		c.usageLoaded = true
	}
	return c.usage
}

func (m model) cachedQueueStats() (int, int) {
	c := m.activeCache()
	if !c.queueLoaded {
		c.queued, c.running = m.o.Sup.QueueStats()
		c.queueLoaded = true
	}
	return c.queued, c.running
}

func (m model) cachedOpenIncidents() int {
	c := m.activeCache()
	if !c.openIncidentsLoaded {
		c.openIncidents = m.o.Sup.OpenIncidents()
		c.openIncidentsLoaded = true
	}
	return c.openIncidents
}

func (m model) footerSnapshot() (int, map[string]supervisor.RoleCount) {
	c := m.activeCache()
	if !c.footerLoaded || time.Since(c.footerAt) >= time.Second {
		if m.o != nil && m.o.Sup != nil {
			c.roleCounts = m.o.Sup.RoleCounts()
			if m.o.Sup.Mail != nil {
				c.userUnread, _ = m.o.Sup.Mail.UnreadCount("user")
			}
		}
		c.footerLoaded = true
		c.footerAt = time.Now()
	}
	return c.userUnread, c.roleCounts
}
