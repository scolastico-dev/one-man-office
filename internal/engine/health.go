package engine

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

// echoSettle is how long after a stalled reminder new output still counts
// as the terminal showing the typed reminder rather than the agent
// answering it.
const echoSettle = defaultGrace + 10*time.Second

// healthState is what the health checks remember between two checks. It
// lives in memory only: after a restart every agent is fresh anyway.
type healthState struct {
	mu sync.Mutex
	// stalls tracks every working agent by session id.
	stalls map[string]*stallRecord
	// reminded holds the open questions whose addressee was reminded, by
	// comment id.
	reminded map[int64]bool
	// flags holds, per ticket, the question whose attention flag the
	// health check owns, so it clears only its own.
	flags map[int64]questionFlag
}

// stallRecord is the silence of one agent. An episode starts with the last
// sign of life and ends with the next one.
type stallRecord struct {
	// lastOutput is the agent's LastOutput when it was last looked at (the
	// agent's clock); activeAt is when the engine last saw it alive (the
	// engine's clock).
	lastOutput time.Time
	activeAt   time.Time
	// Where the ticket stood; a move ends the episode.
	column api.Column
	stage  api.ReviewStage
	round  int
	// remindedAt is when the stalled reminder went out (zero: not yet), by
	// the engine's clock; echoUntil is when, by the agent's clock, output
	// stops counting as the terminal showing the reminder itself.
	remindedAt time.Time
	echoUntil  time.Time
	// flagging is set while the failed step is being written; flagged is
	// the owner of the attention it stored ("" while not flagged).
	flagging bool
	flagged  board.AttentionOwner
}

// questionFlag is the attention one question put on its ticket.
type questionFlag struct {
	comment int64
	owner   board.AttentionOwner
	text    string
}

// checkHealth reminds and flags silent agents and unanswered questions. It
// is code only: no model is asked, and nothing is sent twice for the same
// silence or the same question.
//
// An agent that is supposed to work (it acknowledged its start, is not
// parked in omo ai wait, is not being ended, and is not the CEO) and whose
// transcript gained no line for health.silent_after gets the stalled
// reminder once; silent for health.silent_after more, its ticket gets the
// failed step agent, which tells its supervisor. New output, a wait, or a
// move of the ticket end the silence and clear a failed step the check set.
// A reviewer that owes its round a verdict (see owesVerdict) is supposed to
// work even while it waits: a round collects every verdict before it ends,
// so a reviewer that never answers would otherwise hold its ticket for
// ever. It is reminded and then flagged like any silent agent.
//
// A question open for health.question_unanswered_after gets the
// question-reminder line to its addressee's running agents once; after
// twice that time its ticket is flagged. A question to the user is flagged
// at the first threshold, without a reminder. The answer clears the flag.
//
// It also hands a changed agent environment on to omo's git client.
func (e *Engine) checkHealth(ctx context.Context, now time.Time) {
	e.requestGitEnvironment()
	snap, err := e.b.EngineSnapshot(ctx)
	if err != nil {
		e.log.Printf("engine: read the board for health checks: %v", err)
		return
	}
	tickets := make(map[int64]board.EngineTicket, len(snap.Tickets))
	for _, t := range snap.Tickets {
		tickets[t.ID] = t
	}
	qs, err := e.b.OpenQuestions(ctx)
	if err != nil {
		e.log.Printf("engine: read the open questions: %v", err)
		return
	}
	asking := map[string]bool{}
	for _, q := range qs {
		if q.AuthorSession != "" {
			asking[q.AuthorSession] = true
		}
	}
	g := e.st.General()
	e.checkStalls(ctx, now, time.Duration(g.Health.SilentAfter), tickets, asking)
	e.checkQuestions(ctx, now, time.Duration(g.Health.QuestionUnansweredAfter), snap, tickets, qs)
}

