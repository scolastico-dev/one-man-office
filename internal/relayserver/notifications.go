package relayserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

type notification struct {
	reply chan []byte
	ctx   context.Context
}

func (s *Server) serveNotification(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/notifications/"), "/")
	if len(parts) != 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	m := relayproto.Control{V: 1, Type: "notification.get", HostID: parts[0], UIID: parts[1], QID: randomHandle(), RequestHeader: r.Header.Get("Omo-Notification-Request"), RequestMAC: r.Header.Get("Omo-Notification-MAC")}
	if len(r.Header.Values("Omo-Notification-Request")) != 1 || len(r.Header.Values("Omo-Notification-MAC")) != 1 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, err := relayproto.EncodeControl(m); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	n := &notification{reply: make(chan []byte, 1), ctx: ctx}
	s.mu.Lock()
	h := s.hosts[m.HostID]
	if s.closed || h == nil {
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(h.pending) >= 64 {
		s.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	h.pending[string(m.QID)] = n
	victims := s.enqueueLocked(h, m)
	s.mu.Unlock()
	closePeers(victims)
	defer s.cancelNotification(h, m.QID, n)
	select {
	case body := <-n.reply:
		if len(body) == 0 || ctx.Err() != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.omo.relay-notifications+json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case <-ctx.Done():
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) answerLocked(h *peer, m relayproto.Control) {
	if n := h.pending[string(m.QID)]; n != nil && n.ctx.Err() == nil {
		delete(h.pending, string(m.QID))
		n.reply <- m.Body
	}
}

func (s *Server) cancelNotification(h *peer, qid []byte, n *notification) {
	s.mu.Lock()
	var victims []*peer
	if !h.dead && s.hosts[h.hostID] == h && h.pending[string(qid)] == n {
		delete(h.pending, string(qid))
		victims = s.enqueueLocked(h, relayproto.Control{V: 1, Type: "notification.cancel", QID: qid})
	}
	s.mu.Unlock()
	closePeers(victims)
}
