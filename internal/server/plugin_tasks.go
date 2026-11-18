package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

func (s *Server) pluginTaskTicketLive(ctx context.Context, n int) bool {
	if n <= 0 {
		return false
	}
	t, err := s.board.Get(ctx, board.User, int64(n))
	return err == nil && t.Column != api.ColumnDone
}

func (s *Server) pluginTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.plugins.m.Tasks(r.PathValue("name"))
	if err != nil {
		writeError(w, r, pluginTaskError(err))
		return
	}
	writeJSON(w, http.StatusOK, api.PluginTasks{Tasks: tasks})
}

func (s *Server) stopPluginTask(w http.ResponseWriter, r *http.Request) {
	if err := decodeEmpty(w, r); err != nil {
		writeError(w, r, err)
		return
	}
	err := s.plugins.m.StopTask(r.PathValue("name"), r.PathValue("task"))
	if err != nil {
		writeError(w, r, pluginTaskError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func pluginTaskError(err error) error {
	switch {
	case errors.Is(err, plugins.ErrUnknownTask), errors.Is(err, plugins.ErrUnknownPlugin):
		return api.Errorf(api.CodeNotFound, "no such plugin task")
	case errors.Is(err, plugins.ErrClosed):
		return api.Errorf(api.CodeUnavailable, "plugins are stopped")
	default:
		return api.Errorf(api.CodeValidation, "%s", err.Error())
	}
}

// stopTasksForEvent runs before ordinary hook Dispatch. Event log delivery is
// replayed by id and never uses a plugin's bounded/droppable hook queue.
func (s *Server) stopTasksForEvent(ev api.Event) {
	if ev.Kind == api.KindProxyForwardRemoved {
		var f api.ProxyForward
		if json.Unmarshal(ev.Data, &f) == nil {
			s.pluginForwards.Lock()
			s.forgetPluginForwardLocked(f.ID)
			s.pluginForwards.Unlock()
		}
		return
	}
	if ev.Kind != api.KindTicketMoved {
		return
	}
	var data api.TicketMovedData
	if json.Unmarshal(ev.Data, &data) == nil && data.To == api.ColumnDone {
		s.plugins.m.StopTicketTasks(data.Ticket.Number)
	}
}

// PluginsReady must be called by embedders after starting their API/socket
// listeners. Run does this itself. It is safe to call more than once.
func (s *Server) PluginsReady() { s.plugins.m.Ready() }

// pluginTasksChanged collects invalidations; output never enters the event
// log. Lifecycle changes flush within a second; output is throttled separately.
func (s *Server) pluginTasksChanged(name string) {
	f := &s.plugins
	f.pendingMu.Lock()
	if f.taskPending == nil {
		f.taskPending = map[string]bool{}
	}
	f.taskPending[name] = true
	f.pendingMu.Unlock()
}

// pluginTaskOutputChanged is called only when the visible line tail changes.
func (s *Server) pluginTaskOutputChanged(name string) {
	f := &s.plugins
	f.pendingMu.Lock()
	if f.taskOutputPending == nil {
		f.taskOutputPending = map[string]bool{}
	}
	f.taskOutputPending[name] = true
	f.pendingMu.Unlock()
}

const pluginTaskOutputInterval = 10 * time.Second

func (s *Server) flushPluginTaskEvents(at time.Time) {
	f := &s.plugins
	f.pendingMu.Lock()
	names := f.taskPending
	if names == nil {
		names = map[string]bool{}
	}
	f.taskPending = nil
	if f.taskOutputAt == nil {
		f.taskOutputAt = map[string]time.Time{}
	}
	for name := range f.taskOutputPending {
		if names[name] || at.Sub(f.taskOutputAt[name]) >= pluginTaskOutputInterval {
			names[name] = true
		}
	}
	for name := range names {
		delete(f.taskOutputPending, name)
		f.taskOutputAt[name] = at
	}
	// Keep timestamps only for a live throttle window, including removed
	// plugin names. Quiet or removed namespaces cannot accumulate forever.
	for name, last := range f.taskOutputAt {
		if !f.taskOutputPending[name] && at.Sub(last) >= pluginTaskOutputInterval {
			delete(f.taskOutputAt, name)
		}
	}
	f.pendingMu.Unlock()
	for name := range names {
		if err := s.appendSystemEvent(api.KindPluginTasksChanged, api.PluginTasksChangedData{Name: name}); err != nil {
			s.pluginTasksChanged(name)
		} else {
			s.events.Wake()
		}
	}
}

func (s *Server) writePluginTaskEvents(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-tick.C:
			s.flushPluginTaskEvents(at)
		}
	}
}
