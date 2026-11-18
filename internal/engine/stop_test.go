package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// handoffAsked reports whether the handoff line of a stop of omo was typed
// into an agent.
func handoffAsked(a *fakeAgent) bool { return typedPrefix(a, "omo is stopping.") }

// usageHandoffAsked reports whether the handoff line of a usage stop was
// typed into an agent.
func usageHandoffAsked(a *fakeAgent) bool {
	return typedPrefix(a, "your model reached its usage limit")
}

func typedPrefix(a *fakeAgent, prefix string) bool {
	for _, l := range a.typed() {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// shutdown runs Shutdown with ctx in the background; the channel is closed
// when it returns.
func (h *harness) shutdown(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		h.e.Shutdown(ctx)
		close(done)
	}()
	return done
}

// awaitClosed fails the test unless ch is closed within the deadline.
func (h *harness) awaitClosed(what string, ch <-chan struct{}) {
	h.t.Helper()
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		h.t.Fatalf("timed out waiting for %s", what)
	}
}

// checkAllStopped fails unless every fake agent was killed and every
// session ended with reason.
func (h *harness) checkAllStopped(reason string) {
	h.t.Helper()
	for _, a := range h.runner.all() {
		if !a.isOver() {
			h.t.Fatal("an agent still runs")
		}
	}
	if n := h.liveCount(); n != 0 {
		h.t.Fatalf("%d live sessions", n)
	}
	all, err := h.b.AgentSessions(h.ctx, board.User, false)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, s := range all {
		if s.EndReason != reason || s.EndKind != api.SessionEndShutdown {
			h.t.Fatalf("a %s session ended with %q, want %q", s.Role, s.EndReason, reason)
		}
	}
}

func TestShutdownWaitsForHandoffComments(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	t1, dev1 := h.readyDeveloper("one")
	_, dev2 := h.readyDeveloper("two")
	a1, a2 := h.agent(dev1.ID), h.agent(dev2.ID)
	ctx, cancel := context.WithTimeout(h.ctx, time.Hour)
	defer cancel()
	done := h.shutdown(ctx)
	h.waitFor("both handoff requests", func() bool { return handoffAsked(a1) && handoffAsked(a2) })
	want := "omo is stopping. Do not start anything new. Write one comment on #1 that starts with \"Handoff:\""
	if !strings.HasPrefix(a1.typed()[0], want) {
		t.Fatalf("typed %q", a1.typed())
	}
	h.comment(h.actor(dev1.ID), t1, api.CommentRequest{Body: "Handoff: half done, nothing uncommitted"})
	select {
	case <-done:
		t.Fatal("Shutdown returned before the second agent answered")
	default:
	}
	if a1.isOver() || a2.isOver() {
		t.Fatal("an agent was killed during the handoff")
	}
	waited := make(chan error, 1)
	go func() {
		_, err := h.e.Wait(h.ctx, h.actor(dev2.ID), time.Hour)
		waited <- err
	}()
	h.awaitClosed("Shutdown", done)
	if err := <-waited; err == nil {
		t.Fatal("the wait of a stopped agent returned without error")
	}
	h.checkAllStopped("omo stopped")
	if len(a1.typed()) != 1 || len(a2.typed()) != 1 {
		t.Fatalf("handoff asked more than once: %q %q", a1.typed(), a2.typed())
	}
}

func TestShutdownKillsAfterHandoffTimeout(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	_, dev1 := h.readyDeveloper("one")
	h.readyDeveloper("two")
	ctx, cancel := context.WithTimeout(h.ctx, 50*time.Millisecond)
	defer cancel()
	h.awaitClosed("Shutdown", h.shutdown(ctx))
	if !handoffAsked(h.agent(dev1.ID)) {
		t.Fatal("no handoff request")
	}
	h.checkAllStopped("omo stopped")
}

func TestShutdownWithEndedContextSkipsHandoff(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	_, dev := h.readyDeveloper("one")
	ctx, cancel := context.WithCancel(h.ctx)
	cancel()
	h.awaitClosed("Shutdown", h.shutdown(ctx))
	if n := len(h.agent(dev.ID).typed()); n != 0 {
		t.Fatalf("%d lines typed", n)
	}
	h.checkAllStopped("omo stopped")
}

