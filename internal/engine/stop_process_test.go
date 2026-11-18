//go:build !windows

package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

// serveAgentAPI answers the calls the fake agent makes in these tests
// (who am I, read a ticket, comment, ready, wait) from the board and the
// engine on a socket of its own, and points the agents at it.
func serveAgentAPI(t *testing.T, h *harness) {
	t.Helper()
	dir, err := os.MkdirTemp("", "omo-agents-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ep := filepath.Join(dir, "s")
	ln, err := transport.Listen(ep)
	if err != nil {
		t.Fatal(err)
	}
	reply := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			v = api.Errorf(api.CodeConflict, "%v", err)
		}
		_ = json.NewEncoder(w).Encode(v)
	}
	actor := func(r *http.Request) (board.Actor, error) {
		return h.b.ResolveSession(r.Context(), r.Header.Get(apiclient.SessionHeader))
	}
	ticketOf := func(r *http.Request) int64 {
		n, _ := strconv.ParseInt(r.PathValue("n"), 10, 64)
		return n
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		a, err := actor(r)
		reply(w, board.Me(a), err)
	})
	mux.HandleFunc("GET /api/tickets/{n}", func(w http.ResponseWriter, r *http.Request) {
		a, err := actor(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		tk, err := h.b.Get(r.Context(), a, ticketOf(r))
		reply(w, tk, err)
	})
	mux.HandleFunc("POST /api/tickets/{n}/comments", func(w http.ResponseWriter, r *http.Request) {
		a, err := actor(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		var req api.CommentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			reply(w, nil, err)
			return
		}
		res, err := h.b.Comment(r.Context(), a, ticketOf(r), req)
		reply(w, res, err)
	})
	mux.HandleFunc("POST /api/ai/ready", func(w http.ResponseWriter, r *http.Request) {
		a, err := actor(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		p, err := h.e.Ready(r.Context(), a)
		reply(w, api.ReadyResponse{Prompt: p}, err)
	})
	mux.HandleFunc("POST /api/ai/wait", func(w http.ResponseWriter, r *http.Request) {
		var req api.WaitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			reply(w, nil, err)
			return
		}
		a, err := actor(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		reason, err := h.e.Wait(r.Context(), a, time.Duration(req.Timeout))
		reply(w, api.WaitResponse{Reason: reason}, err)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	h.e.o.Socket = func() string { return ep }
}

// runUntil runs the engine's loop; the returned function stops it and
// waits for Run to return.
func runUntil(t *testing.T, h *harness) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.e.Run(ctx)
		close(done)
	}()
	stop := func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)
	return stop
}

// running waits until n agents of role run (acknowledged their start).
func (h *harness) running(role api.Role, n int) []board.EngineSession {
	h.t.Helper()
	var out []board.EngineSession
	h.waitFor("the agents to run", func() bool {
		out = nil
		for _, s := range h.snap().Sessions {
			if s.Role == role && s.State == api.SessionRunning {
				out = append(out, s)
			}
		}
		return len(out) == n
	})
	return out
}

// checkProcessesGone fails unless every agent process has ended.
func checkProcessesGone(t *testing.T, rr *recordingRunner) {
	t.Helper()
	for _, a := range rr.all() {
		<-a.Done()
	}
}

func TestProcessShutdownCollectsHandoffsAndLeavesNoProcess(t *testing.T) {
	h, rr := newProcessHarness(t, "ready\nawait-line|omo is stopping|5m\ncomment|$TICKET|Handoff: fake state\nwait|10m")
	serveAgentAPI(t, h)
	h.e.nt.grace = 10 * time.Millisecond
	t1, t2 := h.standalone("one"), h.standalone("two")
	h.approve(t1)
	h.approve(t2)
	stop := runUntil(t, h)
	h.running(api.RoleDeveloper, 2)
	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Minute)
	defer cancel()
	begin := time.Now()
	h.e.Shutdown(ctx)
	if took := time.Since(begin); took > time.Minute {
		t.Fatalf("Shutdown took %s although both agents answered", took)
	}
	for _, n := range []int64{t1, t2} {
		if c := h.commentsWith(n, "Handoff: fake state"); c != 1 {
			t.Fatalf("#%d has %d handoff comments", n, c)
		}
	}
	checkProcessesGone(t, rr)
	h.checkEnded("omo stopped")
	stop()
	if n := len(rr.all()); n != 2 {
		t.Fatalf("%d processes started, want 2", n)
	}
}

func TestProcessShutdownKillsSilentAgentsAtTheLimit(t *testing.T) {
	h, rr := newProcessHarness(t, "ready\nhang")
	serveAgentAPI(t, h)
	h.e.nt.grace = 10 * time.Millisecond
	t1, t2 := h.standalone("one"), h.standalone("two")
	h.approve(t1)
	h.approve(t2)
	stop := runUntil(t, h)
	h.running(api.RoleDeveloper, 2)
	ctx, cancel := context.WithTimeout(h.ctx, 300*time.Millisecond)
	defer cancel()
	h.e.Shutdown(ctx)
	checkProcessesGone(t, rr)
	h.checkEnded("omo stopped")
	stop()
}

