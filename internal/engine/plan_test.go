package engine

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// world builds snapshots for planner tests.
type world struct {
	snap  board.EngineSnapshot
	limit int
	in    planInput
	seq   int
}

func newWorld(limit int) *world {
	w := &world{limit: limit}
	w.in = planInput{
		limit:      limit,
		ceoEnabled: true,
		inflight:   map[int64]bool{},
		processes:  map[string]bool{},
	}
	return w
}

func (w *world) ticket(id int64, typ api.TicketType, col api.Column, rank float64, opts ...func(*board.EngineTicket)) *board.EngineTicket {
	t := board.EngineTicket{
		ID: id, Type: typ, Title: fmt.Sprintf("ticket %d", id), Column: col, Rank: rank,
		DependsOn: []int64{}, WaitingFor: []api.Waiting{}, Verdicts: map[string]string{},
		Projects: []board.EngineProject{{ID: "p", Base: "main"}}, Review: api.ReviewAgent, WhenApproved: api.WhenApprovedMerge,
	}
	for _, o := range opts {
		o(&t)
	}
	w.snap.Tickets = append(w.snap.Tickets, t)
	return &w.snap.Tickets[len(w.snap.Tickets)-1]
}

func inStory(s int64) func(*board.EngineTicket) { return func(t *board.EngineTicket) { t.StoryID = s } }
func stage(st api.ReviewStage) func(*board.EngineTicket) {
	return func(t *board.EngineTicket) { t.ReviewStage = st; t.ReviewRound = max(t.ReviewRound, 1) }
}
func round(r int) func(*board.EngineTicket) { return func(t *board.EngineTicket) { t.ReviewRound = r } }
func projects(ids ...string) func(*board.EngineTicket) {
	return func(t *board.EngineTicket) {
		t.Projects = nil
		for _, id := range ids {
			t.Projects = append(t.Projects, board.EngineProject{ID: id, Base: "main"})
		}
	}
}
func waitingOn(n int64) func(*board.EngineTicket) {
	return func(t *board.EngineTicket) {
		t.DependsOn = append(t.DependsOn, n)
		t.WaitingFor = append(t.WaitingFor, api.Waiting{Ticket: int(n), Reason: api.WaitingNotDone})
		t.Waiting = true
	}
}

// session adds a live session with a process.
func (w *world) session(role api.Role, ticket int64, st api.SessionState, opts ...func(*board.EngineSession)) string {
	w.seq++
	s := board.EngineSession{ID: fmt.Sprintf("s%d", w.seq), Role: role, Ticket: ticket, State: st, Purpose: api.PurposeWork}
	for _, o := range opts {
		o(&s)
	}
	w.snap.Sessions = append(w.snap.Sessions, s)
	w.in.processes[s.ID] = true
	return s.ID
}

func sessRound(r int) func(*board.EngineSession) {
	return func(s *board.EngineSession) { s.ReviewRound = r }
}
func sessProject(p string) func(*board.EngineSession) {
	return func(s *board.EngineSession) { s.Project = p }
}
func sessPurpose(p api.Purpose) func(*board.EngineSession) {
	return func(s *board.EngineSession) { s.Purpose = p }
}

func (w *world) plan(t *testing.T) ([]action, api.SchedulerState, map[int64]api.StartBlocked) {
	t.Helper()
	in := w.in
	in.snap = w.snap
	acts, st, blocked := plan(in)
	acts2, st2, blocked2 := plan(in)
	if !reflect.DeepEqual(acts, acts2) || st != st2 || !reflect.DeepEqual(blocked, blocked2) {
		t.Fatalf("plan is not deterministic:\n%v\n%v", acts, acts2)
	}
	checkOrder(t, acts)
	return acts, st, blocked
}

// checkOrder requires ends first, then merges, cleanups and attention, then
// continuations, then gated starts.
func checkOrder(t *testing.T, acts []action) {
	t.Helper()
	phase := func(a action) int {
		switch {
		case a.kind == actEnd:
			return 0
		case a.kind == actStart && a.gated:
			return 3
		case a.kind == actStart:
			return 2
		}
		return 1
	}
	for i := 1; i < len(acts); i++ {
		if phase(acts[i]) < phase(acts[i-1]) {
			t.Fatalf("actions out of order: %v", acts)
		}
	}
}

