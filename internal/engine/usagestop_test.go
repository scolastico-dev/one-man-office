package engine

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// fakeUsage is a usage view whose states the test sets; models it does not
// know are unmetered.
type fakeUsage struct {
	mu     sync.Mutex
	models map[string]api.ModelUsage
	asked  int
}

func (u *fakeUsage) Model(id string) api.ModelUsage {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.asked++
	if m, ok := u.models[id]; ok {
		return m
	}
	return api.ModelUsage{Model: id, State: api.UsageUnmetered}
}

// set gives a model a state and fill; ok, soft and hard come with a window
// and a reset time.
func (u *fakeUsage) set(id, state string, used float64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.models == nil {
		u.models = map[string]api.ModelUsage{}
	}
	m := api.ModelUsage{Model: id, State: state}
	if state == api.UsageOK || state == api.UsageSoft || state == api.UsageHard {
		resets := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
		m.Window, m.UsedPercent, m.ResetsAt = "session", &used, &resets
	}
	u.models[id] = m
}

// withUsage gives the harness's engine a fake usage view.
func (h *harness) withUsage() *fakeUsage {
	u := &fakeUsage{}
	h.engine().o.Usage = u
	return u
}

// setHandoffTimeout changes stop.handoff_timeout.
func (h *harness) setHandoffTimeout(d time.Duration) {
	h.t.Helper()
	g := h.st.General()
	g.Stop.HandoffTimeout = api.Duration(d)
	if err := h.st.SetGeneral(g); err != nil {
		h.t.Fatal(err)
	}
}

// usageStopComments counts the usage stop comments on a ticket.
func (h *harness) usageStopComments(n int64) []string {
	var out []string
	for _, c := range h.comments(n) {
		if strings.HasPrefix(c, "Stopped") {
			out = append(out, c)
		}
	}
	return out
}

// noCrashCounted fails when an end was counted as a failure of its agent.
func (h *harness) noCrashCounted(n int64) {
	h.t.Helper()
	for _, c := range h.comments(n) {
		if strings.Contains(c, "exited unexpectedly") || strings.Contains(c, "fresh agent") {
			h.t.Fatalf("a usage stop counted as a crash: %q", c)
		}
	}
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	for k, r := range h.e.failures {
		if k.ticket == n && r.count > 0 {
			h.t.Fatalf("failure count %d for %+v", r.count, k)
		}
	}
}

func TestHardStopAsksHandoffThenEnds(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHandoffTimeout(time.Hour)
	use := h.withUsage()
	task, dev := h.readyDeveloper("one")
	if dev.Model != "fake/a" {
		t.Fatalf("developer runs on %q", dev.Model)
	}
	a := h.agent(dev.ID)

	// The model reaches its hard limit: the developer is asked for a
	// handoff once, however often the engine passes meanwhile.
	use.set("fake/a", api.UsageHard, 99)
	h.step()
	h.waitFor("the handoff request", func() bool { return usageHandoffAsked(a) })
	h.step()
	h.step()
	if n := len(a.typed()); n != 1 {
		t.Fatalf("%d lines typed: %q", n, a.typed())
	}
	if a.isOver() {
		t.Fatal("the agent was killed before its handoff")
	}

	// It hands off: the session ends as a usage stop, not as a crash.
	h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "Handoff: half done, committed"})
	ended := h.waitEnded(dev.ID)
	if ended.EndReason != usageStopReason {
		t.Fatalf("end reason %q", ended.EndReason)
	}
	h.waitFor("the stop to finish", func() bool { return !h.e.usageAsked(dev.ID) })
	got := h.usageStopComments(task)
	want := "Stopped: fake/a reached its usage limit (session at 99 %, resets " +
		time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC).Local().Format("2006-01-02 15:04 MST") +
		"). omo goes on when a model of this role is below its limits."
	if len(got) != 1 || got[0] != want {
		t.Fatalf("stop comments %q, want one %q", got, want)
	}

	// The ticket keeps its column and worktree and goes on at once on the
	// other model of its role.
	h.step()
	tk := h.ticket(task)
	if tk.Column != api.ColumnInProgress || len(tk.Projects) == 0 || tk.Projects[0].Worktree == "" || !pathExists(tk.Projects[0].Worktree) {
		t.Fatalf("ticket after the stop: %s, projects %+v", tk.Column, tk.Projects)
	}
	next := h.live(api.RoleDeveloper, task)
	if next.Model != "fake/b" || next.ID == dev.ID {
		t.Fatalf("next developer %s on %q", next.ID, next.Model)
	}
	if tk.StartBlocked != "" || tk.Attention != "" {
		t.Fatalf("start_blocked %q attention %q", tk.StartBlocked, tk.Attention)
	}
	h.noCrashCounted(task)
	if len(h.usageStopComments(task)) != 1 {
		t.Fatal("a second stop comment")
	}
}

