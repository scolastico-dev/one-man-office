package server

import "net/http"

// registerTickets adds the ticket, description, dependency, comment, agent
// session and /api/ai/done routes. Handlers take the caller from
// actorFrom(r), read bodies with decode, answer with writeJSON or
// writeError, and write the response only after the board call returned.
func (s *Server) registerTickets(mux *http.ServeMux) {}
