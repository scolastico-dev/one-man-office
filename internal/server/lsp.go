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
		// Only an agent session can confirm its own stream.
		{"POST /api/lsp/confirm", s.confirmLSPListener, agentWrite},
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

func (s *Server) confirmLSPListener(w http.ResponseWriter, r *http.Request) {
	var req api.LSPConfirmRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if !s.needEngine(w, r) {
		return
	}
	a := actorFrom(r)
	if a.Session == "" {
		writeError(w, r, api.Errorf(api.CodeForbidden, "only an agent session has a notification stream to confirm"))
		return
	}
	if !s.mcp.listeners.Confirm(a.Session, req.Token) {
		writeError(w, r, api.Errorf(api.CodeNotFound, "no notification probe with this token for your session; the token belongs to a stream that is gone or to another session"))
		return
	}
	reply(w, r, http.StatusOK, api.LSPConfirmResult{Confirmed: true}, nil)
}
