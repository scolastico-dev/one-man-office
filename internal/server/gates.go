package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

// Plugin gates (see board/gate.go and plugins/gates.go). The board records
// which plugins gate a ticket's move and what each answered; this file asks
// the plugins whose gate has not answered, outside every board transaction,
// and records the answers through the board. A gate row is stored, so a
// request survives a restart: the runner asks again every gate that is
// still pending when it starts, and asks again whenever the board or the
// plugins change.

// gateRunner asks the gates of the plugins.
type gateRunner struct {
	wake chan struct{}

	mu       sync.Mutex
	inflight map[gateKey]bool
}

// gateKey is one gate of one ticket's move.
type gateKey struct {
	request int64
	ticket  int64
	move    api.GateMove
	plugin  string
}

func newGateRunner() *gateRunner {
	return &gateRunner{wake: make(chan struct{}, 1), inflight: map[gateKey]bool{}}
}

// poke makes the runner look at the board again; it never blocks.
func (g *gateRunner) poke() {
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

// gateSource is the board's view of the plugins that gate a move.
type gateSource struct{ s *Server }

func (g gateSource) GateFailureHolds(plugin string, move api.GateMove) bool {
	return g.s.plugins.m != nil && g.s.plugins.m.GateFailureHolds(plugin, move)
}

func (g gateSource) GatePlugins(move api.GateMove) []string {
	if g.s.plugins.m == nil {
		return nil
	}
	return g.s.plugins.m.GatePlugins(move)
}

// pluginGates is the plugins' (omo.gate_release, omo.gate_cancel) access to
// the board's gates.
type pluginGates struct{ s *Server }

func (g pluginGates) GateRelease(ctx context.Context, plugin string, ticket int) error {
	return g.s.board.GateRelease(ctx, int64(ticket), plugin)
}

func (g pluginGates) GateCancel(ctx context.Context, plugin string, ticket int) error {
	return g.s.board.GateCancel(ctx, board.System, int64(ticket), plugin)
}

// registerGates registers the routes of the gates. A plugin's hook gives
// its go-ahead or cancels a held move through them as the user, naming
// itself; the user continues anyway or cancels without a name. All are the
// user's alone.
func (s *Server) registerGates(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"POST /api/tickets/{n}/gate/release", s.gateRelease, userOnly},
		{"POST /api/tickets/{n}/gate/cancel", s.gateCancel, userOnly},
		{"POST /api/tickets/{n}/gate/override", s.gateOverride, userOnly},
	})
}

func (s *Server) gateRelease(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.GateReleaseRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Plugin == "" {
		e := api.Errorf(api.CodeValidation, "a go-ahead names the plugin that gives it; the user continues anyway with override")
		e.Details = api.FieldDetail{Field: "plugin"}
		writeError(w, r, e)
		return
	}
	if err := s.board.GateRelease(r.Context(), n, req.Plugin); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Get(r.Context(), actorFrom(r), n)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) gateCancel(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.GateCancelRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.board.GateCancel(r.Context(), actorFrom(r), n, req.Plugin); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Get(r.Context(), actorFrom(r), n)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) gateOverride(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req struct{}
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.board.GateOverride(r.Context(), actorFrom(r), n); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Get(r.Context(), actorFrom(r), n)
	reply(w, r, http.StatusOK, out, err)
}

// runGates asks the gates that wait for an answer until ctx ends: once at
// the start (a request that was pending when omo stopped), then whenever
// the board or the plugins changed.
func (s *Server) runGates(ctx context.Context) {
	for {
		s.askOpenGates(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.gates.wake:
		}
	}
}

// askOpenGates looks at every gate that has not passed. A held gate whose
// plugin is gone or no longer gates the move counts as go ahead; a pending
// one is asked, each in a goroutine of its own (a plugin runs one gate at a
// time).
func (s *Server) askOpenGates(ctx context.Context) {
	open, err := s.board.OpenGates(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Printf("gates: read the open gates: %v", err)
		}
		return
	}
	for _, g := range open {
		key := gateKey{request: g.Request, ticket: g.Ticket, move: g.Move, plugin: g.Plugin}
		if s.pluginBusy(g.Plugin) {
			// An install, update, change or removal of the plugin is
			// running; its end asks again.
			continue
		}
		if !s.pluginGatesMove(g.Plugin, g.Move) {
			s.givenUp(ctx, key, g)
			continue
		}
		if g.State != api.GatePending {
			continue
		}
		s.gates.mu.Lock()
		if s.gates.inflight[key] {
			s.gates.mu.Unlock()
			continue
		}
		s.gates.inflight[key] = true
		s.gates.mu.Unlock()
		if !s.background(func(ctx context.Context) {
			unanswered := s.askGate(ctx, key, g.FailureHold)
			s.gates.mu.Lock()
			delete(s.gates.inflight, key)
			s.gates.mu.Unlock()
			// A change that came while the run was in flight was skipped (the
			// key was still taken); with no answer recorded, only asking again
			// now settles the gate.
			if unanswered && ctx.Err() == nil {
				s.gates.poke()
			}
		}) {
			s.gates.mu.Lock()
			delete(s.gates.inflight, key)
			s.gates.mu.Unlock()
		}
	}
}

// pluginGatesMove reports whether plugin is loaded, enabled and has a gate
// hook for move.
func (s *Server) pluginGatesMove(plugin string, move api.GateMove) bool {
	for _, name := range s.plugins.m.GatePlugins(move) {
		if name == plugin {
			return true
		}
	}
	return false
}

