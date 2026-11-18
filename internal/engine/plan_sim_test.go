package engine

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// TestPlanNeverDeadlocksRandomWorlds drives random boards to the end with a
// simulated engine, simulated agents and a simulated user, and checks the
// planner's promises on every pass. OMO_SIM_WORLDS sets the number of seeds
// (default 2000, each run with the limits 1, 2 and 3); OMO_SIM_SEED replays
// one seed.
func TestPlanNeverDeadlocksRandomWorlds(t *testing.T) {
	worlds := 2000
	if v := os.Getenv("OMO_SIM_WORLDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("OMO_SIM_WORLDS: %v", err)
		}
		worlds = n
	}
	first, last := uint64(1), uint64(worlds)
	if v := os.Getenv("OMO_SIM_SEED"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			t.Fatalf("OMO_SIM_SEED: %v", err)
		}
		first, last = n, n
	}
	type job struct {
		seed  uint64
		limit int
	}
	jobs := make(chan job)
	type result struct {
		ticks int
		err   string
		seed  uint64
		limit int
	}
	results := make(chan result)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for j := range jobs {
				w := newSim(j.seed, j.limit)
				n := w.run()
				results <- result{ticks: n, err: w.err, seed: j.seed, limit: j.limit}
			}
		})
	}
	go func() {
		for seed := first; seed <= last; seed++ {
			for limit := 1; limit <= 3; limit++ {
				jobs <- job{seed, limit}
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	ticks, worldsRun := 0, 0
	var failures []result
	for r := range results {
		ticks += r.ticks
		worldsRun++
		if r.err != "" {
			failures = append(failures, r)
		}
	}
	if len(failures) > 0 {
		sort.Slice(failures, func(i, j int) bool {
			if failures[i].seed != failures[j].seed {
				return failures[i].seed < failures[j].seed
			}
			return failures[i].limit < failures[j].limit
		})
		t.Errorf("%d of %d worlds failed; the first:\n%s", len(failures), worldsRun, failures[0].err)
		return
	}
	t.Logf("%d worlds, %d ticks", worldsRun, ticks)
}

const simMaxTicks = 5000

type simTicket struct {
	t          board.EngineTicket
	hidden     bool // a task its pm has not written yet
	deps       []int64
	rejections int
	tasksOut   bool // story: the pm wrote its tasks
	needFix    bool // story: the pm owes a fix task
	exportFail bool // the last export failed; the user merges locally
	mergeAt    int  // exported: when the user merges the pull request
	userAt     int  // when the user gets to a state that needs them
	// commits is the commit of the ticket's branch per project, counted
	// up with every change (0: no commits of its own); pins holds them as
	// handed in for the current review round; approved what the engine
	// recorded about approvals (see carry.go).
	commits  map[string]int
	pins     map[string]string
	approved map[string]approval
	// exportLeft holds the projects left out of the exports of a review
	// round (by round), exportFailed those whose current export failed.
	exportLeft   map[string]int
	exportFailed map[string]bool
}

type simSession struct {
	s       board.EngineSession
	process bool
	ended   bool
	// stopping is set while a usage stop waits for the agent's handoff;
	// the session ends at stopAt.
	stopping bool
	stopAt   int
	since    int // tick the session was created
	stalled  int // tick the agent stopped making progress; 0 = it works
}

type simWorker struct {
	a       action
	due     int
	step    int
	session string
	round   int
}

type sim struct {
	err         string
	seed        uint64
	limit       int
	rng         *rand.Rand
	tick        int
	tickets     map[int64]*simTicket
	sessions    []*simSession
	workers     []*simWorker
	paused      bool
	pauseUntil  int
	ceoEnabled  bool
	noModel     map[api.Role]int
	unavailable map[string]int
	failures    map[string]int
	// Models: the catalog, each role's models and assignment, the usage
	// state of each model (flipped at random until usageUntil, then all
	// ok), the tickets the user let start anyway (with their column) and
	// the picker the simulated workers pick models with.
	catalog     fakeCatalog
	roles       map[api.Role]settings.Role
	usage       map[string]string
	usageUntil  int
	forced      map[int64]api.Column
	picker      *picker
	nextTicket  int64
	nextSession int
	nextRank    float64
	log         []string
}

func newSim(seed uint64, limit int) *sim {
	w := &sim{
		seed: seed, limit: limit, rng: rand.New(rand.NewPCG(seed, uint64(limit))),
		tickets: map[int64]*simTicket{}, ceoEnabled: true, noModel: map[api.Role]int{},
		unavailable: map[string]int{}, failures: map[string]int{},
		catalog: fakeCatalog{"m/a": true, "m/b": true, "m/c": true}, roles: map[api.Role]settings.Role{},
		usage: map[string]string{}, forced: map[int64]api.Column{},
	}
	w.picker = newPicker(func(n int) int { return w.rng.IntN(n) })
	w.generate()
	return w
}

func (w *sim) p(prob float64) bool { return w.rng.Float64() < prob }

func (w *sim) logf(format string, args ...any) {
	w.log = append(w.log, fmt.Sprintf("t%d: ", w.tick)+fmt.Sprintf(format, args...))
	if len(w.log) > 60 {
		w.log = w.log[len(w.log)-60:]
	}
}

func (w *sim) rank() float64 { w.nextRank++; return w.nextRank }

func (w *sim) review() api.Review {
	return []api.Review{api.ReviewSkipped, api.ReviewAgent, api.ReviewAgentThenHuman, api.ReviewAgent, api.ReviewHuman}[w.rng.IntN(5)]
}

func (w *sim) newTicket(typ api.TicketType, story int64, projects []string) *simTicket {
	w.nextTicket++
	st := &simTicket{t: board.EngineTicket{
		ID: w.nextTicket, Type: typ, Title: fmt.Sprintf("t%d", w.nextTicket), Column: api.ColumnReady, Rank: w.rank(),
		StoryID: story, Review: w.review(), WhenApproved: api.WhenApprovedMerge, Verdicts: map[string]string{},
	}, commits: map[string]int{}, approved: map[string]approval{}, exportLeft: map[string]int{}, exportFailed: map[string]bool{}}
	if story == 0 && w.p(0.3) {
		st.t.WhenApproved = api.WhenApprovedExport // its effective value: also on a task without a project
	}
	for _, p := range projects {
		st.t.Projects = append(st.t.Projects, board.EngineProject{ID: p, Base: "main"})
	}
	w.tickets[st.t.ID] = st
	return st
}

// generate builds 1-3 stories with 1-4 tasks each (dependencies inside the
// story), dependencies between stories, and 0-3 standalone tasks that
// depend on earlier standalone tasks or stories. Everything is approved.
func (w *sim) generate() {
	var stories, standalone []int64
	for range 1 + w.rng.IntN(3) {
		projects := []string{"a"}
		if w.p(0.4) {
			projects = append(projects, "b")
		}
		s := w.newTicket(api.TypeStory, 0, projects)
		if w.p(0.3) {
			s.t.MaxParallel = 1 + w.rng.IntN(2)
		}
		for _, other := range stories {
			if w.p(0.3) {
				s.deps = append(s.deps, other)
			}
		}
		var tasks []int64
		for range 1 + w.rng.IntN(4) {
			ps := projects[:1+w.rng.IntN(len(projects))]
			if w.p(0.2) {
				ps = nil // a task without a project
			}
			task := w.newTicket(api.TypeTask, s.t.ID, ps)
			task.hidden = true
			for _, other := range tasks {
				if w.p(0.35) {
					task.deps = append(task.deps, other)
				}
			}
			tasks = append(tasks, task.t.ID)
		}
		stories = append(stories, s.t.ID)
	}
	for range w.rng.IntN(4) {
		ps := []string{[]string{"a", "b"}[w.rng.IntN(2)]}
		if w.p(0.25) {
			ps = nil // a task without a project
		}
		task := w.newTicket(api.TypeTask, 0, ps)
		for _, other := range append(slices.Clone(standalone), stories...) {
			if w.p(0.3) {
				task.deps = append(task.deps, other)
			}
		}
		standalone = append(standalone, task.t.ID)
	}
	if w.p(0.25) {
		r := []api.Role{api.RolePM, api.RoleDeveloper, api.RoleReviewer}[w.rng.IntN(3)]
		w.noModel[r] = 10 + w.rng.IntN(150)
	}
	if w.p(0.15) {
		w.unavailable["b"] = 10 + w.rng.IntN(150)
	}
	modes := []string{api.AssignmentRoundRobin, api.AssignmentRandom, api.AssignmentFailover, api.AssignmentSmart}
	for _, r := range []api.Role{api.RoleCEO, api.RolePM, api.RoleDeveloper, api.RoleReviewer} {
		var models []string
		for range 1 + w.rng.IntN(3) {
			models = append(models, []string{"m/a", "m/b", "m/c"}[w.rng.IntN(3)])
		}
		w.roles[r] = settings.Role{Name: string(r), Models: models, Assignment: modes[w.rng.IntN(len(modes))]}
	}
	for _, st := range w.tickets {
		if st.t.Type == api.TypeTask && w.p(0.15) {
			st.t.Model = []string{"m/a", "m/b"}[w.rng.IntN(2)]
		}
	}
	if w.p(0.6) {
		w.usageUntil = 50 + w.rng.IntN(500)
	}
	if w.p(0.3) {
		w.newSession(api.RoleCEO, 0, "", api.PurposeWork, 0, api.SessionRunning, true)
	}
}

// use is the simulated usage view.
func (w *sim) use(id string) api.ModelUsage {
	state := w.usage[id]
	if state == "" {
		state = api.UsageOK
	}
	u := api.ModelUsage{Model: id, State: state}
	if state == api.UsageOK || state == api.UsageSoft || state == api.UsageHard {
		pct := map[string]float64{api.UsageOK: 10, api.UsageSoft: 92, api.UsageHard: 99}[state]
		if state == api.UsageOK {
			pct += float64(len(id)+int(id[len(id)-1])) / 10 // differs per model, for smart
		}
		u.UsedPercent, u.Window = &pct, "session"
	}
	return u
}

// flipUsage changes the usage of a model now and then until usageUntil,
// then puts every model back below its limits.
func (w *sim) flipUsage() {
	if w.tick >= w.usageUntil {
		if len(w.usage) > 0 {
			w.usage = map[string]string{}
			w.logf("usage back to ok")
		}
		return
	}
	if w.p(0.05) {
		m := []string{"m/a", "m/b", "m/c"}[w.rng.IntN(3)]
		states := []string{api.UsageOK, api.UsageSoft, api.UsageHard, api.UsageUnknown, api.UsageUnmetered}
		w.usage[m] = states[w.rng.IntN(len(states))]
		w.logf("usage of %s is %s", m, w.usage[m])
	}
}

// choice builds the model choice of an agent like the engine does.
func (w *sim) choice(r api.Role, purpose api.Purpose, t board.EngineTicket) modelChoice {
	c := modelChoice{role: w.roles[r]}
	if purpose != api.PurposeExport && r == workingRoleOf(t) && r != api.RoleReviewer {
		c.ticketModel = t.Model
		col, ok := w.forced[t.ID]
		c.forced = c.ticketModel != "" && ok && col == t.Column
	}
	return c
}

// check is the simulated planInput.hasModel.
func (w *sim) check(r api.Role, purpose api.Purpose, t board.EngineTicket) startCheck {
	if w.tick < w.noModel[r] {
		return startNoModel
	}
	return canStart(w.choice(r, purpose, t), w.catalog, w.use)
}

func (w *sim) ids() []int64 {
	ids := make([]int64, 0, len(w.tickets))
	for id := range w.tickets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (w *sim) live() []*simSession {
	var out []*simSession
	for _, s := range w.sessions {
		if !s.ended {
			out = append(out, s)
		}
	}
	return out
}

func (w *sim) session(id string) *simSession {
	for _, s := range w.sessions {
		if s.s.ID == id {
			return s
		}
	}
	return nil
}

func (w *sim) newSession(role api.Role, ticket int64, project string, purpose api.Purpose, round int,
	st api.SessionState, process bool) *simSession {
	w.nextSession++
	s := &simSession{s: board.EngineSession{
		ID: fmt.Sprintf("s%d", w.nextSession), Role: role, Ticket: ticket, Project: project, Purpose: purpose,
		ReviewRound: round, State: st,
	}, process: process, since: w.tick}
	w.sessions = append(w.sessions, s)
	return s
}

// waitingFor computes the dependencies that still block a ticket, like the
// board does.
func (w *sim) waitingFor(st *simTicket) []api.Waiting {
	out := []api.Waiting{}
	for _, d := range st.deps {
		o := w.tickets[d].t
		switch {
		case o.Resolution == api.ResolutionCompleted:
		case o.Resolution == api.ResolutionExported && o.ExportMerged:
		case o.ReviewStage == api.ReviewStageAwaitingMerge || o.Resolution == api.ResolutionExported:
			out = append(out, api.Waiting{Ticket: int(d), Reason: api.WaitingExportNotMerged})
		case o.Resolution == api.ResolutionCancelled:
			out = append(out, api.Waiting{Ticket: int(d), Reason: api.WaitingCancelled})
		default:
			out = append(out, api.Waiting{Ticket: int(d), Reason: api.WaitingNotDone})
		}
	}
	return out
}

func hasWorktree(t board.EngineTicket) bool {
	return slices.ContainsFunc(t.Projects, func(p board.EngineProject) bool { return p.Worktree != "" })
}

func (w *sim) snapshot() board.EngineSnapshot {
	snap := board.EngineSnapshot{Paused: w.paused}
	for _, id := range w.ids() {
		st := w.tickets[id]
		t := st.t
		if st.hidden || (t.Column == api.ColumnDone && !hasWorktree(t) && !(t.Resolution == api.ResolutionExported && !t.ExportMerged)) {
			continue
		}
		t.DependsOn = slices.Clone(st.deps)
		if t.DependsOn == nil {
			t.DependsOn = []int64{}
		}
		t.WaitingFor = w.waitingFor(st)
		t.Waiting = len(t.WaitingFor) > 0
		t.Projects = slices.Clone(t.Projects)
		t.Verdicts = map[string]string{}
		for k, v := range st.t.Verdicts {
			t.Verdicts[k] = v
		}
		snap.Tickets = append(snap.Tickets, t)
	}
	for _, s := range w.live() {
		snap.Sessions = append(snap.Sessions, s.s)
	}
	return snap
}

func (w *sim) input(snap board.EngineSnapshot) planInput {
	in := planInput{snap: snap, limits: api.LimitSettings{Tasks: w.limit, Stories: w.limit}, ceoEnabled: w.ceoEnabled, inflight: map[int64]bool{},
		processes: map[string]bool{}}
	for _, k := range w.workers {
		if k.a.ticket != 0 {
			in.inflight[k.a.ticket] = true
		}
		if k.a.kind == actStart {
			in.starts = append(in.starts, pendingStart{ticket: k.a.ticket, role: k.a.role, session: k.session})
		}
	}
	for _, s := range w.live() {
		if s.process {
			in.processes[s.s.ID] = true
		}
	}
	in.exportOver = map[int64]map[string]bool{}
	for _, t := range snap.Tickets {
		if t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageExporting {
			for _, p := range t.Projects {
				if w.exportOver(w.tickets[t.ID], p.ID) {
					if in.exportOver[t.ID] == nil {
						in.exportOver[t.ID] = map[string]bool{}
					}
					in.exportOver[t.ID][p.ID] = true
				}
			}
		}
	}
	tick := w.tick
	in.available = func(p string) bool { return tick >= w.unavailable[p] }
	in.hasModel = w.check
	for _, s := range w.live() {
		if s.process && !s.stopping && s.s.Model != "" && w.use(s.s.Model).State == api.UsageHard {
			in.usageStops = append(in.usageStops, s.s.ID)
		}
	}
	return in
}

// exportOver reports whether the export of project of st is over without
// a pull request: left out or failed.
func (w *sim) exportOver(st *simTicket, project string) bool {
	r, left := st.exportLeft[project]
	return (left && r == st.t.ReviewRound) || st.exportFailed[project]
}

// simFailure stops a world at its first failure.
type simFailure struct{}

func (w *sim) fail(format string, args ...any) {
	var b strings.Builder
	fmt.Fprintf(&b, "seed %d limit %d tick %d (replay: OMO_SIM_SEED=%d go test ./internal/engine -run RandomWorlds): %s\n",
		w.seed, w.limit, w.tick, w.seed, fmt.Sprintf(format, args...))
	fmt.Fprintf(&b, "paused %v ceo %v noModel %v unavailable %v usage %v until %d forced %v roles %v\n", w.paused, w.ceoEnabled,
		w.noModel, w.unavailable, w.usage, w.usageUntil, w.forced, w.roles)
	for _, id := range w.ids() {
		st := w.tickets[id]
		t := st.t
		fmt.Fprintf(&b, "  #%d %s story=%d col=%s res=%s stage=%s round=%d busy=%v failed=%q attn=%q blocked=%q deps=%v waiting=%v hidden=%v verdicts=%v review=%s maxpar=%d projects=%v\n",
			t.ID, t.Type, t.StoryID, t.Column, t.Resolution, t.ReviewStage, t.ReviewRound, t.Busy, t.FailedStep, t.Attention,
			t.StartBlocked, st.deps, w.waitingFor(st), st.hidden, t.Verdicts, t.Review, t.MaxParallel, t.Projects)
	}
	for _, s := range w.live() {
		fmt.Fprintf(&b, "  session %s %s #%d project=%q purpose=%s round=%d state=%s process=%v model=%s stopping=%v\n",
			s.s.ID, s.s.Role, s.s.Ticket, s.s.Project, s.s.Purpose, s.s.ReviewRound, s.s.State, s.process, s.s.Model, s.stopping)
	}
	for _, k := range w.workers {
		fmt.Fprintf(&b, "  worker %v due=%d step=%d session=%s\n", k.a, k.due, k.step, k.session)
	}
	b.WriteString("  recent:\n")
	for _, l := range w.log {
		b.WriteString("    " + l + "\n")
	}
	w.err = b.String()
	panic(simFailure{})
}

// run simulates until every ticket is done or the first failure, and
// returns the ticks it took.
func (w *sim) run() (ticks int) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(simFailure); !ok {
				w.err = fmt.Sprintf("seed %d limit %d tick %d: panic: %v\n%s", w.seed, w.limit, w.tick, r, debug.Stack())
			}
			ticks = w.tick
		}
	}()
	for w.tick = 1; w.tick <= simMaxTicks; w.tick++ {
		w.carry()
		snap := w.snapshot()
		in := w.input(snap)
		acts, state, blocked := plan(in)
		acts2, state2, blocked2 := plan(in)
		if !reflect.DeepEqual(acts, acts2) || state != state2 || !reflect.DeepEqual(blocked, blocked2) {
			w.fail("plan is not deterministic: %v / %v", acts, acts2)
		}
		w.checkPlan(in, acts, state, blocked)
		w.checkProgress(in, acts, blocked)
		w.checkQuiet(in, acts, blocked)
		w.apply(in, acts, blocked)
		w.checkStable(acts)
		w.runWorkers()
		w.agents()
		w.user()
		w.flipUsage()
		if w.done() {
			return w.tick
		}
	}
	w.fail("not every ticket is done after %d ticks", simMaxTicks)
	return w.tick
}

