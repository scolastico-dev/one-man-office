// Package engine makes the board run itself. It starts an agent for every
// ticket that needs one, ends agents whose ticket no longer needs them,
// counts agents that keep crashing, and (in the files beside this one)
// merges finished work, delivers notifications and checks agent health.
//
// The engine is level-triggered: every pass reads one snapshot of the board,
// asks the pure planner what should happen and carries that out. Slow work
// (cutting branches and worktrees, starting a process, merging) runs in
// worker goroutines outside every board transaction; each worker claims its
// ticket first and always ends its claim. A pass is triggered by Wake (board
// changes, settings changes), by the end of a worker and by an agent process
// that ends; with nothing to do the engine is idle.
package engine

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/session"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/webterm"
)

// DefaultHealthInterval is how often the engine checks its agents' health
// and whether they acknowledged their start in time.
const DefaultHealthInterval = 30 * time.Second

// endTimeout bounds how long ending one agent waits for its process to go.
const endTimeout = 10 * time.Second

// Options configure an Engine.
type Options struct {
	Board    *board.Board
	Settings *settings.Store
	Git      *gitops.Git
	// Layout is the omo home. Worktrees live below Layout.Worktrees, the
	// working folders of tasks without a project below Layout.Workdirs,
	// transcripts below Layout.Logs; Layout.Run holds the records of
	// running agent processes (see agents.PTYRunner.RunDir).
	Layout globalhome.Layout
	Runner agents.Runner
	// Socket returns the endpoint agents reach omo on (OMO_SOCKET); nil
	// gives "".
	Socket func() string
	// Emit records an event that is not a board change (scheduler.changed,
	// settings.invalid); nil drops them.
	Emit func(kind string, data any) error
	// Log receives what goes wrong; nil discards.
	Log *log.Logger
	// Now is the clock of timeouts and health checks; nil is time.Now.
	Now func() time.Time
	// HealthInterval is the period of health checks; 0 is
	// DefaultHealthInterval.
	HealthInterval time.Duration
	// Executable is the omo executable whose folder is put in front of the
	// agents' PATH; "" is os.Executable().
	Executable string
	// Usage says how close models are to their usage limits; nil means
	// every model is unmetered.
	Usage UsageView
	// MCP returns the MCP settings: whether Claude Code and Codex agents
	// get omo's MCP bridge for their session. nil means on for both.
	MCP func() api.MCPSettings
}

// Engine runs agents for the board. Create it with New, then Run it.
type Engine struct {
	o      Options
	b      *board.Board
	st     *settings.Store
	git    *gitops.Git
	log    *log.Logger
	now    func() time.Time
	picker *picker
	reg    *agents.Registry
	wake   chan struct{}
	// pathPrefix is the folder of the omo executable.
	pathPrefix string
	// omoPath is the omo executable with its links resolved, whose `mcp
	// stdio` agents get as their MCP server; "" when unknown.
	omoPath string

	// passMu serializes passes.
	passMu sync.Mutex
	// workers counts start, merge and cleanup workers; watchers counts the
	// goroutines that watch agent processes.
	workers  sync.WaitGroup
	watchers sync.WaitGroup

	mu sync.Mutex
	// entries holds every agent the engine started and has not finished
	// ending, by session id; an entry without agent is still starting.
	entries map[string]*entry
	// reserved holds the names of agents being started.
	reserved map[string]bool
	// inflight counts the workers acting on a ticket.
	inflight map[int64]int
	// starts holds the agent starts workers are carrying out.
	starts map[*startJob]bool
	// failures counts consecutive unexpected ends per agent slot.
	failures map[failKey]*failRecord
	// tried remembers, per action, the newest event id of the snapshot it
	// was last tried on and when, so an action that changes nothing on the
	// board is not retried until the board changes or its back-off is over
	// (see firstTry and forgetTried).
	tried map[string]triedAt
	// fails counts, per start or merge action, the attempts in a row that
	// failed without a failed step (see settleAttempt).
	fails map[string]*attemptFailure
	// state is the scheduler state of the last pass; stateSent is set once
	// it was emitted.
	state     api.SchedulerState
	stateSent bool
	// stopping is set once the engine stops: nothing starts any more.
	stopping bool
	// halting is set once halt began in this run.
	halting bool
	running bool
	// halted is closed when the first halt of a run is over.
	halted chan struct{}
	// pause, when set, is called at named points of a start. Only tests
	// set it, to act in the window between two steps.
	pause func(point string)
	// minWait is the shortest wait timeout (MinWait; tests that let waits
	// time out lower it).
	minWait time.Duration
	// disk holds the worktree holds and notes of merges and cleanups.
	disk diskState
	// pins holds the commits handed in for review and the squash commits
	// of stories (see pins.go).
	pins *pinStore
	// exportMu serializes settling exports (see settleExport), which
	// passes and start workers both do.
	exportMu sync.Mutex
	// nt delivers notifications (see notify.go).
	nt *notifier
	// health holds the health checks' memory (see health.go); shut the
	// state of a graceful stop (see stop.go); usage what "Start anyway" and
	// usage stops remember (see usagestop.go), guarded by mu.
	health healthState
	shut   shutdownState
	usage  usageState
	// gitEnv is the agent environment handed to git (see
	// syncGitEnvironment); expandEnv and setGitEnv are its steps, which
	// tests replace.
	gitEnv    gitEnvState
	expandEnv func(context.Context, map[string]string) ([]string, error)
	setGitEnv func([]string)
}

