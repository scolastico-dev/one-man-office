package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/webterm"
)

// engineStartWait bounds how long a request that needs the engine's start
// to be over (opening the CEO) waits for it.
const engineStartWait = 30 * time.Second

// registerEngine adds the routes served by the engine: an agent's ready and
// wait, the scheduler state, stopping all agents, the CEO and an agent's
// terminal.
//
// Who may call them, on either listener (the browser is always the user; on
// the socket a request without a session header is the user):
//   - POST /api/ai/ready, POST /api/ai/wait: an agent session, about itself
//     (the engine refuses the user, and wait also the CEO);
//   - GET /api/scheduler: the user, the CEO and a product manager;
//   - POST /api/agents/stop-all, POST /api/ceo, POST /api/ceo/new-session,
//     GET /api/agents/{id}/terminal: the user only.
func (s *Server) registerEngine(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ai/ready", s.aiReady)
	mux.HandleFunc("POST /api/ai/wait", s.aiWait)
	mux.HandleFunc("GET /api/scheduler", s.getScheduler)
	mux.HandleFunc("POST /api/agents/stop-all", s.stopAllAgents)
	mux.HandleFunc("POST /api/ceo", s.openCEO)
	mux.HandleFunc("POST /api/ceo/new-session", s.newCEOSession)
	mux.HandleFunc("GET /api/agents/{id}/terminal", s.agentTerminal)
}

// needEngine answers 503 unavailable and returns false when the server runs
// no engine.
func (s *Server) needEngine(w http.ResponseWriter, r *http.Request) bool {
	if s.engine != nil {
		return true
	}
	writeError(w, r, api.Errorf(api.CodeUnavailable, "agents are not available in this server"))
	return false
}

// decodeEmpty reads a body that must be the empty JSON object.
func decodeEmpty(w http.ResponseWriter, r *http.Request) error {
	var req struct{}
	return decode(w, r, &req)
}

func (s *Server) aiReady(w http.ResponseWriter, r *http.Request) {
	if err := decodeEmpty(w, r); err != nil {
		writeError(w, r, err)
		return
	}
	if !s.needEngine(w, r) {
		return
	}
	prompt, err := s.engine.Ready(r.Context(), actorFrom(r))
	reply(w, r, http.StatusOK, api.ReadyResponse{Prompt: prompt}, err)
}

// aiWait parks the calling agent until the engine has something for it, the
// timeout passes, the client goes away or the server stops. It holds no
// database connection while it waits, and no server timeout cuts it off.
func (s *Server) aiWait(w http.ResponseWriter, r *http.Request) {
	var req api.WaitRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if !s.needEngine(w, r) {
		return
	}
	a := actorFrom(r)
	switch a.Role {
	case api.RolePM, api.RoleDeveloper, api.RoleReviewer:
		if req.Timeout <= 0 {
			writeError(w, r, api.Errorf(api.CodeBadRequest, "timeout is required and must be longer than zero, such as \"30m\""))
			return
		}
	}
	reason, err := s.engine.Wait(r.Context(), a, time.Duration(req.Timeout))
	if err != nil && r.Context().Err() != nil && errors.Is(err, r.Context().Err()) {
		// The client went away or the server stops: nobody reads an answer.
		return
	}
	reply(w, r, http.StatusOK, api.WaitResponse{Reason: reason}, err)
}

func (s *Server) getScheduler(w http.ResponseWriter, r *http.Request) {
	switch actorFrom(r).Role {
	case api.RoleUser, api.RoleCEO, api.RolePM:
	default:
		writeError(w, r, api.Errorf(api.CodeForbidden, "only the user, the CEO and a product manager see the scheduler"))
		return
	}
	if !s.needEngine(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.engine.Scheduler())
}

