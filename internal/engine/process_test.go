//go:build !windows

package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/fakeagent"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// recordingRunner starts real agent processes and remembers them, so a test
// can assert that none is left.
type recordingRunner struct {
	pty    *agents.PTYRunner
	mu     sync.Mutex
	agents []agents.Agent
}

func (r *recordingRunner) Start(spec agents.Spec) (agents.Agent, error) {
	a, err := r.pty.Start(spec)
	if err == nil {
		r.mu.Lock()
		r.agents = append(r.agents, a)
		r.mu.Unlock()
	}
	return a, err
}

func (r *recordingRunner) all() []agents.Agent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agents.Agent(nil), r.agents...)
}

// newProcessHarness is a harness whose agents are the scripted fake agent
// running script as a real process in a real terminal.
func newProcessHarness(t *testing.T, script string) (*harness, *recordingRunner) {
	t.Helper()
	exe, err := fakeagent.Command()
	if err != nil {
		t.Fatal(err)
	}
	off := false
	prof := settings.Profile{Name: "fake", CLI: api.CLICustom, Command: exe, Env: fakeagent.Env(script),
		PromptDelivery: api.PromptDelivery{Inject: &off}, Models: map[string]settings.Model{"a": {}, "b": {}}}
	rr := &recordingRunner{pty: agents.NewPTYRunner()}
	h := newHarness(t, withRunner(rr), withProfile(prof))
	rr.pty.RunDir = h.layout.Run
	// Registered after the harness, so it runs before the harness halts the
	// engine: halt first, then check.
	t.Cleanup(func() {
		h.e.halt(context.Background(), "test over")
		for _, a := range rr.all() {
			select {
			case <-a.Done():
			case <-time.After(15 * time.Second):
				t.Errorf("an agent process is still running after the test")
				_ = a.Kill()
			}
		}
		if entries, _ := os.ReadDir(filepath.Join(h.layout.Run, "agents")); len(entries) != 0 {
			t.Errorf("agent process records left behind: %d", len(entries))
		}
	})
	return h, rr
}

func TestProcessThatExitsOrCrashesIsFlaggedAfterThreeAttempts(t *testing.T) {
	for _, tc := range []struct{ script, reason string }{
		{"exit|0", "exited unexpectedly: exit status 0"},
		{"print|working\ncrash|3", "exited unexpectedly: exit status 3"},
	} {
		t.Run(strings.SplitN(tc.script, "|", 2)[0], func(t *testing.T) {
			h, rr := newProcessHarness(t, tc.script)
			task := h.standalone("one")
			h.approve(task)
			runEngine(t, h)
			h.waitFor("the failed step", func() bool { return h.ticket(task).FailedStep == api.FailedAgent })
			h.waitFor("every session to end", func() bool { return h.liveCount() == 0 })
			ss := h.sessions(task)
			if len(ss) != 3 {
				t.Fatalf("%d sessions, want 3", len(ss))
			}
			for _, s := range ss {
				if s.State != api.SessionEnded || s.EndReason != tc.reason {
					t.Fatalf("session %s ended with %q, want %q", s.State, s.EndReason, tc.reason)
				}
			}
			if n := len(rr.all()); n != 3 {
				t.Fatalf("%d processes started, want 3", n)
			}
			last := h.comments(task)
			if !strings.Contains(last[len(last)-1], "Needs attention") {
				t.Fatalf("last comment %q", last[len(last)-1])
			}
		})
	}
}

func TestProcessThatNeverRunsReadyIsKilled(t *testing.T) {
	h, rr := newProcessHarness(t, "print|started\nhang")
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	agent := rr.all()[0]
	h.waitFor("the agent's output", func() bool {
		return strings.Contains(strings.Join(agent.Tail(5), "\n"), "started")
	})
	h.clock.Advance(agents.ResolveDelivery(api.PromptDelivery{}).ReadyTimeout())
	h.e.checkStarting(h.ctx, h.clock.Now())
	if got := h.waitEnded(dev.ID); got.EndReason != "did not run omo ai ready in time" {
		t.Fatalf("end reason %q", got.EndReason)
	}
	select {
	case <-agent.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("the process outlived its session")
	}
	h.step()
	if fresh := h.live(api.RoleDeveloper, task); fresh.ID == dev.ID {
		t.Fatal("no fresh agent")
	}
}

func TestProcessThatHangsIsKilledOnCancel(t *testing.T) {
	h, rr := newProcessHarness(t, "hang")
	task := h.standalone("one")
	h.approve(task)
	h.step()
	dev := h.live(api.RoleDeveloper, task)
	if _, err := h.b.Cancel(h.ctx, board.User, task, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	h.step()
	if got := h.waitEnded(dev.ID); got.EndReason != "#1 was cancelled" {
		t.Fatalf("end reason %q", got.EndReason)
	}
	select {
	case <-rr.all()[0].Done():
	default:
		t.Fatal("the process of a cancelled ticket still runs after its session ended")
	}
}

func TestProcessesEndWhenEngineStops(t *testing.T) {
	h, rr := newProcessHarness(t, "hang")
	t1, t2 := h.standalone("one"), h.standalone("two")
	h.approve(t1)
	h.approve(t2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.e.Run(ctx)
		close(done)
	}()
	h.waitFor("two agents", func() bool { return len(rr.all()) == 2 })
	cancel()
	<-done
	for _, a := range rr.all() {
		select {
		case <-a.Done():
		default:
			t.Fatal("Run returned with an agent process still running")
		}
	}
	for _, s := range append(h.sessions(t1), h.sessions(t2)...) {
		if s.State != api.SessionEnded || s.EndReason != "omo stopped" {
			t.Fatalf("session %s %q", s.State, s.EndReason)
		}
	}
}