// owesVerdict reports whether s is a reviewer of t's current review round
// whose project has no verdict yet and that has no open question of its own
// (asking holds the sessions that asked one): its round cannot end until it
// answers, so waiting is no sign of life for it.
func owesVerdict(s board.EngineSession, t board.EngineTicket, asking map[string]bool) bool {
	return s.Role == api.RoleReviewer && !keptAgent(s, t) && !asking[s.ID]
}

// ---- silent agents ----

// stallAction is what one check decided for one agent; it is carried out
// after the health lock is released.
type stallAction struct {
	en                  *entry
	t                   board.EngineTicket
	remind, flag, clear bool
	// silent is how long the agent has been silent, since how long ago
	// it was reminded; clearOwner identifies the flag to clear.
	silent, since time.Duration
	clearOwner    board.AttentionOwner
}

// checkStalls applies the silent-agent rule to every agent the engine runs.
func (e *Engine) checkStalls(ctx context.Context, now time.Time, silentAfter time.Duration, tickets map[int64]board.EngineTicket,
	asking map[string]bool) {
	var todo []stallAction
	seen := map[string]bool{}
	hs := &e.health
	for _, en := range e.liveEntries() {
		if en.sess.Role == api.RoleCEO || en.sess.Ticket == 0 {
			continue // the CEO talks with the user and never waits
		}
		t, ok := tickets[en.sess.Ticket]
		if !ok {
			continue
		}
		e.mu.Lock()
		ending := en.ending != "" || en.crash != ""
		e.mu.Unlock()
		if ending {
			continue
		}
		id := en.sess.ID
		seen[id] = true
		lo := en.agent.LastOutput()
		alive := en.awaitingReady() || (e.parked(id) && !owesVerdict(en.sess, t, asking))

		hs.mu.Lock()
		if hs.stalls == nil {
			hs.stalls = map[string]*stallRecord{}
		}
		rec := hs.stalls[id]
		if rec == nil {
			rec = &stallRecord{lastOutput: lo, activeAt: en.started, column: t.Column, stage: t.ReviewStage, round: t.ReviewRound}
			hs.stalls[id] = rec
		}
		if t.Column != rec.column || t.ReviewStage != rec.stage || t.ReviewRound != rec.round {
			alive = true
		}
		if !lo.Equal(rec.lastOutput) {
			rec.lastOutput = lo
			// Output right after the reminder is the reminder showing in
			// the terminal; output after that answers it.
			if rec.remindedAt.IsZero() || lo.After(rec.echoUntil) {
				alive = true
			}
		}
		a := stallAction{en: en, t: t}
		switch {
		case alive:
			if rec.flagged != "" {
				a.clear, a.clearOwner = true, rec.flagged
			}
			*rec = stallRecord{lastOutput: lo, activeAt: now, column: t.Column, stage: t.ReviewStage, round: t.ReviewRound}
		case silentAfter <= 0 || rec.flagged != "" || rec.flagging:
		case rec.remindedAt.IsZero():
			if silent := now.Sub(rec.activeAt); silent >= silentAfter {
				a.remind, a.silent = true, silent
				rec.remindedAt, rec.echoUntil = now, e.agentNow().Add(echoSettle)
			}
		case now.Sub(rec.remindedAt) >= silentAfter && t.FailedStep == api.FailedNone:
			a.flag, a.silent, a.since = true, now.Sub(rec.activeAt), now.Sub(rec.remindedAt)
			rec.flagging = true
		}
		hs.mu.Unlock()
		if a.remind || a.flag || a.clear {
			todo = append(todo, a)
		}
	}
	hs.mu.Lock()
	for id := range hs.stalls {
		if !seen[id] {
			delete(hs.stalls, id)
		}
	}
	hs.mu.Unlock()

	for _, a := range todo {
		switch {
		case a.remind:
			e.notify(a.en.sess.ID, e.renderPrompt("stalled", prompts.Data{
				Name: a.en.sess.Name, Role: string(a.en.sess.Role), Ticket: int(a.en.sess.Ticket), Silent: shortDuration(a.silent),
			}))
		case a.flag:
			e.flagStalled(ctx, a)
		case a.clear:
			if a.t.FailedStep == api.FailedAgent && a.t.AttentionOwner == a.clearOwner {
				if err := e.b.ClearFailed(ctx, a.t.ID, api.FailedAgent); err != nil {
					e.log.Printf("engine: clear the stall flag of #%d: %v", a.t.ID, err)
				}
			}
		}
	}
}

