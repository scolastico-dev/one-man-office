package server

import (
	"net/http"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/buildinfo"
)

// reservedRoutes are part of the API contract but not implemented yet; they
// answer 501 not_implemented. None is reserved at the moment.
var reservedRoutes = []string{}

// routes builds the mux shared by both listeners.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/server", s.getServer)
	mux.HandleFunc("PUT /api/server/paused", s.putPaused)
	mux.HandleFunc("GET /api/me", s.getMe)
	for _, pattern := range reservedRoutes {
		mux.HandleFunc(pattern, notImplemented)
	}
	s.registerTickets(mux)
	s.registerDiff(mux)
	s.registerSettings(mux)
	s.registerBackup(mux)
	s.registerDiscovery(mux)
	s.registerSetup(mux)
	s.registerEvents(mux)
	s.registerEngine(mux)
	s.registerThread(mux)
	s.registerImages(mux)
	s.registerTerminals(mux)
	s.registerUsage(mux)
	s.registerSkillSets(mux)
	s.registerPlugins(mux)
	s.registerMCPAuth(mux)
	mux.HandleFunc("/api", apiNotFound)
	mux.HandleFunc("/api/", apiNotFound)
	s.registerSPA(mux)
	return mux
}

func notImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, api.Errorf(api.CodeNotImplemented, "%s %s is not implemented in this version", r.Method, r.URL.Path))
}

func apiNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, api.Errorf(api.CodeNotFound, "no API route %s %s", r.Method, r.URL.Path))
}

func (s *Server) serverInfo(r *http.Request) (api.ServerInfo, error) {
	paused, err := s.board.Paused(r.Context())
	if err != nil {
		return api.ServerInfo{}, err
	}
	src := buildinfo.SourceOf(s.opts.Version)
	return api.ServerInfo{
		Version:           s.opts.Version,
		Paused:            paused,
		StartedAt:         s.startedAt,
		Address:           s.Address(),
		AddressConfigured: s.opts.Config.PublicBase() != "",
		Listen:            s.listenAddress(),
		VersionURL:        src.URL,
		VersionCommit:     src.Commit,
	}, nil
}

func (s *Server) getServer(w http.ResponseWriter, r *http.Request) {
	info, err := s.serverInfo(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) putPaused(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	var req api.PausedRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Paused == nil {
		writeError(w, r, api.Errorf(api.CodeBadRequest, "paused is required"))
		return
	}
	if err := s.board.SetPaused(r.Context(), *req.Paused); err != nil {
		writeError(w, r, err)
		return
	}
	s.getServer(w, r)
}

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, board.Me(actorFrom(r)))
}
