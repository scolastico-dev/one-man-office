package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	lua "github.com/yuin/gopher-lua"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/luabox"
)

// A gate is a hook with the event "gate" that decides whether a ticket may
// make a move: its manifest names the move ("release": Planning to Ready For
// Dev, "start": Ready For Dev to In Progress) and, optionally, that a
// failure of the hook holds the move ("failure": "hold") instead of
// letting it go ahead. Gates are never dispatched with the board's events:
// omo asks them, one at a time per plugin, through RunGate, and records the
// answer on the ticket.
//
// The hook answers with go ahead (no answer, "go_ahead" or
// {action = "go_ahead"}), hold ({action = "hold", reason, choices}; each
// choice is {label, action} naming a manual hook of the same plugin) or
// refuse ({action = "refuse", reason}). A held move is let go later by the
// plugin from any hook with omo.gate_release(ticket) (a command hook runs
// "omo ai gate release N", which names the plugin by OMO_PLUGIN_NAME) and
// can be cancelled with omo.gate_cancel(ticket).

// The answers of a gate hook (GateResult.Kind).
const (
	GateGoAhead = "go_ahead"
	GateHold    = "hold"
	GateRefuse  = "refuse"
)

// Values of a gate hook's "failure".
const (
	FailureGoAhead = "go_ahead"
	FailureHold    = "hold"
)

// Bounds of a gate answer.
const (
	maxGateChoices = 8
	maxGateLabel   = 200
)

// ErrNoGate is the error of an ask of a plugin that is not loaded and
// enabled, or has no gate hook for the move any more: the caller takes the
// gate as given up.
var ErrNoGate = errors.New("the plugin has no gate for this move")

// GateDesk is what omo.gate_release and omo.gate_cancel reach: the board's
// gates, on behalf of a plugin. Its methods must honour ctx.
type GateDesk interface {
	// GateRelease is the plugin's go-ahead for the held move of a ticket.
	GateRelease(ctx context.Context, plugin string, ticket int) error
	// GateCancel cancels the move of a ticket that a plugin holds.
	GateCancel(ctx context.Context, plugin string, ticket int) error
}

// GateResult is what a gate hook decided. When the hook could not decide
// (it crashed, ran out of time or gave an answer that is none), Failure is
// the cleaned reason, FailureHolds is what the manifest says to do about it
// and Kind is empty.
type GateResult struct {
	Kind         string
	Reason       string
	Choices      []api.GateChoice
	Failure      string
	FailureHolds bool
}

// gateHook is the plugin's gate hook for move, or nil.
func (p *plugin) gateHook(move api.GateMove) *hook {
	for _, h := range p.hooks {
		if h.Event == EventGate && h.Move == string(move) {
			return h
		}
	}
	return nil
}

// GatePlugins names the loaded, enabled plugins that have a gate hook for
// move, sorted.
func (m *Manager) GatePlugins(move api.GateMove) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, e := range m.entries {
		if e.p != nil && e.settings.Enabled && e.p.gateHook(move) != nil {
			out = append(out, e.settings.Name)
		}
	}
	return out
}

// RunGate runs plugin's gate hook for move with data as the event's data
// and decodes its answer. A plugin runs one gate at a time. It fails with
// ErrNoGate when the plugin is not loaded and enabled or has no such hook,
// and with ErrClosed when the plugin was stopped or reloaded during the
// run (nothing is known then; ask again). Anything else the hook does
// wrong is in the result's Failure.
func (m *Manager) RunGate(ctx context.Context, name string, move api.GateMove, data map[string]any) (GateResult, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return GateResult{}, ErrClosed
	}
	var p *plugin
	for _, e := range m.entries {
		if e.settings.Name == name && e.p != nil && e.settings.Enabled {
			p = e.p
		}
	}
	var h *hook
	if p != nil {
		h = p.gateHook(move)
	}
	m.mu.Unlock()
	if h == nil {
		return GateResult{}, ErrNoGate
	}
	p.gateMu.Lock()
	defer p.gateMu.Unlock()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()
	if data == nil {
		data = map[string]any{}
	}
	ev := Event{Name: EventGate, At: m.now(), Data: data}
	result, err := m.runHook(runCtx, p, h, ev)
	if p.ctx.Err() != nil || m.ctx.Err() != nil {
		return GateResult{}, ErrClosed
	}
	if err != nil {
		if ctx.Err() != nil {
			return GateResult{}, ctx.Err()
		}
		return GateResult{Failure: err.Error(), FailureHolds: h.Failure == FailureHold}, nil
	}
	red := p.redactor()
	if lua, ok := result.(luaAnswer); ok {
		result, red = lua.value, lua.redactor
	}
	res, err := parseGateAnswer(result, red, manualNames(p))
	if err != nil {
		m.logLine(p.name, "error in the gate hook: "+err.Error())
		return GateResult{Failure: err.Error(), FailureHolds: h.Failure == FailureHold}, nil
	}
	return res, nil
}

