package engine

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// ceoSessions returns the live CEO sessions.
func (h *harness) ceoSessions() []board.EngineSession {
	var out []board.EngineSession
	for _, s := range h.snap().Sessions {
		if s.Role == api.RoleCEO {
			out = append(out, s)
		}
	}
	return out
}

func TestStartCEOStartsOneInItsFolder(t *testing.T) {
	h := newHarness(t, withLimit(1))
	s, err := h.e.StartCEO(h.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Role != api.RoleCEO || s.Ticket != nil || s.Workdir != h.layout.CEO {
		t.Fatalf("session %+v", s)
	}
	specs := h.runner.specList()
	if len(specs) != 1 || specs[0].Dir != h.layout.CEO {
		t.Fatalf("specs %+v", specs)
	}
	// The CEO holds no slot: work still starts under a limit of one.
	task := h.standalone("one")
	h.approve(task)
	h.step()
	h.live(api.RoleDeveloper, task)
	if again, err := h.e.StartCEO(h.ctx, false); err != nil || again.ID != s.ID {
		t.Fatalf("second start = %v, %v; want the live CEO", again.ID == s.ID, err)
	}
	if len(h.runner.specList()) != 2 {
		t.Fatal("a second CEO was started")
	}
	p, err := h.e.Ready(h.ctx, h.actor(s.ID))
	if err != nil || !strings.Contains(p, "the CEO on omo") || !strings.Contains(p, "This is a new session.") {
		t.Fatalf("ceo prompt %v:\n%s", err, p)
	}
}

func TestStartCEOIsSingleton(t *testing.T) {
	h := newHarness(t)
	var wg sync.WaitGroup
	ids := make([]string, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := h.e.StartCEO(h.ctx, false)
			if err != nil {
				t.Error(err)
			}
			ids[i] = s.ID
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatal("concurrent starts returned different CEOs")
		}
	}
	if n := len(h.ceoSessions()); n != 1 {
		t.Fatalf("%d CEO sessions", n)
	}
}