// at calls the pause hook, if any, at point.
func (e *Engine) at(point string) {
	if e.pause != nil {
		e.pause(point)
	}
}

// New returns an engine. It starts nothing until Run.
func New(o Options) *Engine {
	e := &Engine{
		o:        o,
		b:        o.Board,
		st:       o.Settings,
		git:      o.Git,
		log:      o.Log,
		now:      o.Now,
		picker:   newPicker(nil),
		reg:      agents.NewRegistry(),
		wake:     make(chan struct{}, 1),
		entries:  map[string]*entry{},
		inflight: map[int64]int{},
		reserved: map[string]bool{},
		starts:   map[*startJob]bool{},
		failures: map[failKey]*failRecord{},
		tried:    map[string]triedAt{},
		fails:    map[string]*attemptFailure{},
		nt:       newNotifier(),
		minWait:  MinWait,
		pins:     &pinStore{},
	}
	if o.Layout.Run != "" {
		e.pins.path = filepath.Join(o.Layout.Run, pinsFile)
	}
	if e.log == nil {
		e.log = log.New(io.Discard, "", 0)
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.git == nil {
		e.git = gitops.New()
	}
	// git never looks for a repository in the agents' worktrees or working
	// folders: commands in a worktree run only through its resolution from
	// the project's records, and omo runs none in a working folder.
	e.git.SetAgentFolders(o.Layout.Worktrees, o.Layout.Workdirs)
	e.expandEnv = func(ctx context.Context, env map[string]string) ([]string, error) {
		return session.ExpandEnvironmentContext(ctx, 0, env)
	}
	e.setGitEnv = e.git.SetEnvironment
	exe := o.Executable
	if exe == "" {
		exe, _ = os.Executable()
	}
	if exe != "" {
		e.pathPrefix = filepath.Dir(exe)
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			e.omoPath = real
		}
	}
	if r, ok := o.Runner.(*agents.PTYRunner); ok && r.RunDir == "" && o.Layout.Run != "" {
		r.RunDir = o.Layout.Run
	}
	return e
}

// Wake asks for a pass soon. It never blocks; wakes that arrive while a pass
// is pending are coalesced.
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
	e.nt.poke()
}

