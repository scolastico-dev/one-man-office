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
// log. A one-second batch bounds event volume even for noisy processes.
func (s *Server) pluginTasksChanged(name string) {
	f := &s.plugins
	f.pendingMu.Lock()
	if f.taskPending == nil {
		f.taskPending = map[string]bool{}
	}
	f.taskPending[name] = true
	f.pendingMu.Unlock()
}
func (s *Server) writePluginTaskEvents(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		f := &s.plugins
		f.pendingMu.Lock()
		names := f.taskPending
		f.taskPending = nil
		f.pendingMu.Unlock()
		for name := range names {
			if err := s.appendSystemEvent(api.KindPluginTasksChanged, api.PluginTasksChangedData{Name: name}); err != nil {
				s.pluginTasksChanged(name)
			} else {
				s.events.Wake()
			}
		}
	}
}