func (w *sim) done() bool {
	for _, st := range w.tickets {
		if st.t.Column != api.ColumnDone {
			return false
		}
	}
	return true
}

// inWork reports whether t holds a slot, independently of the planner: from
// its gated start until it is done, whatever its agents do.
func inWork(t board.EngineTicket, worker bool) bool {
	return t.Column == api.ColumnInProgress || t.Column == api.ColumnReview ||
		(t.Column == api.ColumnReady && (t.Busy || worker))
}

// slotsUsed counts the tickets of a type that hold a slot.
func (w *sim) slotsUsed(typ api.TicketType) int {
	n := 0
	for _, st := range w.tickets {
		if st.t.Type == typ && !st.hidden && inWork(st.t, w.hasWorker(st.t.ID)) {
			n++
		}
	}
	return n
}

// storyLoad counts the tasks of a story that are in work.
func (w *sim) storyLoad(story int64) int {
	n := 0
	for _, st := range w.tickets {
		if st.t.StoryID == story && !st.hidden && inWork(st.t, w.hasWorker(st.t.ID)) {
			n++
		}
	}
	return n
}

func (w *sim) hasWorker(n int64) bool {
	return slices.ContainsFunc(w.workers, func(k *simWorker) bool { return k.a.ticket == n })
}

// startable is the board's definition of a ticket that may start new work,
// written independently of the planner.
func (w *sim) startable(in planInput, t board.EngineTicket) bool {
	if t.Column != api.ColumnReady || len(t.WaitingFor) > 0 || t.FailedStep != "" || t.Busy || in.inflight[t.ID] {
		return false
	}
	if t.StoryID != 0 && w.tickets[t.StoryID].t.Column != api.ColumnInProgress {
		return false
	}
	for _, p := range t.Projects {
		if !in.available(p.ID) {
			return false
		}
	}
	return true
}