// Run runs the engine until ctx ends: it ends what a previous run left
// behind, then passes whenever it is woken and checks agent health on its
// interval. When ctx ends it stops starting agents, waits for its workers,
// kills every agent and ends every session, then returns. A second Run while
// one is running returns at once, and so does every Run once Shutdown has
// begun: a graceful stop is final, even when it came before the run.
func (e *Engine) Run(ctx context.Context) {
	// Shutdown marks itself begun under shut.mu before it sets stopping, so
	// holding shut.mu while the run's state is reset orders the two: either
	// the reset comes first and Shutdown's stopping stays, or the run sees
	// the shutdown and does not start.
	e.shut.mu.Lock()
	e.mu.Lock()
	if e.running || e.shut.begun {
		e.mu.Unlock()
		e.shut.mu.Unlock()
		return
	}
	e.running = true
	e.stopping = false
	e.halting = false
	e.halted = make(chan struct{})
	e.mu.Unlock()
	e.shut.mu.Unlock()
	e.at("run-started")
	defer func() {
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
	}()

	bg := context.WithoutCancel(ctx)
	e.startup(bg)
	e.recoverAtStart(ctx)

	var side sync.WaitGroup
	side.Add(1)
	go func() {
		defer side.Done()
		e.runNotifier(ctx)
	}()

	interval := e.o.HealthInterval
	if interval <= 0 {
		interval = DefaultHealthInterval
	}
	health := time.NewTicker(interval)
	defer health.Stop()
	e.Wake()
	for {
		select {
		case <-ctx.Done():
			e.halt(bg, "omo stopped")
			side.Wait()
			return
		case <-e.wake:
			e.pass(ctx)
		case <-health.C:
			e.healthTick(ctx, e.now())
		}
	}
}

// startup prepares a run before the first pass: it ends agent processes a
// crashed run left behind, ends every session still live in the database
// (none of them has a process of this run) and releases every claim.
func (e *Engine) startup(ctx context.Context) {
	if e.o.Layout.Run != "" {
		if n, err := agents.EndLeftovers(e.o.Layout.Run, 0); err != nil {
			e.log.Printf("engine: ending agents of an earlier run: %v", err)
		} else if n > 0 {
			e.log.Printf("engine: ended %d agent process(es) left by an earlier run", n)
		}
	}
	snap, err := e.b.EngineSnapshot(ctx)
	if err != nil {
		e.log.Printf("engine: read the board at start: %v", err)
	} else {
		for _, s := range snap.Sessions {
			if e.hasEntry(s.ID) {
				continue
			}
			if err := e.b.EndSession(ctx, s.ID, "omo restarted"); err != nil {
				e.log.Printf("engine: end a session of an earlier run on #%d: %s", s.Ticket, scrub(err, s.ID))
			}
		}
	}
	if err := e.b.ReleaseAllClaims(ctx); err != nil {
		e.log.Printf("engine: release claims at start: %v", err)
	}
}

// healthTick runs the periodic checks and lets actions that changed nothing
// be tried again once their back-off is over.
func (e *Engine) healthTick(ctx context.Context, now time.Time) {
	e.forgetTried(now)
	e.checkStarting(ctx, now)
	e.checkHealth(ctx, now)
	e.checkExportsDue(ctx, now)
	e.startDeferredCEO(ctx)
	e.Wake()
}

// halt stops the engine: nothing starts any more, the workers finish, then
// every agent is killed and every live session ended with reason. It is safe
// to call more than once and concurrently; later calls wait for the first.
func (e *Engine) halt(ctx context.Context, reason string) {
	e.mu.Lock()
	first := !e.halting
	e.halting = true
	e.stopping = true
	halted := e.halted
	if halted == nil {
		halted = make(chan struct{})
		e.halted = halted
	}
	e.mu.Unlock()
	if !first {
		select {
		case <-halted:
		case <-ctx.Done():
		}
		return
	}
	defer close(halted)
	e.stopGitEnvironment()
	e.workers.Wait()
	var wg sync.WaitGroup
	for _, id := range e.entryIDs() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.end(ctx, id, reason)
		}()
	}
	wg.Wait()
	e.watchers.Wait()
	if snap, err := e.b.EngineSnapshot(ctx); err == nil {
		for _, s := range snap.Sessions {
			if err := e.b.EndSession(ctx, s.ID, reason); err != nil {
				e.log.Printf("engine: end a session of #%d: %s", s.Ticket, scrub(err, s.ID))
			}
		}
	}
}

// settle waits until no worker is running. Agents keep running.
func (e *Engine) settle() {
	e.workers.Wait()
}

// Scheduler returns the scheduler state of the last pass.
func (e *Engine) Scheduler() api.SchedulerState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// Terminal returns the web terminal of a session's running agent.
func (e *Engine) Terminal(sessionID string) (*webterm.Terminal, bool) {
	return e.reg.Terminal(sessionID)
}

// emit records an event through Options.Emit.
func (e *Engine) emit(kind string, data any) {
	if e.o.Emit == nil {
		return
	}
	if err := e.o.Emit(kind, data); err != nil {
		e.log.Printf("engine: emit %s: %v", kind, err)
	}
}

