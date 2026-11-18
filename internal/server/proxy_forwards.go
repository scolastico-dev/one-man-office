package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func (s *Server) registerProxyForwards(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"POST /api/proxy/forwards/{id}/open", s.openProxyForward, userOnly},
		{"GET /api/proxy/forwards", s.listProxyForwards, agentRead},
		{"POST /api/proxy/forwards", s.createProxyForward, agentWrite},
		{"DELETE /api/proxy/forwards/{id}", s.deleteProxyForward, agentWrite},
	})
}

func (s *Server) shownProxyForward(r *http.Request, f api.ProxyForward) api.ProxyForward {
	if s.opts.Config.ProxyBaseHost != "" {
		origin := s.Address()
		if origin == "" {
			origin = "http://" + s.opts.Config.Listen()
		}
		if public := s.opts.Config.PublicBase(); public != "" {
			origin = public
		}
		if r.Host != "" && s.allowedHost(r) {
			origin = s.proxyBoardURL(r)
		}
		f.Address = s.proxySubdomainOrigin(origin, f.ID) + f.StartPath
	} else {
		f.Address = strings.TrimRight(s.Address(), "/") + f.Address
	}
	return f
}

func (s *Server) listProxyForwards(w http.ResponseWriter, r *http.Request) {
	var ticket *int
	if values, ok := r.URL.Query()["ticket"]; ok {
		if len(values) != 1 {
			writeError(w, r, api.Errorf(api.CodeBadRequest, "ticket must be a single positive number"))
			return
		}
		n, err := strconv.Atoi(values[0])
		if err != nil || n <= 0 {
			writeError(w, r, api.Errorf(api.CodeBadRequest, "ticket must be a positive number"))
			return
		}
		ticket = &n
	}
	forwards, err := s.board.ProxyForwards(r.Context(), actorFrom(r), ticket)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for i := range forwards {
		forwards[i] = s.shownProxyForward(r, forwards[i])
	}
	writeJSON(w, http.StatusOK, api.ProxyForwardList{Forwards: forwards})
}

func (s *Server) createProxyForward(w http.ResponseWriter, r *http.Request) {
	var req api.CreateProxyForward
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	f, err := s.board.CreateProxyForward(r.Context(), actorFrom(r), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.shownProxyForward(r, f))
}

func (s *Server) deleteProxyForward(w http.ResponseWriter, r *http.Request) {
	err := s.removeForwardRecord(r.PathValue("id"), func() error { return s.board.DeleteProxyForward(r.Context(), actorFrom(r), r.PathValue("id")) })
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
