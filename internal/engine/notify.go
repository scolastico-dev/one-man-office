package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// linePrefix starts every notification an agent receives.
const linePrefix = "omo: "

// defaultGrace is how long a notification for a working agent waits before
// it is typed into the agent's terminal. An omo ai wait that arrives within
// it takes the line instead, so a line is not typed into a terminal whose
// agent is just about to park itself (where it would sit unread until the
// wait ends).
const defaultGrace = 2 * time.Second

// eventBatch is how many events the notifier reads at once.
const eventBatch = 500

// maxCEOPending bounds the notifications kept for a CEO that is not running.
const maxCEOPending = 50

// notifier holds what the engine knows about delivering notifications: its
// position in the event log and, per agent session, the lines not yet
// delivered and a parked wait.
type notifier struct {
	wake chan struct{}
	// grace is the delay before a line is typed (defaultGrace; tests set
	// it). Zero or less types at once.
	grace time.Duration

	// passMu serializes passes over the event log; cursor is the id of the
	// last event handled.
	passMu sync.Mutex
	cursor int64
	// lastComment and lastTargets remember the sessions the newest comment
	// reached, so the answer to a question that already reached its asker
	// through a mention does not reach it a second time.
	lastComment int64
	lastTargets map[string]bool

	mu sync.Mutex
	// boxes holds the delivery state of each agent session.
	boxes map[string]*mailbox
	// ceoPending holds notifications for a CEO that is not running yet;
	// the next CEO gets them with its omo ai ready answer.
	ceoPending []string
	// ceoOp is the CEO start in progress, if any.
	ceoOp *ceoOp
}

// mailbox is the delivery state of one agent session. Its fields are
// guarded by notifier.mu.
type mailbox struct {
	// queue holds lines not yet delivered: held until the agent runs omo
	// ai ready, or until the grace period ends.
	queue []string
	// timer types the queue when the grace period ends.
	timer *time.Timer
	// waiter is the parked omo ai wait of the session, if any.
	waiter *waiter
	// timeouts counts the consecutive waits that ended by timeout.
	timeouts int
	// stateMu orders the session state writes (it is not guarded by
	// notifier.mu).
	stateMu sync.Mutex
}

// Why a parked wait was released, besides its timeout and its caller.
const (
	waitOpen = iota
	waitDelivered
	waitReplaced
	waitEnded
)

// waiter is one parked omo ai wait.
type waiter struct {
	lines []string
	why   int
	// wake is closed when the wait is released by a delivery, a newer
	// wait or the end of its session.
	wake chan struct{}
}

func newNotifier() *notifier {
	return &notifier{wake: make(chan struct{}, 1), grace: defaultGrace, boxes: map[string]*mailbox{}}
}

// poke asks the notifier to read new events. It never blocks.
func (nt *notifier) poke() {
	select {
	case nt.wake <- struct{}{}:
	default:
	}
}

// box returns the mailbox of a session, creating it. The caller holds mu.
func (nt *notifier) box(id string) *mailbox {
	mb := nt.boxes[id]
	if mb == nil {
		mb = &mailbox{}
		nt.boxes[id] = mb
	}
	return mb
}

// drop forgets the mailbox of a session that ended and releases its wait.
// The caller holds mu.
func (nt *notifier) drop(id string) {
	mb := nt.boxes[id]
	if mb == nil {
		return
	}
	mb.takeQueue()
	if mb.waiter != nil {
		mb.waiter.release(waitEnded)
		mb.waiter = nil
	}
	delete(nt.boxes, id)
}

// release ends a parked wait with why. The caller holds notifier.mu.
func (w *waiter) release(why int) {
	if w.why != waitOpen {
		return
	}
	w.why = why
	close(w.wake)
}

// takeQueue empties a mailbox's queue and stops its timer. The caller holds
// notifier.mu.
func (mb *mailbox) takeQueue() []string {
	lines := mb.queue
	mb.queue = nil
	if mb.timer != nil {
		mb.timer.Stop()
		mb.timer = nil
	}
	return lines
}

// runNotifier delivers board events (mentions, questions, answers, moves,
// retries) to the agents they concern until ctx ends. It starts after the
// newest event: what happened before this run is in the thread every fresh
// agent is told to read. When ctx ends, every parked wait is released.
func (e *Engine) runNotifier(ctx context.Context) {
	defer e.dropMailboxes()
	for {
		id, err := e.b.LastEventID(ctx)
		if err == nil {
			e.nt.passMu.Lock()
			e.nt.cursor = id
			e.nt.passMu.Unlock()
			break
		}
		e.log.Printf("engine: read the newest event: %v", err)
		t := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.nt.wake:
			e.notifyPass(ctx)
		}
	}
}

