//go:build !windows

package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

// serveAgents answers omo ai ready and omo ai wait from the engine on a
// socket of its own and points the agents at it. It returns the number of
// waits in progress.
func serveAgents(t *testing.T, h *harness) *atomic.Int32 {
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
	var waits atomic.Int32
	reply := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			v = api.Errorf(api.CodeConflict, "%v", err)
		}
		_ = json.NewEncoder(w).Encode(v)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/ai/ready", func(w http.ResponseWriter, r *http.Request) {
		a, err := h.b.ResolveSession(r.Context(), r.Header.Get(apiclient.SessionHeader))
		if err != nil {
			reply(w, nil, err)
			return
		}
		p, err := h.e.Ready(r.Context(), a)
		reply(w, api.ReadyResponse{Prompt: p}, err)
	})
	mux.HandleFunc("POST /api/ai/wait", func(w http.ResponseWriter, r *http.Request) {
		waits.Add(1)
		defer waits.Add(-1)
		var req api.WaitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			reply(w, nil, err)
			return
		}
		a, err := h.b.ResolveSession(r.Context(), r.Header.Get(apiclient.SessionHeader))
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
	return &waits
}

// transcript returns the end of an agent's transcript.
func transcript(a agents.Agent) string { return strings.Join(a.Tail(200), "\n") }

func TestProcessReadyReturnsRolePrompt(t *testing.T) {
	out := filepath.Join(t.TempDir(), "prompt.txt")
	h, rr := newProcessHarness(t, "ready\nsave-prompt|"+out+"\nprint|saved\nhang")
	serveAgents(t, h)
	task := h.standalone("one")
	h.approve(task)
	runEngine(t, h)
	h.waitFor("the agent to save its prompt", func() bool {
		return len(rr.all()) == 1 && strings.Contains(transcript(rr.all()[0]), "saved")
	})
	dev := h.live(api.RoleDeveloper, task)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	p := string(data)
	if !strings.HasPrefix(p, fmt.Sprintf("You are %s, the developer of task #%d", dev.Name, task)) || strings.Contains(p, dev.ID) {
		t.Fatalf("prompt:\n%s", p)
	}
	if st := h.sessionState(dev.ID); st != api.SessionRunning {
		t.Fatalf("state %s after ready", st)
	}
}

func TestProcessWaitIsWokenByMention(t *testing.T) {
	h, rr := newProcessHarness(t, "ready\nwait|1h\nprint|after the wait\nhang")
	serveAgents(t, h)
	task := h.standalone("one")
	h.approve(task)
	runEngine(t, h)
	var dev board.EngineSession
	h.waitFor("the developer to wait", func() bool {
		s, ok := h.liveOK(api.RoleDeveloper, task)
		dev = s
		return ok && s.State == api.SessionWaiting
	})
	if sched := h.e.Scheduler(); sched.Tasks.InWork != 1 {
		t.Fatalf("scheduler %+v: a waiting agent's ticket still holds its slot", sched)
	}
	id := h.comment(board.User, task, api.CommentRequest{Body: "@developer have a look"})
	want := fmt.Sprintf("fake: woke up: omo: #%d comment %d by user mentions @developer: omo ai comments %d --since %d", task, id, task, id-1)
	h.waitFor("the wait to return", func() bool { return len(rr.all()) == 1 && strings.Contains(transcript(rr.all()[0]), "after the wait") })
	if tr := transcript(rr.all()[0]); !strings.Contains(tr, want) || strings.Contains(tr, "fake: typed:") {
		t.Fatalf("transcript lacks %q or has a typed line:\n%s", want, tr)
	}
	h.waitFor("the session to run again", func() bool { return h.sessionState(dev.ID) == api.SessionRunning })
}

func TestProcessLineIsTypedIntoWorkingAgent(t *testing.T) {
	h, rr := newProcessHarness(t, "ready\nawait-line|mentions @developer|60s\nprint|the line arrived\nhang")
	serveAgents(t, h)
	h.e.nt.grace = 10 * time.Millisecond
	task := h.standalone("one")
	h.approve(task)
	runEngine(t, h)
	h.waitFor("the developer to run", func() bool {
		s, ok := h.liveOK(api.RoleDeveloper, task)
		return ok && s.State == api.SessionRunning
	})
	h.comment(board.User, task, api.CommentRequest{Body: "@developer have a look"})
	h.waitFor("the typed line", func() bool {
		return len(rr.all()) == 1 && strings.Contains(transcript(rr.all()[0]), "the line arrived")
	})
	if tr := transcript(rr.all()[0]); strings.Count(tr, "fake: typed: omo: #") != 1 {
		t.Fatalf("want exactly one typed line:\n%s", tr)
	}
}

func TestProcessWaitTimeoutsInvestigate(t *testing.T) {
	h, rr := newProcessHarness(t, "ready\nwait|1ms\nprint|first\nwait|1ms\nprint|second\nwait|1ms\nprint|waited\nhang")
	serveAgents(t, h)
	task := h.standalone("one")
	h.approve(task)
	runEngine(t, h)
	h.waitFor("three waits", func() bool { return len(rr.all()) == 1 && strings.Contains(transcript(rr.all()[0]), "waited") })
	// The transcript view folds a line that repeats an earlier one, so the
	// order of the distinct lines is what is checked: only the third
	// timeout asks the agent to investigate.
	tr := transcript(rr.all()[0])
	woke := strings.Index(tr, "fake: woke up: timeout: nothing arrived in 1ms.")
	second := strings.Index(tr, "\nsecond")
	investigate := strings.Index(tr, "This is timeout 3 in a row")
	if woke < 0 || second < woke || investigate < second || strings.Count(tr, "in a row") != 1 {
		t.Fatalf("transcript:\n%s", tr)
	}
}

func TestProcessWaitEndsWithTheAgent(t *testing.T) {
	h, rr := newProcessHarness(t, "ready\nwait|1h")
	waits := serveAgents(t, h)
	task := h.standalone("one")
	h.approve(task)
	runEngine(t, h)
	h.waitFor("the developer to wait", func() bool {
		s, ok := h.liveOK(api.RoleDeveloper, task)
		return ok && s.State == api.SessionWaiting && waits.Load() == 1
	})
	if _, err := h.b.Cancel(h.ctx, board.User, task, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the agent to end", func() bool {
		select {
		case <-rr.all()[0].Done():
			return true
		default:
			return false
		}
	})
	h.waitFor("the wait to be released", func() bool { return waits.Load() == 0 })
}
