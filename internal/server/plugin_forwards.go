package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

type pluginForwardRegistry struct {
	sync.Mutex
	entries map[string]*pluginForwardState
}
type pluginForwardState struct {
	plugin, task string
	ticket       int
	activity     plugins.ForwardActivity
	since        time.Time // Carries Go's monotonic clock; wire time is informational.
	closing      bool
	timer        *time.Timer
	idle         time.Duration
}

func (s *Server) clearStalePluginForwards() error {
	return s.board.ClearPluginForwards(context.Background(), board.System, "", "")
}

// Resource attachment and exit cleanup serialize independently of task locks.
// Exits publish state before invoking their callback, so either the running
// check refuses attachment or the callback observes the completed attachment.
func (s *Server) forwardTask(plugin, id string, ticket *int) error {
	tasks, err := s.plugins.m.Tasks(plugin)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.ID == id && t.State == "running" {
			n := 0
			if ticket != nil {
				n = *ticket
			}
			if t.Ticket != n {
				return errors.New("forward and task must have the same ticket binding")
			}
			return nil
		}
	}
	return plugins.ErrUnknownTask
}

func (s *Server) AddForward(ctx context.Context, plugin string, req plugins.ForwardAdd) (plugins.Forward, error) {
	decoded, _ := url.QueryUnescape(req.StartPath)
	if s.token != "" && (strings.Contains(req.StartPath, s.token) || strings.Contains(decoded, s.token) || strings.Contains(req.Host, s.token) || strings.Contains(req.Name, s.token)) {
		return plugins.Forward{}, errors.New("forward options contain an omo credential")
	}
	reg := &s.pluginForwards
	reg.Lock()
	defer reg.Unlock()
	if err := ctx.Err(); err != nil {
		return plugins.Forward{}, err
	}
	if req.TaskID != "" {
		if err := s.forwardTask(plugin, req.TaskID, req.Ticket); err != nil {
			return plugins.Forward{}, err
		}
	}
	f, err := s.board.CreatePluginForward(ctx, board.System, plugin, req.TaskID, req.CreateProxyForward)
	if err != nil {
		return plugins.Forward{}, err
	}
	now := time.Now()
	st := &pluginForwardState{plugin: plugin, task: req.TaskID, since: now, activity: plugins.ForwardActivity{NoClientSince: now.UTC().Format(time.RFC3339Nano), Revision: 1}}
	if f.Ticket != nil {
		st.ticket = *f.Ticket
	}
	if reg.entries == nil {
		reg.entries = map[string]*pluginForwardState{}
	}
	reg.entries[f.ID] = st
	return plugins.Forward{ProxyForward: s.shownProxyForward(&http.Request{}, f), Activity: st.activity}, nil
}

func (s *Server) ListForwards(ctx context.Context, plugin string) ([]plugins.Forward, error) {
	reg := &s.pluginForwards
	reg.Lock()
	defer reg.Unlock()
	fs, err := s.board.PluginForwards(ctx, board.System, plugin)
	if err != nil {
		return nil, err
	}
	out := make([]plugins.Forward, 0, len(fs))
	for _, f := range fs {
		if st := reg.entries[f.ID]; st != nil && !st.closing {
			out = append(out, plugins.Forward{ProxyForward: s.shownProxyForward(&http.Request{}, f), Activity: st.activity})
		}
	}
	return out, nil
}

func (s *Server) RemoveForward(ctx context.Context, plugin, id string) error {
	return s.removeForwardRecord(id, func() error { return s.board.DeletePluginForward(ctx, board.System, plugin, id) })
}

type watchedForwardTask struct {
	plugin, id string
	ticket     int
}

// takeWatchedForwardLocked separates external removal from callback cleanup.
// Task/lifecycle callbacks use forget directly: waiting for their own task
// from inside its exit callback would deadlock.
func (s *Server) takeWatchedForwardLocked(id string) watchedForwardTask {
	var task watchedForwardTask
	if st := s.pluginForwards.entries[id]; st != nil && st.idle > 0 && st.task != "" {
		task = watchedForwardTask{plugin: st.plugin, id: st.task, ticket: st.ticket}
	}
	s.forgetPluginForwardLocked(id)
	return task
}

func (s *Server) stopRemovedForward(task watchedForwardTask) {
	if task.id == "" {
		return
	}
	reason := plugins.TaskForwardRemoved
	if task.ticket > 0 && !s.pluginTaskTicketLive(context.Background(), task.ticket) {
		reason = plugins.TaskTicketDone
	}
	_ = s.plugins.m.StopTaskID(task.plugin, task.id, reason)
}

// removeForwardRecord commits deletion and captures the stable watched identity
// atomically with expiry, then awaits the task outside the resource lock.
func (s *Server) removeForwardRecord(id string, remove func() error) error {
	reg := &s.pluginForwards
	reg.Lock()
	err := remove()
	var task watchedForwardTask
	if err == nil {
		task = s.takeWatchedForwardLocked(id)
	}
	reg.Unlock()
	if err != nil {
		return err
	}
	s.stopRemovedForward(task)
	return nil
}

func (s *Server) AttachForward(ctx context.Context, plugin, id, task string) error {
	reg := &s.pluginForwards
	reg.Lock()
	defer reg.Unlock()
	st := reg.entries[id]
	if st == nil || st.plugin != plugin || st.closing {
		return errors.New("plugin forward does not exist")
	}
	f, err := s.board.LiveProxyForward(ctx, board.System, id)
	if err != nil {
		return err
	}
	if st.task != "" && st.task != task {
		return errors.New("forward is already attached to a task")
	}
	if err := s.forwardTask(plugin, task, f.Ticket); err != nil {
		return err
	}
	if err := s.board.AttachPluginForward(ctx, board.System, plugin, id, task); err != nil {
		return err
	}
	st.task = task
	return nil
}

func (s *Server) forgetPluginForwardLocked(id string) {
	if st := s.pluginForwards.entries[id]; st != nil {
		st.closing = true
		if st.timer != nil {
			st.timer.Stop()
		}
		delete(s.pluginForwards.entries, id)
	}
	s.proxyAuth.forgetForward(id)
}

func (s *Server) clearPluginForwards(plugin, task string) {
	reg := &s.pluginForwards
	reg.Lock()
	defer reg.Unlock()
	if err := s.board.ClearPluginForwards(context.Background(), board.System, plugin, task); err != nil {
		s.log.Printf("remove plugin forwards: %v", err)
	}
	for id, st := range reg.entries {
		if st.plugin == plugin && (task == "" || st.task == task) {
			s.forgetPluginForwardLocked(id)
		}
	}
}

func (s *Server) pluginTaskExited(plugin string, task api.PluginTask) {
	s.clearPluginForwards(plugin, task.ID)
}
func (s *Server) pluginUnloaded(plugin, reason string) { s.clearPluginForwards(plugin, "") }