// stopAllAgents pauses omo and ends every agent except the CEO at once,
// without a handoff, and answers with the server info.
func (s *Server) stopAllAgents(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	if err := decodeEmpty(w, r); err != nil {
		writeError(w, r, err)
		return
	}
	if !s.needEngine(w, r) {
		return
	}
	// Pausing and ending the agents are one action: a client that leaves in
	// between must not leave omo paused with agents still running.
	ctx, cancel := s.detached(r, detachedBound)
	defer cancel()
	if err := s.engine.StopAll(ctx); err != nil {
		writeError(w, r, err)
		return
	}
	s.getServer(w, r)
}

func (s *Server) openCEO(w http.ResponseWriter, r *http.Request)       { s.startCEO(w, r, false) }
func (s *Server) newCEOSession(w http.ResponseWriter, r *http.Request) { s.startCEO(w, r, true) }

// startCEO answers with the live CEO session, starting one when there is
// none; fresh ends the live one first. It waits until the engine's start is
// over, so a CEO started now is not mistaken for one of an earlier run.
func (s *Server) startCEO(w http.ResponseWriter, r *http.Request, fresh bool) {
	if !requireUser(w, r) {
		return
	}
	if err := decodeEmpty(w, r); err != nil {
		writeError(w, r, err)
		return
	}
	if !s.needEngine(w, r) || !s.awaitEngineStart(w, r) {
		return
	}
	sess, err := s.engine.StartCEO(r.Context(), fresh)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writeError(w, r, err)
		return
	}
	out := []api.AgentSession{sess}
	s.addTerminalIDs(out)
	writeJSON(w, http.StatusOK, out[0])
}

// awaitEngineStart waits until the engine finished its start (its first
// pass) and answers 503 unavailable when that takes longer than
// engineStartWait or the engine does not run.
func (s *Server) awaitEngineStart(w http.ResponseWriter, r *http.Request) bool {
	if !s.engineRunning() {
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is not running its agents yet; try again in a moment"))
		return false
	}
	t := time.NewTimer(engineStartWait)
	defer t.Stop()
	select {
	case <-s.engineReady:
		return true
	case <-r.Context().Done():
		return false
	case <-t.C:
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is still starting; try again in a moment"))
		return false
	}
}

// agentTerminal connects the user's websocket to the terminal of a running
// agent, named by its terminal id (never by the session id). Typing there is
// typing into the agent with the agent's permissions, so only the user may
// open it. The connection ends when the agent ends, the client goes away or
// the server stops.
func (s *Server) agentTerminal(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) || !s.needEngine(w, r) {
		return
	}
	term, ok := s.terminalByID(r.Context(), r.PathValue("id"))
	if !ok {
		writeError(w, r, api.Errorf(api.CodeNotFound, "no running agent has terminal %s", r.PathValue("id")))
		return
	}
	// The websocket selects the subprotocol "omo" when the client offers it
	// and never echoes another one, so a login token offered as
	// omo-token.<token> stays out of the response.
	term.ServeWebSocket(r.Context(), w, r, s.terminals)
}

// terminalByID finds the terminal of a running agent by its terminal id.
func (s *Server) terminalByID(ctx context.Context, id string) (*webterm.Terminal, bool) {
	if id == "" {
		return nil, false
	}
	live, err := s.board.AgentSessions(ctx, board.User, true)
	if err != nil {
		return nil, false
	}
	for _, sess := range live {
		if t, ok := s.engine.Terminal(sess.ID); ok && t != nil && t.Info().ID == id {
			return t, true
		}
	}
	return nil, false
}

// addTerminalIDs sets TerminalID on the sessions whose agent runs with a
// terminal. Only answers to the user may carry it.
func (s *Server) addTerminalIDs(ss []api.AgentSession) {
	if s.engine == nil {
		return
	}
	for i := range ss {
		if ss[i].State == api.SessionEnded || ss[i].ID == "" {
			continue
		}
		if t, ok := s.engine.Terminal(ss[i].ID); ok && t != nil {
			ss[i].TerminalID = t.Info().ID
		}
	}
}
