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
	// actUsageStop stops a live session whose model reached its hard usage
	// limit: a working agent is asked for a handoff first.
	actUsageStop
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
	case actUsageStop:
		return "usage stop"
	}
	return fmt.Sprintf("action(%d)", int(k))
}

// action is one step the planner asks the engine to take.
//
// For actStart: role and purpose say which agent; project is the project a
// reviewer or an export job works on and empty for the working agent of a
// ticket (the developer of a task, the pm of a story), which works on all of
// the ticket's projects. For actEnd: session and reason. For actAttention:
// reason. For actUsageStop: session.
type action struct {
	kind           actionKind
	ticket         int64
	role           api.Role
	project        string
	purpose        api.Purpose
	gated          bool
	session        string
	reason         string
	attentionOwner board.AttentionOwner
	endKind        api.SessionEndKind
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
	case actUsageStop:
		return fmt.Sprintf("usage stop %s of #%d", a.session, a.ticket)
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
	// limits are how many tasks and stories may be in work when new work
	// starts.
	limits api.LimitSettings
	// stopping is set while omo shuts down: no agent starts.
	stopping bool
	// ceoEnabled is false when the CEO is switched off.
	ceoEnabled bool
	// inflight holds the tickets a worker is acting on (a start, a merge or
	// a cleanup). The planner proposes nothing for them and ends none of
	// their sessions; the worker wakes the engine when it is done.
	inflight map[int64]bool
	// starts lists every agent start a worker is carrying out, the CEO's
	// included; their sessions are left alone until the start is over.
	starts []pendingStart
	// processes holds the ids of the sessions whose agent process is alive.
	processes map[string]bool
	// available reports whether a project is registered and enabled; nil
	// means every project is.
	available func(project string) bool
	// hasModel reports whether an agent of role with purpose on t could
	// start now (startOK), waits for usage (startUsage) or has no model at
	// all (startNoModel); nil means it always could.
	hasModel func(role api.Role, purpose api.Purpose, t board.EngineTicket) startCheck
	// usageStops lists the live sessions whose model reached its hard usage
	// limit and that are not being stopped yet.
	usageStops []string
	// exportOver holds, per ticket that is exporting, the projects whose
	// export is over without a pull request (left out or failed): they get
	// no export job, and a job still running for one is ended.
	exportOver map[int64]map[string]bool
}

// continuationGroup orders the continuations of started work: reviews and
// exports of finished tickets (1), tasks in progress (2), stories in
// progress (3). The order only decides which starts first; none of them
// waits for a slot: their ticket holds one already.
func continuationGroup(t board.EngineTicket) int {
	switch {
	case t.Column == api.ColumnReview:
		return 1
	case t.Type == api.TypeTask:
		return 2
	}
	return 3
}