// pass reads the board, plans and carries out the plan: ends first and to
// completion, then attention changes, then merges, cleanups and starts as
// workers.
func (e *Engine) pass(ctx context.Context) {
	e.passMu.Lock()
	defer e.passMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	// What is in flight is copied before the board is read: a worker that
	// finishes in between is then still in flight for this pass, while its
	// changes are already on the board. The other order would plan its
	// work a second time. It is copied once more after the board is read
	// and both copies count: a start launched outside the pass (the CEO,
	// asked for by the user or a mention) in between has its job, and
	// maybe its session and process, only in the second copy, while its
	// session may already be on the board; without that copy the pass
	// would end it as a session without a process.
	type jobCopy struct {
		ticket  int64
		role    api.Role
		session string
	}
	inflight := map[int64]bool{}
	jobByStart := map[*startJob]jobCopy{}
	known := map[string]bool{}
	processes := map[string]bool{}
	copyInFlight := func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		for n := range e.inflight {
			inflight[n] = true
		}
		for job := range e.starts {
			// The later copy wins: it may know the session's id.
			jobByStart[job] = jobCopy{ticket: job.ticket, role: job.role, session: job.session}
		}
		for id, en := range e.entries {
			known[id] = true
			if en.agent != nil {
				processes[id] = true
			}
		}
		return e.stopping
	}
	stopping := copyInFlight()
	e.at("pass-copied")

	snap, err := e.b.EngineSnapshot(ctx)
	if err != nil {
		e.log.Printf("engine: read the board: %v", err)
		return
	}
	copyInFlight()
	e.pinHandIns(snap)
	if carried, settled := e.carryVerdicts(ctx, snap, inflight), e.settleExports(ctx, snap, inflight); carried || settled {
		// Approvals carried over into a review round, or an export
		// settled: plan on the board as it is now, so no reviewer or
		// export job starts for those projects. In-flight work is copied
		// once more for the same reason as above.
		if snap, err = e.b.EngineSnapshot(ctx); err != nil {
			e.log.Printf("engine: read the board: %v", err)
			return
		}
		copyInFlight()
	}
	jobs := make([]jobCopy, 0, len(jobByStart))
	for _, j := range jobByStart {
		jobs = append(jobs, j)
	}
	g := e.st.General()
	in := planInput{
		snap:       snap,
		limits:     g.Limits,
		stopping:   stopping,
		ceoEnabled: g.CEO.Enabled,
		inflight:   inflight,
		processes:  processes,
		available:  e.available,
		hasModel:   e.hasModel,
		usageStops: e.usageStops(snap),
		exportOver: e.exportSettled(snap),
	}
	// A job whose session was just created may not know its id yet; the
	// live session of its ticket and role that has no agent is its own.
	claimed := map[string]bool{}
	for _, job := range jobs {
		claimed[job.session] = true
	}
	for _, job := range jobs {
		id := job.session
		for _, s := range snap.Sessions {
			if id != "" {
				break
			}
			if s.Ticket == job.ticket && s.Role == job.role && !claimed[s.ID] && !known[s.ID] {
				id = s.ID
				claimed[id] = true
			}
		}
		in.starts = append(in.starts, pendingStart{ticket: job.ticket, role: job.role, session: id})
	}
	e.mu.Lock()
	e.resetFailures(snap)
	e.mu.Unlock()

	acts, state, blocked := plan(in)
	e.publish(state)
	e.writeStartBlocked(ctx, snap, blocked)

	var ends sync.WaitGroup
	for _, a := range acts {
		if a.kind != actEnd {
			continue
		}
		ends.Add(1)
		go func() {
			defer ends.Done()
			e.end(ctx, a.session, a.reason)
		}()
	}
	ends.Wait()

	work := context.WithoutCancel(ctx)
	// The usage stops of one ticket are carried out together, so the
	// ticket gets one comment about them.
	var stopOrder []int64
	stops := map[int64][]action{}
	for _, a := range acts {
		if a.kind != actUsageStop {
			continue
		}
		if _, ok := stops[a.ticket]; !ok {
			stopOrder = append(stopOrder, a.ticket)
		}
		stops[a.ticket] = append(stops[a.ticket], a)
	}
	for _, n := range stopOrder {
		e.usageStop(work, stops[n])
	}
	for _, a := range acts {
		switch a.kind {
		case actAttention:
			if err := e.b.SetAttention(ctx, a.ticket, a.reason); err != nil {
				e.log.Printf("engine: attention on #%d: %v", a.ticket, err)
			}
		case actMerge, actCleanup, actStart:
			if stopping || !e.firstTry(ctx, a, snap.LastEventID) {
				continue
			}
			e.launchAction(work, a, snap)
		}
	}
}