func starts(acts []action) []action {
	var out []action
	for _, a := range acts {
		if a.kind == actStart {
			out = append(out, a)
		}
	}
	return out
}

func ofKind(acts []action, k actionKind) []action {
	var out []action
	for _, a := range acts {
		if a.kind == k {
			out = append(out, a)
		}
	}
	return out
}

func TestPlanContinuationsIgnoreFullSlots(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeTask, api.ColumnInProgress, 1)
	w.session(api.RoleDeveloper, 1, api.SessionRunning)
	w.ticket(2, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageAgent))
	w.ticket(3, api.TypeTask, api.ColumnReady, 1)
	acts, st, blocked := w.plan(t)
	ss := starts(acts)
	if len(ss) != 1 || ss[0].ticket != 2 || ss[0].role != api.RoleReviewer || ss[0].gated || ss[0].project != "p" {
		t.Fatalf("starts = %v", ss)
	}
	if blocked[3] != api.StartBlockedSlot {
		t.Fatalf("start_blocked[3] = %q", blocked[3])
	}
	if st.Running != 2 || st.Limit != 1 || st.Paused {
		t.Fatalf("state = %+v", st)
	}
}

func TestPlanGroupsAndRankOrder(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeTask, api.ColumnReady, 1) // standalone, top of the column
	w.ticket(2, api.TypeStory, api.ColumnInProgress, 1)
	w.session(api.RolePM, 2, api.SessionWaiting)
	w.ticket(3, api.TypeTask, api.ColumnReady, 5, inStory(2))
	acts, _, blocked := w.plan(t)
	ss := starts(acts)
	if len(ss) != 1 || ss[0].ticket != 3 || !ss[0].gated || ss[0].role != api.RoleDeveloper {
		t.Fatalf("starts = %v", ss)
	}
	if blocked[1] != api.StartBlockedSlot || blocked[3] != "" {
		t.Fatalf("blocked = %v", blocked)
	}

	// Within a group the column rank decides, then the number.
	w = newWorld(1)
	w.ticket(4, api.TypeTask, api.ColumnReady, 3)
	w.ticket(5, api.TypeTask, api.ColumnReady, 2)
	w.ticket(6, api.TypeTask, api.ColumnReady, 2)
	acts, _, blocked = w.plan(t)
	if ss := starts(acts); len(ss) != 1 || ss[0].ticket != 5 {
		t.Fatalf("starts = %v", ss)
	}
	if blocked[4] != api.StartBlockedSlot || blocked[6] != api.StartBlockedSlot {
		t.Fatalf("blocked = %v", blocked)
	}

	// With room for two, both start in priority order.
	w.in.limit = 2
	acts, _, _ = w.plan(t)
	if ss := starts(acts); len(ss) != 2 || ss[0].ticket != 5 || ss[1].ticket != 6 {
		t.Fatalf("starts = %v", ss)
	}
}

func TestPlanGroupFunctions(t *testing.T) {
	task := board.EngineTicket{ID: 1, Type: api.TypeTask, StoryID: 2}
	if newWorkGroup(task, api.ColumnInProgress) != 2 || newWorkGroup(task, api.ColumnReady) != 3 {
		t.Fatal("task of a story")
	}
	if newWorkGroup(board.EngineTicket{Type: api.TypeTask}, "") != 3 || newWorkGroup(board.EngineTicket{Type: api.TypeStory}, "") != 3 {
		t.Fatal("standalone task or story")
	}
	review := board.EngineTicket{Type: api.TypeStory, Column: api.ColumnReview}
	work := board.EngineTicket{Type: api.TypeTask, Column: api.ColumnInProgress}
	story := board.EngineTicket{Type: api.TypeStory, Column: api.ColumnInProgress}
	if !(continuationGroup(review) < continuationGroup(work) && continuationGroup(work) < continuationGroup(story)) {
		t.Fatal("continuation groups out of order")
	}
}

