package server

import (
	"net/http"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func (s *Server) registerLSP(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		// The caller is always its own session: nobody ignores an issue in
		// another session's worktree. The engine refuses the other roles.
		{"POST /api/lsp/ignore", s.ignoreLSPIssue, agentWrite},
	})
}

func (s *Server) ignoreLSPIssue(w http.ResponseWriter, r *http.Request) {
	var req api.LSPIgnoreRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if !s.needEngine(w, r) {
		return
	}
	out, err := s.engine.IgnoreLSPIssue(actorFrom(r), req.ID)
	reply(w, r, http.StatusOK, out, err)
}
