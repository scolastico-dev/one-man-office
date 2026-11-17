package plugins

import (
	"errors"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestManualTicketAction(t *testing.T) {
	e := newEnv(t)
	manual := func(name, scope string, h map[string]any) map[string]any {
		h["event"], h["name"], h["description"], h["scope"] = EventManual, name, "Action "+name, scope
		return h
	}
	e.plugin("p", manifestOf(
		manual("hello", ScopeTicket, map[string]any{"lua": "hello.lua"}),
		manual("text", ScopeGlobal, map[string]any{"lua": "text.lua"}),
		manual("json", ScopeGlobal, map[string]any{"command": helperArgs(t, "print", `{"message":"from json"}`)}),
		manual("plain", ScopeGlobal, map[string]any{"command": helperArgs(t, "print", "plain words\n")}),
		manual("long", ScopeGlobal, map[string]any{"lua": "long.lua"}),
		manual("list", ScopeGlobal, map[string]any{"lua": "list.lua"}),
		manual("boom", ScopeGlobal, map[string]any{"lua": "boom.lua"}),
	), map[string]string{
		"hello.lua": `assert(event.event == "manual" and event.data.action == "hello"); return {message = "hello #" .. event.data.ticket.number}`,
		"text.lua":  `assert(event.data.ticket == nil); return "just text"`,
		"long.lua":  `return string.rep("x", 5000)`,
		"list.lua":  `return {1, 2}`,
		"boom.lua":  `error("nope")`,
	})
	e.load(enabled("p", nil))
	ticket := TicketPayload(api.TicketSummary{Number: 12, Title: "t"}, "")
	ctx := t.Context()
	for _, c := range []struct {
		action string
		ticket map[string]any
		want   string
	}{
		{"hello", ticket, "hello #12"},
		{"text", nil, "just text"},
		{"json", nil, "from json"},
		{"plain", nil, "plain words"},
	} {
		got, err := e.m.Trigger(ctx, "p", c.action, c.ticket)
		if err != nil || got != c.want {
			t.Errorf("%s = %q, %v; want %q", c.action, got, err, c.want)
		}
	}
	if got, err := e.m.Trigger(ctx, "p", "long", nil); err != nil || len([]rune(got)) != maxMessageRunes || !strings.HasSuffix(got, "…") {
		t.Errorf("long answer: %d characters, %v", len([]rune(got)), err)
	}
	if _, err := e.m.Trigger(ctx, "p", "list", nil); err == nil || !strings.Contains(err.Error(), "neither text") {
		t.Errorf("list answer: %v", err)
	}
	if _, err := e.m.Trigger(ctx, "p", "boom", nil); err == nil || err.Error() != "line 1: nope" {
		t.Errorf("failing action: %v", err)
	}
	for _, c := range []struct {
		plugin, action string
		ticket         map[string]any
		want           error
	}{
		{"p", "hello", nil, ErrTicketRequired},
		{"p", "missing", nil, ErrUnknownAction},
		{"other", "hello", ticket, ErrUnknownPlugin},
	} {
		if _, err := e.m.Trigger(ctx, c.plugin, c.action, c.ticket); !errors.Is(err, c.want) {
			t.Errorf("%s/%s: %v, want %v", c.plugin, c.action, err, c.want)
		}
	}
}

func TestManualActionOfDisabledPlugin(t *testing.T) {
	e := newEnv(t)
	e.plugin("p", manifestOf(map[string]any{"event": EventManual, "name": "a", "description": "A", "lua": "a.lua"}), map[string]string{"a.lua": `return "a"`})
	s := enabled("p", nil)
	s.Enabled = false
	e.load(s)
	if _, err := e.m.Trigger(t.Context(), "p", "a", nil); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("action of a disabled plugin: %v", err)
	}
}