func TestPlanPerStoryLimit(t *testing.T) {
	w := newWorld(10)
	w.ticket(1, api.TypeStory, api.ColumnInProgress, 1, func(t *board.EngineTicket) { t.MaxParallel = 1 })
	w.session(api.RolePM, 1, api.SessionWaiting)
	w.ticket(2, api.TypeTask, api.ColumnInProgress, 1, inStory(1))
	w.session(api.RoleDeveloper, 2, api.SessionRunning)
	w.ticket(3, api.TypeTask, api.ColumnReady, 1, inStory(1))
	w.ticket(4, api.TypeTask, api.ColumnReady, 2, inStory(1))
	acts, _, blocked := w.plan(t)
	if ss := starts(acts); len(ss) != 0 {
		t.Fatalf("starts = %v", ss)
	}
	if blocked[3] != api.StartBlockedSlot || blocked[4] != api.StartBlockedSlot {
		t.Fatalf("blocked = %v", blocked)
	}

	// The task in progress moves to review: one slot of the story is free.
	w.snap.Tickets[1].Column = api.ColumnReview
	w.snap.Tickets[1].ReviewStage = api.ReviewStageHuman
	w.snap.Sessions[1].State = api.SessionWaiting
	acts, _, blocked = w.plan(t)
	if ss := starts(acts); len(ss) != 1 || ss[0].ticket != 3 {
		t.Fatalf("starts = %v", ss)
	}
	if blocked[3] != "" || blocked[4] != api.StartBlockedSlot {
		t.Fatalf("blocked = %v", blocked)
	}

	// A task of the story whose start is in flight counts against the limit.
	w.in.inflight[3] = true
	w.in.starts = []pendingStart{{ticket: 3, role: api.RoleDeveloper}}
	acts, _, blocked = w.plan(t)
	if ss := starts(acts); len(ss) != 0 || blocked[4] != api.StartBlockedSlot || blocked[3] != "" {
		t.Fatalf("starts = %v, blocked = %v", ss, blocked)
	}
}

func TestPlanWaitingSessionsHoldNoSlot(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeStory, api.ColumnInProgress, 1)
	w.session(api.RolePM, 1, api.SessionWaiting)
	w.ticket(2, api.TypeTask, api.ColumnReady, 1, inStory(1))
	w.session(api.RoleCEO, 0, api.SessionRunning) // the CEO never counts
	acts, st, _ := w.plan(t)
	if ss := starts(acts); len(ss) != 1 || ss[0].ticket != 2 || !ss[0].gated {
		t.Fatalf("starts = %v", ss)
	}
	if st.Running != 1 {
		t.Fatalf("running = %d", st.Running)
	}
}

func TestPlanCountsStartsInFlight(t *testing.T) {
	w := newWorld(2)
	w.ticket(1, api.TypeTask, api.ColumnInProgress, 1)
	w.ticket(2, api.TypeTask, api.ColumnReady, 1)
	w.ticket(3, api.TypeTask, api.ColumnReady, 2)
	w.ticket(4, api.TypeTask, api.ColumnReady, 3)
	// Ticket 1: a continuation whose session exists but has no process yet;
	// ticket 2: a gated start that has not created its session.
	id := w.session(api.RoleDeveloper, 1, api.SessionStarting)
	delete(w.in.processes, id)
	w.in.inflight[1], w.in.inflight[2] = true, true
	w.in.starts = []pendingStart{{ticket: 1, role: api.RoleDeveloper, session: id}, {ticket: 2, role: api.RoleDeveloper}}
	acts, st, blocked := w.plan(t)
	if len(acts) != 0 {
		t.Fatalf("actions = %v", acts)
	}
	if st.Running != 2 || blocked[3] != api.StartBlockedSlot || blocked[4] != api.StartBlockedSlot || blocked[2] != "" {
		t.Fatalf("state = %+v, blocked = %v", st, blocked)
	}
	// A start whose session has already ended again still counts until the
	// worker is done; a CEO start never counts.
	w.snap.Sessions = nil
	w.in.starts = []pendingStart{{ticket: 1, role: api.RoleDeveloper, session: id}, {role: api.RoleCEO}}
	w.in.inflight = map[int64]bool{1: true}
	acts, st, _ = w.plan(t)
	if ss := starts(acts); len(ss) != 1 || ss[0].ticket != 2 || st.Running != 2 {
		t.Fatalf("starts = %v, state = %+v", ss, st)
	}
}