func TestHardStopEndsAfterHandoffTimeout(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.e.usage.timeout = 50 * time.Millisecond // the setting's minimum is 5s
	use := h.withUsage()
	task, dev := h.readyDeveloper("one")
	a := h.agent(dev.ID)

	// Both models of the role are at their limit: the silent agent is
	// ended after the handoff timeout and nothing replaces it.
	use.set("fake/a", api.UsageHard, 99)
	use.set("fake/b", api.UsageHard, 98)
	h.step()
	if ended := h.waitEnded(dev.ID); ended.EndReason != usageStopReason {
		t.Fatalf("end reason %q", ended.EndReason)
	}
	if !usageHandoffAsked(a) || !a.isOver() {
		t.Fatalf("asked %v, killed %v", usageHandoffAsked(a), a.isOver())
	}
	h.waitFor("the stop to finish", func() bool { return !h.e.usageAsked(dev.ID) })
	h.step()
	tk := h.ticket(task)
	if tk.StartBlocked != api.StartBlockedUsage || tk.Attention != "" || tk.Column != api.ColumnInProgress {
		t.Fatalf("ticket: %s start_blocked %q attention %q", tk.Column, tk.StartBlocked, tk.Attention)
	}
	if _, ok := h.liveOK(api.RoleDeveloper, task); ok {
		t.Fatal("a developer started on a model at its limit")
	}

	// Soft is not enough to start either.
	use.set("fake/b", api.UsageSoft, 91)
	h.step()
	if _, ok := h.liveOK(api.RoleDeveloper, task); ok {
		t.Fatal("a developer started on a model over its soft limit")
	}

	// The window resets: the ticket goes on by itself.
	use.set("fake/b", api.UsageOK, 3)
	h.step()
	next := h.live(api.RoleDeveloper, task)
	if next.Model != "fake/b" {
		t.Fatalf("resumed on %q", next.Model)
	}
	h.step()
	if got := h.ticket(task).StartBlocked; got != "" {
		t.Fatalf("start_blocked %q after the start", got)
	}
	h.noCrashCounted(task)
	if n := len(h.usageStopComments(task)); n != 1 {
		t.Fatalf("%d stop comments", n)
	}
}

func TestHardStopEndsParkedAndCEOAtOnce(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHandoffTimeout(time.Hour)
	use := h.withUsage()
	task, parked := h.readyDeveloper("one")
	ch := h.park(h.ctx, parked.ID, time.Hour)
	ceo, err := h.e.StartCEO(h.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Ready(h.ctx, h.actor(ceo.ID)); err != nil {
		t.Fatal(err)
	}
	use.set(parked.Model, api.UsageHard, 100)
	use.set(ceo.Model, api.UsageHard, 100)
	h.step()
	for _, id := range []string{parked.ID, ceo.ID} {
		if s := h.waitEnded(id); s.EndReason != usageStopReason {
			t.Fatalf("end reason %q", s.EndReason)
		}
		if n := len(h.agent(id).typed()); n != 0 {
			t.Fatalf("%d lines typed into an agent that is ended at once", n)
		}
	}
	if r := h.result(ch); r.err == nil {
		t.Fatalf("the parked wait returned %q without error", r.reason)
	}
	h.waitFor("the stops to finish", func() bool { return !h.e.usageAsked(parked.ID) && !h.e.usageAsked(ceo.ID) })
	if n := len(h.usageStopComments(task)); n != 1 {
		t.Fatalf("%d stop comments on the ticket", n)
	}

	// The CEO does not start on a model at its limit either.
	use.set("fake/a", api.UsageHard, 100)
	use.set("fake/b", api.UsageSoft, 90)
	_, err = h.e.StartCEO(h.ctx, false)
	if errCodeOf(err) != api.CodeUnavailable || !strings.Contains(err.Error(), "usage limit") {
		t.Fatalf("StartCEO = %v", err)
	}
}

func TestHardStopDuringShutdownAsksOnce(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.setHandoffTimeout(time.Hour)
	use := h.withUsage()
	task, dev := h.readyDeveloper("one")
	a := h.agent(dev.ID)
	use.set(dev.Model, api.UsageHard, 99)
	h.step()
	h.waitFor("the handoff request", func() bool { return usageHandoffAsked(a) })
	done := h.shutdown(h.ctx)
	// The graceful stop waits for the same handoff without asking again.
	h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "Handoff: done for now"})
	h.awaitClosed("Shutdown", done)
	if n := len(a.typed()); n != 1 {
		t.Fatalf("%d handoff lines: %q", n, a.typed())
	}
	if h.liveCount() != 0 || !a.isOver() {
		t.Fatal("something still runs")
	}
}