func TestStartCEOWhilePaused(t *testing.T) {
	h := newHarness(t)
	if err := h.b.SetPaused(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartCEO(h.ctx, false); err != nil {
		t.Fatal(err)
	}
	h.step()
	if n := len(h.ceoSessions()); n != 1 {
		t.Fatalf("%d CEO sessions after a pass while paused", n)
	}
}

func TestCEONewSessionReplacesOld(t *testing.T) {
	h := newHarness(t)
	old, err := h.e.StartCEO(h.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := h.e.StartCEO(h.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == old.ID {
		t.Fatal("no new session")
	}
	if h.waitEnded(old.ID).EndReason == "" || !h.agent(old.ID).isKilled() {
		t.Fatal("the old CEO is still running")
	}
	if n := len(h.ceoSessions()); n != 1 {
		t.Fatalf("%d CEO sessions", n)
	}
	// After the CEO exited by itself, a new session starts again.
	h.agent(fresh.ID).exit(nil)
	h.waitEnded(fresh.ID)
	again, err := h.e.StartCEO(h.ctx, false)
	if err != nil || again.ID == fresh.ID {
		t.Fatalf("start after exit: %v", err)
	}
}

func TestCEOSwitchedOffIsRefused(t *testing.T) {
	h := newHarness(t)
	g := h.st.General()
	g.CEO.Enabled = false
	if err := h.st.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.StartCEO(h.ctx, false); errCodeOf(err) != api.CodeValidation {
		t.Fatalf("err = %v, want validation", err)
	}
}

func TestCEOWaitIsRefused(t *testing.T) {
	h := newHarness(t)
	s, err := h.e.StartCEO(h.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Ready(h.ctx, h.actor(s.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Wait(h.ctx, h.actor(s.ID), time.Minute); errCodeOf(err) != api.CodeForbidden {
		t.Fatalf("err = %v, want forbidden", err)
	}
}

func TestCEOStartsOnMentionUnlessPaused(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	if err := h.b.SetPaused(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	first := h.comment(board.User, task, api.CommentRequest{Body: "@ceo while paused"})
	h.notifyNow()
	h.e.settle()
	if n := len(h.ceoSessions()); n != 0 {
		t.Fatal("a mention started the CEO while paused")
	}
	if err := h.b.SetPaused(h.ctx, false); err != nil {
		t.Fatal(err)
	}
	second := h.comment(board.User, task, api.CommentRequest{Body: "@ceo now"})
	h.notifyNow()
	h.e.settle()
	ceo := h.ceoSessions()
	if len(ceo) != 1 {
		t.Fatalf("%d CEO sessions after a mention", len(ceo))
	}
	p, err := h.e.Ready(h.ctx, h.actor(ceo[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{first, second} {
		if !strings.Contains(p, fmt.Sprintf("comment %d by user mentions @ceo", id)) {
			t.Fatalf("the CEO's ready answer lacks comment %d:\n%s", id, p)
		}
	}
	// Once running, a mention reaches it like any role.
	h.typeAtOnce()
	h.comment(board.User, task, api.CommentRequest{Body: "@ceo again"})
	h.notifyNow()
	h.e.settle()
	if got := h.agent(ceo[0].ID).typed(); len(got) != 1 || len(h.ceoSessions()) != 1 {
		t.Fatalf("typed %q with %d CEOs", got, len(h.ceoSessions()))
	}
}

func TestLinesForACEOThatNeverBecameReadyDoNotReachTheNext(t *testing.T) {
	h := newHarness(t)
	task := h.standalone("one")
	release := make(chan struct{})
	entered := make(chan agents.Spec, 1)
	h.runner.mu.Lock()
	h.runner.block, h.runner.entered = release, entered
	h.runner.err = errors.New("no such program")
	h.runner.mu.Unlock()
	first := h.comment(board.User, task, api.CommentRequest{Body: "@ceo first"})
	h.notifyNow()
	<-entered // the CEO session exists; its process start hangs
	second := h.comment(board.User, task, api.CommentRequest{Body: "@ceo second"})
	h.notifyNow()
	close(release) // the start fails: the session ends without omo ai ready
	h.e.settle()
	h.notifyNow()
	if n := len(h.ceoSessions()); n != 0 {
		t.Fatalf("%d live CEO sessions after the failed start", n)
	}

	h.runner.mu.Lock()
	h.runner.block, h.runner.entered, h.runner.err = nil, nil, nil
	h.runner.mu.Unlock()
	third := h.comment(board.User, task, api.CommentRequest{Body: "@ceo third"})
	h.notifyNow()
	h.e.settle()
	ceo := h.ceoSessions()
	if len(ceo) != 1 {
		t.Fatalf("%d CEO sessions", len(ceo))
	}
	p, err := h.e.Ready(h.ctx, h.actor(ceo[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{first, second} {
		if strings.Contains(p, fmt.Sprintf("comment %d by user", id)) {
			t.Fatalf("the next CEO got comment %d, queued for the CEO that never started:\n%s", id, p)
		}
	}
	if !strings.Contains(p, fmt.Sprintf("comment %d by user mentions @ceo", third)) {
		t.Fatalf("the CEO's own line is missing:\n%s", p)
	}
}

// endCEO ends every live CEO session and lets the notifier see it.
func (h *harness) endCEO() {
	h.t.Helper()
	for _, s := range h.ceoSessions() {
		h.e.end(h.ctx, s.ID, "test")
	}
	h.notifyNow()
}

func TestCEOStartsByAgentsAreRateLimited(t *testing.T) {
	h := newHarness(t)
	task, dev := h.readyDeveloper("one")
	h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "@ceo first"})
	h.notifyNow()
	h.e.settle()
	if n := len(h.ceoSessions()); n != 1 {
		t.Fatalf("%d CEO sessions after an agent's first mention", n)
	}
	h.endCEO()

	h.clock.Advance(ceoStartEvery - time.Second)
	second := h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "@ceo second"})
	h.notifyNow()
	h.e.settle()
	h.e.startDeferredCEO(h.ctx)
	h.e.settle()
	if n := len(h.ceoSessions()); n != 0 {
		t.Fatal("an agent started the CEO again within the limit")
	}

	h.clock.Advance(time.Second)
	h.e.healthTick(h.ctx, h.clock.Now())
	h.e.settle()
	ceo := h.ceoSessions()
	if len(ceo) != 1 {
		t.Fatalf("%d CEO sessions once the limit passed", len(ceo))
	}
	p, err := h.e.Ready(h.ctx, h.actor(ceo[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, fmt.Sprintf("comment %d by %s mentions @ceo", second, dev.Name)) {
		t.Fatalf("the held-back mention is not in the CEO's ready answer:\n%s", p)
	}
}

func TestCEOStartsByUserAreNotLimited(t *testing.T) {
	h := newHarness(t)
	task, dev := h.readyDeveloper("one")
	h.comment(h.actor(dev.ID), task, api.CommentRequest{Body: "@ceo first"})
	h.notifyNow()
	h.e.settle()
	h.endCEO()
	for i := range 2 {
		h.comment(board.User, task, api.CommentRequest{Body: "@ceo from the user"})
		h.notifyNow()
		h.e.settle()
		if n := len(h.ceoSessions()); n != 1 {
			t.Fatalf("user mention %d: %d CEO sessions", i+1, n)
		}
		h.endCEO()
	}
	if _, err := h.e.StartCEO(h.ctx, false); err != nil {
		t.Fatal(err)
	}
}