func (w *sim) checkPlan(in planInput, acts []action, state api.SchedulerState, blocked map[int64]api.StartBlocked) {
	byID := map[int64]board.EngineTicket{}
	for _, t := range in.snap.Tickets {
		byID[t.ID] = t
	}
	// The slots in use, counted independently from the board: tickets in
	// In Progress or Review, tickets a gated start is claiming, and the
	// gated starts of this plan. What their agents do (run, wait, nothing)
	// never matters.
	used := map[api.TicketType]int{}
	for _, t := range in.snap.Tickets {
		if inWork(t, in.inflight[t.ID]) {
			used[t.Type]++
		}
	}
	for _, a := range acts {
		if a.kind == actStart && a.gated {
			used[byID[a.ticket].Type]++
		}
	}
	if state.Tasks.InWork != used[api.TypeTask] || state.Stories.InWork != used[api.TypeStory] ||
		state.Tasks.Limit != w.limit || state.Stories.Limit != w.limit {
		w.fail("the scheduler counts %+v, the board shows %d tasks and %d stories in work", state, used[api.TypeTask], used[api.TypeStory])
	}
	seen := map[string]bool{}
	started := map[int64]bool{}
	gatedPerStory := map[int64]int{}
	for _, a := range acts {
		if a.kind != actStart {
			continue
		}
		key := fmt.Sprintf("%d/%s/%s/%s", a.ticket, a.role, a.project, a.purpose)
		if seen[key] {
			w.fail("two starts for one slot: %v", acts)
		}
		seen[key] = true
		started[a.ticket] = true
		t, ok := byID[a.ticket]
		switch {
		case !ok:
			w.fail("start for a ticket not on the board: %v", a)
		case t.Busy || t.FailedStep != "" || in.inflight[t.ID]:
			w.fail("start for a busy, failed or in-flight ticket: %v", a)
		case in.snap.Paused:
			w.fail("start while paused: %v", a)
		case a.gated && (t.Column != api.ColumnReady || len(t.WaitingFor) > 0 || !w.startable(in, t)):
			w.fail("gated start for a ticket that cannot start: %v", a)
		case !a.gated && t.Column != api.ColumnInProgress && t.Column != api.ColumnReview:
			w.fail("continuation for a ticket that has not started: %v", a)
		case in.hasModel(a.role, a.purpose, t) != startOK:
			w.fail("start without a model below its usage limits: %v (%v)", a, in.hasModel(a.role, a.purpose, t))
		case a.role == api.RoleReviewer && (t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageAgent || t.Verdicts[a.project] != ""):
			w.fail("reviewer start for a project that needs no review: %v", a)
		case a.role == api.RoleReviewer && t.Type == api.TypeStory && w.tickets[a.ticket].pins[a.project] == "0":
			w.fail("reviewer start for a project without commits of its own: %v", a)
		case a.role == api.RoleReviewer && w.tickets[a.ticket].pins[a.project] != "" && // a task without a project has no commit
			w.tickets[a.ticket].approved[a.project].Commit == w.tickets[a.ticket].pins[a.project]:
			w.fail("reviewer start for project %s, approved for commit %s, which is unchanged: %v",
				a.project, w.tickets[a.ticket].pins[a.project], a)
		case a.purpose == api.PurposeExport && (t.ReviewStage != api.ReviewStageExporting || w.exportOver(w.tickets[a.ticket], a.project) ||
			slices.ContainsFunc(t.Projects, func(p board.EngineProject) bool { return p.ID == a.project && p.ExportURL != "" })):
			w.fail("export start for a project that needs no export: %v", a)
		}
		if a.gated && t.StoryID != 0 {
			gatedPerStory[t.StoryID]++
		}
	}
	for _, t := range in.snap.Tickets {
		if t.Column != api.ColumnReady {
			reason, ok := blocked[t.ID]
			working := t.Column == api.ColumnInProgress || t.Column == api.ColumnReview
			if ok && (!working || (reason != api.StartBlockedUsage && reason != api.StartBlockedNone)) {
				w.fail("start_blocked %q for #%d in %s", reason, t.ID, t.Column)
			}
			if ok && reason == api.StartBlockedUsage && started[t.ID] && !slices.ContainsFunc(acts, func(a action) bool {
				return a.kind == actStart && a.ticket == t.ID && in.hasModel(a.role, a.purpose, t) == startOK
			}) {
				w.fail("#%d waits for usage and starts", t.ID)
			}
			continue
		}
		reason, ok := blocked[t.ID]
		if !ok {
			w.fail("no start_blocked entry for ready #%d", t.ID)
		}
		can := w.startable(in, t)
		switch {
		case !can && reason != "":
			w.fail("#%d cannot start for a reason the board shows, but start_blocked = %q", t.ID, reason)
		case can && started[t.ID] && reason != "":
			w.fail("#%d starts but start_blocked = %q", t.ID, reason)
		case can && !started[t.ID] && reason == "":
			w.fail("#%d could start, did not, and has no reason", t.ID)
		case can && in.snap.Paused && reason != api.StartBlockedPaused:
			w.fail("#%d paused but start_blocked = %q", t.ID, reason)
		case can && !in.snap.Paused && (reason == api.StartBlockedNoModel) != (in.hasModel(workingRole(t), api.PurposeWork, t) == startNoModel):
			w.fail("#%d start_blocked = %q disagrees with the models", t.ID, reason)
		case can && !in.snap.Paused && (reason == api.StartBlockedUsage) != (in.hasModel(workingRole(t), api.PurposeWork, t) == startUsage):
			w.fail("#%d start_blocked = %q disagrees with the usage", t.ID, reason)
		case can && reason == api.StartBlockedSlot && used[t.Type] < w.limit:
			story := w.tickets[t.StoryID]
			if t.StoryID == 0 || story.t.MaxParallel == 0 || w.storyLoad(t.StoryID)+gatedPerStory[t.StoryID] < story.t.MaxParallel {
				w.fail("#%d blocked by slot with %d of %d %ss in work and room in its story", t.ID, used[t.Type], w.limit, t.Type)
			}
		}
	}
}