// dropMailboxes releases every parked wait and stops every pending line.
func (e *Engine) dropMailboxes() {
	e.nt.mu.Lock()
	defer e.nt.mu.Unlock()
	for id := range e.nt.boxes {
		e.nt.drop(id)
	}
}

// notifyPass handles every event after the cursor, in order. Each event is
// handled once: the cursor moves past it whether or not a delivery worked.
func (e *Engine) notifyPass(ctx context.Context) {
	nt := e.nt
	nt.passMu.Lock()
	defer nt.passMu.Unlock()
	for ctx.Err() == nil {
		evs, err := e.b.EventsAfter(ctx, nt.cursor, eventBatch)
		if err != nil {
			e.log.Printf("engine: read events: %v", err)
			return
		}
		if len(evs) == 0 {
			return
		}
		// The snapshot is read after the events, so every session an event
		// of the batch created is in it (unless it ended since).
		snap, err := e.b.EngineSnapshot(ctx)
		if err != nil {
			e.log.Printf("engine: read the board for notifications: %v", err)
			return
		}
		w := e.newWorld(ctx, snap, evs)
		for _, ev := range evs {
			e.apply(ctx, route(ev, w))
			nt.cursor = ev.ID
		}
		if len(evs) < eventBatch {
			return
		}
	}
}

// ---- who learns what ----

// ticketInfo is what routing needs to know about a ticket.
type ticketInfo struct {
	id, story int64
	typ       api.TicketType
	column    api.Column
	round     int
}

// routeView is the board as routing sees it while handling one batch of events.
type routeView struct {
	sessions []board.EngineSession
	// tickets are the tickets of the snapshot (every ticket not done, and
	// some done ones).
	tickets map[int64]ticketInfo
	// lookup finds a ticket the snapshot does not have.
	lookup func(n int64) (ticketInfo, bool)
	// asker returns the session that asked question comment id ("" for
	// the user, the system or an unknown comment).
	asker func(id int64) string
	// bornAt and sessionBornAt give the event that created a ticket or a
	// session in this batch; doneAt the event that moved a ticket to Done.
	// Routing an event ignores what did not exist yet and counts as open
	// what was done only later.
	bornAt        map[int64]int64
	sessionBornAt map[string]int64
	doneAt        map[int64]int64
	// doneTasks maps each task moved to Done in this batch to its story;
	// the snapshot may no longer list it.
	doneTasks  map[int64]int64
	paused     bool
	ceoEnabled bool
}

// delivery is one line for one session.
type delivery struct {
	session, line string
	// answer is set on the line telling an asker that comment answer
	// answered its question.
	answer int64
}

// routing is what one event asks for.
type routing struct {
	deliveries []delivery
	// comment is the comment the deliveries are about (comment.created).
	comment int64
	// ceo holds lines for a CEO that is not running; startCEO asks for it
	// to be started for them.
	ceo      []string
	startCEO bool
	// restart is a ticket whose agents end so fresh ones start, for
	// restartReason.
	restart       int64
	restartReason string
	// ended is a session that ended.
	ended string
}

func (w routeView) ticket(n int64) (ticketInfo, bool) {
	if t, ok := w.tickets[n]; ok {
		return t, true
	}
	if w.lookup != nil {
		return w.lookup(n)
	}
	return ticketInfo{}, false
}

// existed reports whether session s existed at event id.
func (w routeView) existed(s board.EngineSession, id int64) bool {
	born, ok := w.sessionBornAt[s.ID]
	return !ok || born <= id
}

// sessionsOf returns the live sessions of role on ticket t that existed at
// event id: a story's product manager (for a task, its story's), the
// developer working on a task (never an export job), every reviewer of the
// ticket's current round, the CEO.
func (w routeView) sessionsOf(role api.Role, t ticketInfo, id int64) []string {
	var out []string
	for _, s := range w.sessions {
		if s.Role != role || !w.existed(s, id) {
			continue
		}
		ok := false
		switch role {
		case api.RolePM:
			story := t.story
			if t.typ == api.TypeStory {
				story = t.id
			}
			ok = story != 0 && s.Ticket == story
		case api.RoleDeveloper:
			ok = s.Ticket == t.id && purposeOf(s) == api.PurposeWork
		case api.RoleReviewer:
			ok = s.Ticket == t.id && s.ReviewRound == t.round
		case api.RoleCEO:
			ok = s.Ticket == 0
		}
		if ok {
			out = append(out, s.ID)
		}
	}
	return out
}

