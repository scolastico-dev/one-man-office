package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// Actions of a plugin are its manual hooks. Only the user runs them (the
// server's routes are the user's only); the hook gets the event
// {"action": <name>, "ticket": <ticket payload or null>}.

// Trigger runs the manual hook action of plugin name with ticket (a payload of
// TicketPayload, nil for a global action) and returns its answer for the
// user: a Lua hook's returned string or {message = "…"}, or a command's
// standard output (JSON "…" or {"message": "…"}, else the text), cleaned and
// cut to 1,000 characters. One action per plugin runs at a time. The run is
// bounded by the hook's time limit and by ctx.
//
// Errors: ErrClosed, ErrUnknownPlugin, ErrUnknownAction, ErrTicketRequired
// (a ticket action without ticket), ErrBusy, or the hook's cleaned error
// text.
func (m *Manager) Trigger(ctx context.Context, name, action string, ticket map[string]any) (string, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", ErrClosed
	}
	var p *plugin
	for _, e := range m.entries {
		if e.settings.Name == name && e.p != nil {
			p = e.p
		}
	}
	if p == nil {
		m.mu.Unlock()
		return "", ErrUnknownPlugin
	}
	var h *hook
	for _, x := range p.hooks {
		if x.Event == EventManual && x.Name == action {
			h = x
		}
	}
	if h == nil {
		m.mu.Unlock()
		return "", ErrUnknownAction
	}
	if h.Scope == ScopeTicket && ticket == nil {
		m.mu.Unlock()
		return "", ErrTicketRequired
	}
	if !p.busy.CompareAndSwap(false, true) {
		m.mu.Unlock()
		return "", ErrBusy
	}
	m.manual.Add(1)
	m.mu.Unlock()
	defer m.manual.Done()
	defer p.busy.Store(false)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()

	var t any
	if ticket != nil {
		t = ticket
	}
	ev := Event{Name: EventManual, At: m.now(), Data: map[string]any{"action": action, "ticket": t}}
	result, err := m.runHook(runCtx, p, h, ev)
	if err != nil {
		return "", err
	}
	msg, err := answer(result)
	if err != nil {
		m.logLine(p.name, "error in the manual hook: "+err.Error())
		return "", err
	}
	return cutRunes(p.redactor().Clean(msg), maxMessageRunes), nil
}

// errAnswer is the error of an answer that is neither text nor a message.
var errAnswer = errors.New(`the action's answer is neither text nor {"message": "…"}`)

// answer is the text of a hook's answer: from Lua a string, a table with a
// string message, or nil; from a command its standard output.
func answer(result any) (string, error) {
	switch v := result.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case map[string]any:
		if s, ok := v["message"].(string); ok {
			return s, nil
		}
		return "", errAnswer
	case []byte:
		out := bytes.TrimSpace(v)
		if len(out) == 0 {
			return "", nil
		}
		var parsed any
		if json.Unmarshal(out, &parsed) == nil {
			switch p := parsed.(type) {
			case string:
				return p, nil
			case map[string]any:
				if s, ok := p["message"].(string); ok {
					return s, nil
				}
			}
		}
		if !utf8.Valid(out) {
			return strings.ToValidUTF8(string(out), "\uFFFD"), nil
		}
		return string(out), nil
	}
	return "", errAnswer
}
