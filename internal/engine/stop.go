package engine

import (
	"context"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

// handoffPoll is how often a graceful stop looks whether the agents it asked
// for a handoff have answered.
const handoffPoll = 100 * time.Millisecond

// stopAllReason is the end reason of the agents "stop all agents" ends.
const stopAllReason = "stopped by the user"

// shutdownState is the state of a graceful stop.
type shutdownState struct {
	mu sync.Mutex
	// begun is set by the first Shutdown; hurry is closed by a later one,
	// which cuts the handoff wait short.
	begun   bool
	hurry   chan struct{}
	hurried bool
}

// handoffAsk is one agent asked for a handoff.
type handoffAsk struct {
	en     *entry
	column api.Column
}

// Shutdown stops the engine gracefully. From the first call on no agent
// starts any more. Every working product manager, developer and reviewer
// (one that acknowledged its start, is not parked in omo ai wait and is not
// the CEO) is asked once, with the handoff prompt, to write a handoff
// comment on its ticket. Shutdown waits until each asked agent has
// commented on its ticket since, parked itself in omo ai wait, handed in
// (its ticket moved) or exited, until ctx ends, or until a second Shutdown
// call, whichever comes first; then it kills every agent, the CEO
// included, ends every session with "omo stopped" and returns once nothing
// it started runs any more. A ctx that has already ended skips the
// handoff. Calling it again is safe: a later call ends the wait at once
// and returns when the stop is over.
func (e *Engine) Shutdown(ctx context.Context) {
	stopCtx := context.WithoutCancel(ctx)
	e.shut.mu.Lock()
	if e.shut.begun {
		if !e.shut.hurried {
			e.shut.hurried = true
			close(e.shut.hurry)
		}
		e.shut.mu.Unlock()
		e.halt(stopCtx, "omo stopped")
		return
	}
	e.shut.begun = true
	e.shut.hurry = make(chan struct{})
	hurry := e.shut.hurry
	e.shut.mu.Unlock()

	e.mu.Lock()
	e.stopping = true
	e.mu.Unlock()
	if ctx.Err() == nil {
		asked, after := e.askForHandoff(stopCtx)
		e.awaitHandoffs(ctx, hurry, asked, after)
	}
	e.halt(stopCtx, "omo stopped")
}

// askForHandoff sends the handoff prompt to every working agent and returns
// them with the id of the newest comment before the request.
func (e *Engine) askForHandoff(ctx context.Context) ([]handoffAsk, int64) {
	after, err := e.b.LastCommentID(ctx)
	if err != nil {
		e.log.Printf("engine: read the newest comment before the handoff: %v", err)
		return nil, 0
	}
	snap, err := e.b.EngineSnapshot(ctx)
	if err != nil {
		e.log.Printf("engine: read the board for the handoff: %v", err)
		return nil, 0
	}
	columns := map[int64]api.Column{}
	for _, t := range snap.Tickets {
		columns[t.ID] = t.Column
	}
	var asked []handoffAsk
	for _, en := range e.liveEntries() {
		switch en.sess.Role {
		case api.RolePM, api.RoleDeveloper, api.RoleReviewer:
		default:
			continue
		}
		e.mu.Lock()
		ending := en.ending != "" || en.crash != ""
		e.mu.Unlock()
		column, ok := columns[en.sess.Ticket]
		if ending || !ok || en.awaitingReady() || e.parked(en.sess.ID) {
			continue
		}
		e.notify(en.sess.ID, e.renderPrompt("handoff", prompts.Data{
			Name: en.sess.Name, Role: string(en.sess.Role), Ticket: int(en.sess.Ticket),
		}))
		asked = append(asked, handoffAsk{en: en, column: column})
	}
	return asked, after
}

// awaitHandoffs waits until every asked agent has answered, ctx ends or
// hurry is closed.
func (e *Engine) awaitHandoffs(ctx context.Context, hurry <-chan struct{}, asked []handoffAsk, after int64) {
	tick := time.NewTicker(handoffPoll)
	defer tick.Stop()
	for len(asked) > 0 {
		open := asked[:0]
		for _, a := range asked {
			if !e.answeredHandoff(ctx, a, after) {
				open = append(open, a)
			}
		}
		asked = open
		if len(asked) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-hurry:
			return
		case <-tick.C:
		}
	}
}

// answeredHandoff reports whether an asked agent is done with its handoff:
// it exited, parked itself, commented on its ticket after comment after,
// or its ticket moved.
func (e *Engine) answeredHandoff(ctx context.Context, a handoffAsk, after int64) bool {
	select {
	case <-a.en.agent.Exited():
		return true
	case <-a.en.finished:
		return true
	default:
	}
	id := a.en.sess.ID
	if e.parked(id) {
		return true
	}
	if ok, err := e.b.SessionCommentedAfter(context.WithoutCancel(ctx), id, a.en.sess.Ticket, after); err == nil && ok {
		return true
	}
	t, ok := e.ticketNow(context.WithoutCancel(ctx), a.en.sess.Ticket)
	return !ok || t.Column != a.column
}

// StopAll pauses omo and ends every agent except the CEO at once, without a
// handoff: product managers, developers, reviewers and export jobs. Tickets
// keep their columns and their worktrees; while paused nothing starts, and
// lifting the pause gives started work fresh agents, as at a start. The CEO
// is the user's own conversation and keeps running.
func (e *Engine) StopAll(ctx context.Context) error {
	if err := e.b.SetPaused(ctx, true); err != nil {
		return err
	}
	// A pass that read the board before the pause finishes launching its
	// starts first; every later pass sees the pause and starts nothing.
	e.passMu.Lock()
	e.passMu.Unlock()
	defer e.Wake()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		e.endAllButCEO(ctx)
		if !e.agentStartsPending() {
			return nil
		}
		// A start that was already under way ends with an agent (ended on
		// the next round) or without one.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// endAllButCEO ends every live session that is not the CEO's and whose
// agent is not still being started (that one is ended on a later round,
// once its process runs).
func (e *Engine) endAllButCEO(ctx context.Context) {
	snap, err := e.b.EngineSnapshot(ctx)
	if err != nil {
		e.log.Printf("engine: read the board to stop all agents: %v", err)
	}
	ids := map[string]bool{}
	e.mu.Lock()
	starting := map[int64]bool{}
	for job := range e.starts {
		if job.role != api.RoleCEO {
			starting[job.ticket] = true
		}
	}
	for id, en := range e.entries {
		if en.sess.Role != api.RoleCEO && en.agent != nil {
			ids[id] = true
		}
	}
	for _, s := range snap.Sessions {
		if s.Role == api.RoleCEO || starting[s.Ticket] {
			continue
		}
		if en, ok := e.entries[s.ID]; ok && en.agent == nil {
			continue
		}
		ids[s.ID] = true
	}
	e.mu.Unlock()
	var wg sync.WaitGroup
	for id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.end(ctx, id, stopAllReason)
		}()
	}
	wg.Wait()
}

// agentStartsPending reports whether a worker is still starting an agent
// other than the CEO.
func (e *Engine) agentStartsPending() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for job := range e.starts {
		if job.role != api.RoleCEO {
			return true
		}
	}
	return false
}