// manualNames are the manual hooks of p, which a choice may name.
func manualNames(p *plugin) map[string]bool {
	out := map[string]bool{}
	for _, h := range p.hooks {
		if h.Event == EventManual {
			out[h.Name] = true
		}
	}
	return out
}

var errGateAnswer = errors.New(`the gate's answer is none of: nothing, "go_ahead", or an object with action go_ahead, hold or refuse`)

// parseGateAnswer decodes what a gate hook returned: a Lua value or the
// standard output of a command.
func parseGateAnswer(result any, red *luabox.Redactor, actions map[string]bool) (GateResult, error) {
	switch v := result.(type) {
	case nil:
		return GateResult{Kind: GateGoAhead}, nil
	case string:
		if strings.TrimSpace(v) == GateGoAhead {
			return GateResult{Kind: GateGoAhead}, nil
		}
		return GateResult{}, errGateAnswer
	case []byte:
		out := bytes.TrimSpace(v)
		if len(out) == 0 {
			return GateResult{Kind: GateGoAhead}, nil
		}
		var parsed any
		dec := json.NewDecoder(bytes.NewReader(out))
		dec.UseNumber()
		if err := dec.Decode(&parsed); err != nil {
			return parseGateAnswer(string(out), red, actions)
		}
		if _, ok := parsed.([]byte); ok {
			return GateResult{}, errGateAnswer
		}
		return parseGateAnswer(parsed, red, actions)
	case map[string]any:
		return parseGateObject(v, red, actions)
	}
	return GateResult{}, errGateAnswer
}

func parseGateObject(v map[string]any, red *luabox.Redactor, actions map[string]bool) (GateResult, error) {
	for k := range v {
		switch k {
		case "action", "reason", "choices":
		default:
			return GateResult{}, errGateAnswer
		}
	}
	action, _ := v["action"].(string)
	res := GateResult{Kind: action}
	switch action {
	case GateGoAhead, GateHold, GateRefuse:
	default:
		return GateResult{}, errGateAnswer
	}
	if r, ok := v["reason"]; ok {
		s, isString := r.(string)
		if !isString {
			return GateResult{}, errors.New("the gate's reason is not text")
		}
		res.Reason = cutRunes(strings.TrimSpace(red.Clean(s)), maxMessageRunes)
	}
	if action != GateGoAhead && res.Reason == "" {
		return GateResult{}, fmt.Errorf("a %s answer needs a reason", strings.ReplaceAll(action, "_", " "))
	}
	if c, ok := v["choices"]; ok {
		if action != GateHold {
			return GateResult{}, errors.New("choices belong to a hold")
		}
		list, isList := c.([]any)
		if !isList || len(list) > maxGateChoices {
			return GateResult{}, fmt.Errorf("choices is a list of at most %d buttons", maxGateChoices)
		}
		for _, item := range list {
			obj, isObj := item.(map[string]any)
			label, _ := obj["label"].(string)
			name, _ := obj["action"].(string)
			if !isObj || len(obj) != 2 || strings.TrimSpace(label) == "" || !actions[name] {
				return GateResult{}, errors.New("a choice is {label, action} and names a manual hook of the plugin")
			}
			res.Choices = append(res.Choices, api.GateChoice{Label: cutRunes(strings.TrimSpace(red.Clean(label)), maxGateLabel), Action: name})
		}
	}
	return res, nil
}

// luaGateRelease is omo.gate_release(ticket) and luaGateCancel
// omo.gate_cancel(ticket): true, or nil and a phrase.
func (m *Manager) luaGate(box *luabox.Box, p *plugin, call func(GateDesk, context.Context, string, int) error) lua.LGFunction {
	return func(L *lua.LState) int {
		n := L.CheckInt(1)
		if n <= 0 {
			L.RaiseError("a ticket number is a positive number")
		}
		if m.o.Gates == nil {
			return fail(L, "gates are unavailable")
		}
		if err := call(m.o.Gates, box.Context(), p.name, n); err != nil {
			return fail(L, box.Redactor().Clean(err.Error()))
		}
		L.Push(lua.LTrue)
		return 1
	}
}