// newWorkGroup orders new work: tasks of stories in
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
// not in progress, a missing project). Tickets in progress or in review
// whose next agent waits for usage get StartBlockedUsage, and "" once
// nothing they need waits for usage.
//
// Slots. A ticket holds a slot of its kind (task or story) from the moment
// it leaves Ready For Dev for In Progress until it is done: in In Progress
// and in Review, whatever its agents do (work, wait in omo ai wait, wait for
// an answer or for usage, or none running at all after a usage stop or a
// restart), and while a gated start is claiming it in Ready For Dev. Done
// (completed, cancelled or exported) and a move back to Ready For Dev or
// Planning give the slot back. The count is read from the board on every
// pass, so nothing is kept between passes or restored after a restart. A
// task counts against limits.tasks whether or not it belongs to a story;
// its story's own max_parallel limits it further. The CEO never counts.
//
// Why the scheduler cannot deadlock:
//
//   - Started work is every ticket in In Progress or Review, and it already
//     holds its slot. Every agent such a ticket needs (its developer or
//     product manager again, a reviewer, an export job) is started without
//     looking at the limits. So no started ticket ever waits for a free slot,
//     and the slots can never all be held by tickets that cannot finish
//     because their reviewer, product manager or developer cannot run. (Only
//     a missing model or project can stop such a start, and then the ticket
//     is flagged for a person.)
//   - The limits only delay new work: a developer for a task in Ready For
//     Dev or a product manager for a story in Ready For Dev. Such a start
//     waits until fewer tickets of its kind are in work than its limit
//     allows. The count falls whenever a ticket of that kind is done or is
//     moved back, and no ticket in work needs a new start of another ticket
//     to get there, except through dependencies and a story's tasks.
//   - Dependencies cannot form a cycle (the board refuses them), and a ticket
//     that waits for a dependency stays in Ready For Dev without a slot. A
//     task of a story needs only its story in progress; a story in progress
//     holds a story slot, never a task slot, so the tasks it waits for compete
//     only with other tasks, every one of which can finish without a new
//     slot. A story with no task left to do hands in as before.
//   - So the slots stay taken only by work that does not finish: an agent
//     that stalls (the health check flags it), a review or a question that
//     waits for the user, or a failed step. Each is shown to a person, and
//     their retry, answer, approval or cancel frees the slot in the end.
//     Merges hold no slot of their own and pause does not stop them.
//   - Nothing starves: new work starts in the order of its group and its
//     place in the column, which the user controls; a ticket held back by
//     its limit does not hold back a ticket of the other kind. Lowering a
//     limit below the tickets in work ends nothing: they finish, and new work
//     waits until the count is below the new limit.
func plan(in planInput) ([]action, api.SchedulerState, map[int64]api.StartBlocked) {
	p := newPlanner(in)
	p.ends()
	p.usageStops()
	p.mergesAndCleanups()
	blocked := map[int64]api.StartBlocked{}
	for _, t := range p.tickets {
		if t.Column == api.ColumnReady {
			blocked[t.ID] = api.StartBlockedNone
		}
	}
	p.load = p.inWork()
	if in.snap.Paused || in.stopping {
		for _, t := range p.candidates() {
			blocked[t.ID] = api.StartBlockedPaused
		}
		return p.acts, p.state(), blocked
	}
	p.continuations(blocked)
	p.newWork(blocked)
	p.attention()
	return p.ordered(), p.state(), blocked
}