func TestUsageWaitAfterRestart(t *testing.T) {
	h := newHarness(t)
	use := h.withUsage()
	task, dev := h.readyDeveloper("one")
	waiting := h.standalone("two")
	if _, err := h.b.Update(h.ctx, board.User, waiting, api.UpdateTicket{Model: api.Some("fake/a")}); err != nil {
		t.Fatal(err)
	}
	h.approve(waiting)
	use.set("fake/a", api.UsageSoft, 92)
	use.set("fake/b", api.UsageSoft, 93)
	h.step()
	if err := h.e.StartAnyway(h.ctx, waiting); err != nil {
		t.Fatal(err)
	}

	// omo restarts while both models are over their soft limit (the
	// tracker keeps its readings across restarts): the developer's session
	// ends with the old run, no agent starts again, both tickets wait for
	// usage, and "Start anyway" is forgotten.
	h.restart()
	h.engine().o.Usage = use
	h.step()
	if s := h.waitEnded(dev.ID); s.EndReason != "omo stopped" {
		t.Fatalf("end reason %q", s.EndReason)
	}
	if n := h.liveCount(); n != 0 {
		t.Fatalf("%d live sessions after the restart", n)
	}
	for _, n := range []int64{task, waiting} {
		if tk := h.ticket(n); tk.StartBlocked != api.StartBlockedUsage || tk.Attention != "" {
			t.Fatalf("#%d: start_blocked %q attention %q", n, tk.StartBlocked, tk.Attention)
		}
	}

	// Usage goes down: both go on.
	use.set("fake/a", api.UsageOK, 1)
	use.set("fake/b", api.UsageOK, 1)
	h.step()
	h.live(api.RoleDeveloper, task)
	h.live(api.RoleDeveloper, waiting)
	h.step()
	for _, n := range []int64{task, waiting} {
		if got := h.ticket(n).StartBlocked; got != "" {
			t.Fatalf("#%d start_blocked %q", n, got)
		}
	}
}

func TestUsageStopsNeedAUsageView(t *testing.T) {
	h := newHarness(t)
	_, dev := h.readyDeveloper("one")
	h.step()
	if got := h.e.usageStops(h.snap()); len(got) != 0 {
		t.Fatalf("usage stops %v without a usage view", got)
	}
	if got := h.e.use(dev.Model); got.State != api.UsageUnmetered {
		t.Fatalf("use = %+v", got)
	}
}

// Several agents of one ticket stopped for usage in one pass get one
// comment, which names the model and how many agents were stopped.
func TestHardStopOfSeveralAgentsCommentsOnce(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	h.e.usage.timeout = 50 * time.Millisecond
	use := h.withUsage()
	task, dev := h.readyDeveloper("one")
	if _, err := h.b.Done(h.ctx, h.actor(dev.ID), api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	h.step()
	rev := h.live(api.RoleReviewer, task)
	if rev.Model != dev.Model {
		t.Fatalf("reviewer on %q, developer on %q", rev.Model, dev.Model)
	}
	use.set(dev.Model, api.UsageHard, 99)
	use.set("fake/b", api.UsageHard, 99)
	h.step()
	for _, id := range []string{dev.ID, rev.ID} {
		if s := h.waitEnded(id); s.EndReason != usageStopReason {
			t.Fatalf("end reason %q", s.EndReason)
		}
	}
	h.waitFor("the stops to finish", func() bool { return !h.e.usageAsked(dev.ID) && !h.e.usageAsked(rev.ID) })
	got := h.usageStopComments(task)
	if len(got) != 1 || !strings.HasPrefix(got[0], "Stopped 2 agents: "+dev.Model+" reached its usage limit (session at 99 %") {
		t.Fatalf("stop comments %q", got)
	}
}