// checkProgress requires, independently of the planner, that every piece
// of started work that misses an agent gets one in this pass, ungated, that
// every approved ticket is merged, every done ticket cleaned up once its
// sessions are gone, and every session without a reason to live is ended.
func (w *sim) checkProgress(in planInput, acts []action, blocked map[int64]api.StartBlocked) {
	has := func(match func(a action) bool) bool { return slices.ContainsFunc(acts, match) }
	ended := map[string]bool{}
	for _, a := range acts {
		if a.kind == actEnd {
			ended[a.session] = true
		}
	}
	// Agents are ended only for a listed reason. While its ticket is in
	// progress or in review, the working agent ends only when its process
	// is gone or a newer session took its place; a reviewer also when the
	// next review round of its project begins, which is when this pass
	// starts that round's reviewer or one is live already. A reviewer whose
	// round still waits for its verdict never ends for the next round.
	for _, a := range acts {
		if a.kind != actEnd || a.role == api.RoleCEO || a.purpose == api.PurposeExport {
			continue
		}
		s := w.session(a.session)
		st := w.tickets[a.ticket]
		if s == nil || st == nil {
			continue
		}
		t := st.t
		if t.Column != api.ColumnInProgress && t.Column != api.ColumnReview {
			continue
		}
		hard := a.reason == "agent process is gone" || a.reason == "a newer session took over"
		switch {
		case hard:
		case a.role == api.RoleReviewer && a.reason == newRoundText(t.ID):
			project := s.s.Project
			if project == "" && len(t.Projects) == 1 {
				project = t.Projects[0].ID
			}
			next := has(func(b action) bool {
				return b.kind == actStart && b.role == api.RoleReviewer && b.ticket == t.ID && b.project == project
			}) || slices.ContainsFunc(in.snap.Sessions, func(o board.EngineSession) bool {
				return o.Role == api.RoleReviewer && o.Ticket == t.ID && o.ReviewRound > s.s.ReviewRound && (o.Project == project || o.Project == "")
			})
			if !next || s.s.ReviewRound >= t.ReviewRound {
				w.fail("reviewer %s of #%d (round %d) ended for a new round that has no reviewer of %s: %v", a.session, a.ticket,
					s.s.ReviewRound, project, acts)
			}
		default:
			w.fail("%s %s of #%d in %s ended: %q", a.role, a.session, a.ticket, t.Column, a.reason)
		}
	}
	// Every live session on a model at its hard limit is stopped, paused or
	// not, once; none other is.
	for _, s := range w.live() {
		hard := s.process && s.s.Model != "" && w.use(s.s.Model).State == api.UsageHard
		stop := has(func(a action) bool { return a.kind == actUsageStop && a.session == s.s.ID })
		switch {
		case stop && (s.stopping || !hard):
			w.fail("usage stop of %s (stopping %v, model %s %s)", s.s.ID, s.stopping, s.s.Model, w.use(s.s.Model).State)
		case hard && !s.stopping && !ended[s.s.ID] && !stop:
			w.fail("%s runs on %s at its hard limit and is not stopped", s.s.ID, s.s.Model)
		}
	}
	if in.snap.Paused {
		return
	}
	pending := map[string]bool{}
	for _, st := range in.starts {
		pending[st.session] = true
	}
	var kept []board.EngineSession
	for _, s := range in.snap.Sessions {
		if ended[s.ID] {
			continue
		}
		kept = append(kept, s)
		if in.inflight[s.Ticket] || pending[s.ID] || s.Role == api.RoleCEO {
			continue
		}
		t := w.tickets[s.Ticket].t
		if !in.processes[s.ID] || t.Column == api.ColumnDone || t.Column == api.ColumnReady || t.Column == api.ColumnPlanning {
			w.fail("session %s (%s on #%d in %s, process %v) stays live", s.ID, s.Role, s.Ticket, t.Column, in.processes[s.ID])
		}
		// A kept agent stays only as long as the rules keep it: a reviewer
		// until a reviewer of a later round works on its project, an export
		// job only while its project still needs an export, the working
		// agent while its ticket is in progress or in review.
		switch {
		case s.Role == api.RoleReviewer && slices.ContainsFunc(in.snap.Sessions, func(o board.EngineSession) bool {
			return o.Role == api.RoleReviewer && o.Ticket == s.Ticket && o.ReviewRound > s.ReviewRound && o.Project == s.Project
		}):
			w.fail("reviewer %s of #%d (round %d, project %s) stays live beside a reviewer of a later round", s.ID, s.Ticket,
				s.ReviewRound, s.Project)
		case s.Purpose == api.PurposeExport && (t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting ||
			w.exportOver(w.tickets[s.Ticket], s.Project) ||
			slices.ContainsFunc(t.Projects, func(p board.EngineProject) bool { return p.ID == s.Project && p.ExportURL != "" })):
			w.fail("export job %s of #%d (project %s) stays live: %s stage %q", s.ID, s.Ticket, s.Project, t.Column, t.ReviewStage)
		case s.Role == workingRoleOf(t) && s.Purpose == api.PurposeWork &&
			t.Column != api.ColumnInProgress && t.Column != api.ColumnReview:
			w.fail("%s %s of #%d stays live in %s", s.Role, s.ID, s.Ticket, t.Column)
		}
	}
	// At most one reviewer per ticket and project stays live.
	reviewers := map[string]string{}
	for _, s := range kept {
		if s.Role != api.RoleReviewer {
			continue
		}
		key := fmt.Sprintf("%d/%s", s.Ticket, s.Project)
		if other, ok := reviewers[key]; ok {
			w.fail("two reviewers of #%d project %s stay live: %s and %s", s.Ticket, s.Project, other, s.ID)
		}
		reviewers[key] = s.ID
	}
	live := func(t board.EngineTicket, match func(s board.EngineSession) bool) bool {
		return slices.ContainsFunc(kept, func(s board.EngineSession) bool { return s.Ticket == t.ID && match(s) })
	}
	for _, t := range in.snap.Tickets {
		if t.Busy || t.FailedStep != "" || in.inflight[t.ID] {
			continue
		}
		if t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageMerging &&
			!has(func(a action) bool { return a.kind == actMerge && a.ticket == t.ID }) {
			w.fail("#%d is approved but not merged", t.ID)
		}
		if t.Column == api.ColumnDone && hasWorktree(t) && !slices.ContainsFunc(in.snap.Sessions, func(s board.EngineSession) bool { return s.Ticket == t.ID }) &&
			!has(func(a action) bool { return a.kind == actCleanup && a.ticket == t.ID }) {
			w.fail("#%d is done but not cleaned up", t.ID)
		}
		type want struct {
			role    api.Role
			project string
			purpose api.Purpose
		}
		var wants []want
		switch {
		case t.Column == api.ColumnInProgress:
			role := workingRoleOf(t)
			if !live(t, func(s board.EngineSession) bool { return s.Role == role && s.Purpose == api.PurposeWork }) {
				wants = append(wants, want{role, "", api.PurposeWork})
			}
		case t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageAgent:
			for _, key := range t.ReviewKeys() {
				if t.Verdicts[key] == "" && !live(t, func(s board.EngineSession) bool {
					return s.Role == api.RoleReviewer && s.ReviewRound == t.ReviewRound && s.Project == key
				}) {
					wants = append(wants, want{api.RoleReviewer, key, api.PurposeWork})
				}
			}
		case t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageExporting:
			for _, p := range t.Projects {
				if p.ExportURL == "" && !w.exportOver(w.tickets[t.ID], p.ID) &&
					!live(t, func(s board.EngineSession) bool { return s.Purpose == api.PurposeExport && s.Project == p.ID }) {
					wants = append(wants, want{api.RoleDeveloper, p.ID, api.PurposeExport})
				}
			}
		}
		noProject := slices.ContainsFunc(t.Projects, func(p board.EngineProject) bool { return !in.available(p.ID) })
		for _, x := range wants {
			check := in.hasModel(x.role, x.purpose, t)
			if !noProject && check == startUsage {
				if blocked[t.ID] != api.StartBlockedUsage {
					w.fail("#%d needs a %s that waits for usage, but start_blocked = %q", t.ID, x.role, blocked[t.ID])
				}
				continue
			}
			if noProject || check != startOK {
				if t.Attention == "" && !has(func(a action) bool { return a.kind == actAttention && a.ticket == t.ID && a.reason != "" }) {
					w.fail("#%d needs a %s it cannot get, and nobody is told", t.ID, x.role)
				}
				continue
			}
			if !has(func(a action) bool {
				return a.kind == actStart && !a.gated && a.ticket == t.ID && a.role == x.role && a.project == x.project && a.purpose == x.purpose
			}) {
				w.fail("#%d in %s needs a %s (project %q, %s) and none is started", t.ID, t.Column, x.role, x.project, x.purpose)
			}
		}
	}
}

