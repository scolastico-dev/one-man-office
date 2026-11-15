package engine

import (
	"context"
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
// fresh ends the live one first. The CEO is never gated: it holds no slot
// and starts while omo is paused. Concurrent calls start one CEO.
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
	if !hasUsableModel(c, e.st) {
		return api.AgentSession{}, api.Errorf(api.CodeValidation, "no usable model for the ceo role; set models in roles/ceo.yaml")
	}
	model, err := e.picker.pick(api.RoleCEO, c, e.st)
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
