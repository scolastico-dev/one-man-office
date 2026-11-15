package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

func errCodeOf(err error) string {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestWaitTimesOutAndInvestigatesEveryThird(t *testing.T) {
	h := newHarness(t)
	_, dev := h.readyDeveloper("one")
	a := h.actor(dev.ID)
	for i := 1; i <= 7; i++ {
		r, err := h.e.Wait(h.ctx, a, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(r, "timeout: nothing arrived in 1ms.") {
			t.Fatalf("wait %d = %q", i, r)
		}
		investigate := strings.Contains(r, "in a row")
		if want := i == 3 || i == 7; investigate != want {
			t.Fatalf("wait %d = %q; investigate %v, want %v", i, r, investigate, want)
		}
		if i == 3 && !strings.Contains(r, "This is timeout 3 in a row") {
			t.Fatalf("wait 3 = %q", r)
		}
		if i == 4 {
			// Something delivered resets the count.
			h.e.notify(dev.ID, "omo: something")
			if r, err := h.e.Wait(h.ctx, a, time.Hour); err != nil || r != "omo: something" {
				t.Fatalf("wait = %q, %v", r, err)
			}
		}
		if i == 7 {
			if !strings.Contains(r, "This is timeout 3 in a row") {
				t.Fatalf("the count did not restart after a delivery: %q", r)
			}
		}
	}
	if st := h.sessionState(dev.ID); st != api.SessionRunning {
		t.Fatalf("state %s after the waits", st)
	}
}

func TestWaitReleasesSlot(t *testing.T) {
	h := newHarness(t, withLimit(1))
	_, dev := h.readyDeveloper("one")
	next := h.standalone("two")
	h.approve(next)
	h.step()
	if _, ok := h.liveOK(api.RoleDeveloper, next); ok {
		t.Fatal("the second task started over the limit")
	}
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	ch := h.park(ctx, dev.ID, time.Hour)
	h.step()
	h.live(api.RoleDeveloper, next)
	cancel()
	if r := h.result(ch); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("wait = %q, %v", r.reason, r.err)
	}
}

func TestWaitValidatesTimeout(t *testing.T) {
	h := newHarness(t)
	_, dev := h.readyDeveloper("one")
	a := h.actor(dev.ID)
	for _, d := range []time.Duration{0, -time.Second, 24*time.Hour + time.Second} {
		if _, err := h.e.Wait(h.ctx, a, d); errCodeOf(err) != api.CodeValidation {
			t.Fatalf("timeout %s: %v, want validation", d, err)
		}
	}
	if r, err := h.e.Wait(h.ctx, a, time.Nanosecond); err != nil || !strings.HasPrefix(r, "timeout:") {
		t.Fatalf("wait = %q, %v", r, err)
	}
}

func TestWaitIsForAgentsThatWait(t *testing.T) {
	h := newHarness(t)
	if _, err := h.e.Wait(h.ctx, board.User, time.Minute); errCodeOf(err) != api.CodeForbidden {
		t.Fatalf("user: %v", err)
	}
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	if _, err := h.e.Wait(h.ctx, h.actor(dev.ID), time.Minute); errCodeOf(err) != api.CodeInvalidTransition {
		t.Fatalf("before ready: %v, want invalid_transition", err)
	}
}

func TestSecondWaitReplacesFirst(t *testing.T) {
	h := newHarness(t)
	_, dev := h.readyDeveloper("one")
	first := h.park(h.ctx, dev.ID, time.Hour)
	second := make(chan waitResult, 1)
	a := h.actor(dev.ID)
	go func() {
		r, err := h.e.Wait(h.ctx, a, time.Hour)
		second <- waitResult{r, err}
	}()
	if r := h.result(first); errCodeOf(r.err) != api.CodeConflict {
		t.Fatalf("first wait = %q, %v; want conflict", r.reason, r.err)
	}
	if st := h.sessionState(dev.ID); st != api.SessionWaiting {
		t.Fatalf("state %s while the second wait is parked", st)
	}
	h.e.notify(dev.ID, "omo: hello")
	if r := h.result(second); r.err != nil || r.reason != "omo: hello" {
		t.Fatalf("second wait = %q, %v", r.reason, r.err)
	}
}

func TestWaitIsReleasedWhenSessionEnds(t *testing.T) {
	h := newHarness(t)
	task, dev := h.readyDeveloper("one")
	ch := h.park(h.ctx, dev.ID, time.Hour)
	if _, err := h.b.Cancel(h.ctx, board.User, task, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	h.step()
	if r := h.result(ch); r.err == nil {
		t.Fatalf("wait of an ended session returned %q", r.reason)
	}
	h.notifyNow()
	h.e.nt.mu.Lock()
	n := len(h.e.nt.boxes)
	h.e.nt.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d mailboxes left after the session ended", n)
	}
}

func TestWaitIsReleasedWhenEngineStops(t *testing.T) {
	h := newHarness(t)
	_, dev := h.readyDeveloper("one")
	ch := h.park(h.ctx, dev.ID, time.Hour)
	h.e.halt(h.ctx, "omo stopped")
	if r := h.result(ch); r.err == nil {
		t.Fatalf("wait returned %q after omo stopped", r.reason)
	}
}

func TestWaitCallerGoneKeepsTheLine(t *testing.T) {
	h := newHarness(t)
	h.e.nt.grace = time.Hour
	_, dev := h.readyDeveloper("one")
	ctx, cancel := context.WithCancel(h.ctx)
	ch := h.park(ctx, dev.ID, time.Hour)
	cancel()
	if r := h.result(ch); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("wait = %q, %v", r.reason, r.err)
	}
	if st := h.sessionState(dev.ID); st != api.SessionRunning {
		t.Fatalf("state %s after the caller left", st)
	}
	h.e.notify(dev.ID, "omo: later")
	if r, err := h.e.Wait(h.ctx, h.actor(dev.ID), time.Hour); err != nil || r != "omo: later" {
		t.Fatalf("next wait = %q, %v", r, err)
	}
}

func TestWaitMinimum(t *testing.T) {
	h := newHarness(t)
	h.e.minWait = MinWait
	_, dev := h.readyDeveloper("one")
	a := h.actor(dev.ID)
	_, err := h.e.Wait(h.ctx, a, MinWait-time.Second)
	if errCodeOf(err) != api.CodeValidation || !strings.Contains(err.Error(), "at least 1m0s") {
		t.Fatalf("a wait below the minimum: %v, want a validation error naming 1m0s", err)
	}
	// The minimum itself is accepted: the wait parks.
	ctx, cancel := context.WithCancel(h.ctx)
	ch := h.park(ctx, dev.ID, MinWait)
	cancel()
	if r := h.result(ch); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("wait of the minimum = %q, %v", r.reason, r.err)
	}
}