// planner holds the indexes of one pass.
type planner struct {
	in      planInput
	tickets []board.EngineTicket
	byID    map[int64]*board.EngineTicket
	// kept are the live sessions that stay after this pass's ends.
	kept []board.EngineSession
	// load counts the tickets in work, the gated starts of this pass
	// included.
	load slotCount
	acts []action
	// cont and gated collect the starts; attn the attention changes; ends
	// lateEnds the ends decided while planning the starts, which run before
	// them.
	cont, gated, attn, lateEnds []action
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

func (p *planner) hasModel(role api.Role, purpose api.Purpose, t board.EngineTicket) startCheck {
	if p.in.hasModel == nil {
		return startOK
	}
	return p.in.hasModel(role, purpose, t)
}

// usageStops proposes stopping every session of usageStops that stays live
// after this pass's ends.
func (p *planner) usageStops() {
	for _, s := range p.kept {
		if slices.Contains(p.in.usageStops, s.ID) {
			p.acts = append(p.acts, action{kind: actUsageStop, ticket: s.Ticket, role: s.Role, project: s.Project,
				purpose: purposeOf(s), session: s.ID})
		}
	}
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

// reviewsKey reports whether key is one of t's review keys: a project of
// t, or the ticket itself ("") for a task without a project (see
// board.EngineTicket.ReviewKeys).
func reviewsKey(t board.EngineTicket, key string) bool {
	return slices.Contains(t.ReviewKeys(), key)
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
//
// Agents live as long as their ticket's work goes on, not only while they
// have something to do: the working agent (the developer of a task, the
// product manager of a story) from In Progress until the ticket is done,
// parked while the ticket is in review, so a rejection, a merge conflict, a
// question or a mention wakes the same session with its context; a reviewer
// from its start until the next review round of its project begins (see
// newerReviewer and planner.continuations) or the ticket is done, parked
// after its verdict, so questions about its review reach it. Only a move
// back to Planning or Ready For Dev, a project taken off the ticket, or the
// hard reasons decided elsewhere (a lost process, a usage stop, a stop, a
// retry) end them earlier.
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
		switch {
		case ok && t.Resolution == api.ResolutionCancelled:
			return false, fmt.Sprintf("#%d was cancelled", s.Ticket)
		case ok && t.Resolution == api.ResolutionExported:
			return false, fmt.Sprintf("#%d was exported", s.Ticket)
		}
		return false, fmt.Sprintf("#%d is done", s.Ticket)
	}
	working := s.Role == workingRole(*t) && purposeOf(s) == api.PurposeWork
	switch t.Column {
	case api.ColumnPlanning, api.ColumnReady:
		return false, fmt.Sprintf("#%d was moved to %s", t.ID, columnWords(t.Column))
	case api.ColumnInProgress, api.ColumnReview:
		switch {
		case working:
			return true, ""
		case s.Role == api.RoleReviewer:
			project := reviewProject(s, *t)
			if !reviewsKey(*t, project) {
				return false, fmt.Sprintf("project %q is not a project of #%d", s.Project, t.ID)
			}
			if p.newerReviewer(s, *t) {
				return false, newRoundText(t.ID)
			}
			return true, ""
		case t.Column == api.ColumnReview && purposeOf(s) == api.PurposeExport:
			if t.ReviewStage != api.ReviewStageExporting {
				return false, "the export is over"
			}
			for _, pr := range t.Projects {
				if pr.ID == s.Project {
					if pr.ExportURL != "" || p.in.exportOver[t.ID][pr.ID] {
						return false, "the export of project " + pr.ID + " is done"
					}
					return true, ""
				}
			}
			return false, fmt.Sprintf("project %q is not a project of #%d", s.Project, t.ID)
		}
	}
	return false, fmt.Sprintf("#%d in %s has no work for this %s", t.ID, columnWords(t.Column), roleWords(s.Role))
}

// keptAgent reports whether session s is kept on t without work of its own
// to do: the working agent of a ticket in review, parked until a rejection,
// a merge conflict, a question or a mention wakes it, and a reviewer that
// owes no verdict (its verdict is in, or its round is over), parked for
// questions about its review. Waiting is all a kept agent has to do.
func keptAgent(s board.EngineSession, t board.EngineTicket) bool {
	switch {
	case s.Role == api.RoleReviewer:
		owes := t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageAgent && s.ReviewRound == t.ReviewRound &&
			t.Verdicts[reviewProject(s, t)] == ""
		return !owes
	case purposeOf(s) == api.PurposeWork && s.Role == workingRole(t):
		return t.Column == api.ColumnReview
	}
	return false
}

// newRoundText is the end reason of a reviewer whose project's next review
// round began.
func newRoundText(n int64) string { return fmt.Sprintf("a new review round of #%d began", n) }

// newerReviewer reports whether a reviewer of a later review round than
// reviewer s works on s's project of t.
func (p *planner) newerReviewer(s board.EngineSession, t board.EngineTicket) bool {
	project := reviewProject(s, t)
	for _, o := range p.in.snap.Sessions {
		if o.Role == api.RoleReviewer && o.Ticket == s.Ticket && o.ReviewRound > s.ReviewRound &&
			reviewProject(o, t) == project {
			return true
		}
	}
	return false
}

// columnWords names a column as the board's title does; end reasons are read
// by the user, so they never carry the identifier.
func columnWords(c api.Column) string {
	switch c {
	case api.ColumnPlanning:
		return "Planning"
	case api.ColumnReady:
		return "Ready For Dev"
	case api.ColumnInProgress:
		return "In Progress"
	case api.ColumnReview:
		return "Review"
	case api.ColumnDone:
		return "Done"
	}
	return string(c)
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
		endKind := p.sessionEndKind(s)
		if ok {
			if newest[p.slotOf(s)] != i {
				ok, reason, endKind = false, "a newer session took over", api.SessionEndStopped
			} else if !p.in.processes[s.ID] {
				ok, reason, endKind = false, "agent process is gone", api.SessionEndCrashed
			}
		}
		if ok {
			p.keep(s)
			continue
		}
		p.acts = append(p.acts, action{kind: actEnd, ticket: s.Ticket, role: s.Role, project: s.Project,
			purpose: purposeOf(s), session: s.ID, reason: reason, endKind: endKind})
	}
}

