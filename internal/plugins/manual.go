package plugins

import (
	"context"
	"errors"
	"net/url"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Actions of a plugin are its manual hooks. Only the user runs them (the
// server's routes are the user's only); the hook gets the event
// {"action": <name>, "ticket": <ticket payload or null>, "project": <id object or null>}.

// Trigger is the text-only interface for existing runtime callers.
func (m *Manager) Trigger(ctx context.Context, name, action string, ticket map[string]any) (string, error) {
	result, err := m.TriggerAction(ctx, name, action, ticket, nil)
	return result.Message, err
}

// TriggerAction runs a manual hook, optionally with ticket and project context.
// project is {"id": <registered project id>} or nil; folder resolution is owned
// by the server. The caller enforces access before passing these payloads.
// Messages are cleaned and cut; navigation is validated and never rewritten.
func (m *Manager) TriggerAction(ctx context.Context, name, action string, ticket, project map[string]any) (api.PluginRunResult, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return api.PluginRunResult{}, ErrClosed
	}
	var p *plugin
	for _, e := range m.entries {
		if e.settings.Name == name && e.p != nil {
			p = e.p
		}
	}
	if p == nil {
		m.mu.Unlock()
		return api.PluginRunResult{}, ErrUnknownPlugin
	}
	var h *hook
	for _, x := range p.hooks {
		if x.Event == EventManual && x.Name == action {
			h = x
		}
	}
	if h == nil {
		m.mu.Unlock()
		return api.PluginRunResult{}, ErrUnknownAction
	}
	if h.Scope == ScopeTicket && ticket == nil {
		m.mu.Unlock()
		return api.PluginRunResult{}, ErrTicketRequired
	}
	if !p.busy.CompareAndSwap(false, true) {
		m.mu.Unlock()
		return api.PluginRunResult{}, ErrBusy
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
	var pr any
	if project != nil {
		pr = project
	}
	ev := Event{Name: EventManual, At: m.now(), Data: map[string]any{"action": action, "ticket": t, "project": pr}}
	result, err := m.runHook(runCtx, p, h, ev)
	if err != nil {
		return api.PluginRunResult{}, err
	}
	red := p.redactor()
	if lua, ok := result.(luaAnswer); ok {
		result, red = lua.value, lua.redactor
	}
	out, err := answer(result)
	if err != nil {
		m.logLine(p.name, "error in the manual hook: "+err.Error())
		return api.PluginRunResult{}, err
	}
	if out.URL != "" {
		decoded, err := url.QueryUnescape(out.URL)
		if err != nil || red.Clean(out.URL) != out.URL || red.Clean(decoded) != decoded {
			return api.PluginRunResult{}, errors.New("the action URL contains configuration secrets or hidden characters")
		}
	}
	out.Message = cutRunes(red.Clean(out.Message), maxMessageRunes)
	return out, nil
}
