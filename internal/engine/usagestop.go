package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

// usageStopReason is the end reason of an agent stopped because its model
// reached its hard usage limit.
const usageStopReason = "usage limit reached"

// usageState is the engine's memory about usage. Its fields are guarded by
// Engine.mu and kept in memory only: a restart forgets them.
type usageState struct {
	// forced holds the tickets whose next start may use their own model
	// although it is near its usage limit ("Start anyway"), with the column
	// the ticket was in when the user asked.
	forced map[int64]api.Column
	// stopping holds the sessions being stopped for usage, so none is asked
	// for a handoff twice.
	stopping map[string]bool
	// timeout, when set, replaces stop.handoff_timeout; only tests set it.
	timeout time.Duration
}

// usageStops returns the live sessions whose model is at its hard usage
// limit and that the engine may stop now: it started their agent, nobody
// ends them yet and no usage stop runs for them. It also forgets "Start
// anyway" for tickets that changed column since, unless a worker acts on
// them (a gated start moves its ticket before the agent starts).
func (e *Engine) usageStops(snap board.EngineSnapshot) []string {
	columns := map[int64]api.Column{}
	for _, t := range snap.Tickets {
		columns[t.ID] = t.Column
	}
	type candidate struct{ id, model string }
	var cands []candidate
	e.mu.Lock()
	for n, col := range e.usage.forced {
		if now, ok := columns[n]; (!ok || now != col) && e.inflight[n] == 0 {
			delete(e.usage.forced, n)
		}
	}
	for _, s := range snap.Sessions {
		en, ok := e.entries[s.ID]
		if !ok || en.agent == nil || en.ending != "" || en.crash != "" || e.usage.stopping[s.ID] {
			continue
		}
		cands = append(cands, candidate{s.ID, s.Model})
	}
	e.mu.Unlock()
	if e.o.Usage == nil {
		return nil
	}
	// The usage view is asked outside Engine.mu: it may take locks of its
	// own.
	var out []string
	for _, c := range cands {
		if c.model != "" && e.use(c.model).State == api.UsageHard {
			out = append(out, c.id)
		}
	}
	return out
}

// usageStop stops the agents of the sessions of acts, all of one ticket,
// whose model reached its hard usage limit, in the background: a working
// product manager, developer or reviewer is asked once for a handoff, as at
// a graceful stop (see wantsHandoff), and ended once it answered (see
// answeredHandoff) or after stop.handoff_timeout; a parked or starting agent
// and the CEO are ended at once. Each session ends with usageStopReason,
// which counts as no failure; the ticket keeps its column, worktree and
// commits, and gets one comment for all agents of this pass saying why. The
// planner then starts the ticket's next agent on a model below its limits,
// or marks the ticket as waiting for usage.
func (e *Engine) usageStop(ctx context.Context, acts []action) {
	var ens []*entry
	e.mu.Lock()
	for _, a := range acts {
		en, ok := e.entries[a.session]
		if e.stopping || !ok || en.agent == nil || en.ending != "" || e.usage.stopping[a.session] {
			continue
		}
		if e.usage.stopping == nil {
			e.usage.stopping = map[string]bool{}
		}
		e.usage.stopping[a.session] = true
		ens = append(ens, en)
	}
	if len(ens) == 0 {
		e.mu.Unlock()
		return
	}
	// Tracked as a watcher, not a worker: halt waits for workers before it
	// kills the agents, and this one may wait for a handoff.
	e.watchers.Add(1)
	e.mu.Unlock()
	go func() {
		defer func() {
			e.mu.Lock()
			for _, en := range ens {
				delete(e.usage.stopping, en.sess.ID)
			}
			e.mu.Unlock()
			e.watchers.Done()
			e.Wake()
		}()
		var wg sync.WaitGroup
		stopped := make([]*api.ModelUsage, len(ens))
		for i, en := range ens {
			wg.Add(1)
			go func() {
				defer wg.Done()
				u := e.use(en.sess.Model)
				if e.stopForUsage(ctx, en) {
					stopped[i] = &u
				}
			}()
		}
		wg.Wait()
		ticket := ens[0].sess.Ticket
		if ticket == 0 {
			return
		}
		var models []string
		usage := map[string]api.ModelUsage{}
		count := 0
		for i, u := range stopped {
			if u == nil {
				continue
			}
			count++
			m := ens[i].sess.Model
			if _, ok := usage[m]; !ok {
				models = append(models, m)
				usage[m] = *u
			}
		}
		if count == 0 {
			return
		}
		if _, err := e.b.Comment(ctx, board.System, ticket, api.CommentRequest{Body: usageStopText(count, models, usage)}); err != nil {
			e.log.Printf("engine: comment on the usage stop of #%d: %v", ticket, err)
		}
	}()
}

