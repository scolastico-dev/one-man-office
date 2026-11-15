package engine

import (
	"fmt"
	"slices"
	"sort"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// actionKind is what the engine is asked to do.
type actionKind int

const (
	// actEnd ends a live session: kill its process, then end the session
	// with the reason.
	actEnd actionKind = iota + 1
	// actStart starts an agent on a ticket. A gated start is new work: the
	// engine claims the ticket, prepares its branches and moves it to In
	// Progress before the agent starts. Any other start continues work that
	// has already started.
	actStart
	// actMerge merges a ticket in Review at stage merging.
	actMerge
	// actCleanup removes what a done ticket still has on disk.
	actCleanup
	// actAttention sets the ticket's attention to the reason, or clears it
	// when the reason is empty. The planner only ever clears texts it set.
	actAttention
)

func (k actionKind) String() string {
	switch k {
	case actEnd:
		return "end"
	case actStart:
		return "start"
	case actMerge:
		return "merge"
	case actCleanup:
		return "cleanup"
	case actAttention:
		return "attention"
	}
	return fmt.Sprintf("action(%d)", int(k))
}

// action is one step the planner asks the engine to take.
//
// For actStart: role and purpose say which agent; project is the project a
// reviewer or an export job works on and empty for the working agent of a
// ticket (the developer of a task, the pm of a story), which works on all of
// the ticket's projects. For actEnd: session and reason. For actAttention:
// reason.
type action struct {
	kind    actionKind
	ticket  int64
	role    api.Role
	project string
	purpose api.Purpose
	gated   bool
	session string
	reason  string
}

func (a action) String() string {
	switch a.kind {
	case actEnd:
		return fmt.Sprintf("end %s of #%d (%s)", a.session, a.ticket, a.reason)
	case actStart:
		s := fmt.Sprintf("start %s on #%d", a.role, a.ticket)
		if a.project != "" {
			s += " project " + a.project
		}
		if a.purpose == api.PurposeExport {
			s += " (export)"
		}
		if a.gated {
			s += " gated"
		}
		return s
	case actAttention:
		return fmt.Sprintf("attention #%d %q", a.ticket, a.reason)
	}
	return fmt.Sprintf("%s #%d", a.kind, a.ticket)
}

// pendingStart is an agent start a worker is carrying out. session is the
// id of the session the worker created, "" until it has.
type pendingStart struct {
	ticket  int64
	role    api.Role
	session string
}

// planInput is everything one planning pass looks at.
type planInput struct {
	snap board.EngineSnapshot
	// limit is the most agents that may run when new work starts.
	limit int
	// stopping is set while omo shuts down: no agent starts.
	stopping bool
	// ceoEnabled is false when the CEO is switched off.
	ceoEnabled bool
	// inflight holds the tickets a worker is acting on (a start, a merge or
	// a cleanup). The planner proposes nothing for them and ends none of
	// their sessions; the worker wakes the engine when it is done.
	inflight map[int64]bool
	// starts lists every agent start a worker is carrying out, the CEO's
	// included. A start counts against the limit until its session appears
	// among the snapshot's live sessions, where it is counted instead.
	starts []pendingStart
	// processes holds the ids of the sessions whose agent process is alive.
	processes map[string]bool
	// available reports whether a project is registered and enabled; nil
	// means every project is.
	available func(project string) bool
	// hasModel reports whether an agent of role with purpose on t would find
	// a usable model; nil means it always would.
	hasModel func(role api.Role, purpose api.Purpose, t board.EngineTicket) bool
}

// continuationGroup orders the continuations of started work: reviews and
// exports of finished tickets (1), tasks in progress (2), stories in
// progress (3). The order only decides which starts first; none of them
// waits for a slot.
func continuationGroup(t board.EngineTicket) int {
	switch {
	case t.Column == api.ColumnReview:
		return 1
	case t.Type == api.TypeTask:
		return 2
	}
	return 3
}

// newWorkGroup orders new work when slots are short: tasks of stories in
// progress (2) before new stories and standalone tasks (3). storyCol is the
// column of t's story. Within a group the column rank decides; stories
// pinned to the top of a column in the browser are not part of the order.
func newWorkGroup(t board.EngineTicket, storyCol api.Column) int {
	if t.Type == api.TypeTask && t.StoryID != 0 && storyCol == api.ColumnInProgress {
		return 2
	}
	return 3
}

// projectText says that project p is not usable.
func projectText(p string) string {
	return fmt.Sprintf("project %s is missing or disabled; omo starts no agent on this ticket until it is available again", p)
}

// plan computes what the engine should do now from one snapshot of the
// board. It is a pure function of its input: no clock, no I/O, no state
// kept between calls, and a deterministic order. It returns the actions in
// the order they must be carried out (every end first and finished before
// any start, then merges, cleanups and attention changes, then the
// continuations of started work, then the starts of new work), the
// scheduler state after the actions, and for every ticket in Ready For Dev
// why it has not started ("" when it started or when the board already
// shows the reason: a dependency, a failed step, a claim, a story that is
// not in progress, a missing project).
//
// Why the scheduler cannot deadlock:
//
//   - Started work is every ticket in In Progress or Review. Every agent such
//     a ticket needs (its developer or product manager again, a reviewer, an
//     export job) is started without looking at the limit. So no started
//     ticket ever waits for a free slot, and the slots can never all be held
//     by tickets that cannot finish because their reviewer, product manager
//     or developer cannot run. (Only a missing model or project can stop such
//     a start, and then the ticket is flagged for a person.)
//   - The limit only delays new work: a developer for a task in Ready For Dev
//     or a product manager for a story in Ready For Dev. Such a start waits
//     until fewer agents run than the limit allows. The count falls whenever
//     an agent waits (a waiting agent holds no slot), ends, or its ticket is
//     done, and no running agent needs a new start of another ticket to get
//     there, except through dependencies.
//   - Dependencies cannot form a cycle (the board refuses them), and a ticket
//     that waits for a dependency stays in Ready For Dev without a slot. A
//     task of a story needs only its story in progress, whose product
//     manager is started work and so never waits for a slot.
//   - So all slots can stay taken only by agents that run and never wait or
//     finish, which are stalled agents: the health check marks those as
//     failed, and the person who retries or cancels them frees the slot.
//     Merges hold no slot and pause does not stop them.
//   - Nothing starves: new work starts in the order of its group and its
//     place in the column, which the user controls. Continuations can push
//     the number of running agents above the limit, but only while they
//     finish work that has already started.
func plan(in planInput) ([]action, api.SchedulerState, map[int64]api.StartBlocked) {
	p := newPlanner(in)
	p.ends()
	p.mergesAndCleanups()
	blocked := map[int64]api.StartBlocked{}
	for _, t := range p.tickets {
		if t.Column == api.ColumnReady {
			blocked[t.ID] = api.StartBlockedNone
		}
	}
	state := api.SchedulerState{Limit: in.limit, Paused: in.snap.Paused}
	if in.snap.Paused || in.stopping {
		for _, t := range p.candidates() {
			blocked[t.ID] = api.StartBlockedPaused
		}
		state.Running = p.count
		return p.acts, state, blocked
	}
	p.continuations()
	p.newWork(blocked)
	p.attention()
	state.Running = p.count
	return p.ordered(), state, blocked
}

// planner holds the indexes of one pass.
type planner struct {
	in      planInput
	tickets []board.EngineTicket
	byID    map[int64]*board.EngineTicket
	// kept are the live sessions that stay after this pass's ends.
	kept []board.EngineSession
	// count is the number of agents holding a slot.
	count int
	acts  []action
	// cont and gated collect the starts; attn the attention changes.
	cont, gated, attn []action
	// want is the attention text the planner wants on a ticket it decided
	// about ("" = none of its own).
	want map[int64]string
}

func newPlanner(in planInput) *planner {
	p := &planner{in: in, byID: map[int64]*board.EngineTicket{}, want: map[int64]string{}}
	p.tickets = slices.Clone(in.snap.Tickets)
	sort.SliceStable(p.tickets, func(i, j int) bool { return p.tickets[i].ID < p.tickets[j].ID })
	for i := range p.tickets {
		p.byID[p.tickets[i].ID] = &p.tickets[i]
	}
	return p
}

func (p *planner) inflight(n int64) bool { return n != 0 && p.in.inflight[n] }

func (p *planner) available(project string) bool {
	return p.in.available == nil || p.in.available(project)
}

func (p *planner) hasModel(role api.Role, purpose api.Purpose, t board.EngineTicket) bool {
	return p.in.hasModel == nil || p.in.hasModel(role, purpose, t)
}

// pendingSession reports whether a worker is still starting the session.
func (p *planner) pendingSession(id string) bool {
	for _, s := range p.in.starts {
		if s.session != "" && s.session == id {
			return true
		}
	}
	return false
}

// workingRole is the agent that works on t: the pm of a story, the
// developer of a task.
func workingRole(t board.EngineTicket) api.Role {
	if t.Type == api.TypeStory {
		return api.RolePM
	}
	return api.RoleDeveloper
}

// reviewProject is the project a reviewer session works on: its own, or the
// only project of a ticket with one.
func reviewProject(s board.EngineSession, t board.EngineTicket) string {
	if s.Project == "" && len(t.Projects) == 1 {
		return t.Projects[0].ID
	}
	return s.Project
}

func hasProject(t board.EngineTicket, id string) bool {
	for _, pr := range t.Projects {
		if pr.ID == id {
			return true
		}
	}
	return false
}

func purposeOf(s board.EngineSession) api.Purpose {
	if s.Purpose == "" {
		return api.PurposeWork
	}
	return s.Purpose
}

// slot identifies what a session does: two live sessions never share one.
// The project and round matter for reviewers, the project for export jobs.
type slot struct {
	ticket  int64
	role    api.Role
	purpose api.Purpose
	project string
	round   int
}

// slotOf returns the slot of session s on ticket t.
func slotOf(s board.EngineSession, t *board.EngineTicket) slot {
	k := slot{ticket: s.Ticket, role: s.Role, purpose: purposeOf(s)}
	switch {
	case s.Role == api.RoleReviewer && t != nil:
		k.project, k.round = reviewProject(s, *t), s.ReviewRound
	case s.Role == api.RoleReviewer:
		k.project, k.round = s.Project, s.ReviewRound
	case k.purpose == api.PurposeExport:
		k.project = s.Project
	}
	return k
}

// allowed says whether ticket state still needs session s, and why not.
func (p *planner) allowed(s board.EngineSession) (bool, string) {
	if s.Role == api.RoleCEO {
		if s.Ticket != 0 {
			return false, "a ceo session is not bound to a ticket"
		}
		if !p.in.ceoEnabled {
			return false, "the CEO is switched off"
		}
		return true, ""
	}
	t, ok := p.byID[s.Ticket]
	if !ok || t.Column == api.ColumnDone {
		if ok && t.Resolution == api.ResolutionCancelled {
			return false, "cancelled"
		}
		if ok && t.Resolution == api.ResolutionExported {
			return false, "exported"
		}
		return false, "done"
	}
	working := s.Role == workingRole(*t) && purposeOf(s) == api.PurposeWork
	switch t.Column {
	case api.ColumnPlanning, api.ColumnReady:
		return false, fmt.Sprintf("#%d was moved to %s", t.ID, t.Column)
	case api.ColumnInProgress:
		if working {
			return true, ""
		}
	case api.ColumnReview:
		switch {
		case working:
			return true, ""
		case s.Role == api.RoleReviewer:
			if s.ReviewRound != t.ReviewRound {
				return false, fmt.Sprintf("review round %d is over", s.ReviewRound)
			}
			if !hasProject(*t, reviewProject(s, *t)) {
				return false, fmt.Sprintf("project %q is not a project of #%d", s.Project, t.ID)
			}
			return true, ""
		case purposeOf(s) == api.PurposeExport:
			if t.ReviewStage != api.ReviewStageExporting {
				return false, "the export is over"
			}
			for _, pr := range t.Projects {
				if pr.ID == s.Project {
					if pr.ExportURL != "" {
						return false, "the export of project " + pr.ID + " is done"
					}
					return true, ""
				}
			}
			return false, fmt.Sprintf("project %q is not a project of #%d", s.Project, t.ID)
		}
	}
	return false, fmt.Sprintf("#%d in %s has no work for this %s", t.ID, t.Column, s.Role)
}

// ends proposes ending every live session that the state of its ticket no
// longer allows, every duplicate of a slot (the newest stays), and every
// session whose process is gone. Sessions a worker is still starting, and
// those of tickets a worker is acting on, are left for the next pass.
func (p *planner) ends() {
	newest := map[slot]int{} // index into sessions
	sessions := p.in.snap.Sessions
	for i, s := range sessions {
		if p.inflight(s.Ticket) || p.pendingSession(s.ID) {
			continue
		}
		if ok, _ := p.allowed(s); ok {
			newest[p.slotOf(s)] = i
		}
	}
	for i, s := range sessions {
		if p.inflight(s.Ticket) || p.pendingSession(s.ID) {
			p.keep(s)
			continue
		}
		ok, reason := p.allowed(s)
		if ok {
			if newest[p.slotOf(s)] != i {
				ok, reason = false, "a newer session took over"
			} else if !p.in.processes[s.ID] {
				ok, reason = false, "agent process is gone"
			}
		}
		if ok {
			p.keep(s)
			continue
		}
		p.acts = append(p.acts, action{kind: actEnd, ticket: s.Ticket, role: s.Role, project: s.Project,
			purpose: purposeOf(s), session: s.ID, reason: reason})
	}
	// Starts in flight whose session is not live yet hold a slot too.
	live := map[string]bool{}
	for _, s := range sessions {
		live[s.ID] = true
	}
	for _, st := range p.in.starts {
		if st.role != api.RoleCEO && (st.session == "" || !live[st.session]) {
			p.count++
		}
	}
}

// slotOf returns the slot of a session.
func (p *planner) slotOf(s board.EngineSession) slot { return slotOf(s, p.byID[s.Ticket]) }

// keep records a session that stays live.
func (p *planner) keep(s board.EngineSession) {
	p.kept = append(p.kept, s)
	if s.Role != api.RoleCEO && (s.State == api.SessionStarting || s.State == api.SessionRunning) {
		p.count++
	}
}

// mergesAndCleanups proposes merging approved tickets and cleaning up done
// ones. A cleanup waits until no session of the ticket is live, so no agent
// still works in a folder that is removed.
func (p *planner) mergesAndCleanups() {
	liveOn := map[int64]bool{}
	for _, s := range p.in.snap.Sessions {
		liveOn[s.Ticket] = true
	}
	var cleanups []action
	for _, t := range p.tickets {
		if p.inflight(t.ID) || t.Busy || t.FailedStep != api.FailedNone {
			continue
		}
		switch {
		case t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageMerging:
			p.acts = append(p.acts, action{kind: actMerge, ticket: t.ID})
		case t.Column == api.ColumnDone && !liveOn[t.ID] && slices.ContainsFunc(t.Projects,
			func(pr board.EngineProject) bool { return pr.Worktree != "" }):
			cleanups = append(cleanups, action{kind: actCleanup, ticket: t.ID})
		}
	}
	p.acts = append(p.acts, cleanups...)
}

// startable reports whether t may begin new work apart from the limits and
// the models: in Ready, not waiting for a dependency, no failed step, not
// claimed, no worker on it and, for a task of a story, its story in
// progress. Projects are checked separately.
func (p *planner) startable(t board.EngineTicket) bool {
	if t.Column != api.ColumnReady || t.Waiting || len(t.WaitingFor) > 0 || t.FailedStep != api.FailedNone ||
		t.Busy || p.inflight(t.ID) {
		return false
	}
	if t.Type == api.TypeTask && t.StoryID != 0 {
		story, ok := p.byID[t.StoryID]
		if !ok || story.Column != api.ColumnInProgress {
			return false
		}
	}
	return true
}

// missingProject returns the first project of t that is not available.
func (p *planner) missingProject(t board.EngineTicket) string {
	for _, pr := range t.Projects {
		if !p.available(pr.ID) {
			return pr.ID
		}
	}
	return ""
}

// candidates returns the tickets that may start new work now, in priority
// order: group, then column rank, then number.
func (p *planner) candidates() []board.EngineTicket {
	var out []board.EngineTicket
	for _, t := range p.tickets {
		if p.startable(t) && p.missingProject(t) == "" {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		gi, gj := newWorkGroup(out[i], p.storyColumn(out[i])), newWorkGroup(out[j], p.storyColumn(out[j]))
		if gi != gj {
			return gi < gj
		}
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (p *planner) storyColumn(t board.EngineTicket) api.Column {
	if story, ok := p.byID[t.StoryID]; ok && t.StoryID != 0 {
		return story.Column
	}
	return ""
}

// need is one agent a ticket of started work is missing.
type need struct {
	role    api.Role
	project string
	purpose api.Purpose
}

// needs lists the agents t is missing to go on with started work.
func (p *planner) needs(t board.EngineTicket) []need {
	var out []need
	hasLive := func(match func(s board.EngineSession) bool) bool {
		for _, s := range p.kept {
			if s.Ticket == t.ID && match(s) {
				return true
			}
		}
		return false
	}
	switch t.Column {
	case api.ColumnInProgress:
		role := workingRole(t)
		if !hasLive(func(s board.EngineSession) bool { return s.Role == role && purposeOf(s) == api.PurposeWork }) {
			out = append(out, need{role: role, purpose: api.PurposeWork})
		}
	case api.ColumnReview:
		switch t.ReviewStage {
		case api.ReviewStageAgent:
			for _, pr := range t.Projects {
				if t.Verdicts[pr.ID] != "" {
					continue
				}
				if !hasLive(func(s board.EngineSession) bool {
					return s.Role == api.RoleReviewer && s.ReviewRound == t.ReviewRound && reviewProject(s, t) == pr.ID
				}) {
					out = append(out, need{role: api.RoleReviewer, project: pr.ID, purpose: api.PurposeWork})
				}
			}
		case api.ReviewStageExporting:
			if t.Type == api.TypeTask && t.StoryID != 0 {
				break // a task of a story is never exported
			}
			for _, pr := range t.Projects {
				if pr.ExportURL != "" {
					continue
				}
				if !hasLive(func(s board.EngineSession) bool {
					return purposeOf(s) == api.PurposeExport && s.Project == pr.ID
				}) {
					out = append(out, need{role: api.RoleDeveloper, project: pr.ID, purpose: api.PurposeExport})
				}
			}
		}
	}
	return out
}

// continuations starts every agent that started work is missing. They do
// not look at the limit: see plan.
func (p *planner) continuations() {
	var work []board.EngineTicket
	for _, t := range p.tickets {
		if t.Column == api.ColumnInProgress || t.Column == api.ColumnReview {
			work = append(work, t)
		}
	}
	sort.SliceStable(work, func(i, j int) bool {
		gi, gj := continuationGroup(work[i]), continuationGroup(work[j])
		if gi != gj {
			return gi < gj
		}
		if work[i].Rank != work[j].Rank {
			return work[i].Rank < work[j].Rank
		}
		return work[i].ID < work[j].ID
	})
	for _, t := range work {
		if t.Busy || t.FailedStep != api.FailedNone || p.inflight(t.ID) {
			continue
		}
		p.want[t.ID] = ""
		needs := p.needs(t)
		if len(needs) == 0 {
			continue
		}
		if missing := p.missingProject(t); missing != "" {
			p.want[t.ID] = projectText(missing)
			continue
		}
		for _, n := range needs {
			if !p.hasModel(n.role, n.purpose, t) {
				if p.want[t.ID] == "" {
					p.want[t.ID] = noModelText(n.role)
				}
				continue
			}
			p.cont = append(p.cont, action{kind: actStart, ticket: t.ID, role: n.role, project: n.project, purpose: n.purpose})
			p.count++
		}
	}
}

// newWork starts new work in priority order while the limits allow and
// records why every other startable ticket did not start.
func (p *planner) newWork(blocked map[int64]api.StartBlocked) {
	perStory := map[int64]int{}
	for _, t := range p.tickets {
		if t.Type == api.TypeTask && t.StoryID != 0 &&
			(t.Column == api.ColumnInProgress || (t.Column == api.ColumnReady && (p.inflight(t.ID) || t.Busy))) {
			perStory[t.StoryID]++
		}
	}
	for _, t := range p.tickets {
		if t.Column != api.ColumnReady || t.Busy || t.FailedStep != api.FailedNone || p.inflight(t.ID) {
			continue
		}
		p.want[t.ID] = ""
		if p.startable(t) {
			if missing := p.missingProject(t); missing != "" {
				p.want[t.ID] = projectText(missing)
			}
		}
	}
	for _, t := range p.candidates() {
		role := workingRole(t)
		storyFull := false
		if t.Type == api.TypeTask && t.StoryID != 0 {
			if story := p.byID[t.StoryID]; story.MaxParallel > 0 && perStory[t.StoryID] >= story.MaxParallel {
				storyFull = true
			}
		}
		switch {
		case !p.hasModel(role, api.PurposeWork, t):
			blocked[t.ID] = api.StartBlockedNoModel
		case p.count >= p.in.limit || storyFull:
			blocked[t.ID] = api.StartBlockedSlot
		default:
			p.gated = append(p.gated, action{kind: actStart, ticket: t.ID, role: role, purpose: api.PurposeWork, gated: true})
			p.count++
			if t.StoryID != 0 {
				perStory[t.StoryID]++
			}
		}
	}
}

// ownText reports whether text is an attention text the planner sets on t.
func ownText(t board.EngineTicket, text string) bool {
	if text == "" {
		return false
	}
	for _, r := range []api.Role{api.RolePM, api.RoleDeveloper, api.RoleReviewer} {
		if text == noModelText(r) {
			return true
		}
	}
	for _, pr := range t.Projects {
		if text == projectText(pr.ID) {
			return true
		}
	}
	return false
}

// attention sets the planner's own attention texts where they are wanted,
// never over a text somebody else set, and clears them where they no
// longer apply.
func (p *planner) attention() {
	for _, t := range p.tickets {
		want, decided := p.want[t.ID]
		if !decided || t.Attention == want {
			continue
		}
		own := ownText(t, t.Attention)
		if (want != "" && (t.Attention == "" || own)) || (want == "" && own) {
			p.attn = append(p.attn, action{kind: actAttention, ticket: t.ID, reason: want})
		}
	}
}

// ordered returns every action in execution order.
func (p *planner) ordered() []action {
	out := slices.Clone(p.acts)
	out = append(out, p.attn...)
	out = append(out, p.cont...)
	return append(out, p.gated...)
}
