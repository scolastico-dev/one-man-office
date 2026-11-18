package plugins

import "time"

// Ready marks the socket/API as serving, and schedules omo_started once for
// each enabled lifecycle. Later calls are harmless. Load schedules newly
// enabled/installed plugins once ready; settings and code refreshes do not.
func (m *Manager) Ready() {
	m.mu.Lock()
	m.ready = true
	m.mu.Unlock()
	m.queueStartup()
}

func (m *Manager) queueStartup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ready || m.closed {
		return
	}
	for _, e := range m.entries {
		if e.p == nil || e.started || e.startQueued {
			continue
		}
		// Load starts the delivery goroutine before this call. Reserving startup
		// here bypasses ordinary event dropping and rate limiting.
		e.startQueued = true
		if !e.p.wants(EventOmoStarted) {
			e.started = true
			continue
		}
		e.p.startup <- job{ev: Event{Name: EventOmoStarted, At: m.now(), Data: map[string]any{"at": m.now().UTC().Format(time.RFC3339Nano)}}}
	}
}