// givenUp settles a gate whose plugin can no longer answer it (it was
// disabled, removed or no longer gates the move). The move goes ahead, with
// a note on the ticket and a line in the plugin's log, unless the gate's
// failure policy at the time of the request was to hold: then it holds
// (once) with that reason, and only the user's override or cancel ends it.
func (s *Server) givenUp(ctx context.Context, key gateKey, g board.OpenGate) {
	if g.FailureHold {
		if g.State == api.GatePending {
			s.finishGate(ctx, key, board.GateAnswer{Kind: board.GateHold,
				Reason: fmt.Sprintf("Plugin %s can no longer answer its gate (it is disabled, removed or no longer gates this move); its manifest says a failure holds.", key.plugin)},
				"the gate of #%d was given up: the plugin is disabled, removed or no longer gates this move; its manifest says a failure holds", key.ticket)
		}
		return
	}
	s.finishGate(ctx, key, board.GateAnswer{Kind: board.GateGoAhead,
		Note: fmt.Sprintf("Plugin %s can no longer answer its gate (it is disabled, removed or no longer gates this move): the move goes ahead.", key.plugin)},
		"the gate of #%d was given up: the plugin is disabled, removed or no longer gates this move; the move goes ahead", key.ticket)
}

// finishGate records an answer and ignores that the gate is gone already.
func (s *Server) finishGate(ctx context.Context, key gateKey, ans board.GateAnswer, logFormat string, logArgs ...any) {
	if logFormat != "" {
		s.plugins.m.LogLine(key.plugin, fmt.Sprintf(logFormat, logArgs...))
	}
	err := s.board.GateAnswer(ctx, key.ticket, key.request, key.plugin, ans)
	var ae *api.Error
	if err != nil && ctx.Err() == nil && !(errors.As(err, &ae) && ae.Code == api.CodeConflict) {
		s.log.Printf("gates: record the answer of %s on #%d: %v", key.plugin, key.ticket, err)
	}
}

// askGate runs plugin's gate hook for one ticket's move and records what
// it decided. It reports true when the run ended with no answer recorded
// because the plugin was stopped or reloaded during it: the caller asks
// again once the run no longer counts as in flight.
func (s *Server) askGate(ctx context.Context, key gateKey, failureHold bool) (unanswered bool) {
	var res plugins.GateResult
	data, err := s.gateEventData(ctx, key)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		// The hook cannot be asked: that is a failure of the gate, answered
		// by its failure policy like any other, never left pending.
		s.log.Printf("gates: build the gate event of #%d: %v", key.ticket, err)
		res = plugins.GateResult{Failure: "the ticket could not be read to ask the gate", FailureHolds: failureHold}
	} else {
		res, err = s.plugins.m.RunGate(ctx, key.plugin, key.move, data)
		switch {
		case errors.Is(err, plugins.ErrNoGate):
			s.givenUp(ctx, key, board.OpenGate{FailureHold: failureHold, State: api.GatePending})
			return false
		case err != nil:
			// Stopped or reloaded during the run (or omo is stopping, which
			// the next start asks again).
			return ctx.Err() == nil
		}
	}
	if res.Failure != "" {
		if res.FailureHolds {
			s.finishGate(ctx, key, board.GateAnswer{Kind: board.GateHold,
				Reason: fmt.Sprintf("The gate of plugin %s failed: %s", key.plugin, res.Failure)},
				"the gate of #%d failed (%s); its manifest says a failure holds", key.ticket, res.Failure)
			return false
		}
		s.finishGate(ctx, key, board.GateAnswer{Kind: board.GateGoAhead,
			Note: fmt.Sprintf("The gate of plugin %s failed (%s): the move goes ahead.", key.plugin, res.Failure)},
			"the gate of #%d failed (%s); the move goes ahead", key.ticket, res.Failure)
		return
	}
	ans := board.GateAnswer{Reason: res.Reason, Choices: res.Choices}
	switch res.Kind {
	case plugins.GateHold:
		ans.Kind = board.GateHold
	case plugins.GateRefuse:
		ans.Kind = board.GateRefuse
	default:
		ans.Kind = board.GateGoAhead
	}
	s.finishGate(ctx, key, ans, "")
	return false
}

// gateEventData is the data of a gate hook's event: the ticket as the other
// events carry it (with its folders), its type, whether it belongs to a
// story, its projects with their own folders and, per project, the branch
// its worktree would be based on.
func (s *Server) gateEventData(ctx context.Context, key gateKey) (map[string]any, error) {
	t, err := s.board.Get(ctx, board.User, key.ticket)
	if err != nil {
		return nil, err
	}
	var story *api.Ticket
	if t.Story != nil {
		st, err := s.board.Get(ctx, board.User, int64(*t.Story))
		if err == nil {
			story = &st
		}
	}
	projects := make([]any, 0, len(t.Projects))
	for _, p := range t.Projects {
		base := p.BaseBranch
		if story != nil {
			// A task is cut from its story's branch.
			base = ""
			for _, sp := range story.Projects {
				if sp.Project == p.Project {
					base = sp.Branch
					if base == "" {
						base = sp.BaseBranch
					}
				}
			}
		}
		projects = append(projects, map[string]any{
			"project":     p.Project,
			"base_branch": base,
			"folder":      s.pluginFolder(ctx, 0, p.Project),
		})
	}
	return map[string]any{
		"move":     string(key.move),
		"ticket":   s.pluginTicketPayload(ctx, t.TicketSummary),
		"type":     string(t.Type),
		"in_story": t.Story != nil,
		"projects": projects,
	}, nil
}

// pluginBusy reports whether an operation on the plugin runs.
func (s *Server) pluginBusy(name string) bool {
	f := &s.plugins
	f.busyMu.Lock()
	defer f.busyMu.Unlock()
	return f.busy[name]
}
