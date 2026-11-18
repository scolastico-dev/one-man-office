package server

import (
	"bufio"
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// WatchForward schedules a plugin-selected no-WebSocket interval for an attached
// task. All comparisons use monotonic time. Revision-guarded expiration and
// downstream upgrade admission share one lock; no stale deadline can kill a
// reconnected browser. Ordinary requests never reset this interval.
func (s *Server) WatchForward(ctx context.Context, plugin, id string, seconds float64) error {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0.01 || seconds > 604800 {
		return errors.New("idle seconds must be between 0.01 and 604800")
	}
	reg := &s.pluginForwards
	reg.Lock()
	defer reg.Unlock()
	st := reg.entries[id]
	if st == nil || st.plugin != plugin || st.closing || st.task == "" {
		return errors.New("live task-bound plugin forward is required")
	}
	f, err := s.board.LiveProxyForward(ctx, board.System, id)
	if err != nil {
		return err
	}
	if err := s.forwardTask(plugin, st.task, f.Ticket); err != nil {
		return err
	}
	st.idle = time.Duration(seconds * float64(time.Second))
	st.activity.Revision++ // Invalidates a callback from a replaced policy.
	s.scheduleForwardIdleLocked(id, st)
	return nil
}

func (s *Server) scheduleForwardIdleLocked(id string, st *pluginForwardState) {
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	if st.closing || st.idle == 0 || st.activity.ActiveWebSockets != 0 {
		return
	}
	revision := st.activity.Revision
	st.timer = time.AfterFunc(time.Until(st.since.Add(st.idle)), func() { s.expireForward(id, st, revision) })
}

func (s *Server) expireForward(id string, st *pluginForwardState, revision uint64) {
	reg := &s.pluginForwards
	reg.Lock()
	if reg.entries[id] != st || st.closing || st.activity.Revision != revision || st.activity.ActiveWebSockets != 0 || st.idle == 0 {
		reg.Unlock()
		return
	}
	if time.Until(st.since.Add(st.idle)) > 0 {
		s.scheduleForwardIdleLocked(id, st)
		reg.Unlock()
		return
	}
	// Persistence may have removed the forward before its reliable event was
	// consumed. A vanished registration cannot retain a task's idle policy.
	if _, err := s.board.LiveProxyForward(context.Background(), board.System, id); err != nil {
		var apiErr *api.Error
		if errors.As(err, &apiErr) && apiErr.Code == api.CodeNotFound {
			s.forgetPluginForwardLocked(id)
		} else {
			// Do not stop on an uncertain read. Retry this same guarded deadline.
			st.timer = time.AfterFunc(time.Second, func() { s.expireForward(id, st, revision) })
		}
		reg.Unlock()
		return
	}
	// Mark closed before releasing the lock. Pending handshakes must then fail;
	// StopTaskID waits for a callback which takes this lock, so never await here.
	st.closing = true
	reg.Unlock()
	_ = s.plugins.m.StopTaskID(st.plugin, st.task, "idle")
}

func (s *Server) forwardSocketOpenedLocked(id string, st *pluginForwardState) {
	st.activity.ActiveWebSockets++
	st.activity.EverConnected = true
	st.activity.NoClientSince = ""
	st.activity.Revision++
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
}
func (s *Server) forwardSocketClosedLocked(id string, st *pluginForwardState) {
	st.activity.ActiveWebSockets--
	st.activity.Revision++
	if st.activity.ActiveWebSockets == 0 {
		st.since = time.Now()
		st.activity.NoClientSince = st.since.UTC().Format(time.RFC3339Nano)
		s.scheduleForwardIdleLocked(id, st)
	}
}

// proxyActivityWriter preserves streaming/ResponseController behavior. A WS
// counts only after the upstream accepted it and downstream hijack succeeded.
// ReverseProxy owns both connections until its handler returns, even after
// hijacking, so the handler's defer accounts for every close/error/cancel path.
type proxyActivityWriter struct {
	http.ResponseWriter
	s         *Server
	ctx       context.Context
	id        string
	state     *pluginForwardState
	websocket bool
	upgraded  bool
}

func (w *proxyActivityWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *proxyActivityWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w *proxyActivityWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	reg := &w.s.pluginForwards
	reg.Lock()
	defer reg.Unlock()
	if reg.entries[w.id] != w.state || w.state.closing {
		return nil, nil, errors.New("forward is closed")
	}
	if _, err := w.s.board.LiveProxyForward(w.ctx, board.System, w.id); err != nil {
		return nil, nil, errors.New("forward is closed")
	}
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil && w.websocket {
		w.upgraded = true
		w.s.forwardSocketOpenedLocked(w.id, w.state)
	}
	return conn, rw, err
}

func (s *Server) trackForwardRequest(w http.ResponseWriter, r *http.Request, id string, owned bool) (http.ResponseWriter, func(), bool) {
	reg := &s.pluginForwards
	reg.Lock()
	defer reg.Unlock()
	st := reg.entries[id]
	if st == nil {
		if owned {
			return w, func() {}, false
		}
		return w, func() {}, true
	} // Persistent generic user forward.
	if st.closing {
		return w, func() {}, false
	}
	st.activity.ActiveRequests++
	wrap := &proxyActivityWriter{ResponseWriter: w, s: s, ctx: r.Context(), id: id, state: st, websocket: strings.EqualFold(r.Header.Get("Upgrade"), "websocket")}
	return wrap, func() {
		reg.Lock()
		defer reg.Unlock()
		st.activity.ActiveRequests--
		if wrap.upgraded {
			s.forwardSocketClosedLocked(id, st)
		}
	}, true
}