// checkQuiet fails when nothing happens and nothing can happen by itself,
// but some ticket could still make progress under the rules.
func (w *sim) checkQuiet(in planInput, acts []action, blocked map[int64]api.StartBlocked) {
	for _, a := range acts {
		if a.kind != actAttention {
			return
		}
	}
	if len(w.workers) > 0 {
		return
	}
	for _, s := range w.live() {
		if s.process && s.s.Role != api.RoleCEO && (s.s.State == api.SessionRunning || s.s.State == api.SessionStarting) {
			return
		}
	}
	if in.snap.Paused {
		return // the user unpauses
	}
	for _, t := range in.snap.Tickets {
		if t.Column == api.ColumnDone {
			continue
		}
		for _, a := range acts {
			if a.ticket == t.ID {
				t.Attention = a.reason // proposed in this pass
			}
		}
		if !w.explained(in, t, blocked) {
			w.fail("deadlock: #%d (%s, %s, stage %q) could make progress, but nothing is proposed or running", t.ID, t.Type, t.Column, t.ReviewStage)
		}
	}
}

// explained reports whether a quiet ticket waits for a person or for
// another ticket, as the board or the planner says.
func (w *sim) explained(in planInput, t board.EngineTicket, blocked map[int64]api.StartBlocked) bool {
	switch {
	case t.FailedStep != "", t.Attention != "":
		return true
	case t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageHuman:
		return true
	case t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageAwaitingMerge:
		return true // the remote merge and the engine's check happen later
	case t.Column == api.ColumnReady && len(t.WaitingFor) > 0:
		return true
	case t.Column == api.ColumnReady && (blocked[t.ID] == api.StartBlockedNoModel || blocked[t.ID] == api.StartBlockedPaused):
		return true
	case blocked[t.ID] == api.StartBlockedUsage && w.tick <= w.usageUntil:
		// It goes on by itself once usage is below the limits, and the
		// simulated usage always gets there at usageUntil, or the user
		// presses Start anyway.
		return true
	case t.Column == api.ColumnReady && blocked[t.ID] == api.StartBlockedSlot && w.slotsUsed(t.Type) >= w.limit:
		// Its kind's slots are held by tickets in work, each of which is
		// checked as an open ticket of its own.
		return true
	case t.Column == api.ColumnReady && blocked[t.ID] == api.StartBlockedSlot && t.StoryID != 0 &&
		w.tickets[t.StoryID].t.MaxParallel > 0 && w.storyLoad(t.StoryID) >= w.tickets[t.StoryID].t.MaxParallel:
		// The story's own limit is held by tasks in work, each of which
		// is checked as an open ticket of its own.
		return true
	case t.Column == api.ColumnReady && t.StoryID != 0 && w.tickets[t.StoryID].t.Column != api.ColumnInProgress:
		return true
	case t.Column == api.ColumnInProgress && t.Type == api.TypeStory:
		// The pm waits while some task of its story is still open.
		for _, st := range w.tickets {
			if st.t.StoryID == t.ID && !st.hidden && st.t.Column != api.ColumnDone {
				return w.hasLive(t.ID, api.RolePM)
			}
		}
	}
	return false
}

func (w *sim) hasLive(ticket int64, role api.Role) bool {
	for _, s := range w.live() {
		if s.s.Ticket == ticket && s.s.Role == role && s.s.Purpose == api.PurposeWork {
			return true
		}
	}
	return false
}