func TestPlanPausedStartsNothingButMerges(t *testing.T) {
	w := newWorld(5)
	w.snap.Paused = true
	w.ticket(1, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageMerging))
	w.ticket(2, api.TypeTask, api.ColumnReview, 2, stage(api.ReviewStageAgent))
	w.ticket(3, api.TypeTask, api.ColumnReady, 1)
	w.ticket(4, api.TypeTask, api.ColumnReady, 2, waitingOn(1))
	w.ticket(5, api.TypeTask, api.ColumnInProgress, 1) // its developer is gone
	w.in.hasModel = func(api.Role, api.Purpose, board.EngineTicket) bool { return false }
	acts, st, blocked := w.plan(t)
	if len(acts) != 1 || acts[0].kind != actMerge || acts[0].ticket != 1 {
		t.Fatalf("actions = %v", acts)
	}
	if blocked[3] != api.StartBlockedPaused || blocked[4] != "" {
		t.Fatalf("blocked = %v", blocked)
	}
	if !st.Paused {
		t.Fatal("state not paused")
	}

	// Stopping behaves the same.
	w.snap.Paused = false
	w.in.stopping = true
	acts, _, blocked = w.plan(t)
	if len(starts(acts)) != 0 || blocked[3] != api.StartBlockedPaused {
		t.Fatalf("stopping: actions = %v, blocked = %v", acts, blocked)
	}
}

func TestPlanEndsSessionsOnInactiveTickets(t *testing.T) {
	w := newWorld(5)
	w.ticket(1, api.TypeTask, api.ColumnReady, 1)
	dev := w.session(api.RoleDeveloper, 1, api.SessionWaiting)
	w.ticket(2, api.TypeTask, api.ColumnReview, 1, round(2), func(t *board.EngineTicket) { t.ReviewStage = api.ReviewStageAgent })
	oldRev := w.session(api.RoleReviewer, 2, api.SessionWaiting, sessRound(1), sessProject("p"))
	w.ticket(3, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageHuman))
	export := w.session(api.RoleDeveloper, 3, api.SessionWaiting, sessPurpose(api.PurposeExport), sessProject("p"))
	ceo := w.session(api.RoleCEO, 0, api.SessionRunning)
	w.ticket(4, api.TypeTask, api.ColumnInProgress, 1)
	gone := w.session(api.RoleDeveloper, 4, api.SessionRunning)
	delete(w.in.processes, gone)
	w.ticket(5, api.TypeTask, api.ColumnDone, 1, func(t *board.EngineTicket) { t.Resolution = api.ResolutionCancelled })
	cancelled := w.session(api.RoleDeveloper, 5, api.SessionRunning)
	notInSnap := w.session(api.RoleDeveloper, 99, api.SessionWaiting)
	w.in.ceoEnabled = false

	acts, _, _ := w.plan(t)
	ends := map[string]string{}
	for _, a := range ofKind(acts, actEnd) {
		ends[a.session] = a.reason
	}
	for _, id := range []string{dev, oldRev, export, ceo, gone, cancelled, notInSnap} {
		if ends[id] == "" {
			t.Errorf("session %s not ended: %v", id, acts)
		}
	}
	if ends[gone] != "agent process is gone" || ends[cancelled] != "cancelled" {
		t.Fatalf("reasons = %v", ends)
	}
	// Ends come first; the new round's reviewer and the developer of #4
	// start after them, and #1 starts again only after its session ended.
	if len(ends) != 7 {
		t.Fatalf("ends = %v", ends)
	}
	ss := starts(acts)
	want := []string{"2 reviewer", "4 developer", "1 developer"}
	var got []string
	for _, s := range ss {
		got = append(got, fmt.Sprintf("%d %s", s.ticket, s.role))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("starts = %v, want %v", got, want)
	}
	for _, s := range ss {
		if s.ticket == 2 && s.purpose != api.PurposeWork {
			t.Fatal("reviewer purpose")
		}
	}
}

