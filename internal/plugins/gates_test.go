package plugins

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func gateHook(move, file string, extra map[string]any) map[string]any {
	h := map[string]any{"event": EventGate, "move": move, "lua": file}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

func manualHook(name, file string) map[string]any {
	return map[string]any{"event": EventManual, "name": name, "description": "Action " + name, "scope": ScopeTicket, "lua": file}
}

func TestGateManifestRules(t *testing.T) {
	for _, c := range []struct {
		name, raw, want string
	}{
		{"needs a move", `{"hooks":[{"event":"gate","lua":"x.lua"}]}`, "move"},
		{"unknown move", `{"hooks":[{"event":"gate","move":"review","lua":"x.lua"}]}`, "release or start"},
		{"unknown failure", `{"hooks":[{"event":"gate","move":"start","failure":"ignore","lua":"x.lua"}]}`, "failure"},
		{"move on another event", `{"hooks":[{"event":"ticket_moved","move":"start","lua":"x.lua"}]}`, "gate hooks only"},
		{"failure on another event", `{"hooks":[{"event":"ticket_moved","failure":"hold","lua":"x.lua"}]}`, "gate hooks only"},
		{"the same move twice", `{"hooks":[{"event":"gate","move":"start","lua":"x.lua"},{"event":"gate","move":"start","lua":"y.lua"}]}`, "another gate hook"},
	} {
		if _, err := ParseManifest([]byte(c.raw)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", c.name, err, c.want)
		}
	}
	for _, raw := range []string{
		`{"hooks":[{"event":"gate","move":"release","lua":"x.lua"},{"event":"gate","move":"start","failure":"hold","lua":"y.lua"}]}`,
		`{"hooks":[{"event":"gate","move":"start","failure":"go_ahead","lua":"x.lua"}]}`,
	} {
		if _, err := ParseManifest([]byte(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestGateHooksAreListedInTheInfoAndByMove(t *testing.T) {
	e := newEnv(t)
	e.plugin("a", manifestOf(gateHook("release", "g.lua", nil), gateHook("start", "g.lua", map[string]any{"failure": "hold"})), map[string]string{"g.lua": `return nil`})
	e.plugin("b", manifestOf(gateHook("start", "g.lua", nil)), map[string]string{"g.lua": `return nil`})
	e.plugin("off", manifestOf(gateHook("start", "g.lua", nil)), map[string]string{"g.lua": `return nil`})
	off := enabled("off", nil)
	off.Enabled = false
	e.load(enabled("a", nil), enabled("b", nil), off)

	if got := e.m.GatePlugins(api.GateMoveStart); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("start gates = %v, want [a b]: a disabled plugin gates nothing", got)
	}
	if got := e.m.GatePlugins(api.GateMoveRelease); !slices.Equal(got, []string{"a"}) {
		t.Errorf("release gates = %v, want [a]", got)
	}
	var hooks []api.PluginHook
	for _, h := range e.info("a").Hooks {
		if h.Event == EventGate {
			hooks = append(hooks, h)
		}
	}
	want := []api.PluginHook{{Event: EventGate, Move: "release"}, {Event: EventGate, Move: "start", Failure: "hold"}}
	if !slices.Equal(hooks, want) {
		t.Errorf("info hooks = %+v, want %+v", hooks, want)
	}
}

func runGate(t *testing.T, e *env, plugin string, move api.GateMove) GateResult {
	t.Helper()
	res, err := e.m.RunGate(t.Context(), plugin, move, map[string]any{"move": string(move)})
	if err != nil {
		t.Fatalf("RunGate(%s): %v", plugin, err)
	}
	return res
}

func TestGateAnswers(t *testing.T) {
	e := newEnv(t)
	e.plugin("p", manifestOf(
		gateHook("release", "gate.lua", nil),
		manualHook("pull", "noop.lua"),
		manualHook("skip", "noop.lua"),
	), map[string]string{
		"noop.lua": `return nil`,
		"gate.lua": `
assert(event.event == "gate" and event.data.move == "release")
local mode = config.mode
if mode == "go" then return {action = "go_ahead"} end
if mode == "string" then return "go_ahead" end
if mode == "nothing" then return nil end
if mode == "hold" then
  return {action = "hold", reason = "main is 3 commits behind", choices = {{label = "Pull", action = "pull"}, {label = "Skip", action = "skip"}}}
end
if mode == "refuse" then return {action = "refuse", reason = "no releases on fridays"} end
if mode == "secret" then return {action = "refuse", reason = "token " .. config.token .. " is bad"} end
if mode == "number" then return 42 end
if mode == "noreason" then return {action = "hold"} end
if mode == "unknown-key" then return {action = "go_ahead", extra = true} end
if mode == "bad-action" then return {action = "maybe"} end
if mode == "unknown-hook" then return {action = "hold", reason = "x", choices = {{label = "Do", action = "missing"}}} end
if mode == "choices-on-refuse" then return {action = "refuse", reason = "x", choices = {{label = "Pull", action = "pull"}}} end
error("boom " .. config.token)
`})
	for _, c := range []struct {
		mode string
		want GateResult
	}{
		{"go", GateResult{Kind: GateGoAhead}},
		{"string", GateResult{Kind: GateGoAhead}},
		{"nothing", GateResult{Kind: GateGoAhead}},
		{"hold", GateResult{Kind: GateHold, Reason: "main is 3 commits behind", Choices: []api.GateChoice{{Label: "Pull", Action: "pull"}, {Label: "Skip", Action: "skip"}}}},
		{"refuse", GateResult{Kind: GateRefuse, Reason: "no releases on fridays"}},
		{"secret", GateResult{Kind: GateRefuse, Reason: "token [redacted] is bad"}},
	} {
		e.load(enabled("p", map[string]any{"mode": c.mode, "token": "s3cret-token"}))
		got := runGate(t, e, "p", api.GateMoveRelease)
		if got.Kind != c.want.Kind || got.Reason != c.want.Reason || !slices.Equal(got.Choices, c.want.Choices) || got.Failure != "" {
			t.Errorf("mode %s: %+v, want %+v", c.mode, got, c.want)
		}
	}
	for _, mode := range []string{"number", "noreason", "unknown-key", "bad-action", "unknown-hook", "choices-on-refuse", "crash"} {
		e.load(enabled("p", map[string]any{"mode": mode, "token": "s3cret-token"}))
		got := runGate(t, e, "p", api.GateMoveRelease)
		if got.Failure == "" || strings.Contains(got.Failure, "s3cret-token") {
			t.Errorf("mode %s: failure = %q, want a failure without the configuration's secret", mode, got.Failure)
		}
		if got.FailureHolds {
			t.Errorf("mode %s: the failure holds although the manifest does not say so", mode)
		}
	}
}

func TestGateCommandHookAnswersWithJSON(t *testing.T) {
	e := newEnv(t)
	e.plugin("c", manifestOf(
		map[string]any{"event": EventGate, "move": "start", "command": helperArgs(t, "print", `{"action":"refuse","reason":"from a command"}`)},
	), nil)
	e.load(enabled("c", nil))
	if got := runGate(t, e, "c", api.GateMoveStart); got.Kind != GateRefuse || got.Reason != "from a command" {
		t.Fatalf("answer = %+v", got)
	}
	e.plugin("c", manifestOf(map[string]any{"event": EventGate, "move": "start", "command": helperArgs(t, "print", "")}), nil)
	e.load(enabled("c", map[string]any{"again": true}))
	if got := runGate(t, e, "c", api.GateMoveStart); got.Kind != GateGoAhead {
		t.Fatalf("an empty answer = %+v, want go ahead", got)
	}
	e.plugin("c", manifestOf(map[string]any{"event": EventGate, "move": "start", "command": helperArgs(t, "fail", "bad")}), nil)
	e.load(enabled("c", map[string]any{"again": 2}))
	if got := runGate(t, e, "c", api.GateMoveStart); got.Failure == "" {
		t.Fatalf("a failing command = %+v, want a failure", got)
	}
}

func TestGateFailureThatHoldsFollowsTheManifest(t *testing.T) {
	e := newEnv(t)
	e.plugin("p", manifestOf(gateHook("start", "g.lua", map[string]any{"failure": "hold"})), map[string]string{"g.lua": `error("down")`})
	e.load(enabled("p", nil))
	got := runGate(t, e, "p", api.GateMoveStart)
	if got.Failure == "" || !got.FailureHolds {
		t.Fatalf("result = %+v, want a failure that holds", got)
	}
	if !strings.Contains(e.logText("p"), "gate hook") {
		t.Errorf("the plugin's log does not name the failed gate hook: %q", e.logText("p"))
	}
}

func TestGateTimeoutIsAFailure(t *testing.T) {
	e := newEnv(t)
	e.plugin("p", manifestOf(gateHook("start", "g.lua", map[string]any{"timeout": "1s"})), map[string]string{"g.lua": `while true do end`})
	e.load(enabled("p", nil))
	got := runGate(t, e, "p", api.GateMoveStart)
	if got.Failure == "" {
		t.Fatalf("result = %+v, want a failure by the time limit", got)
	}
}

func TestGateOfAPluginThatIsGone(t *testing.T) {
	e := newEnv(t)
	e.plugin("p", manifestOf(gateHook("start", "g.lua", nil)), map[string]string{"g.lua": `return nil`})
	s := enabled("p", nil)
	e.load(s)
	if _, err := e.m.RunGate(t.Context(), "p", api.GateMoveRelease, nil); !errors.Is(err, ErrNoGate) {
		t.Errorf("a move the plugin does not gate: %v, want ErrNoGate", err)
	}
	s.Enabled = false
	e.load(s)
	if _, err := e.m.RunGate(t.Context(), "p", api.GateMoveStart, nil); !errors.Is(err, ErrNoGate) {
		t.Errorf("a disabled plugin: %v, want ErrNoGate", err)
	}
	if _, err := e.m.RunGate(t.Context(), "other", api.GateMoveStart, nil); !errors.Is(err, ErrNoGate) {
		t.Errorf("an unknown plugin: %v, want ErrNoGate", err)
	}
}

type fakeGateDesk struct {
	mu    sync.Mutex
	calls []string
}

func (d *fakeGateDesk) GateRelease(_ context.Context, plugin string, ticket int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "release "+plugin+" "+itoa(ticket))
	return nil
}

func (d *fakeGateDesk) GateCancel(_ context.Context, plugin string, ticket int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "cancel "+plugin+" "+itoa(ticket))
	return errors.New("no such hold")
}

func TestLuaHelpersReleaseAndCancelAHeldMove(t *testing.T) {
	e := newEnv(t)
	desk := &fakeGateDesk{}
	e.m.o.Gates = desk
	e.plugin("p", manifestOf(
		manualHook("go", "go.lua"),
		manualHook("stop", "stop.lua"),
	), map[string]string{
		"go.lua":   `local ok, err = omo.gate_release(event.data.ticket.number); assert(ok == true, tostring(err)); return "released"`,
		"stop.lua": `local ok, err = omo.gate_cancel(event.data.ticket.number); assert(ok == nil and err ~= nil); return err`,
	})
	e.load(enabled("p", nil))
	ticket := TicketPayload(api.TicketSummary{Number: 12, Title: "t"}, "")
	if got, err := e.m.Trigger(t.Context(), "p", "go", ticket); err != nil || got != "released" {
		t.Fatalf("go = %q, %v", got, err)
	}
	if got, err := e.m.Trigger(t.Context(), "p", "stop", ticket); err != nil || got == "" {
		t.Fatalf("stop = %q, %v", got, err)
	}
	desk.mu.Lock()
	defer desk.mu.Unlock()
	if !slices.Equal(desk.calls, []string{"release p 12", "cancel p 12"}) {
		t.Fatalf("calls = %v", desk.calls)
	}
}