func (w *sim) apply(in planInput, acts []action, blocked map[int64]api.StartBlocked) {
	for _, a := range acts {
		switch a.kind {
		case actEnd:
			s := w.session(a.session)
			if s == nil || s.ended {
				w.fail("end of a session that is not live: %v", a)
				return
			}
			s.ended = true
			w.logf("engine %v", a)
		case actMerge, actCleanup:
			if a.kind == actMerge {
				w.tickets[a.ticket].t.Busy = true
			}
			w.workers = append(w.workers, &simWorker{a: a, due: w.tick + w.rng.IntN(3)})
			w.logf("engine %v", a)
		case actAttention:
			w.tickets[a.ticket].t.Attention = a.reason
			w.logf("engine %v", a)
		case actUsageStop:
			s := w.session(a.session)
			if s == nil || s.ended || s.stopping {
				w.fail("usage stop of a session that is not live or already stopping: %v", a)
				return
			}
			s.stopping = true
			s.stopAt = w.tick
			if s.s.Role != api.RoleCEO && s.s.State == api.SessionRunning {
				s.stopAt += w.rng.IntN(4) // the handoff
			}
			w.logf("engine %v", a)
		case actStart:
			st := w.tickets[a.ticket]
			if a.gated {
				if n := w.slotsUsed(st.t.Type); n >= w.limit {
					w.fail("gated start %v with %d of %d %ss in work", a, n, w.limit, st.t.Type)
				}
				if story := w.tickets[st.t.StoryID]; st.t.StoryID != 0 && story.t.MaxParallel > 0 &&
					w.storyLoad(st.t.StoryID) >= story.t.MaxParallel {
					w.fail("gated start %v beyond the story's limit %d", a, story.t.MaxParallel)
				}
				st.t.Busy = true // the claim
				st.t.StartBlocked = ""
			}
			for _, s := range w.live() {
				if s.s.Ticket != a.ticket {
					continue
				}
				if a.role == api.RoleReviewer && s.s.Role == api.RoleReviewer && s.s.ReviewRound != st.t.ReviewRound &&
					(s.s.Project == a.project || s.s.Project == "") {
					w.fail("reviewer start %v while the reviewer of round %d of its project stays", a, s.s.ReviewRound)
				}
				if w.sameSlot(s, a, st.t) {
					w.fail("start %v for a slot that has live session %s", a, s.s.ID)
				}
			}
			w.workers = append(w.workers, &simWorker{a: a, due: w.tick + w.rng.IntN(3), round: st.t.ReviewRound})
			w.logf("engine %v", a)
		}
	}
	for id, reason := range blocked {
		t := &w.tickets[id].t
		if !simStartBlockedFits(t.Column, reason) || (reason != "" && (t.Busy || t.FailedStep != "")) {
			continue
		}
		t.StartBlocked = reason
	}
}

// simStartBlockedFits is the board's rule for which reason a column takes:
// any in Ready, only usage or none in progress and in review.
func simStartBlockedFits(col api.Column, v api.StartBlocked) bool {
	switch col {
	case api.ColumnReady:
		return true
	case api.ColumnInProgress, api.ColumnReview:
		return v == api.StartBlockedUsage || v == api.StartBlockedNone
	}
	return false
}

func (w *sim) sameSlot(s *simSession, a action, t board.EngineTicket) bool {
	sp := s.s.Purpose
	if s.s.Role != a.role || sp != a.purpose {
		return false
	}
	switch {
	case a.role == api.RoleReviewer:
		proj := s.s.Project
		if proj == "" && len(t.Projects) == 1 {
			proj = t.Projects[0].ID
		}
		return s.s.ReviewRound == t.ReviewRound && proj == a.project
	case a.purpose == api.PurposeExport:
		return s.s.Project == a.project
	}
	return true
}

// checkStable plans again right after the actions were applied: nothing
// proposed before may be proposed again.
func (w *sim) checkStable(prev []action) {
	in := w.input(w.snapshot())
	again, _, _ := plan(in)
	for _, a := range again {
		for _, b := range prev {
			if a.kind == b.kind && a.ticket == b.ticket && a.role == b.role && a.project == b.project &&
				a.purpose == b.purpose && a.session == b.session && a.reason == b.reason {
				w.fail("%v proposed again after it was applied", a)
			}
		}
	}
}

func (w *sim) resetFailures(n int64) {
	for k := range w.failures {
		if strings.HasPrefix(k, fmt.Sprintf("%d/", n)) {
			delete(w.failures, k)
		}
	}
}

// move changes a ticket's column like the board: claims, failed steps,
// attention and the start reason are cleared.
func (w *sim) move(st *simTicket, col api.Column) {
	t := &st.t
	t.Column, t.Busy, t.FailedStep, t.Attention, t.StartBlocked = col, false, "", "", ""
	t.Rank = w.rank()
	if !w.hasWorker(t.ID) {
		delete(w.forced, t.ID)
	}
	if col != api.ColumnReview {
		t.ReviewStage = ""
	}
	w.resetFailures(t.ID)
}

func (w *sim) wake(ticket int64, role api.Role) {
	for _, s := range w.live() {
		if s.s.Ticket == ticket && s.s.Role == role && s.s.Purpose == api.PurposeWork && s.s.State == api.SessionWaiting {
			s.s.State = api.SessionRunning
		}
	}
}

// finished records a task or story reaching Done and notifies its pm.
func (w *sim) finished(st *simTicket, res api.Resolution) {
	w.move(st, api.ColumnDone)
	st.t.Resolution = res
	if st.t.StoryID != 0 {
		w.wake(st.t.StoryID, api.RolePM)
	}
	w.logf("#%d done (%s)", st.t.ID, res)
}

// waitExport models the board's successful export: the ticket keeps its
// active slot and agents until the remote work lands and a check sees it.
func (w *sim) waitExport(st *simTicket) {
	st.t.ReviewStage, st.t.Resolution, st.t.ExportMerged = api.ReviewStageAwaitingMerge, "", false
	st.mergeAt = w.tick + 1 + w.rng.IntN(20)
	w.logf("#%d exported, waiting for the merge", st.t.ID)
}

func (w *sim) approvedStage(st *simTicket) api.ReviewStage {
	if st.t.StoryID != 0 || st.exportFail || st.t.WhenApproved != api.WhenApprovedExport {
		return api.ReviewStageMerging
	}
	return api.ReviewStageExporting
}

// pass moves st on to stage after its review, like the board: a task
// without a project has nothing to merge or export and is done instead.
func (w *sim) pass(st *simTicket, stage api.ReviewStage) {
	if st.t.Type == api.TypeTask && len(st.t.Projects) == 0 &&
		(stage == api.ReviewStageMerging || stage == api.ReviewStageExporting) {
		w.finished(st, api.ResolutionCompleted)
		return
	}
	st.t.ReviewStage = stage
}

func (w *sim) handIn(st *simTicket) {
	w.move(st, api.ColumnReview)
	st.t.ReviewRound++
	st.t.Verdicts = map[string]string{}
	st.pins = map[string]string{}
	for _, p := range st.t.Projects {
		st.pins[p.ID] = strconv.Itoa(st.commits[p.ID])
	}
	switch st.t.Review {
	case api.ReviewSkipped:
		w.pass(st, w.approvedStage(st))
	case api.ReviewHuman:
		st.t.ReviewStage = api.ReviewStageHuman
	default:
		st.t.ReviewStage = api.ReviewStageAgent
	}
	w.logf("#%d handed in, round %d, stage %s", st.t.ID, st.t.ReviewRound, st.t.ReviewStage)
}

func (w *sim) runWorkers() {
	var keep []*simWorker
	for _, k := range w.workers {
		if k.due > w.tick {
			keep = append(keep, k)
			continue
		}
		if w.work(k) {
			keep = append(keep, k)
		}
	}
	w.workers = keep
}