func TestPlanKeepsAllowedSessions(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeStory, api.ColumnReview, 1, projects("a", "b"), stage(api.ReviewStageAgent))
	w.session(api.RolePM, 1, api.SessionWaiting)
	w.session(api.RoleReviewer, 1, api.SessionRunning, sessRound(1), sessProject("a"))
	w.session(api.RoleReviewer, 1, api.SessionWaiting, sessRound(1), sessProject("b"))
	w.ticket(2, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageExporting))
	w.session(api.RoleDeveloper, 2, api.SessionWaiting)
	w.session(api.RoleDeveloper, 2, api.SessionRunning, sessPurpose(api.PurposeExport), sessProject("p"))
	w.ticket(3, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageMerging))
	w.session(api.RoleReviewer, 3, api.SessionWaiting, sessRound(1)) // single project: "" is that project
	w.session(api.RoleCEO, 0, api.SessionRunning)
	acts, _, _ := w.plan(t)
	if len(ofKind(acts, actEnd)) != 0 || len(starts(acts)) != 0 {
		t.Fatalf("actions = %v", acts)
	}
}

func TestPlanEndsDuplicateSessionsKeepingNewest(t *testing.T) {
	w := newWorld(5)
	w.ticket(1, api.TypeTask, api.ColumnInProgress, 1)
	old := w.session(api.RoleDeveloper, 1, api.SessionRunning)
	w.session(api.RoleDeveloper, 1, api.SessionRunning)
	w.ticket(2, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageExporting))
	oldExp := w.session(api.RoleDeveloper, 2, api.SessionRunning, sessPurpose(api.PurposeExport), sessProject("p"))
	w.session(api.RoleDeveloper, 2, api.SessionRunning, sessPurpose(api.PurposeExport), sessProject("p"))
	acts, _, _ := w.plan(t)
	ends := ofKind(acts, actEnd)
	if len(ends) != 2 || ends[0].session != old || ends[1].session != oldExp || len(starts(acts)) != 0 {
		t.Fatalf("actions = %v", acts)
	}
}

func TestPlanEndsExportJobOfExportedProject(t *testing.T) {
	w := newWorld(5)
	w.ticket(1, api.TypeStory, api.ColumnReview, 1, projects("a", "b"), stage(api.ReviewStageExporting),
		func(t *board.EngineTicket) { t.Projects[0].ExportURL = "https://example.test/pr/1" })
	done := w.session(api.RoleDeveloper, 1, api.SessionWaiting, sessPurpose(api.PurposeExport), sessProject("a"))
	w.session(api.RolePM, 1, api.SessionWaiting)
	acts, _, _ := w.plan(t)
	ends, ss := ofKind(acts, actEnd), starts(acts)
	if len(ends) != 1 || ends[0].session != done {
		t.Fatalf("ends = %v", ends)
	}
	if len(ss) != 1 || ss[0].project != "b" || ss[0].purpose != api.PurposeExport || ss[0].role != api.RoleDeveloper || ss[0].gated {
		t.Fatalf("starts = %v", ss)
	}
}

func TestPlanReviewerPerProjectWithoutVerdict(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeStory, api.ColumnReview, 1, projects("a", "b", "c"), stage(api.ReviewStageAgent),
		func(t *board.EngineTicket) { t.Verdicts["a"] = "approve" })
	w.session(api.RoleReviewer, 1, api.SessionWaiting, sessRound(1), sessProject("a"))
	w.session(api.RoleReviewer, 1, api.SessionRunning, sessRound(1), sessProject("c"))
	acts, _, _ := w.plan(t)
	ss := starts(acts)
	if len(ss) != 1 || ss[0].project != "b" || ss[0].role != api.RoleReviewer || ss[0].gated {
		t.Fatalf("starts = %v", ss)
	}
	// Without any reviewer: one per project without a verdict, in project order.
	w.snap.Sessions = nil
	acts, _, _ = w.plan(t)
	ss = starts(acts)
	if len(ss) != 2 || ss[0].project != "b" || ss[1].project != "c" {
		t.Fatalf("starts = %v", ss)
	}
}

