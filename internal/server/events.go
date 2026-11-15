package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// streamWriteTimeout bounds one write to an event stream client; a client
// that does not take its data in time is disconnected.
const streamWriteTimeout = 10 * time.Second

// registerEvents adds the event stream route.
func (s *Server) registerEvents(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/events", s.streamEvents)
}

// boardChanged is the board's Notify hook: it runs after every committed
// board change, outside the transaction. It must not block.
func (s *Server) boardChanged() { s.events.Wake() }

// runEvents runs the event fan-out until ctx ends. New starts it; Close
// stops it and waits for it to return.
func (s *Server) runEvents(ctx context.Context) { s.events.Run(ctx) }

// resumePoint reads the id a stream resumes after from Last-Event-ID or the
// since query; -1 means "after the newest event".
func resumePoint(r *http.Request) (int64, error) {
	v := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if v == "" {
		v = strings.TrimSpace(r.URL.Query().Get("since"))
	}
	if v == "" {
		return -1, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id < 0 {
		return 0, api.Errorf(api.CodeBadRequest, "invalid resume point %q: want an event id", v)
	}
	return id, nil
}

// streamEvents serves GET /api/events as server-sent events: replayed and
// live events with their ids, resync when the resume point cannot be served
// gap-free, and a keep-alive comment while idle. The stream ends when the
// client disconnects or falls behind (it resumes with Last-Event-ID) and
// when the server shuts down.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	after, err := resumePoint(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	sub, err := s.events.Subscribe(after)
	if errors.Is(err, errBrokerClosed) {
		err = api.Errorf(api.CodeUnavailable, "the server is shutting down")
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer sub.Cancel()

	rc := http.NewResponseController(w)
	defer rc.SetWriteDeadline(time.Time{})
	send := func(frame string) bool {
		rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
		if _, err := io.WriteString(w, frame); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if !send("retry: 3000\n\n") {
		return
	}
	if sub.Resync {
		data, _ := json.Marshal(api.ResyncData{Latest: sub.From})
		if !send("event: resync\ndata: " + string(data) + "\n\n") {
			return
		}
	}

	ping := time.NewTicker(s.events.pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-sub.Events:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				s.log.Printf("event stream: encode event %d: %v", ev.ID, err)
				return
			}
			if !send(fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Kind, data)) {
				return
			}
		case <-ping.C:
			if !send(": ping\n\n") {
				return
			}
		}
	}
}
