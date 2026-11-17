package engine

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// How long agents live: the developer of a task (the product manager of a
// story) from In Progress until the ticket is done, parked in between; a
// reviewer from its start until the next review round of its project
// begins or the ticket is done, parked after its verdict.

// roundReviewer returns the live reviewer of ticket n started for review
// round r.
func (h *harness) roundReviewer(n int64, r int) board.EngineSession {
	h.t.Helper()
	for _, s := range h.snap().Sessions {
		if s.Role == api.RoleReviewer && s.Ticket == n && s.ReviewRound == r {
			return s
		}
	}
	h.t.Fatalf("no live reviewer of round %d on #%d", r, n)
	return board.EngineSession{}
}

// endReasonOf returns the end reason of session id ("" while it is live).
func (h *harness) endReasonOf(id string) string {
	h.t.Helper()
	all, err := h.b.AgentSessions(h.ctx, board.User, false)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, s := range all {
		if s.ID == id && s.State == api.SessionEnded {
			return s.EndReason
		}
	}
	return ""
}

// readyAgent lets a started session acknowledge its start.
func (h *harness) readyAgent(id string) {
	h.t.Helper()
	if _, err := h.e.Ready(h.ctx, h.actor(id)); err != nil {
		h.t.Fatal(err)
	}
}