// flagStalled gives the ticket of a silent agent the failed step agent and
// remembers the owner of the flag that was stored.
func (e *Engine) flagStalled(ctx context.Context, a stallAction) {
	name := a.en.sess.Name
	reason := fmt.Sprintf("%s has produced no output for %s, also not in the %s since omo reminded it. "+
		"Choices: retry the ticket to replace it with a fresh agent, ask it a question if it may be waiting for something, or cancel the ticket.",
		name, shortDuration(a.silent), shortDuration(a.since))
	owner := board.AttentionOwner("stall:" + a.en.sess.ID)
	var stored board.AttentionOwner
	if err := e.b.SetFailedOwned(ctx, a.t.ID, api.FailedAgent, owner, reason); err != nil {
		e.log.Printf("engine: flag the silent agent %s on #%d: %v", name, a.t.ID, err)
	} else if t, ok := e.ticketNow(ctx, a.t.ID); ok && t.FailedStep == api.FailedAgent {
		stored = t.AttentionOwner
	}
	e.health.mu.Lock()
	if rec := e.health.stalls[a.en.sess.ID]; rec != nil && rec.flagging {
		// When nothing was stored, a later check tries again.
		rec.flagging, rec.flagged = false, stored
	}
	e.health.mu.Unlock()
}

// agentNow is the time by the clock the agents' LastOutput is measured with.
func (e *Engine) agentNow() time.Time {
	if r, ok := e.o.Runner.(*agents.PTYRunner); ok && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// parked reports whether a session is parked in omo ai wait.
func (e *Engine) parked(id string) bool {
	e.nt.mu.Lock()
	defer e.nt.mu.Unlock()
	mb := e.nt.boxes[id]
	return mb != nil && mb.waiter != nil
}

// ---- unanswered questions ----

// checkQuestions applies the unanswered-question rule to every open
// question.
func (e *Engine) checkQuestions(ctx context.Context, now time.Time, after time.Duration, snap board.EngineSnapshot,
	tickets map[int64]board.EngineTicket, qs []board.OpenQuestion) {
	ceoOn := e.st.General().CEO.Enabled
	open := map[int64]bool{}
	for _, q := range qs {
		open[q.Comment] = true
	}
	type reminder struct{ session, line string }
	var reminders []reminder
	var flags []struct {
		ticket int64
		flag   questionFlag
	}
	var clears []int64

	hs := &e.health
	hs.mu.Lock()
	if hs.reminded == nil {
		hs.reminded = map[int64]bool{}
		hs.flags = map[int64]questionFlag{}
	}
	flagging := map[int64]bool{}
	for _, q := range qs {
		t, ok := tickets[q.Ticket]
		if !ok || t.Column == api.ColumnDone || after <= 0 {
			continue
		}
		age := now.Sub(q.CreatedAt)
		if age < after {
			continue
		}
		to := q.To
		if to == api.RoleCEO && !ceoOn {
			to = api.RoleUser // supervision fell to the user
		}
		if to != api.RoleUser && !hs.reminded[q.Comment] {
			if ids := e.addressees(to, t, snap.Sessions); len(ids) > 0 {
				hs.reminded[q.Comment] = true
				line := e.renderPrompt("question-reminder", prompts.Data{Question: int(q.Comment), Ticket: int(q.Ticket)})
				for _, id := range ids {
					reminders = append(reminders, reminder{id, line})
				}
			}
		}
		if (to != api.RoleUser && age < 2*after) || flagging[q.Ticket] {
			continue
		}
		if f, ok := hs.flags[q.Ticket]; ok && t.AttentionOwner == f.owner {
			if open[f.comment] {
				continue // the ticket already shows an open question
			}
		} else if t.Attention != "" {
			continue // an attention text somebody else set stays
		}
		flagging[q.Ticket] = true
		flags = append(flags, struct {
			ticket int64
			flag   questionFlag
		}{q.Ticket, questionFlag{comment: q.Comment, owner: board.AttentionOwner("question:" + strconv.FormatInt(q.Comment, 10)),
			text: fmt.Sprintf("question %d to the %s has waited %s for an answer", q.Comment, roleWords(to), shortDuration(age))}})
	}
	for id := range hs.reminded {
		if !open[id] {
			delete(hs.reminded, id)
		}
	}
	for n, f := range hs.flags {
		if flagging[n] || (open[f.comment] && tickets[n].AttentionOwner == f.owner) {
			continue
		}
		// Answered, replaced by somebody else's flag, or the ticket is
		// gone: forget it, and clear it where it still shows.
		delete(hs.flags, n)
		if t, ok := tickets[n]; ok && !open[f.comment] && t.AttentionOwner == f.owner {
			clears = append(clears, n)
		}
	}
	hs.mu.Unlock()

	for _, r := range reminders {
		e.notify(r.session, r.line)
	}
	for _, n := range clears {
		if err := e.b.SetAttention(ctx, n, ""); err != nil {
			e.log.Printf("engine: clear the question flag of #%d: %v", n, err)
		}
	}
	for _, f := range flags {
		if err := e.b.SetAttentionOwned(ctx, f.ticket, f.flag.owner, f.flag.text); err != nil {
			e.log.Printf("engine: flag question %d on #%d: %v", f.flag.comment, f.ticket, err)
			continue
		}
		hs.mu.Lock()
		hs.flags[f.ticket] = f.flag
		hs.mu.Unlock()
	}
}

// addressees returns the sessions of role on t whose agent the engine runs:
// the story's product manager, the working developer, the reviewers of the
// ticket (see ticketReviewers), the CEO.
func (e *Engine) addressees(role api.Role, t board.EngineTicket, sessions []board.EngineSession) []string {
	runs := func(s board.EngineSession) bool {
		_, ag, known := e.lookupAgent(s.ID)
		return known && ag != nil
	}
	if role == api.RoleReviewer {
		return ticketReviewers(sessions, t.ID, runs)
	}
	var out []string
	for _, s := range sessions {
		if s.Role != role {
			continue
		}
		ok := false
		switch role {
		case api.RolePM:
			story := t.StoryID
			if t.Type == api.TypeStory {
				story = t.ID
			}
			ok = story != 0 && s.Ticket == story
		case api.RoleDeveloper:
			ok = s.Ticket == t.ID && purposeOf(s) == api.PurposeWork
		case api.RoleCEO:
			ok = s.Ticket == 0
		}
		if ok && runs(s) {
			out = append(out, s.ID)
		}
	}
	return out
}

// roleWords names a role in a flag text.
func roleWords(r api.Role) string {
	switch r {
	case api.RoleCEO:
		return "CEO"
	case api.RolePM:
		return "product manager"
	}
	return string(r)
}

// shortDuration writes d in whole minutes when it is at least a minute
// ("16m", "1h5m"), else in whole seconds.
func shortDuration(d time.Duration) string {
	if d >= time.Minute {
		d = d.Truncate(time.Minute)
	} else {
		d = d.Truncate(time.Second)
	}
	text, _ := api.Duration(d).MarshalText()
	return string(text)
}

// ---- git environment ----

// gitEnvRetry is how long omo waits before it expands an agent
// environment again that failed (a command that failed or hung); each
// further failure of the same environment doubles the wait, up to
// gitEnvRetryMax. A changed environment is tried at once.
const (
	gitEnvRetry    = time.Minute
	gitEnvRetryMax = 30 * time.Minute
)

// gitEnvState is what syncGitEnvironment remembers. mu is held across
// reading the settings, expanding and handing the result to git, so a slow
// expansion of an older environment can never overwrite a newer one.
type gitEnvState struct {
	mu sync.Mutex
	// applied is the agent environment omo's git client got last; set is
	// false until one was applied.
	applied map[string]string
	set     bool
	// failed is the error last logged for the environment failedEnv, so a
	// failure that repeats is logged once until it changes; failedEnv is
	// not expanded again before retryAt, and wait is the pause after its
	// last failure.
	failed    string
	failedEnv map[string]string
	retryAt   time.Time
	wait      time.Duration

	// The background sync (see requestGitEnvironment): bg guards running
	// (a sync goroutine runs), again (a request came while it ran) and
	// cancel (ends its expansion); done counts the goroutine.
	bg      sync.Mutex
	running bool
	again   bool
	cancel  context.CancelFunc
	done    sync.WaitGroup
}

// syncGitEnvironment gives omo's own git client the agent environment of
// the general settings, so omo's merge commits carry the same author and
// committer as the agents' commits: at start, and again whenever that
// setting changed. An environment that cannot be expanded (a command that
// fails or hangs) leaves git with the environment it had and is tried
// again after a pause that grows with each failure (see gitEnvRetry); the
// failure is logged once until it changes. An expansion ends with ctx.
func (e *Engine) syncGitEnvironment(ctx context.Context) {
	s := &e.gitEnv
	s.mu.Lock()
	defer s.mu.Unlock()
	env := e.st.General().AgentEnv
	if s.set && maps.Equal(env, s.applied) {
		return
	}
	same := s.failedEnv != nil && maps.Equal(env, s.failedEnv)
	if same && e.now().Before(s.retryAt) {
		return
	}
	vars, err := e.expandEnv(ctx, env)
	if err != nil {
		if ctx.Err() != nil {
			return // omo stops; nothing failed
		}
		if msg := err.Error(); msg != s.failed || !same {
			s.failed = msg
			e.log.Printf("engine: the agent environment for git: %v; git keeps its previous environment and omo tries again later", err)
		}
		if same {
			s.wait = min(2*s.wait, gitEnvRetryMax)
		} else {
			s.failedEnv, s.wait = maps.Clone(env), gitEnvRetry
		}
		s.retryAt = e.now().Add(s.wait)
		return
	}
	e.setGitEnv(session.ProcessEnvironment(vars))
	s.applied, s.set = maps.Clone(env), true
	s.failed, s.failedEnv, s.wait, s.retryAt = "", nil, 0, time.Time{}
}

// requestGitEnvironment runs syncGitEnvironment in the background, so a
// command of the agent environment that hangs never holds up the caller
// (the engine's loop, the settings feed): at most one runs at a time, and
// a request while it runs makes it run once more afterwards. Nothing runs
// once the engine stops; stopGitEnvironment ends a running one.
func (e *Engine) requestGitEnvironment() {
	s := &e.gitEnv
	s.bg.Lock()
	defer s.bg.Unlock()
	// halt sets stopping before stopGitEnvironment takes bg: a request
	// either sees it or its goroutine is cancelled and waited for.
	if e.isStopping() {
		return
	}
	if s.running {
		s.again = true
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.running, s.cancel = true, cancel
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		defer cancel()
		for {
			e.syncGitEnvironment(ctx)
			s.bg.Lock()
			if !s.again || ctx.Err() != nil {
				s.running, s.again, s.cancel = false, false, nil
				s.bg.Unlock()
				return
			}
			s.again = false
			s.bg.Unlock()
		}
	}()
}

// stopGitEnvironment ends a background sync of the agent environment (its
// command is killed) and waits for it.
func (e *Engine) stopGitEnvironment() {
	s := &e.gitEnv
	s.bg.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.bg.Unlock()
	s.done.Wait()
}