func TestPlanSkipsFailedBusyInflightUnavailable(t *testing.T) {
	w := newWorld(5)
	w.ticket(1, api.TypeTask, api.ColumnReady, 1, func(t *board.EngineTicket) { t.FailedStep = api.FailedStart })
	w.ticket(2, api.TypeTask, api.ColumnReady, 1, func(t *board.EngineTicket) { t.Busy = true })
	w.ticket(3, api.TypeTask, api.ColumnReady, 1)
	w.in.inflight[3] = true
	w.ticket(4, api.TypeTask, api.ColumnReady, 1, projects("gone"))
	w.ticket(5, api.TypeStory, api.ColumnReady, 1)
	w.ticket(6, api.TypeTask, api.ColumnReady, 1, inStory(5)) // story not in progress
	w.ticket(7, api.TypeTask, api.ColumnReady, 1, waitingOn(3))
	w.ticket(8, api.TypeTask, api.ColumnInProgress, 1, func(t *board.EngineTicket) { t.FailedStep = api.FailedAgent })
	w.ticket(9, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageMerging), func(t *board.EngineTicket) { t.FailedStep = api.FailedMerge })
	w.ticket(10, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageMerging), func(t *board.EngineTicket) { t.Busy = true })
	w.ticket(11, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageMerging))
	w.in.inflight[11] = true
	w.ticket(12, api.TypeTask, api.ColumnInProgress, 1, projects("gone"))
	w.in.available = func(p string) bool { return p != "gone" }
	acts, _, blocked := w.plan(t)
	ss := starts(acts)
	if len(ss) != 1 || ss[0].ticket != 5 {
		t.Fatalf("starts = %v", ss)
	}
	if len(ofKind(acts, actMerge)) != 0 {
		t.Fatalf("merges = %v", acts)
	}
	for _, n := range []int64{1, 2, 3, 4, 6, 7} {
		if v, ok := blocked[n]; !ok || v != "" {
			t.Errorf("start_blocked[%d] = %q (present %v), want \"\"", n, v, ok)
		}
	}
	// The unavailable project is reported as attention on both tickets.
	att := map[int64]string{}
	for _, a := range ofKind(acts, actAttention) {
		att[a.ticket] = a.reason
	}
	if len(att) != 2 || !strings.Contains(att[4], "project gone") || !strings.Contains(att[12], "project gone") {
		t.Fatalf("attention = %v", att)
	}
}

func TestPlanNoModelBlocksStart(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeTask, api.ColumnReady, 1)
	w.ticket(2, api.TypeStory, api.ColumnReady, 2)
	w.in.hasModel = func(r api.Role, _ api.Purpose, _ board.EngineTicket) bool { return r != api.RoleDeveloper }
	acts, st, blocked := w.plan(t)
	ss := starts(acts)
	// The task has no model and takes no slot; the story behind it starts.
	if len(ss) != 1 || ss[0].ticket != 2 || ss[0].role != api.RolePM {
		t.Fatalf("starts = %v", ss)
	}
	if blocked[1] != api.StartBlockedNoModel || blocked[2] != "" || st.Running != 1 {
		t.Fatalf("blocked = %v, state = %+v", blocked, st)
	}
	if len(ofKind(acts, actAttention)) != 0 {
		t.Fatalf("a ready ticket without a model got attention: %v", acts)
	}
}

func TestPlanNoModelForContinuationSetsAttention(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeTask, api.ColumnInProgress, 1)
	w.ticket(2, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageAgent))
	w.ticket(3, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageAgent), func(t *board.EngineTicket) { t.Attention = "question 4 is unanswered" })
	w.in.hasModel = func(api.Role, api.Purpose, board.EngineTicket) bool { return false }
	acts, _, _ := w.plan(t)
	att := ofKind(acts, actAttention)
	if len(starts(acts)) != 0 || len(att) != 2 {
		t.Fatalf("actions = %v", acts)
	}
	if att[0].ticket != 1 || att[0].reason != "no usable model for the developer role; set models in roles/developer.yaml" ||
		att[1].ticket != 2 || att[1].reason != "no usable model for the reviewer role; set models in roles/reviewer.yaml" {
		t.Fatalf("attention = %v", att)
	}

	// Applied: nothing more is proposed.
	w.snap.Tickets[0].Attention = att[0].reason
	w.snap.Tickets[1].Attention = att[1].reason
	acts, _, _ = w.plan(t)
	if len(acts) != 0 {
		t.Fatalf("actions after applying = %v", acts)
	}

	// Models are back: the agents start and the planner clears its own flags,
	// never somebody else's.
	w.in.hasModel = nil
	acts, _, _ = w.plan(t)
	att = ofKind(acts, actAttention)
	if len(starts(acts)) != 3 || len(att) != 2 || att[0].reason != "" || att[1].reason != "" {
		t.Fatalf("actions = %v", acts)
	}
}

