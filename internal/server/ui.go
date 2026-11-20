package server

import (
	"net/http"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

func (s *Server) registerUI(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"POST /api/ui/capabilities", s.exchangeUICapabilities, userOnly},
		{"GET /api/ui/notifications", s.getUINotifications, userOnly},
	})
}

func (s *Server) exchangeUICapabilities(w http.ResponseWriter, r *http.Request) {
	var client api.UICapabilities
	if err := decode(w, r, &client); err != nil {
		writeError(w, r, err)
		return
	}
	// Client flags describe that UI only, not global server state. Unknown or
	// absent flags never prevent the client from using the features it knows.
	writeJSON(w, http.StatusOK, api.UICapabilities{Features: []string{api.FeatureUICapabilities, api.FeatureUINotificationPolling}})
}

func (s *Server) getUINotifications(w http.ResponseWriter, r *http.Request) {
	cursor := ""
	if values, ok := r.URL.Query()["cursor"]; ok {
		if len(values) != 1 || values[0] == "" {
			writeError(w, r, api.Errorf(api.CodeBadRequest, "cursor must be given once and must not be empty"))
			return
		}
		cursor = values[0]
	}
	limit, err := strictInt(r, "limit")
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Bound before converting to int, including on 32-bit platforms.
	if limit < 0 || limit > board.MaxUINotificationPage {
		limit = board.MaxUINotificationPage + 1
	}
	out, err := s.board.UserNotificationFeed(r.Context(), actorFrom(r), cursor, int(limit))
	reply(w, r, http.StatusOK, out, err)
}
