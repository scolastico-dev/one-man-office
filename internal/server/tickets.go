package server

import (
	"net/http"
	"strconv"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// registerTickets adds the ticket, description, dependency, comment, agent
// session and /api/ai/done routes. Handlers take the caller from
// actorFrom(r), read bodies with decode, answer with writeJSON or
// writeError, and write the response only after the board call returned.
func (s *Server) registerTickets(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tickets", s.listTickets)
	mux.HandleFunc("POST /api/tickets", s.createTicket)
	mux.HandleFunc("GET /api/tickets/{n}", s.getTicket)
	mux.HandleFunc("PATCH /api/tickets/{n}", s.patchTicket)
	mux.HandleFunc("PUT /api/tickets/{n}/description", s.putDescription)
	mux.HandleFunc("GET /api/tickets/{n}/descriptions", s.getDescriptions)
	mux.HandleFunc("POST /api/tickets/{n}/descriptions/{version}/restore", s.restoreDescription)
	mux.HandleFunc("POST /api/tickets/{n}/move", s.moveTicket)
	mux.HandleFunc("POST /api/tickets/{n}/approve", s.approveTicket)
	mux.HandleFunc("POST /api/tickets/{n}/reject", s.rejectTicket)
	mux.HandleFunc("POST /api/tickets/{n}/cancel", s.cancelTicket)
	mux.HandleFunc("POST /api/tickets/{n}/mark-merged", s.markMerged)
	mux.HandleFunc("POST /api/tickets/{n}/dependencies", s.addDependency)
	mux.HandleFunc("DELETE /api/tickets/{n}/dependencies/{m}", s.removeDependency)
	mux.HandleFunc("GET /api/tickets/{n}/comments", s.getComments)
	mux.HandleFunc("POST /api/tickets/{n}/comments", s.postComment)
	mux.HandleFunc("GET /api/tickets/{n}/events", s.getTicketEvents)
	mux.HandleFunc("GET /api/tickets/{n}/agents", s.getTicketAgents)
	mux.HandleFunc("GET /api/agents", s.listAgents)
	mux.HandleFunc("POST /api/ai/done", s.aiDone)
}

// pathInt reads the integer path value name; anything else is bad_request.
func pathInt(r *http.Request, name string) (int64, error) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || v < 1 {
		return 0, api.Errorf(api.CodeBadRequest, "%s must be a positive integer, got %q", name, r.PathValue(name))
	}
	return v, nil
}

// queryInt reads an optional integer query parameter.
func queryInt(r *http.Request, name string) (int, bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, false, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, api.Errorf(api.CodeBadRequest, "%s must be an integer, got %q", name, raw)
	}
	return v, true, nil
}

// ticketFilter parses the query of GET /api/tickets.
func ticketFilter(r *http.Request) (api.TicketFilter, error) {
	q := r.URL.Query()
	f := api.TicketFilter{
		Column:     api.Column(q.Get("column")),
		Type:       api.TicketType(q.Get("type")),
		Project:    q.Get("project"),
		Supervisor: api.Role(q.Get("supervisor")),
		Search:     q.Get("q"),
	}
	story, ok, err := queryInt(r, "story")
	if err != nil {
		return f, err
	}
	if ok {
		f.Story = &story
	}
	if f.DoneLimit, _, err = queryInt(r, "done_limit"); err != nil {
		return f, err
	}
	if f.DoneBefore, _, err = queryInt(r, "done_before"); err != nil {
		return f, err
	}
	if raw := q.Get("needs_me"); raw != "" {
		if f.NeedsMe, err = strconv.ParseBool(raw); err != nil {
			return f, api.Errorf(api.CodeBadRequest, "needs_me must be a boolean, got %q", raw)
		}
	}
	return f, nil
}

func (s *Server) listTickets(w http.ResponseWriter, r *http.Request) {
	f, err := ticketFilter(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.List(r.Context(), actorFrom(r), f)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) createTicket(w http.ResponseWriter, r *http.Request) {
	var req api.CreateTicket
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Create(r.Context(), actorFrom(r), req)
	reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) getTicket(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Get(r.Context(), actorFrom(r), n)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) patchTicket(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.UpdateTicket
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Update(r.Context(), actorFrom(r), n, req)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) putDescription(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.DescriptionUpdate
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.SetDescription(r.Context(), actorFrom(r), n, req)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) getDescriptions(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Descriptions(r.Context(), actorFrom(r), n)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) restoreDescription(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := pathInt(r, "version")
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.RestoreDescription(r.Context(), actorFrom(r), n, int(v))
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) moveTicket(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.MoveRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Move(r.Context(), actorFrom(r), n, req)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) approveTicket(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.ApproveRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Approve(r.Context(), actorFrom(r), n, req)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) rejectTicket(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.RejectRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Reject(r.Context(), actorFrom(r), n, req)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) cancelTicket(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.CancelRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Cancel(r.Context(), actorFrom(r), n, req)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) markMerged(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.MarkMerged(r.Context(), actorFrom(r), n)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) addDependency(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.DependencyRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.AddDependency(r.Context(), actorFrom(r), n, int64(req.DependsOn))
	reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) removeDependency(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	m, err := pathInt(r, "m")
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.RemoveDependency(r.Context(), actorFrom(r), n, m)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) getComments(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Comments(r.Context(), actorFrom(r), n)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) postComment(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.CommentRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Comment(r.Context(), actorFrom(r), n, req)
	reply(w, r, http.StatusCreated, out, err)
}

func (s *Server) getTicketEvents(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.TicketEvents(r.Context(), actorFrom(r), n)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) getTicketAgents(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.TicketSessions(r.Context(), actorFrom(r), n)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	live := false
	if raw := r.URL.Query().Get("live"); raw != "" {
		var err error
		if live, err = strconv.ParseBool(raw); err != nil {
			writeError(w, r, api.Errorf(api.CodeBadRequest, "live must be a boolean, got %q", raw))
			return
		}
	}
	out, err := s.board.Sessions(r.Context(), 0, live)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) aiDone(w http.ResponseWriter, r *http.Request) {
	var req api.DoneRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Done(r.Context(), actorFrom(r), req)
	reply(w, r, http.StatusOK, out, err)
}

// reply answers with v, or with err when the board call failed.
func reply(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, status, v)
}