// liveSession reports whether session id is live.
func (w routeView) liveSession(id string) bool {
	for _, s := range w.sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}

// openTasks counts the tasks of story s other than except that were open
// at event id.
func (w routeView) openTasks(s, except, id int64) int {
	n := 0
	open := func(t int64, column api.Column) {
		if born, ok := w.bornAt[t]; ok && born > id {
			return
		}
		done, ok := w.doneAt[t]
		if column != api.ColumnDone || (ok && done > id) {
			n++
		}
	}
	for _, t := range w.tickets {
		if t.story == s && t.typ == api.TypeTask && t.id != except {
			open(t.id, t.column)
		}
	}
	for t, story := range w.doneTasks {
		if _, listed := w.tickets[t]; !listed && story == s && t != except {
			open(t, api.ColumnDone)
		}
	}
	return n
}

// route decides who learns of ev, and how. The author of an event never
// learns of it. Ticket titles and comment bodies are never part of a line:
// a line says what happened and which command shows it.
func route(ev api.Event, w routeView) routing {
	var r routing
	skip := ev.Actor.Session
	add := func(ids []string, line string, answer int64) {
		for _, id := range ids {
			if id != "" && id != skip {
				r.deliveries = append(r.deliveries, delivery{session: id, line: line, answer: answer})
			}
		}
	}
	switch ev.Kind {
	case api.KindCommentCreated:
		var d api.CommentCreatedData
		if json.Unmarshal(ev.Data, &d) != nil {
			return r
		}
		c := d.Comment
		r.comment = int64(c.ID)
		if c.Author.Session != "" {
			skip = c.Author.Session
		}
		t, ok := w.ticket(int64(c.Ticket))
		if !ok {
			return r
		}
		author := c.Author.Name
		if author == "" {
			author = string(c.Author.Role)
		}
		seen := map[api.Role]bool{}
		for _, m := range c.Mentions {
			if seen[m.Role] {
				continue
			}
			seen[m.Role] = true
			line := fmt.Sprintf("%s#%d comment %d by %s mentions @%s: omo ai comments %d --since %d",
				linePrefix, c.Ticket, c.ID, author, m.Role, c.Ticket, c.ID-1)
			if c.QuestionTo == m.Role {
				line = fmt.Sprintf("%s#%d question %d for you from %s: omo ai comments %d --questions",
					linePrefix, c.Ticket, c.ID, author, c.Ticket)
			}
			switch m.Role {
			case api.RolePM, api.RoleDeveloper, api.RoleReviewer:
				add(w.sessionsOf(m.Role, t, ev.ID), line, 0)
			case api.RoleCEO:
				if c.Author.Role == api.RoleCEO {
					continue
				}
				if ids := w.sessionsOf(api.RoleCEO, t, ev.ID); len(ids) > 0 {
					add(ids, line, 0)
				} else if w.ceoEnabled {
					r.ceo = append(r.ceo, line)
					r.startCEO = !w.paused
				}
			}
		}
	case api.KindQuestionAnswered:
		var d api.QuestionAnsweredData
		if json.Unmarshal(ev.Data, &d) != nil || w.asker == nil {
			return r
		}
		asker := w.asker(int64(d.Question))
		if asker == "" || !w.liveSession(asker) {
			return r
		}
		add([]string{asker}, fmt.Sprintf("%syour question %d on #%d was answered (comment %d): omo ai comments %d --since %d",
			linePrefix, d.Question, d.Ticket, d.Answer, d.Ticket, d.Answer-1), int64(d.Answer))
	case api.KindTicketMoved:
		var d api.TicketMovedData
		if json.Unmarshal(ev.Data, &d) != nil {
			return r
		}
		n := int64(d.Ticket.Number)
		supervisor := d.Ticket.Supervisor
		if supervisor == api.RoleCEO && !w.ceoEnabled {
			supervisor = api.RoleUser
		}
		ceo := w.sessionsOf(api.RoleCEO, ticketInfo{}, ev.ID)
		if d.To == api.ColumnDone {
			if d.Ticket.Story != nil && d.Ticket.Type == api.TypeTask {
				s := int64(*d.Ticket.Story)
				line := fmt.Sprintf("%stask #%d of story #%d is done (%s).", linePrefix, n, s, d.Ticket.Resolution)
				if w.openTasks(s, n, ev.ID) == 0 {
					line += fmt.Sprintf(" Every task of story #%d is done or cancelled; when the story is complete, run omo ai done.", s)
				}
				add(w.sessionsOf(api.RolePM, ticketInfo{id: n, story: s, typ: api.TypeTask}, ev.ID), line, 0)
			}
			if supervisor == api.RoleCEO {
				add(ceo, fmt.Sprintf("%s#%d is done (%s).", linePrefix, n, d.Ticket.Resolution), 0)
			}
		}
		if d.Action == board.ActionReject && supervisor == api.RoleCEO {
			add(ceo, fmt.Sprintf("%sthe review of #%d was rejected.", linePrefix, n), 0)
		}
	case api.KindTicketRetried:
		var d api.TicketRetriedData
		if json.Unmarshal(ev.Data, &d) != nil || d.Step != api.FailedAgent {
			return r
		}
		r.restart = int64(d.Ticket)
		r.restartReason = "restarted by " + string(ev.Actor.Role)
	case api.KindAgentEnded:
		var d api.AgentData
		if json.Unmarshal(ev.Data, &d) != nil {
			return r
		}
		r.ended = d.Agent.ID
	}
	return r
}