// sessionEndKind classifies a planned end from board state, before rendering
// the explanation. No word in the explanation changes this outcome.
func (p *planner) sessionEndKind(s board.EngineSession) api.SessionEndKind {
	t := p.byID[s.Ticket]
	if s.Role != api.RoleCEO && (t == nil || t.Column == api.ColumnDone) {
		if t != nil && t.Resolution == api.ResolutionCancelled {
			return api.SessionEndStopped
		}
		return api.SessionEndFinished
	}
	if t != nil && s.Role == api.RoleReviewer && reviewsKey(*t, reviewProject(s, *t)) && p.newerReviewer(s, *t) {
		return api.SessionEndFinished
	}
	if t != nil && t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageExporting && purposeOf(s) == api.PurposeExport {
		for _, pr := range t.Projects {
			if pr.ID == s.Project && (pr.ExportURL != "" || p.in.exportOver[t.ID][pr.ID]) {
				return api.SessionEndFinished
			}
		}
	}
	return api.SessionEndStopped
}

// slotOf returns the slot of a session.
func (p *planner) slotOf(s board.EngineSession) slot { return slotOf(s, p.byID[s.Ticket]) }

// keep records a session that stays live.
func (p *planner) keep(s board.EngineSession) {
	p.kept = append(p.kept, s)
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
	if t.StartGate == board.StartGateOpen {
		// A plugin has not given its go-ahead to the start. The board
		// shows start_blocked "gate" by itself; the ticket takes no slot
		// meanwhile.
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
			for _, key := range t.ReviewKeys() {
				if t.Verdicts[key] != "" {
					continue
				}
				if !hasLive(func(s board.EngineSession) bool {
					return s.Role == api.RoleReviewer && s.ReviewRound == t.ReviewRound && reviewProject(s, t) == key
				}) {
					out = append(out, need{role: api.RoleReviewer, project: key, purpose: api.PurposeWork})
				}
			}
		case api.ReviewStageExporting:
			if t.Type == api.TypeTask && t.StoryID != 0 {
				break // a task of a story is never exported
			}
			for _, pr := range t.Projects {
				if pr.ExportURL != "" || p.in.exportOver[t.ID][pr.ID] {
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
// not look at the limit: see plan. A ticket whose missing agent waits for
// usage is marked in blocked.
func (p *planner) continuations(blocked map[int64]api.StartBlocked) {
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
		blocked[t.ID] = api.StartBlockedNone
		needs := p.needs(t)
		if len(needs) == 0 {
			continue
		}
		if missing := p.missingProject(t); missing != "" {
			p.want[t.ID] = projectText(missing)
			continue
		}
		for _, n := range needs {
			switch p.hasModel(n.role, n.purpose, t) {
			case startNoModel:
				if p.want[t.ID] == "" {
					p.want[t.ID] = noModelText(n.role)
				}
				continue
			case startUsage:
				blocked[t.ID] = api.StartBlockedUsage
				continue
			}
			if n.role == api.RoleReviewer {
				p.endEarlierReviewers(t, n.project)
			}
			p.cont = append(p.cont, action{kind: actStart, ticket: t.ID, role: n.role, project: n.project, purpose: n.purpose})
		}
	}
}

// endEarlierReviewers ends the reviewers of earlier review rounds of t that
// stay for project: the reviewer of the round that starts now takes their
// place. A usage stop planned for one of them is dropped; the end
// supersedes it.
func (p *planner) endEarlierReviewers(t board.EngineTicket, project string) {
	kept := p.kept[:0]
	for _, s := range p.kept {
		if s.Role != api.RoleReviewer || s.Ticket != t.ID || s.ReviewRound >= t.ReviewRound || reviewProject(s, t) != project {
			kept = append(kept, s)
			continue
		}
		p.acts = slices.DeleteFunc(p.acts, func(a action) bool { return a.kind == actUsageStop && a.session == s.ID })
		p.lateEnds = append(p.lateEnds, action{kind: actEnd, ticket: s.Ticket, role: s.Role, project: s.Project,
			purpose: purposeOf(s), session: s.ID, reason: newRoundText(t.ID), endKind: api.SessionEndFinished})
	}
	p.kept = kept
}

// slotCount is how many tickets hold a slot: tasks, stories, and per story
// its tasks.
type slotCount struct {
	tasks, stories int
	perStory       map[int64]int
}

// holdsSlot reports whether t holds a slot of its kind: it is in In
// Progress or Review, or a gated start is claiming it in Ready For Dev.
func (p *planner) holdsSlot(t board.EngineTicket) bool {
	switch t.Column {
	case api.ColumnInProgress, api.ColumnReview:
		return true
	case api.ColumnReady:
		return t.Busy || p.inflight(t.ID)
	}
	return false
}

// inWork counts the tickets that hold a slot, from the board alone.
func (p *planner) inWork() slotCount {
	c := slotCount{perStory: map[int64]int{}}
	for _, t := range p.tickets {
		if p.holdsSlot(t) {
			c.add(t)
		}
	}
	return c
}

// add counts t as in work.
func (c *slotCount) add(t board.EngineTicket) {
	if t.Type == api.TypeStory {
		c.stories++
		return
	}
	c.tasks++
	if t.StoryID != 0 {
		c.perStory[t.StoryID]++
	}
}

// state is the scheduler state after this pass's starts.
func (p *planner) state() api.SchedulerState {
	return api.SchedulerState{
		Tasks:   api.SlotLoad{InWork: p.load.tasks, Limit: p.in.limits.Tasks},
		Stories: api.SlotLoad{InWork: p.load.stories, Limit: p.in.limits.Stories},
		Paused:  p.in.snap.Paused,
	}
}

// full reports whether t may not start because its kind's limit, or for a
// task of a story its story's max_parallel, is reached.
func (p *planner) full(t board.EngineTicket) bool {
	if t.Type == api.TypeStory {
		return p.load.stories >= p.in.limits.Stories
	}
	if p.load.tasks >= p.in.limits.Tasks {
		return true
	}
	if t.StoryID != 0 {
		if story := p.byID[t.StoryID]; story != nil && story.MaxParallel > 0 && p.load.perStory[t.StoryID] >= story.MaxParallel {
			return true
		}
	}
	return false
}

// newWork starts new work in priority order while the limits allow and
// records why every other startable ticket did not start.
func (p *planner) newWork(blocked map[int64]api.StartBlocked) {
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
		check := p.hasModel(role, api.PurposeWork, t)
		switch {
		case check == startNoModel:
			blocked[t.ID] = api.StartBlockedNoModel
		case check == startUsage:
			blocked[t.ID] = api.StartBlockedUsage
		case p.full(t):
			blocked[t.ID] = api.StartBlockedSlot
		default:
			p.gated = append(p.gated, action{kind: actStart, ticket: t.ID, role: role, purpose: api.PurposeWork, gated: true})
			p.load.add(t)
		}
	}
}

// attention sets the planner's own attention texts where they are wanted,
// never over a text somebody else set, and clears them where they no
// longer apply.
func (p *planner) attention() {
	for _, t := range p.tickets {
		want, decided := p.want[t.ID]
		if !decided || (t.Attention == want && (want == "" || t.AttentionOwner == board.AttentionPlanner)) {
			continue
		}
		own := t.AttentionOwner == board.AttentionPlanner
		if (want != "" && (t.Attention == "" || own)) || (want == "" && own) {
			p.attn = append(p.attn, action{kind: actAttention, ticket: t.ID, reason: want, attentionOwner: board.AttentionPlanner})
		}
	}
}

// ordered returns every action in execution order.
func (p *planner) ordered() []action {
	out := slices.Clone(p.lateEnds)
	out = append(out, p.acts...)
	out = append(out, p.attn...)
	out = append(out, p.cont...)
	return append(out, p.gated...)
}
