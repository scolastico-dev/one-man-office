package engine

import (
	"context"
	"strings"
	"time"
)

// end ends a session: when the engine holds its agent, the agent is marked
// as ended on purpose with reason, killed (with every process it started),
// and end waits until the watcher has ended the session; otherwise the
// session is ended at once. It never takes longer than about endTimeout.
func (e *Engine) end(ctx context.Context, id, reason string) {
	e.mu.Lock()
	en, ok := e.entries[id]
	if ok && en.ending == "" {
		en.ending = reason
	}
	e.mu.Unlock()
	if !ok {
		if err := e.b.EndSession(ctx, id, reason); err != nil {
			e.log.Printf("engine: end a session: %s", scrub(err, id))
		}
		return
	}
	if en.agent != nil {
		if err := en.agent.Kill(); err != nil {
			e.log.Printf("engine: kill %s on #%d: %v", en.sess.Name, en.sess.Ticket, err)
		}
	}
	timer := time.NewTimer(endTimeout + time.Second)
	defer timer.Stop()
	select {
	case <-en.finished:
	case <-timer.C:
		// The watcher still waits for the terminal; the session is over
		// for the board either way.
		e.log.Printf("engine: %s on #%d did not end in time", en.sess.Name, en.sess.Ticket)
		if err := e.b.EndSession(ctx, id, reason); err != nil {
			e.log.Printf("engine: end a session: %s", scrub(err, id))
		}
	}
}

// watch waits for an agent's main process to exit, then kills whatever it
// left behind, waits for its terminal to close and ends its session. An exit
// nobody asked for is a crash: it is counted against the agent's slot (see
// recordFailure) before the session ends, so no pass starts a fresh agent
// before the count is known. Finally the engine is woken.
func (e *Engine) watch(en *entry) {
	defer e.watchers.Done()
	ctx := context.Background()
	<-en.agent.Exited()
	_ = en.agent.Kill()
	timer := time.NewTimer(endTimeout)
	select {
	case <-en.agent.Done():
	case <-timer.C:
		e.log.Printf("engine: the terminal of %s on #%d stayed open after its process ended", en.sess.Name, en.sess.Ticket)
	}
	timer.Stop()

	e.mu.Lock()
	reason, crash := en.ending, en.crash
	e.mu.Unlock()
	if reason == "" {
		what := crash
		reason = crash
		if crash == "" {
			exit := exitText(en.agent.ExitErr())
			what = "exited unexpectedly (" + exit + ")"
			reason = "exited unexpectedly: " + exit
		}
		e.recordFailure(ctx, en, what)
	}
	if err := e.b.EndSession(ctx, en.sess.ID, reason); err != nil {
		e.log.Printf("engine: end the session of %s on #%d: %s", en.sess.Name, en.sess.Ticket, scrub(err, en.sess.ID))
	}
	e.mu.Lock()
	if e.entries[en.sess.ID] == en {
		delete(e.entries, en.sess.ID)
	}
	e.mu.Unlock()
	close(en.finished)
	e.Wake()
}

// scrub returns err's text with the session id replaced: board errors about
// a session name it, and a session id never goes into a log line.
func scrub(err error, id string) string {
	if err == nil {
		return ""
	}
	if id == "" {
		return err.Error()
	}
	return strings.ReplaceAll(err.Error(), id, "[session]")
}

// exitText describes how a process ended.
func exitText(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}