// A rejection wakes the developer that handed the work in, the same
// session with its context, and no second developer starts; the reviewer
// that rejected stays, parked, until the next round's reviewer starts.
// The round-2 reviewer approves and stays through the user's review, where
// the user asks it and the developer questions by mentions; both answer in
// the thread. The merge ends both.
func TestDeveloperAndReviewerLiveUntilTheTicketIsDone(t *testing.T) {
	h := gitHarness(t)
	h.typeAtOnce()
	n := h.create(api.CreateTicket{Type: api.TypeTask, Title: "Lifetime", Review: api.ReviewAgentThenHuman})
	h.startTask(n, h.project, "life.txt", "a\n")
	dev := h.live(api.RoleDeveloper, n)
	h.readyAgent(dev.ID)
	h.handIn(n)
	devWait := h.park(h.ctx, dev.ID, time.Hour)
	h.step()
	rev1 := h.roundReviewer(n, 1)
	h.readyAgent(rev1.ID)
	if _, err := h.b.Done(h.ctx, h.actor(rev1.ID), api.DoneRequest{Verdict: board.VerdictReject, Comment: "life.txt needs a second line"}); err != nil {
		t.Fatal(err)
	}
	rev1Wait := h.park(h.ctx, rev1.ID, time.Hour)
	h.notifyNow()
	if r := h.result(devWait); r.err != nil || !strings.Contains(r.reason, "mentions @developer") {
		t.Fatalf("the developer's wait returned %q, %v", r.reason, r.err)
	}
	h.steps(2)
	if tk := h.ticket(n); tk.Column != api.ColumnInProgress {
		t.Fatalf("#%d is in %s after the rejection", n, tk.Column)
	}
	if got := h.live(api.RoleDeveloper, n); got.ID != dev.ID {
		t.Fatal("a fresh developer replaced the one that was woken")
	}
	if reason := h.endReasonOf(rev1.ID); reason != "" {
		t.Fatalf("the reviewer of round 1 ended after its rejection: %q", reason)
	}

	// The developer asks the kept reviewer about its finding.
	h.comment(h.actor(dev.ID), n, api.CommentRequest{Body: "@reviewer one more line, or two?"})
	h.notifyNow()
	if r := h.result(rev1Wait); r.err != nil || !strings.Contains(r.reason, "mentions @reviewer") {
		t.Fatalf("the kept reviewer's wait returned %q, %v", r.reason, r.err)
	}

	// The second hand-in begins round 2: its reviewer starts and the one of
	// round 1 ends at that moment.
	commit(t, h.worktree(h.project, n), "life.txt", "a\nb\n")
	h.handIn(n)
	devWait = h.park(h.ctx, dev.ID, time.Hour)
	h.step()
	rev2 := h.roundReviewer(n, 2)
	if reason := h.endReasonOf(rev1.ID); reason != "a new review round of #1 began" {
		t.Fatalf("the reviewer of round 1 ended with %q", reason)
	}
	h.readyAgent(rev2.ID)
	if _, err := h.b.Done(h.ctx, h.actor(rev2.ID), api.DoneRequest{Verdict: board.VerdictApprove, Comment: "fine"}); err != nil {
		t.Fatal(err)
	}
	rev2Wait := h.park(h.ctx, rev2.ID, time.Hour)
	h.steps(2)
	if tk := h.ticket(n); tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageHuman {
		t.Fatalf("#%d is in %s/%s after the approval", n, tk.Column, tk.ReviewStage)
	}
	for _, id := range []string{dev.ID, rev2.ID} {
		if reason := h.endReasonOf(id); reason != "" {
			t.Fatalf("an agent ended during the user's review: %q", reason)
		}
	}

	// The user asks both; each is woken and answers in the thread.
	h.comment(board.User, n, api.CommentRequest{Body: "@reviewer did you run the tests? @developer why two lines?"})
	h.notifyNow()
	for _, w := range []<-chan waitResult{rev2Wait, devWait} {
		if r := h.result(w); r.err != nil || !strings.Contains(r.reason, "comment") {
			t.Fatalf("a kept agent's wait returned %q, %v", r.reason, r.err)
		}
	}
	h.comment(h.actor(rev2.ID), n, api.CommentRequest{Body: "Yes, all of them."})
	h.comment(h.actor(dev.ID), n, api.CommentRequest{Body: "The reviewer asked for it."})

	// The user approves: the merge ends both, saying why.
	if _, err := h.b.Approve(h.ctx, board.User, n, api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	h.steps(3)
	if tk := h.ticket(n); tk.Column != api.ColumnDone {
		t.Fatalf("#%d is in %s after the user's approval", n, tk.Column)
	}
	for _, id := range []string{dev.ID, rev2.ID} {
		if reason := h.endReasonOf(id); reason != "#1 was merged" {
			t.Fatalf("an agent ended with %q", reason)
		}
	}
	devs := 0
	for _, s := range h.sessions(n) {
		if s.Role == api.RoleDeveloper {
			devs++
		}
	}
	if devs != 1 {
		t.Fatalf("%d developer sessions, want the one that worked both rounds", devs)
	}
}

// A ticket in the user's review keeps its slot while its developer and its
// reviewer are parked: with a limit of one a new task waits until the
// review is approved and the ticket is done.
func TestTicketInReviewKeepsItsSlot(t *testing.T) {
	h := newHarness(t, withLimit(1))
	n := h.create(api.CreateTicket{Type: api.TypeTask, Title: "kept", Review: api.ReviewAgentThenHuman})
	h.approve(n)
	h.step()
	dev := h.live(api.RoleDeveloper, n)
	h.readyAgent(dev.ID)
	h.handIn(n)
	h.park(h.ctx, dev.ID, time.Hour)
	h.step()
	rev := h.roundReviewer(n, 1)
	h.readyAgent(rev.ID)
	if _, err := h.b.Done(h.ctx, h.actor(rev.ID), api.DoneRequest{Verdict: board.VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	h.park(h.ctx, rev.ID, time.Hour)
	other := h.standalone("next")
	h.approve(other)
	h.step()
	if tk := h.ticket(other); tk.Column != api.ColumnReady || tk.StartBlocked != api.StartBlockedSlot {
		t.Fatalf("#%d: column %s, start_blocked %q; want ready, slot", other, tk.Column, tk.StartBlocked)
	}
	if sched := h.e.Scheduler(); sched.Tasks != (api.SlotLoad{InWork: 1, Limit: 1}) {
		t.Fatalf("scheduler %+v, want the task in review counted", sched)
	}
	if reason := h.endReasonOf(rev.ID); reason != "" {
		t.Fatalf("the kept reviewer ended: %q", reason)
	}
	if _, err := h.b.Approve(h.ctx, board.User, n, api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	h.step()
	h.step()
	if tk := h.ticket(n); tk.Column != api.ColumnDone {
		t.Fatalf("#%d: column %s, want done", n, tk.Column)
	}
	h.live(api.RoleDeveloper, other)
	if sched := h.e.Scheduler(); sched.Tasks != (api.SlotLoad{InWork: 1, Limit: 1}) {
		t.Fatalf("scheduler %+v, want only the new task counted", sched)
	}
}

// A kept agent that only waits is not silent and not told to investigate:
// a reviewer parked after its verdict and a developer parked during the
// review get no stalled reminder and no failed step however long they
// wait, and their waits time out without the investigate line.
func TestKeptAgentsAreNeitherStalledNorInvestigating(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHealth(15*time.Minute, 24*time.Hour)
	n := h.create(api.CreateTicket{Type: api.TypeTask, Title: "kept", Review: api.ReviewAgentThenHuman})
	h.approve(n)
	h.step()
	dev := h.live(api.RoleDeveloper, n)
	h.readyAgent(dev.ID)
	h.handIn(n)
	h.step()
	rev := h.roundReviewer(n, 1)
	h.readyAgent(rev.ID)
	if _, err := h.b.Done(h.ctx, h.actor(rev.ID), api.DoneRequest{Verdict: board.VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	h.step()
	for _, s := range []board.EngineSession{dev, rev} {
		a := h.actor(s.ID)
		for i := 1; i <= 6; i++ {
			r, err := h.e.Wait(h.ctx, a, time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(r, "timeout: nothing arrived in 1ms.") || strings.Contains(r, "in a row") {
				t.Fatalf("%s wait %d = %q", s.Role, i, r)
			}
		}
	}
	h.park(h.ctx, dev.ID, 24*time.Hour)
	h.park(h.ctx, rev.ID, 24*time.Hour)
	for range 4 {
		h.check(16 * time.Minute)
	}
	if tk := h.ticket(n); tk.FailedStep != api.FailedNone || tk.Attention != "" {
		t.Fatalf("a kept agent was flagged: %s %q", tk.FailedStep, tk.Attention)
	}
	for _, s := range []board.EngineSession{dev, rev} {
		if stalledLines(h.agent(s.ID)) != 0 {
			t.Fatalf("the kept %s got a stalled reminder", s.Role)
		}
	}
}

// A kept reviewer whose agent exits is not counted as a failure and not
// replaced: nothing needs it until the next round, which starts its own
// reviewer. The comment says so.
func TestCrashOfKeptReviewerStartsNoReviewer(t *testing.T) {
	h := newHarness(t)
	n := h.create(api.CreateTicket{Type: api.TypeTask, Title: "kept", Review: api.ReviewAgentThenHuman})
	h.approve(n)
	h.step()
	h.handIn(n)
	h.step()
	rev := h.roundReviewer(n, 1)
	h.readyAgent(rev.ID)
	if _, err := h.b.Done(h.ctx, h.actor(rev.ID), api.DoneRequest{Verdict: board.VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	h.step()
	h.agent(rev.ID).exit(errors.New("exit status 1"))
	h.waitEnded(rev.ID)
	h.steps(2)
	if _, ok := h.liveOK(api.RoleReviewer, n); ok {
		t.Fatal("a reviewer started for a review that has its verdict")
	}
	if got := h.e.attempts(slotKey(rev)); got != 0 {
		t.Fatalf("%d failures counted for a kept reviewer", got)
	}
	c := h.lastComment(n).Body
	if !strings.Contains(c, "after its review of round 1") || strings.Contains(c, "omo starts a fresh agent") {
		t.Fatalf("comment on the crash:\n%s", c)
	}
}

// After a restart of omo the kept agents are not started again just to
// wait: a ticket in the user's review gets no developer and no reviewer.
// A rejection then starts a fresh developer, which reads the thread; the
// next round gets a fresh reviewer.
func TestRestartStartsNoKeptAgent(t *testing.T) {
	h := newHarness(t)
	n := h.create(api.CreateTicket{Type: api.TypeTask, Title: "kept", Review: api.ReviewAgentThenHuman})
	h.approve(n)
	h.step()
	h.handIn(n)
	h.step()
	rev := h.roundReviewer(n, 1)
	if _, err := h.b.Done(h.ctx, h.actor(rev.ID), api.DoneRequest{Verdict: board.VerdictApprove}); err != nil {
		t.Fatal(err)
	}
	h.step()
	if h.liveCount() != 2 {
		t.Fatalf("%d live sessions before the restart, want the kept developer and reviewer", h.liveCount())
	}
	h.crash()
	h.boot(2)
	if h.liveCount() != 0 {
		t.Fatalf("%d sessions started after the restart for a ticket in the user's review", h.liveCount())
	}
	for _, r := range h.endReasons(n) {
		if r != "omo restarted" {
			t.Fatalf("end reasons %q", h.endReasons(n))
		}
	}
	if _, err := h.b.Reject(h.ctx, board.User, n, api.RejectRequest{Comment: "one more thing"}); err != nil {
		t.Fatal(err)
	}
	h.step()
	h.live(api.RoleDeveloper, n)
}

// A story's product manager lives from In Progress until the story is
// done, parked while the story is reviewed; each project of the story has
// its reviewer, and a mention of @reviewer reaches all of them, also the
// one kept from an earlier round whose approval carried over.
func TestStoryKeepsItsPMAndOneReviewerPerProject(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	a, b := h.ids[0], h.ids[1]
	h.typeAtOnce()
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{a, b}, Review: api.ReviewAgent})
	h.startStory(s)
	pm := h.live(api.RolePM, s)
	for _, p := range []string{a, b} {
		task := h.storyTask(s, p, "In "+p)
		h.finishTask(task, p, "new.txt", p+"\n")
		h.steps(2)
	}
	h.handIn(s)
	h.steps(2)
	h.verdict(s, a, board.VerdictApprove, "")
	h.verdict(s, b, board.VerdictReject, "b is broken")
	h.step()
	if got := h.live(api.RolePM, s); got.ID != pm.ID {
		t.Fatal("the story's product manager was replaced")
	}
	// Only b changes: a's approval carries over and its reviewer stays.
	fix := h.storyTask(s, b, "Fix b")
	h.finishTask(fix, b, "fix.txt", "fixed\n")
	h.steps(2)
	h.handIn(s)
	h.steps(2)
	rounds := h.reviewerRounds(s)
	if rounds[a] != 1 || rounds[b] != 2 || len(rounds) != 2 {
		t.Fatalf("reviewers by project and round %v", rounds)
	}
	var waits []<-chan waitResult
	for _, x := range h.snap().Sessions {
		if x.Role == api.RoleReviewer && x.Ticket == s {
			h.readyAgent(x.ID)
			waits = append(waits, h.park(h.ctx, x.ID, time.Hour))
		}
	}
	h.comment(board.User, s, api.CommentRequest{Body: "@reviewer anything else?"})
	h.notifyNow()
	for _, w := range waits {
		if r := h.result(w); r.err != nil || !strings.Contains(r.reason, "mentions @reviewer") {
			t.Fatalf("a reviewer's wait returned %q, %v", r.reason, r.err)
		}
	}
	h.verdict(s, b, board.VerdictApprove, "")
	h.steps(3)
	if tk := h.ticket(s); tk.Column != api.ColumnDone {
		t.Fatalf("story in %s", tk.Column)
	}
	if h.liveCount() != 0 {
		t.Fatalf("%d sessions live after the story is done", h.liveCount())
	}
	var reasons []string
	for _, x := range h.sessions(s) {
		reasons = append(reasons, x.EndReason)
	}
	if !slices.Contains(reasons, "#1 was merged") {
		t.Fatalf("end reasons %q", reasons)
	}
}
