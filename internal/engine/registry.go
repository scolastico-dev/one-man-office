package engine

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/names"
)

// entry is one agent the engine started. It stays in Engine.entries from
// just before its process starts until its session has been ended.
type entry struct {
	// agent is nil while the process is being started.
	agent agents.Agent
	// sess holds the session's record as the engine created it.
	sess board.EngineSession
	// slot is the agent slot the session fills (for failure accounting).
	slot     failKey
	delivery agents.Delivery
	// started is when the process was started, by the engine's clock.
	started time.Time
	// ready is set once the agent acknowledged its start (omo ai ready).
	ready atomic.Bool
	// The fields below are guarded by Engine.mu.
	//
	// ending is the reason the engine is ending the agent; "" while nobody
	// asked it to end, so an exit is unexpected.
	ending  string
	endKind api.SessionEndKind
	// crash, when set, is what made the engine kill an agent that counts
	// as failed (it did not acknowledge its start in time).
	crash string
	// finished is closed once the session has been ended and the entry
	// removed.
	finished chan struct{}
}

// awaitingReady reports whether the agent has not acknowledged its start.
func (en *entry) awaitingReady() bool { return !en.ready.Load() }

// hasEntry reports whether the engine holds an agent for the session.
func (e *Engine) hasEntry(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.entries[id]
	return ok
}

// lookup returns the entry of a session.
func (e *Engine) lookup(id string) (*entry, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	en, ok := e.entries[id]
	return en, ok
}

// lookupAgent returns the entry of a session and its agent, nil while the
// agent is still being started. The agent is read under the lock that its
// start writes it under.
func (e *Engine) lookupAgent(id string) (*entry, agents.Agent, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	en, ok := e.entries[id]
	if !ok {
		return nil, nil, false
	}
	return en, en.agent, true
}

// entryIDs returns the session ids of every entry.
func (e *Engine) entryIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.entries))
	for id := range e.entries {
		out = append(out, id)
	}
	return out
}

// liveEntries returns every entry whose process is running.
func (e *Engine) liveEntries() []*entry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*entry, 0, len(e.entries))
	for _, en := range e.entries {
		if en.agent != nil {
			out = append(out, en)
		}
	}
	return out
}

// markReady records that the session's agent acknowledged its start. It
// reports whether the session has an entry and whether it was still
// waiting for that.
func (e *Engine) markReady(id string) (known, wasStarting bool) {
	en, ok := e.lookup(id)
	if !ok {
		return false, false
	}
	return true, !en.ready.Swap(true)
}

// reserveName picks a name for a new agent of role whose first name no
// live session, agent or other start in progress uses, and reserves it
// until releaseName.
func (e *Engine) reserveName(role api.Role, sessions []board.EngineSession) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	used := map[string]bool{}
	for _, en := range e.entries {
		used[names.FirstName(en.sess.Name)] = true
	}
	for n := range e.reserved {
		used[names.FirstName(n)] = true
	}
	for _, s := range sessions {
		used[names.FirstName(s.Name)] = true
	}
	name, err := names.Pick(string(role), func(n string) bool { return used[names.FirstName(n)] })
	if err != nil {
		return "", err
	}
	e.reserved[name] = true
	return name, nil
}

// releaseName ends the reservation of a name.
func (e *Engine) releaseName(name string) {
	e.mu.Lock()
	delete(e.reserved, name)
	e.mu.Unlock()
}

// checkStarting kills every agent that has not acknowledged its start
// within its delivery's ready timeout; the kill counts as a failure of its
// slot.
func (e *Engine) checkStarting(_ context.Context, now time.Time) {
	var late []*entry
	e.mu.Lock()
	for _, en := range e.entries {
		if en.agent == nil || en.ending != "" || en.crash != "" || !en.awaitingReady() {
			continue
		}
		if now.Sub(en.started) >= en.delivery.ReadyTimeout() {
			en.crash = "did not run omo ai ready in time"
			late = append(late, en)
		}
	}
	e.mu.Unlock()
	for _, en := range late {
		e.log.Printf("engine: %s on #%d did not acknowledge its start in %s; ending it", en.sess.Name, en.sess.Ticket,
			en.delivery.ReadyTimeout())
		_ = en.agent.Kill()
	}
}
