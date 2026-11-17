package engine

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// ceoOp is one CEO start; done is closed when it is over.
type ceoOp struct {
	done chan struct{}
	sess api.AgentSession
	err  error
}

// StartCEO returns the live CEO session, starting one when there is none;
// fresh ends the live one first. The CEO is never gated: it counts against
// no limit and starts while omo is paused. Concurrent calls start one CEO.
func (e *Engine) StartCEO(ctx context.Context, fresh bool) (api.AgentSession, error) {
	if !e.st.General().CEO.Enabled {
		return api.AgentSession{}, api.Errorf(api.CodeValidation, "the CEO is switched off; switch it on in the settings first")
	}
	for {
		op, mine := e.ceoBegin(fresh)
		select {
		case <-op.done:
		case <-ctx.Done():
			return api.AgentSession{}, ctx.Err()
		}
		if mine || !fresh {
			return op.sess, op.err
		}
		// Another start was running when a fresh session was asked for:
		// now start the fresh one.
	}
}

// ceoBegin returns the CEO start in progress, or begins one (mine is then
// true). It never blocks on the start itself.
func (e *Engine) ceoBegin(fresh bool) (op *ceoOp, mine bool) {
	nt := e.nt
	nt.mu.Lock()
	if cur := nt.ceoOp; cur != nil {
		nt.mu.Unlock()
		return cur, false
	}
	op = &ceoOp{done: make(chan struct{})}
	nt.ceoOp = op
	nt.mu.Unlock()
	finish := func() {
		nt.mu.Lock()
		if nt.ceoOp == op {
			nt.ceoOp = nil
		}
		nt.mu.Unlock()
		close(op.done)
	}
	job := &startJob{role: api.RoleCEO}
	ok := e.launch(context.Background(), 0, job, func(ctx context.Context) {
		defer finish()
		op.sess, op.err = e.startCEO(ctx, job, fresh)
	})
	if !ok {
		op.err = api.Errorf(api.CodeUnavailable, "omo is stopping")
		finish()
	}
	return op, true
}

// startCEO runs in a worker: it ends the live CEO when fresh, returns the
// live one if there is one, and otherwise starts a CEO in the CEO folder.
func (e *Engine) startCEO(ctx context.Context, job *startJob, fresh bool) (api.AgentSession, error) {
	snap, err := e.b.EngineSnapshot(ctx)
	if err != nil {
		return api.AgentSession{}, err
	}
	var live []string
	for _, s := range snap.Sessions {
		if s.Role == api.RoleCEO {
			live = append(live, s.ID)
		}
	}
	if fresh {
		for _, id := range live {
			e.end(ctx, id, "new session requested by the user")
		}
		live = nil
	}
	if len(live) > 0 {
		return e.agentSession(ctx, live[len(live)-1])
	}
	if !e.st.General().CEO.Enabled {
		return api.AgentSession{}, api.Errorf(api.CodeValidation, "the CEO is switched off; switch it on in the settings first")
	}
	c := e.modelChoice(api.RoleCEO, api.PurposeWork, board.EngineTicket{}, "", 0)
	switch canStart(c, e.st, e.use) {
	case startNoModel:
		return api.AgentSession{}, api.Errorf(api.CodeValidation, "no usable model for the ceo role; set models in roles/ceo.yaml")
	case startUsage:
		return api.AgentSession{}, ceoUsageError()
	}
	model, err := e.picker.pick(api.RoleCEO, c, e.st, e.use)
	if errors.Is(err, errUsageWait) {
		return api.AgentSession{}, ceoUsageError()
	}
	if err != nil {
		return api.AgentSession{}, api.Errorf(api.CodeValidation, "no usable model for the ceo role: %v", err)
	}
	if err := os.MkdirAll(e.o.Layout.CEO, 0o700); err != nil {
		return api.AgentSession{}, fmt.Errorf("create the CEO folder: %w", err)
	}
	sess, err := e.spawn(ctx, spawnRequest{role: api.RoleCEO, purpose: api.PurposeWork, model: model,
		workdir: e.o.Layout.CEO, job: job, sessions: snap.Sessions})
	if err != nil {
		return api.AgentSession{}, err
	}
	if sess.State == api.SessionEnded || !e.hasEntry(sess.ID) {
		return api.AgentSession{}, api.Errorf(api.CodeUnavailable, "omo is stopping")
	}
	return e.agentSession(ctx, sess.ID)
}

// agentSession reads the current record of a session.
func (e *Engine) agentSession(ctx context.Context, id string) (api.AgentSession, error) {
	all, err := e.b.AgentSessions(ctx, board.User, true)
	if err != nil {
		return api.AgentSession{}, err
	}
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
	}
	return api.AgentSession{}, api.Errorf(api.CodeConflict, "the CEO session ended while it started")
}

// ceoUsageError is the answer to a CEO start while every model of the ceo
// role is near its usage limit: like every other role, the CEO does not
// start on such a model.
func ceoUsageError() error {
	return api.Errorf(api.CodeUnavailable, "every model of the ceo role is near its usage limit; start the CEO again once one is below its limits")
}

// ceoStopReason is the end reason of a CEO session the user stopped. The
// newest CEO session ending with it is what makes the CEO stopped (see
// CEOStopped), so a stop also holds across a restart of omo.
const ceoStopReason = "stopped by the user"

// StopCEO ends the CEO on the user's request, after a start under way is
// over. A stopped CEO stays stopped: the mentions and questions of agents no
// longer start it, their lines wait for its next session, until the user
// starts it (from the CEO tab, a new session, or a mention of the user's
// own). It answers conflict when no CEO runs or starts.
func (e *Engine) StopCEO(ctx context.Context) error {
	stopped := false
	for {
		nt := e.nt
		nt.mu.Lock()
		op := nt.ceoOp
		nt.ceoDeferred = false
		nt.mu.Unlock()
		if op != nil {
			select {
			case <-op.done:
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		snap, err := e.b.EngineSnapshot(ctx)
		if err != nil {
			return err
		}
		var live []string
		for _, s := range snap.Sessions {
			if s.Role == api.RoleCEO {
				live = append(live, s.ID)
			}
		}
		if len(live) == 0 {
			if !stopped {
				return api.Errorf(api.CodeConflict, "the CEO is not running")
			}
			return nil
		}
		// A start an agent's mention began while these end is waited for
		// on the next round and ended too.
		for _, id := range live {
			e.end(ctx, id, ceoStopReason)
		}
		stopped = true
	}
}

// CEOStopped reports whether the user stopped the CEO and has not started
// it since: whether the newest CEO session ended with ceoStopReason.
func (e *Engine) CEOStopped(ctx context.Context) (bool, error) {
	all, err := e.b.AgentSessions(ctx, board.System, false)
	if err != nil {
		return false, err
	}
	for _, s := range all { // newest first
		if s.Role == api.RoleCEO {
			return s.State == api.SessionEnded && s.EndReason == ceoStopReason, nil
		}
	}
	return false, nil
}

// ceoHeld reports whether a start that the user did not ask for must leave
// the CEO alone because the user stopped it. A board that cannot be read
// holds it too: a start nobody asked for can wait.
func (e *Engine) ceoHeld(ctx context.Context) bool {
	stopped, err := e.CEOStopped(ctx)
	if err != nil {
		e.log.Printf("engine: read whether the CEO is stopped: %v", err)
		return true
	}
	return stopped
}