// work carries out one worker step and reports whether more steps follow.
func (w *sim) work(k *simWorker) bool {
	st := w.tickets[k.a.ticket]
	switch k.a.kind {
	case actMerge:
		st.t.Busy = false
		switch {
		case w.p(0.06):
			w.move(st, api.ColumnInProgress)
			st.needFix = st.t.Type == api.TypeStory
			w.wake(st.t.ID, workingRoleOf(st.t))
			w.logf("#%d merge conflict", st.t.ID)
		case w.p(0.04):
			st.t.FailedStep, st.t.Attention = api.FailedMerge, "merge blocked by local changes"
			w.logf("#%d merge blocked", st.t.ID)
		default:
			if story := w.tickets[st.t.StoryID]; st.t.StoryID != 0 {
				for _, p := range st.t.Projects {
					if st.commits[p.ID] > 0 {
						story.commits[p.ID]++
					}
				}
			}
			if slices.ContainsFunc(st.t.Projects, func(p board.EngineProject) bool { return p.ExportURL != "" }) {
				w.waitExport(st)
			} else {
				w.finished(st, api.ResolutionCompleted)
			}
		}
		return false
	case actCleanup:
		for i := range st.t.Projects {
			st.t.Projects[i].Worktree = ""
		}
		return false
	}
	// A start.
	if k.step == 0 {
		model, ok := w.pickModel(k, st)
		if !ok {
			if k.a.gated {
				st.t.Busy = false // the claim is released
			}
			return false
		}
		if k.a.gated {
			if w.p(0.03) {
				st.t.Busy = false
				st.t.FailedStep, st.t.Attention = api.FailedStart, "branch could not be created"
				w.logf("#%d start failed", st.t.ID)
				return false
			}
			w.move(st, api.ColumnInProgress) // BeginWork
			for i := range st.t.Projects {
				st.t.Projects[i].Worktree = "wt"
			}
		}
		if !w.mayCreate(k, st) {
			w.logf("#%d session for %v refused", st.t.ID, k.a)
			return false
		}
		round := 0
		if k.a.role == api.RoleReviewer {
			round = k.round
		}
		for _, s := range w.live() {
			if s.s.Ticket == st.t.ID && w.sameSlot(s, k.a, st.t) {
				w.fail("worker creates a second session for slot %v (live %s)", k.a, s.s.ID)
			}
		}
		s := w.newSession(k.a.role, k.a.ticket, k.a.project, k.a.purpose, round, api.SessionStarting, false)
		s.s.Model = model
		k.session = s.s.ID
		k.step = 1
		k.due = w.tick + w.rng.IntN(2)
		return true
	}
	s := w.session(k.session)
	if s != nil && !s.ended {
		if w.p(0.03) {
			w.crash(s, "start failed")
		} else {
			s.process = true
		}
	}
	return false
}

// pickModel picks the model of a start with the real picker, as the engine
// does before it moves the ticket: a start whose models all went over their
// soft limit since the pass is dropped. No agent may start on a model over
// its soft limit unless the user pressed Start anyway.
func (w *sim) pickModel(k *simWorker, st *simTicket) (string, bool) {
	c := w.choice(k.a.role, k.a.purpose, st.t)
	model, err := w.picker.pick(k.a.role, c, w.catalog, w.use)
	if errors.Is(err, errUsageWait) {
		w.logf("#%d start of %v waits for usage", st.t.ID, k.a)
		return "", false
	}
	if err != nil {
		w.fail("pick for %v: %v", k.a, err)
	}
	if state := w.use(model).State; (state == api.UsageSoft || state == api.UsageHard) && !(c.forced && model == c.ticketModel) {
		w.fail("agent of %v starts on %s in state %s", k.a, model, state)
	}
	if c.forced {
		delete(w.forced, st.t.ID) // used once
	}
	return model, true
}

func workingRoleOf(t board.EngineTicket) api.Role {
	if t.Type == api.TypeStory {
		return api.RolePM
	}
	return api.RoleDeveloper
}

// mayCreate applies the board's rules for creating a session.
func (w *sim) mayCreate(k *simWorker, st *simTicket) bool {
	t := st.t
	switch {
	case k.a.role == api.RoleReviewer:
		return t.Column == api.ColumnReview && t.ReviewRound == k.round
	case k.a.purpose == api.PurposeExport:
		return t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageExporting
	}
	return t.Column != api.ColumnDone
}

// crash ends a session the way the engine's exit watcher does, with failure
// accounting: the third consecutive failure of a slot flags the ticket.
func (w *sim) crash(s *simSession, why string) {
	s.process = false
	st := w.tickets[s.s.Ticket]
	if s.s.Role == api.RoleCEO || st == nil {
		s.ended = true
		return
	}
	if w.p(0.5) {
		s.ended = true // the exit watcher ends it; otherwise the planner finds the process gone
	}
	if keptAgent(s.s, st.t) {
		// A kept agent has no work of its own: its end is no failure, and
		// nothing replaces it until its ticket needs it again.
		w.logf("%s %s of #%d ended while kept (%s)", s.s.ID, s.s.Role, s.s.Ticket, why)
		return
	}
	key := fmt.Sprintf("%d/%s/%s/%s", s.s.Ticket, s.s.Role, s.s.Project, s.s.Purpose)
	w.failures[key]++
	w.logf("%s %s of #%d crashed (%s), failure %d", s.s.ID, s.s.Role, s.s.Ticket, why, w.failures[key])
	if w.failures[key] < 3 || st.t.Column == api.ColumnDone {
		return
	}
	delete(w.failures, key)
	if s.s.Purpose == api.PurposeExport {
		if st.t.Column == api.ColumnReview && st.t.ReviewStage == api.ReviewStageExporting {
			st.exportFailed[s.s.Project] = true
		}
		return
	}
	st.t.FailedStep, st.t.Attention = api.FailedAgent, "agent keeps crashing"
}

// agents lets every live agent act once, in random order.
func (w *sim) agents() {
	live := w.live()
	w.rng.Shuffle(len(live), func(i, j int) { live[i], live[j] = live[j], live[i] })
	for _, s := range live {
		if s.ended || !s.process {
			continue
		}
		if s.stopping && w.tick >= s.stopAt {
			// The usage stop ends it, which counts as no failure.
			s.ended, s.process = true, false
			w.logf("%s %s of #%d stopped for usage", s.s.ID, s.s.Role, s.s.Ticket)
			continue
		}
		if s.s.Role != api.RoleCEO && w.p(0.01) {
			w.crash(s, "exit 1")
			continue
		}
		switch s.s.State {
		case api.SessionStarting:
			if w.tick-s.since > 8 {
				w.crash(s, "did not run omo ai ready in time")
			} else if w.p(0.7) {
				s.s.State = api.SessionRunning
			}
		case api.SessionRunning:
			switch {
			case s.s.Role == api.RoleCEO:
			case s.stalled != 0:
				// The health check flags an agent that stays silent.
				if st := w.tickets[s.s.Ticket]; w.tick-s.stalled > 15 && st.t.FailedStep == "" && st.t.Column != api.ColumnDone {
					st.t.FailedStep, st.t.Attention = api.FailedAgent, "agent produced no output"
					w.logf("%s on #%d is stalled", s.s.ID, s.s.Ticket)
				}
			case w.p(0.004):
				s.stalled = w.tick
			case w.p(0.6):
				w.act(s)
			}
		}
	}
}

func (w *sim) act(s *simSession) {
	st := w.tickets[s.s.Ticket]
	t := &st.t
	s.s.State = api.SessionWaiting
	switch {
	case s.s.Purpose == api.PurposeExport:
		if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting {
			return
		}
		for i := range t.Projects {
			if t.Projects[i].ID != s.s.Project || t.Projects[i].ExportURL != "" {
				continue
			}
			if w.p(0.1) {
				st.exportFailed[s.s.Project] = true
				w.logf("#%d export of %s failed", t.ID, s.s.Project)
				return
			}
			t.Projects[i].ExportURL = "https://example.test/pr/" + s.s.Project
		}
		if !slices.ContainsFunc(t.Projects, func(p board.EngineProject) bool { return p.ExportURL == "" }) {
			w.waitExport(st)
		}
	case s.s.Role == api.RolePM:
		if t.Column != api.ColumnInProgress {
			return
		}
		switch {
		case !st.tasksOut:
			st.tasksOut = true
			for _, id := range w.ids() {
				if task := w.tickets[id]; task.hidden && task.t.StoryID == t.ID {
					task.hidden = false
					task.t.Rank = w.rank()
				}
			}
			w.logf("pm of #%d wrote its tasks", t.ID)
		case st.needFix:
			st.needFix = false
			w.fixTask(st)
		default:
			open, completed := false, false
			for _, task := range w.tickets {
				if task.t.StoryID != t.ID || task.hidden {
					continue
				}
				open = open || task.t.Column != api.ColumnDone
				completed = completed || task.t.Resolution == api.ResolutionCompleted
			}
			switch {
			case open:
			case !completed:
				w.fixTask(st)
			default:
				w.handIn(st)
			}
		}
	case s.s.Role == api.RoleDeveloper:
		if t.Column == api.ColumnInProgress {
			// Most hand-ins change something; some leave the branch as it
			// was cut.
			for _, p := range t.Projects {
				if w.p(0.85) {
					st.commits[p.ID]++
				}
			}
			w.handIn(st)
		}
	case s.s.Role == api.RoleReviewer:
		project := s.s.Project
		if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageAgent || s.s.ReviewRound != t.ReviewRound ||
			t.Verdicts[project] != "" {
			return
		}
		if st.rejections < 2 && w.p(0.2) {
			st.rejections++
			t.Verdicts[project] = "reject"
			w.logf("#%d rejected by the reviewer of %s", t.ID, project)
		} else {
			t.Verdicts[project] = "approve"
		}
		w.roundOver(st)
	}
}