// newWorld builds the routing view of one batch of events.
func (e *Engine) newWorld(ctx context.Context, snap board.EngineSnapshot, evs []api.Event) routeView {
	w := routeView{
		sessions:      snap.Sessions,
		tickets:       make(map[int64]ticketInfo, len(snap.Tickets)),
		bornAt:        map[int64]int64{},
		sessionBornAt: map[string]int64{},
		doneAt:        map[int64]int64{},
		doneTasks:     map[int64]int64{},
		paused:        snap.Paused,
		ceoEnabled:    e.st.General().CEO.Enabled,
	}
	for _, t := range snap.Tickets {
		w.tickets[t.ID] = ticketInfo{id: t.ID, story: t.StoryID, typ: t.Type, column: t.Column, round: t.ReviewRound}
	}
	for _, ev := range evs {
		switch ev.Kind {
		case api.KindTicketCreated:
			if ev.Ticket != nil {
				w.bornAt[int64(*ev.Ticket)] = ev.ID
			}
		case api.KindAgentCreated:
			var d api.AgentData
			if json.Unmarshal(ev.Data, &d) == nil {
				w.sessionBornAt[d.Agent.ID] = ev.ID
			}
		case api.KindTicketMoved:
			var d api.TicketMovedData
			if json.Unmarshal(ev.Data, &d) == nil && d.To == api.ColumnDone {
				w.doneAt[int64(d.Ticket.Number)] = ev.ID
				if d.Ticket.Type == api.TypeTask && d.Ticket.Story != nil {
					w.doneTasks[int64(d.Ticket.Number)] = int64(*d.Ticket.Story)
				}
			}
		}
	}
	w.lookup = func(n int64) (ticketInfo, bool) {
		t, err := e.b.Get(ctx, board.User, n)
		if err != nil {
			return ticketInfo{}, false
		}
		info := ticketInfo{id: n, typ: t.Type, column: t.Column, round: t.ReviewRound}
		if t.Story != nil {
			info.story = int64(*t.Story)
		}
		return info, true
	}
	w.asker = func(id int64) string {
		c, err := e.b.CommentByID(ctx, id)
		if err != nil {
			return ""
		}
		return c.Author.Session
	}
	return w
}

// apply carries out what route decided for one event.
func (e *Engine) apply(ctx context.Context, r routing) {
	nt := e.nt
	if r.comment != 0 {
		nt.lastComment, nt.lastTargets = r.comment, map[string]bool{}
	}
	for _, d := range r.deliveries {
		if d.answer != 0 && d.answer == nt.lastComment && nt.lastTargets[d.session] {
			continue // the answer itself already reached the asker
		}
		if r.comment != 0 {
			nt.lastTargets[d.session] = true
		}
		e.deliver(d.session, d.line, true)
	}
	if len(r.ceo) > 0 {
		nt.mu.Lock()
		nt.ceoPending = append(nt.ceoPending, r.ceo...)
		if over := len(nt.ceoPending) - maxCEOPending; over > 0 {
			nt.ceoPending = append([]string(nil), nt.ceoPending[over:]...)
		}
		nt.mu.Unlock()
		if r.startCEO {
			e.ceoBegin(false)
		}
	}
	if r.restart != 0 {
		e.restartAgents(ctx, r.restart, r.restartReason)
	}
	if r.ended != "" {
		nt.mu.Lock()
		nt.drop(r.ended)
		nt.mu.Unlock()
	}
}