// launchAction runs a merge, cleanup or start in a worker. The outcome of a
// start or merge is settled by settleAttempt.
func (e *Engine) launchAction(ctx context.Context, a action, snap board.EngineSnapshot) {
	var t board.EngineTicket
	for _, x := range snap.Tickets {
		if x.ID == a.ticket {
			t = x
		}
	}
	switch a.kind {
	case actMerge:
		e.launch(ctx, a.ticket, nil, func(ctx context.Context) {
			e.attempt(ctx, a, func() error { return e.runMerge(ctx, t) })
		})
	case actCleanup:
		e.launch(ctx, a.ticket, nil, func(ctx context.Context) { e.cleanup(ctx, t) })
	case actStart:
		job := &startJob{action: a, ticket: a.ticket, role: a.role}
		e.launch(ctx, a.ticket, job, func(ctx context.Context) {
			e.attempt(ctx, a, func() error { return e.startWork(ctx, job) })
		})
	}
}

// launch runs fn in a worker that holds ticket in flight (and, for a start,
// the pending start job) until it returns. It reports false, running
// nothing, once the engine stops. A panic in fn is logged; the worker's own
// deferred clean-up has run by then.
func (e *Engine) launch(ctx context.Context, ticket int64, job *startJob, fn func(ctx context.Context)) bool {
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		return false
	}
	if ticket != 0 {
		e.inflight[ticket]++
	}
	if job != nil {
		e.starts[job] = true
	}
	e.workers.Add(1)
	e.mu.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				e.log.Printf("engine: worker for #%d panicked: %v\n%s", ticket, r, debug.Stack())
			}
			e.mu.Lock()
			if ticket != 0 {
				if e.inflight[ticket]--; e.inflight[ticket] <= 0 {
					delete(e.inflight, ticket)
				}
			}
			delete(e.starts, job)
			e.mu.Unlock()
			e.workers.Done()
			e.Wake()
		}()
		fn(ctx)
	}()
	return true
}

// publish stores the scheduler state and emits scheduler.changed when it
// differs from the last one emitted.
func (e *Engine) publish(state api.SchedulerState) {
	e.mu.Lock()
	changed := !e.stateSent || state != e.state
	e.state, e.stateSent = state, true
	e.mu.Unlock()
	if changed {
		e.emit(api.KindSchedulerChanged, api.SchedulerChangedData{State: state})
	}
}

// writeStartBlocked writes the start_blocked values that differ from the
// board's.
func (e *Engine) writeStartBlocked(ctx context.Context, snap board.EngineSnapshot, blocked map[int64]api.StartBlocked) {
	changes := map[int64]api.StartBlocked{}
	for _, t := range snap.Tickets {
		if want, ok := blocked[t.ID]; ok && want != t.StartBlocked {
			changes[t.ID] = want
		}
	}
	if len(changes) == 0 {
		return
	}
	if err := e.b.SetStartBlocked(ctx, changes); err != nil {
		e.log.Printf("engine: record why tickets have not started: %v", err)
	}
}

// available reports whether a project is registered, enabled and on disk.
func (e *Engine) available(project string) bool {
	p, ok := e.st.Project(project)
	return ok && p.Enabled && !p.Missing
}

// modelChoice builds the inputs of a model pick for an agent of role with
// purpose on t.
func (e *Engine) modelChoice(role api.Role, purpose api.Purpose, t board.EngineTicket, previous string, attempt int) modelChoice {
	r, _ := e.st.Role(string(role))
	c := modelChoice{role: r, attempt: attempt}
	if purpose != api.PurposeExport && role == workingRole(t) && role != api.RoleReviewer {
		c.ticketModel, c.previous = t.Model, previous
	}
	return c
}