// roundOver ends the review round of st like the board once every project
// has its verdict: back to work with a rejection, else on.
func (w *sim) roundOver(st *simTicket) {
	t := &st.t
	rejected := false
	for _, key := range t.ReviewKeys() {
		switch t.Verdicts[key] {
		case "":
			return
		case "reject":
			rejected = true
		}
	}
	if rejected {
		w.move(st, api.ColumnInProgress)
		st.needFix = t.Type == api.TypeStory
		w.wake(t.ID, workingRoleOf(*t))
		w.logf("#%d review round %d over with rejections", t.ID, t.ReviewRound)
		return
	}
	if t.Review == api.ReviewAgentThenHuman {
		t.ReviewStage = api.ReviewStageHuman
	} else {
		w.pass(st, w.approvedStage(st))
	}
	w.logf("#%d approved by reviewers, stage %s", t.ID, t.ReviewStage)
}

// carry is the engine's step before planning (see carryVerdicts): it
// records the approvals of every ticket's current round and carries them
// over into rounds whose reviewers are still missing, with the engine's
// own decision functions.
func (w *sim) carry() {
	for _, id := range w.ids() {
		st := w.tickets[id]
		t := &st.t
		if st.pins == nil || (t.Column != api.ColumnReview && t.Column != api.ColumnInProgress) {
			continue
		}
		st.approved, _ = recordApprovals(*t, st.pins, st.approved)
		if t.Busy || w.hasWorker(id) || t.FailedStep != "" {
			continue
		}
		cs := carryOver(*t, st.pins, st.approved, func(_, commit string) bool { return commit == "0" })
		if len(cs) == 0 {
			continue
		}
		for _, c := range cs {
			st.approved[c.project] = c.from
			t.Verdicts[c.project] = "approve"
		}
		w.logf("#%d round %d carries %d approval(s) over", id, t.ReviewRound, len(cs))
		w.roundOver(st)
	}
	w.settleExports()
}

// settleExports is the engine's settleExports: projects without commits of
// their own are left out, and an export without a project still going on
// ends (see export.go).
func (w *sim) settleExports() {
	for _, id := range w.ids() {
		st := w.tickets[id]
		t := &st.t
		if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting || t.Busy || w.hasWorker(id) || t.FailedStep != "" {
			continue
		}
		for _, p := range t.Projects {
			if p.ExportURL == "" && !w.exportOver(st, p.ID) && st.pins[p.ID] == "0" {
				st.exportLeft[p.ID] = t.ReviewRound
			}
		}
		failed, exported := false, 0
		pending := false
		for _, p := range t.Projects {
			switch {
			case p.ExportURL != "":
				exported++
			case st.exportFailed[p.ID]:
				failed = true
			case !w.exportOver(st, p.ID):
				pending = true
			}
		}
		switch {
		case pending:
		case failed:
			t.ReviewStage, st.exportFail, t.Attention = api.ReviewStageHuman, true, "export failed"
			st.exportFailed = map[string]bool{}
			w.logf("#%d export failed", id)
		case exported > 0:
			w.waitExport(st)
		default:
			w.finished(st, api.ResolutionCompleted)
		}
	}
}

func (w *sim) fixTask(story *simTicket) {
	fix := w.newTicket(api.TypeTask, story.t.ID, []string{story.t.Projects[0].ID})
	w.logf("pm of #%d wrote fix task #%d", story.t.ID, fix.t.ID)
}

// user plays the person: answers human reviews, retries failed steps,
// cancels tickets stuck on a cancelled dependency, merges pull requests,
// pauses now and then and cancels a task now and then.
func (w *sim) user() {
	if w.paused && w.tick >= w.pauseUntil {
		w.paused = false
	} else if !w.paused && w.p(0.004) {
		w.paused, w.pauseUntil = true, w.tick+1+w.rng.IntN(30)
	}
	if w.p(0.005) {
		w.ceoEnabled = !w.ceoEnabled
	}
	if w.ceoEnabled && w.p(0.01) && !slices.ContainsFunc(w.live(), func(s *simSession) bool { return s.s.Role == api.RoleCEO }) {
		w.newSession(api.RoleCEO, 0, "", api.PurposeWork, 0, api.SessionRunning, true)
	}
	if w.p(0.002) {
		w.restart()
	}
	for _, id := range w.ids() {
		st := w.tickets[id]
		t := &st.t
		if st.hidden {
			continue
		}
		needsUser := t.FailedStep != "" || (t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageHuman) ||
			(t.Column == api.ColumnReady && slices.ContainsFunc(w.waitingFor(st), func(x api.Waiting) bool { return x.Reason == api.WaitingCancelled }))
		if !needsUser {
			st.userAt = 0
		} else if st.userAt == 0 {
			st.userAt = w.tick + 1 + w.rng.IntN(5)
		} else if w.tick >= st.userAt {
			st.userAt = 0
			switch {
			case t.FailedStep != "":
				if t.FailedStep == api.FailedAgent {
					for _, s := range w.live() {
						if s.s.Ticket == t.ID {
							s.ended = true
						}
					}
				}
				t.FailedStep, t.Attention = "", ""
				w.resetFailures(t.ID)
				w.logf("user retries #%d", t.ID)
			case t.Column == api.ColumnReview:
				t.Attention = ""
				w.pass(st, w.approvedStage(st))
				w.logf("user approves #%d, stage %s", t.ID, t.ReviewStage)
			case !t.Busy:
				w.finished(st, api.ResolutionCancelled)
			}
		}
		if t.ReviewStage == api.ReviewStageAwaitingMerge && !w.paused && w.tick >= st.mergeAt {
			w.finished(st, api.ResolutionExported)
			t.ExportMerged = true
		}
	}
	for _, id := range w.ids() {
		t := w.tickets[id].t
		if t.StartBlocked == api.StartBlockedUsage && t.Model != "" && w.p(0.02) {
			w.forced[id] = t.Column
			w.logf("user starts #%d anyway", id)
		}
	}
	if w.p(0.003) {
		var tasks []*simTicket
		for _, id := range w.ids() {
			st := w.tickets[id]
			if st.t.Type == api.TypeTask && !st.hidden && !st.t.Busy && !w.hasWorker(id) &&
				(st.t.Column == api.ColumnReady || st.t.Column == api.ColumnInProgress) {
				tasks = append(tasks, st)
			}
		}
		if len(tasks) > 0 {
			st := tasks[w.rng.IntN(len(tasks))]
			w.logf("user cancels #%d", st.t.ID)
			w.finished(st, api.ResolutionCancelled)
		}
	}
}

// restart simulates omo ending without a handoff and starting again: every
// agent process is gone, workers are lost and claims are released.
func (w *sim) restart() {
	w.logf("omo restarts")
	for _, s := range w.sessions {
		s.process = false
	}
	w.workers = nil
	for _, st := range w.tickets {
		st.t.Busy = false
	}
	w.failures = map[string]int{}
	w.forced = map[int64]api.Column{}
}