func TestShutdownAsksNeitherParkedNorStartingAgentsNorCEO(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	_, parked := h.readyDeveloper("one")
	ch := h.park(h.ctx, parked.ID, time.Hour)
	starting := h.standalone("two")
	h.approve(starting)
	h.step()
	fresh := h.live(api.RoleDeveloper, starting)
	ceo, err := h.e.StartCEO(h.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Ready(h.ctx, h.actor(ceo.ID)); err != nil {
		t.Fatal(err)
	}
	// Nobody is asked, so a stop with an hour for handoffs returns at once.
	ctx, cancel := context.WithTimeout(h.ctx, time.Hour)
	defer cancel()
	h.awaitClosed("Shutdown", h.shutdown(ctx))
	if r := h.result(ch); r.err == nil || strings.Contains(r.reason, "stopping") {
		t.Fatalf("the parked wait returned %q, %v", r.reason, r.err)
	}
	for _, id := range []string{parked.ID, fresh.ID, ceo.ID} {
		if n := len(h.agent(id).typed()); n != 0 {
			t.Fatalf("%d lines typed", n)
		}
	}
	h.checkAllStopped("omo stopped")
}

func TestShutdownStartsNothingAndSecondStopProceeds(t *testing.T) {
	h := newHarness(t)
	h.typeAtOnce()
	_, dev := h.readyDeveloper("one")
	ctx, cancel := context.WithTimeout(h.ctx, time.Hour)
	defer cancel()
	first := h.shutdown(ctx)
	h.waitFor("the handoff request", func() bool { return handoffAsked(h.agent(dev.ID)) })
	next := h.standalone("two")
	h.approve(next)
	h.step()
	h.e.healthTick(h.ctx, h.clock.Now())
	h.step()
	if _, ok := h.liveOK(api.RoleDeveloper, next); ok || len(h.runner.specList()) != 1 {
		t.Fatal("an agent started during the handoff")
	}
	select {
	case <-first:
		t.Fatal("Shutdown returned before the agent answered or the time ran out")
	default:
	}
	h.awaitClosed("the second Shutdown", h.shutdown(h.ctx))
	h.awaitClosed("the first Shutdown", first)
	h.checkAllStopped("omo stopped")
}

func TestStopAllPausesAndKillsAllButCEO(t *testing.T) {
	h := newHarness(t)
	task, dev := h.readyDeveloper("one")
	reviewed := h.create(api.CreateTicket{Type: api.TypeTask, Title: "two", Review: api.ReviewAgent})
	h.approve(reviewed)
	h.step()
	h.handIn(reviewed)
	h.step()
	rev := h.live(api.RoleReviewer, reviewed)
	parkedDev := h.live(api.RoleDeveloper, reviewed)
	ceo, err := h.e.StartCEO(h.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	started := len(h.runner.specList())

	if err := h.e.StopAll(h.ctx); err != nil {
		t.Fatal(err)
	}
	if paused, err := h.b.Paused(h.ctx); err != nil || !paused {
		t.Fatalf("paused %v, %v", paused, err)
	}
	for _, id := range []string{dev.ID, rev.ID, parkedDev.ID} {
		if a := h.agent(id); !a.isKilled() {
			t.Fatal("an agent survived stop all")
		}
		if got := h.waitEnded(id); got.EndReason != "stopped by the user" || got.EndKind != api.SessionEndUser {
			t.Fatalf("ended with %q, kind %q", got.EndReason, got.EndKind)
		}
	}
	if h.agent(ceo.ID).isOver() || len(h.ceoSessions()) != 1 {
		t.Fatal("stop all ended the CEO")
	}
	h.steps(2)
	h.e.healthTick(h.ctx, h.clock.Now())
	h.steps(2)
	if n := len(h.runner.specList()); n != started {
		t.Fatalf("%d agents started while paused", n-started)
	}
	if c := h.ticket(task).Column; c != api.ColumnInProgress {
		t.Fatalf("#%d moved to %s", task, c)
	}
	if tk := h.ticket(reviewed); tk.Column != api.ColumnReview || tk.FailedStep != api.FailedNone {
		t.Fatalf("#%d is %s/%s", reviewed, tk.Column, tk.FailedStep)
	}

	// Lifting the pause resumes the work with fresh agents, as at a start.
	if err := h.b.SetPaused(h.ctx, false); err != nil {
		t.Fatal(err)
	}
	h.step()
	fresh := h.live(api.RoleDeveloper, task)
	h.live(api.RoleReviewer, reviewed)
	p, err := h.e.Ready(h.ctx, h.actor(fresh.ID))
	if err != nil || !strings.Contains(p, "This is a fresh session on work that was already started") {
		t.Fatalf("prompt of the fresh developer (%v) lacks the restart warning:\n%s", err, p)
	}
}

func TestStopAllEndsAgentBeingStarted(t *testing.T) {
	h := newHarness(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	h.runner.mu.Lock()
	h.runner.block = release
	h.runner.mu.Unlock()
	h.e.pause = func(point string) {
		if point == "spawn" {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
	}
	task := h.standalone("one")
	h.approve(task)
	h.e.pass(h.ctx)
	h.awaitClosedSignal(entered)
	stopped := make(chan error, 1)
	go func() { stopped <- h.e.StopAll(h.ctx) }()
	h.waitFor("the pause", func() bool { p, _ := h.b.Paused(h.ctx); return p })
	close(release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	h.e.settle()
	for _, a := range h.runner.all() {
		if !a.isOver() {
			t.Fatal("the agent being started survived stop all")
		}
	}
	if n := h.liveCount(); n != 0 {
		t.Fatalf("%d live sessions", n)
	}
}

// A start under way when omo is paused ends the agent it started again:
// while paused nothing starts.
func TestStartCompletedAfterPauseEndsItself(t *testing.T) {
	h := newHarness(t)
	h.e.pause = func(point string) {
		if point == "spawn" {
			if err := h.b.SetPaused(h.ctx, true); err != nil {
				t.Error(err)
			}
		}
	}
	task := h.standalone("one")
	h.approve(task)
	h.step()
	h.e.pause = nil
	agents := h.runner.all()
	if len(agents) != 1 {
		t.Fatalf("%d agents started", len(agents))
	}
	if !agents[0].isKilled() {
		t.Fatal("the agent whose start completed after the pause still runs")
	}
	if got := h.waitEnded(agents[0].id); got.EndReason != "omo was paused while the agent started" {
		t.Fatalf("ended with %q", got.EndReason)
	}
	if c := h.ticket(task).Column; c != api.ColumnInProgress {
		t.Fatalf("#%d is %s", task, c)
	}
	if err := h.b.SetPaused(h.ctx, false); err != nil {
		t.Fatal(err)
	}
	h.step()
	h.live(api.RoleDeveloper, task)
}

// Stop all agents skips an agent whose process is still being started;
// when that start completes after the round, before Stop all looks at the
// pending starts again, the start ends its agent itself.
func TestStopAllEndsAgentWhoseStartCompletesAfterARound(t *testing.T) {
	h := newHarness(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	h.runner.mu.Lock()
	h.runner.block = release
	h.runner.mu.Unlock()
	var once sync.Once
	h.e.pause = func(point string) {
		switch point {
		case "spawn":
			select {
			case entered <- struct{}{}:
			default:
			}
		case "stop-all-round":
			once.Do(func() {
				close(release)
				h.waitFor("the start to complete", func() bool { return !h.e.agentStartsPending() })
			})
		}
	}
	task := h.standalone("one")
	h.approve(task)
	h.e.pass(h.ctx)
	h.awaitClosedSignal(entered)
	if err := h.e.StopAll(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.e.settle()
	h.e.pause = nil
	for _, a := range h.runner.all() {
		if !a.isOver() {
			t.Fatal("an agent whose start completed during stop all survived it")
		}
	}
	if n := h.liveCount(); n != 0 {
		t.Fatalf("%d live sessions", n)
	}
}

// awaitClosedSignal waits for one value on ch.
func (h *harness) awaitClosedSignal(ch <-chan struct{}) {
	h.t.Helper()
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		h.t.Fatal("timed out")
	}
}

// runInBackground runs Run with ctx; the channel is closed when it returns.
func (h *harness) runInBackground(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		h.e.Run(ctx)
		close(done)
	}()
	return done
}

// schedulerEmitted reports whether a pass published the scheduler state.
func (h *harness) schedulerEmitted() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, k := range h.emits {
		if k == api.KindSchedulerChanged {
			return true
		}
	}
	return false
}

func TestShutdownBeforeRunIsNotUndone(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	h.awaitClosed("Shutdown", h.shutdown(h.ctx))
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	h.awaitClosed("Run after Shutdown", h.runInBackground(ctx))
	h.e.settle()
	if n := len(h.runner.specList()); n != 0 {
		t.Fatalf("%d agents started after Shutdown", n)
	}
	if n := h.liveCount(); n != 0 {
		t.Fatalf("%d live sessions after Shutdown", n)
	}
	// A later Run does not undo it either.
	h.awaitClosed("a second Run after Shutdown", h.runInBackground(ctx))
	if n := len(h.runner.specList()); n != 0 {
		t.Fatalf("%d agents started by a second Run", n)
	}
}

func TestShutdownDuringRunStartupStartsNothing(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	h.approve(task)
	stopped := make(chan struct{})
	h.e.pause = func(point string) {
		if point == "run-started" {
			h.e.Shutdown(h.ctx)
			close(stopped)
		}
	}
	ctx, cancel := context.WithCancel(h.ctx)
	done := h.runInBackground(ctx)
	h.awaitClosed("Shutdown during startup", stopped)
	h.waitFor("the first pass", h.schedulerEmitted)
	// The first pass has finished launching once it releases passMu.
	h.e.passMu.Lock()
	h.e.passMu.Unlock()
	h.e.settle()
	if n := len(h.runner.specList()); n != 0 {
		t.Fatalf("%d agents started after Shutdown", n)
	}
	cancel()
	h.awaitClosed("Run", done)
	if _, ok := h.liveOK(api.RoleDeveloper, task); ok {
		t.Fatal("a developer is live after Shutdown")
	}
}