func TestProcessStopAllKillsAllButCEO(t *testing.T) {
	h, rr := newProcessHarness(t, "ready\nhang")
	serveAgentAPI(t, h)
	t1, t2 := h.standalone("one"), h.standalone("two")
	h.approve(t1)
	h.approve(t2)
	runUntil(t, h)
	devs := h.running(api.RoleDeveloper, 2)
	ceo, err := h.e.StartCEO(h.ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	h.running(api.RoleCEO, 1)
	if err := h.e.StopAll(h.ctx); err != nil {
		t.Fatal(err)
	}
	var ceoAgent agents.Agent
	for _, a := range rr.all() {
		if a.SessionID() == ceo.ID {
			ceoAgent = a
			continue
		}
		<-a.Done()
	}
	for _, d := range devs {
		if got := h.waitEnded(d.ID); got.EndReason != "stopped by the user" {
			t.Fatalf("ended with %q", got.EndReason)
		}
	}
	select {
	case <-ceoAgent.Done():
		t.Fatal("stop all ended the CEO")
	default:
	}
	if paused, _ := h.b.Paused(h.ctx); !paused || len(h.ceoSessions()) != 1 {
		t.Fatalf("paused %v, %d CEO sessions", paused, len(h.ceoSessions()))
	}
	if n := len(rr.all()); n != 3 {
		t.Fatalf("%d processes started, want 3", n)
	}
}

func TestProcessSilentAgentIsRemindedThenFlagged(t *testing.T) {
	h, rr := newProcessHarness(t, "ready\nhang")
	serveAgentAPI(t, h)
	h.typeAtOnce()
	h.setHealth(15*time.Minute, 24*time.Hour)
	task := h.standalone("one")
	h.approve(task)
	h.step()
	h.running(api.RoleDeveloper, 1)
	h.check(16 * time.Minute)
	agent := rr.all()[0]
	h.waitFor("the reminder in the terminal", func() bool {
		return strings.Contains(transcript(agent), "fake: typed: omo: you have produced no output for 16m")
	})
	h.check(16 * time.Minute)
	if tk := h.ticket(task); tk.FailedStep != api.FailedAgent {
		t.Fatalf("failed step %q: the reminder's own echo counted as an answer", tk.FailedStep)
	}
	if n := strings.Count(transcript(agent), "fake: typed: omo: you have produced no output"); n != 1 {
		t.Fatalf("%d reminders in the transcript", n)
	}
	select {
	case <-agent.Done():
		t.Fatal("the flag ended the agent")
	default:
	}
}

// checkEnded fails unless every session ended with reason.
func (h *harness) checkEnded(reason string) {
	h.t.Helper()
	all, err := h.b.AgentSessions(h.ctx, board.User, false)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, s := range all {
		if s.State != api.SessionEnded || s.EndReason != reason {
			h.t.Fatalf("a %s session is %s (%q), want ended with %q", s.Role, s.State, s.EndReason, reason)
		}
	}
}

func TestProcessRecoveryAfterCrashEndsOldAgentsAndResumes(t *testing.T) {
	h, rr := newProcessHarness(t, "ready\nhang")
	serveAgentAPI(t, h)
	t1, t2 := h.standalone("one"), h.standalone("two")
	h.approve(t1)
	h.approve(t2)
	h.step()
	old := h.running(api.RoleDeveloper, 2)
	before := rr.all()

	// omo crashes: its agent processes stay, its sessions stay live. The
	// dead engine must not react to what follows.
	dead := h.e
	dead.mu.Lock()
	for _, en := range dead.entries {
		en.ending, en.endKind = "the test's crash", api.SessionEndShutdown
	}
	dead.mu.Unlock()
	e := New(Options{
		Board: h.b, Settings: h.st, Git: h.git, Layout: h.layout, Runner: rr,
		Socket: dead.o.Socket, Log: dead.o.Log, Now: h.clock.Now,
	})
	h.mu.Lock()
	h.e = e
	h.mu.Unlock()
	runUntil(t, h)

	for _, a := range before {
		<-a.Done()
	}
	fresh := h.running(api.RoleDeveloper, 2)
	for _, s := range fresh {
		if s.ID == old[0].ID || s.ID == old[1].ID {
			t.Fatal("an old session was kept")
		}
	}
	// The old sessions are over (ended by the new run, or by the dead
	// engine's watcher, which a real crash does not leave behind).
	for _, n := range []int64{t1, t2} {
		if got := h.endReasons(n); len(got) != 1 {
			t.Fatalf("#%d ended sessions %q", n, got)
		}
	}
	if n := len(rr.all()); n != 4 {
		t.Fatalf("%d processes started, want 4", n)
	}
}