func TestPlanIgnoresStoryPinning(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeStory, api.ColumnReady, 2)
	w.ticket(2, api.TypeTask, api.ColumnReady, 1)
	acts, _, blocked := w.plan(t)
	if ss := starts(acts); len(ss) != 1 || ss[0].ticket != 2 {
		t.Fatalf("starts = %v", ss)
	}
	if blocked[1] != api.StartBlockedSlot {
		t.Fatalf("blocked = %v", blocked)
	}
}

func TestPlanMergesAndCleanups(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageMerging))
	w.session(api.RoleDeveloper, 1, api.SessionWaiting) // parked sessions do not hold back a merge
	w.ticket(2, api.TypeTask, api.ColumnDone, 1, func(t *board.EngineTicket) {
		t.Resolution = api.ResolutionCompleted
		t.Projects[0].Worktree = "/w/2"
	})
	w.ticket(3, api.TypeTask, api.ColumnDone, 1, func(t *board.EngineTicket) {
		t.Resolution = api.ResolutionCancelled
		t.Projects[0].Worktree = "/w/3"
	})
	still := w.session(api.RoleDeveloper, 3, api.SessionRunning)
	w.ticket(4, api.TypeTask, api.ColumnDone, 1, func(t *board.EngineTicket) {
		t.Resolution = api.ResolutionExported // export not merged: the export check is the engine's timer
	})
	acts, _, _ := w.plan(t)
	var got []string
	for _, a := range acts {
		got = append(got, fmt.Sprintf("%v %d %s", a.kind, a.ticket, a.session))
	}
	want := []string{"end 3 " + still, "merge 1 ", "cleanup 2 "}
	if !slices.Equal(got, want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	// Once the session has ended, #3 is cleaned up too.
	w.snap.Sessions = w.snap.Sessions[:1]
	acts, _, _ = w.plan(t)
	if c := ofKind(acts, actCleanup); len(c) != 2 || c[1].ticket != 3 {
		t.Fatalf("cleanups = %v", c)
	}
}

func TestPlanLeavesInFlightSessionsAlone(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeTask, api.ColumnReady, 1) // cancelled or moved meanwhile: handled when the worker is done
	id := w.session(api.RoleDeveloper, 1, api.SessionStarting)
	delete(w.in.processes, id)
	w.in.inflight[1] = true
	ceo := w.session(api.RoleCEO, 0, api.SessionStarting)
	delete(w.in.processes, ceo)
	w.in.starts = []pendingStart{{role: api.RoleCEO, session: ceo}}
	acts, _, _ := w.plan(t)
	if len(acts) != 0 {
		t.Fatalf("actions = %v", acts)
	}
}

func TestPlanStableAfterApplying(t *testing.T) {
	w := newWorld(2)
	w.ticket(1, api.TypeStory, api.ColumnReady, 1)
	w.ticket(2, api.TypeTask, api.ColumnReady, 2)
	w.ticket(3, api.TypeTask, api.ColumnReady, 3)
	w.ticket(4, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageMerging))
	acts, _, _ := w.plan(t)
	if len(acts) != 3 {
		t.Fatalf("actions = %v", acts)
	}
	// The engine applies them: claims and workers in flight.
	for _, a := range acts {
		w.in.inflight[a.ticket] = true
		if a.kind == actStart {
			w.in.starts = append(w.in.starts, pendingStart{ticket: a.ticket, role: a.role})
		}
	}
	again, st, blocked := w.plan(t)
	if len(again) != 0 || st.Running != 2 || blocked[3] != api.StartBlockedSlot {
		t.Fatalf("again = %v, state = %+v, blocked = %v", again, st, blocked)
	}
}

func TestPlanNilInputs(t *testing.T) {
	// A zero input plans nothing and does not panic.
	acts, st, blocked := plan(planInput{})
	if len(acts) != 0 || st.Running != 0 || len(blocked) != 0 {
		t.Fatalf("%v %v %v", acts, st, blocked)
	}
}