// restartAgents ends every live agent of ticket n after a retry of its
// agent step, so the next pass starts fresh ones with a clean failure
// count.
func (e *Engine) restartAgents(ctx context.Context, n int64, reason string) {
	e.resetFailuresOf(n)
	e.launch(context.WithoutCancel(ctx), n, nil, func(ctx context.Context) {
		snap, err := e.b.EngineSnapshot(ctx)
		if err != nil {
			e.log.Printf("engine: read the board to restart the agents of #%d: %v", n, err)
			return
		}
		var wg sync.WaitGroup
		for _, s := range snap.Sessions {
			if s.Ticket != n {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				e.end(ctx, s.ID, reason)
			}()
		}
		wg.Wait()
		// An agent killed here is not a crash; forget anything counted
		// while it ended.
		e.resetFailuresOf(n)
	})
}

// ---- delivery ----

// notify delivers one notification line to a session the engine runs: a
// parked wait returns with it, an agent that has not run omo ai ready gets
// it with its prompt, and a working agent gets it typed into its terminal
// after a short grace period (in which a wait takes it instead). A session
// the engine does not run gets nothing. The line should start with "omo: ";
// it is added when missing.
func (e *Engine) notify(sessionID, line string) {
	e.deliver(sessionID, line, false)
}

// deliver is notify; live says the session is known to be live on the
// board, so a line for a session whose agent is still being set up is kept
// for its omo ai ready.
func (e *Engine) deliver(id, line string, live bool) {
	if !strings.HasPrefix(line, linePrefix) {
		line = linePrefix + line
	}
	en, known := e.lookup(id)
	if !known && !live {
		return
	}
	nt := e.nt
	nt.mu.Lock()
	mb := nt.box(id)
	mb.timeouts = 0
	if w := mb.waiter; w != nil {
		w.lines = append(w.lines, line)
		w.release(waitDelivered)
		mb.waiter = nil
		nt.mu.Unlock()
		return
	}
	mb.queue = append(mb.queue, line)
	if !known || en.agent == nil || en.awaitingReady() {
		nt.mu.Unlock() // kept for omo ai ready
		return
	}
	if nt.grace <= 0 {
		lines := mb.takeQueue()
		nt.mu.Unlock()
		typeLines(en, lines)
		return
	}
	if mb.timer == nil {
		mb.timer = time.AfterFunc(nt.grace, func() { e.flush(id) })
	}
	nt.mu.Unlock()
}

// flush types the lines of a session whose grace period ended.
func (e *Engine) flush(id string) {
	en, known := e.lookup(id)
	nt := e.nt
	nt.mu.Lock()
	mb := nt.boxes[id]
	if mb == nil {
		nt.mu.Unlock()
		return
	}
	mb.timer = nil
	if !known || en.agent == nil || en.awaitingReady() {
		nt.mu.Unlock()
		return
	}
	lines := mb.takeQueue()
	nt.mu.Unlock()
	typeLines(en, lines)
}

// typeLines types lines into an agent's terminal; agents.Notify adds the
// prefix itself.
func typeLines(en *entry, lines []string) {
	for _, l := range lines {
		en.agent.Notify(strings.TrimPrefix(l, linePrefix))
	}
}

// syncState writes the session state that matches its mailbox: waiting
// while a wait is parked, running otherwise.
func (e *Engine) syncState(ctx context.Context, id string, mb *mailbox) {
	mb.stateMu.Lock()
	defer mb.stateMu.Unlock()
	e.nt.mu.Lock()
	st := api.SessionRunning
	if mb.waiter != nil {
		st = api.SessionWaiting
	}
	e.nt.mu.Unlock()
	if err := e.b.SetSessionState(context.WithoutCancel(ctx), id, st); err != nil &&
		!isCode(err, api.CodeConflict) && !isCode(err, api.CodeNotFound) {
		e.log.Printf("engine: set a session %s: %s", st, scrub(err, id))
	}
}

// isCode reports whether err is an api error with code.
func isCode(err error, code string) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == code
}