// stopForUsage carries out one usage stop (see usageStop) and reports
// whether it ended the agent.
func (e *Engine) stopForUsage(ctx context.Context, en *entry) bool {
	if column, ok := e.handoffColumn(ctx, en); ok {
		after, err := e.b.LastCommentID(ctx)
		if err != nil {
			e.log.Printf("engine: read the newest comment before a usage stop: %v", err)
		} else if ask, asked := e.askHandoff(en, column, prompts.StopUsage); asked {
			e.awaitOneHandoff(ctx, ask, after)
		}
	}
	e.mu.Lock()
	cur, ok := e.entries[en.sess.ID]
	mine := ok && cur == en && cur.ending == "" && cur.crash == ""
	e.mu.Unlock()
	if !mine {
		return false // it ended by itself, or something else ends it
	}
	e.end(ctx, en.sess.ID, usageStopReason)
	return true
}

// handoffColumn returns the column of the ticket of a session that is to be
// asked for a handoff, and false for one that is ended at once.
func (e *Engine) handoffColumn(ctx context.Context, en *entry) (api.Column, bool) {
	if !e.wantsHandoff(en) {
		return "", false
	}
	t, ok := e.ticketNow(ctx, en.sess.Ticket)
	if !ok {
		return "", false
	}
	return t.Column, true
}

// awaitOneHandoff waits until an asked agent answered, the handoff timeout
// passed or ctx ended.
func (e *Engine) awaitOneHandoff(ctx context.Context, ask handoffAsk, after int64) {
	timeout := time.Duration(e.st.General().Stop.HandoffTimeout)
	e.mu.Lock()
	if e.usage.timeout > 0 {
		timeout = e.usage.timeout
	}
	e.mu.Unlock()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(handoffPoll)
	defer tick.Stop()
	for !e.answeredHandoff(ctx, ask, after) {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
		}
	}
}

// usageAsked reports whether a usage stop runs for a session.
func (e *Engine) usageAsked(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage.stopping[id]
}

// usageStopText is the comment about count agents of one ticket stopped
// for usage in one pass, on models (in the order they were met) with their
// usage.
func usageStopText(count int, models []string, usage map[string]api.ModelUsage) string {
	var parts []string
	for _, model := range models {
		u := usage[model]
		var why []string
		// The window as people read it: its label where the script gave one.
		window := u.WindowLabel
		if window == "" {
			window = u.Window
		}
		if window != "" && u.UsedPercent != nil {
			why = append(why, fmt.Sprintf("%s at %.0f %%", window, *u.UsedPercent))
		} else if window != "" {
			why = append(why, window)
		}
		if u.ResetsAt != nil {
			why = append(why, "resets "+u.ResetsAt.Local().Format("2006-01-02 15:04 MST"))
		}
		part := fmt.Sprintf("%s reached its usage limit", model)
		if len(why) > 0 {
			part += " (" + strings.Join(why, ", ") + ")"
		}
		parts = append(parts, part)
	}
	head := "Stopped: "
	if count > 1 {
		head = fmt.Sprintf("Stopped %d agents: ", count)
	}
	return head + strings.Join(parts, "; ") + ". omo goes on when a model of this role is below its limits."
}
