package engine

import (
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// setTicketModel gives ticket n a model of its own.
func (h *harness) setTicketModel(n int64, model string) {
	h.t.Helper()
	if _, err := h.b.Update(h.ctx, board.User, n, api.UpdateTicket{Model: api.Some(model)}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) forcedCount() int {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	return len(h.e.usage.forced)
}

func TestStartAnywayOnlyForOwnModel(t *testing.T) {
	h := newHarness(t)
	use := h.withUsage()
	own := h.standalone("own model")
	h.setTicketModel(own, "fake/a")
	h.approve(own)
	roles := h.standalone("role models")
	h.approve(roles)
	idle := h.standalone("not approved")

	use.set("fake/a", api.UsageSoft, 91)
	use.set("fake/b", api.UsageSoft, 92)
	h.step()
	for _, n := range []int64{own, roles} {
		if got := h.ticket(n).StartBlocked; got != api.StartBlockedUsage {
			t.Fatalf("#%d start_blocked %q", n, got)
		}
	}

	// Only a ticket that waits for usage on a model of its own may start
	// anyway.
	for _, n := range []int64{roles, idle} {
		if err := h.e.StartAnyway(h.ctx, n); errCodeOf(err) != api.CodeInvalidTransition {
			t.Fatalf("StartAnyway(#%d) = %v", n, err)
		}
	}
	if err := h.e.StartAnyway(h.ctx, 999); errCodeOf(err) != api.CodeNotFound {
		t.Fatalf("StartAnyway(#999) = %v", err)
	}
	if h.forcedCount() != 0 {
		t.Fatal("a refused StartAnyway was remembered")
	}

	// The forced start uses the ticket's own model; the other still waits.
	if err := h.e.StartAnyway(h.ctx, own); err != nil {
		t.Fatal(err)
	}
	h.step()
	dev := h.live(api.RoleDeveloper, own)
	if dev.Model != "fake/a" {
		t.Fatalf("forced start on %q", dev.Model)
	}
	if _, ok := h.liveOK(api.RoleDeveloper, roles); ok {
		t.Fatal("the ticket without its own model started")
	}
	if h.forcedCount() != 0 {
		t.Fatal("Start anyway is kept after the start it was for")
	}

	// Once: when the agent ends, the next agent waits for usage again.
	h.agent(dev.ID).exit(nil)
	h.waitEnded(dev.ID)
	h.step()
	if _, ok := h.liveOK(api.RoleDeveloper, own); ok {
		t.Fatal("a second agent started on the model over its limit")
	}
	if tk := h.ticket(own); tk.StartBlocked != api.StartBlockedUsage || tk.Column != api.ColumnInProgress {
		t.Fatalf("#%d: %s start_blocked %q", own, tk.Column, tk.StartBlocked)
	}

	// In progress it may start anyway again.
	if err := h.e.StartAnyway(h.ctx, own); err != nil {
		t.Fatal(err)
	}
	h.step()
	if again := h.live(api.RoleDeveloper, own); again.Model != "fake/a" {
		t.Fatalf("forced restart on %q", again.Model)
	}
}

func TestStartAnywayForgottenOnColumnChange(t *testing.T) {
	h := newHarness(t)
	use := h.withUsage()
	n := h.standalone("own model")
	h.setTicketModel(n, "fake/a")
	h.approve(n)
	use.set("fake/a", api.UsageSoft, 91)
	h.step()
	if got := h.ticket(n).StartBlocked; got != api.StartBlockedUsage {
		t.Fatalf("start_blocked %q", got)
	}
	if err := h.e.StartAnyway(h.ctx, n); err != nil {
		t.Fatal(err)
	}
	if _, err := h.b.Move(h.ctx, board.User, n, api.MoveRequest{Column: api.ColumnPlanning}); err != nil {
		t.Fatal(err)
	}
	h.step()
	if h.forcedCount() != 0 {
		t.Fatal("Start anyway survived a column change")
	}
	h.approve(n)
	h.step()
	if _, ok := h.liveOK(api.RoleDeveloper, n); ok {
		t.Fatal("started on the model over its limit after the column change")
	}
	if got := h.ticket(n).StartBlocked; got != api.StartBlockedUsage {
		t.Fatalf("start_blocked %q", got)
	}
}
