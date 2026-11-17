package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDesk plays the engine's running agents: it lists agents and records
// what was sent to each terminal.
type fakeDesk struct {
	mu     sync.Mutex
	agents []RunningAgent
	sent   map[string][]string
	fail   error
}

func (d *fakeDesk) RunningAgents(ctx context.Context) ([]RunningAgent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail != nil {
		return nil, d.fail
	}
	return slices.Clone(d.agents), nil
}

func (d *fakeDesk) SendToAgent(ctx context.Context, terminal, text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range d.agents {
		if a.Terminal == terminal {
			if d.sent == nil {
				d.sent = map[string][]string{}
			}
			d.sent[terminal] = append(d.sent[terminal], text)
			return nil
		}
	}
	return ErrNoSuchAgent
}

func (d *fakeDesk) set(agents ...RunningAgent) {
	d.mu.Lock()
	d.agents = agents
	d.mu.Unlock()
}

// texts is what terminal got, in order.
func (d *fakeDesk) texts(terminal string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.sent[terminal])
}

func (d *fakeDesk) total() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, s := range d.sent {
		n += len(s)
	}
	return n
}

// newEnvWithDesk is newEnv whose manager reaches desk (nil: no desk).
func newEnvWithDesk(t *testing.T, desk AgentDesk) *env {
	t.Helper()
	e := newEnv(t)
	e.m.Close()
	o := e.m.o
	o.Agents = desk
	e.m = New(o)
	return e
}

func TestPauseEventsLoad(t *testing.T) {
	m, err := ParseManifest([]byte(`{"hooks":[{"event":"omo_paused","lua":"a.lua"},{"event":"omo_resumed","command":["x"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Hooks[0].Event != EventPaused || m.Hooks[1].Event != EventResumed {
		t.Fatalf("hooks %+v", m.Hooks)
	}
	if !slices.Contains(Events, EventPaused) || !slices.Contains(Events, EventResumed) {
		t.Fatalf("Events %v", Events)
	}
}

func TestLuaAgentHelpers(t *testing.T) {
	desk := &fakeDesk{}
	desk.set(
		RunningAgent{Terminal: "term-ceo", Name: "Ada", Role: "ceo", Model: "fake/a", Purpose: "work"},
		RunningAgent{Terminal: "term-dev", Name: "Bob", Role: "developer", Model: "fake/b", Purpose: "work", Project: "app", Ticket: 7},
	)
	e := newEnvWithDesk(t, desk)
	e.plugin("p", manifestOf(luaHook(EventPaused, "h.lua")), map[string]string{"h.lua": `
local list = omo.agents()
local rows = {}
for _, a in ipairs(list) do
  rows[#rows + 1] = { terminal = a.terminal, name = a.name, role = a.role, model = a.model, purpose = a.purpose, project = a.project, ticket = a.ticket or "none" }
end
omo.store_set("agents", rows)
local ok1 = omo.send_to_agent("term-dev", "halt\nnow")
local ok2, why2 = omo.send_to_agent("term-gone", "halt")
local ok3, why3 = omo.send_to_agent("term-dev", "   ")
local ok4, why4 = pcall(omo.send_to_agent, "term-dev", string.rep("x", 5000))
omo.store_set("results", { ok1 = ok1, why2 = why2, ok2 = ok2 == nil, why3 = why3, ok3 = ok3 == nil, ok4 = ok4, why4 = tostring(why4) })
`})
	e.load(enabled("p", nil))
	e.m.Dispatch(Event{Name: EventPaused, At: time.Now(), Data: map[string]any{}})
	eventually(t, "the hook", func() bool { return e.stored("p", "results") != "" })

	var rows []map[string]any
	if err := json.Unmarshal([]byte(e.stored("p", "agents")), &rows); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"terminal": "term-ceo", "name": "Ada", "role": "ceo", "model": "fake/a", "purpose": "work", "project": "", "ticket": "none"},
		{"terminal": "term-dev", "name": "Bob", "role": "developer", "model": "fake/b", "purpose": "work", "project": "app", "ticket": float64(7)},
	}
	if len(rows) != len(want) {
		t.Fatalf("agents %v", rows)
	}
	for i := range want {
		for k, v := range want[i] {
			if rows[i][k] != v {
				t.Fatalf("agent %d: %s = %v, want %v (%v)", i, k, rows[i][k], v, rows[i])
			}
		}
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(e.stored("p", "results")), &res); err != nil {
		t.Fatal(err)
	}
	if res["ok1"] != true || res["ok2"] != true || res["why2"] != ErrNoSuchAgent.Error() ||
		res["ok3"] != true || res["why3"] != "the text is empty" || res["ok4"] != false || !strings.Contains(res["why4"].(string), "longer than") {
		t.Fatalf("results %v", res)
	}
	// The desk gets the text as written; omo's notification path makes it
	// one line.
	if got := desk.texts("term-dev"); !slices.Equal(got, []string{"halt\nnow"}) {
		t.Fatalf("sent %q", got)
	}
	if desk.total() != 1 {
		t.Fatalf("%d texts sent", desk.total())
	}
}

func TestLuaAgentHelpersWithoutAgents(t *testing.T) {
	for _, c := range []struct {
		name string
		desk AgentDesk
		want string
	}{
		{"no desk", nil, "omo runs no agents"},
		{"failing desk", &fakeDesk{fail: errors.New("database is locked /secret/path")}, "the running agents cannot be read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnvWithDesk(t, c.desk)
			e.plugin("p", manifestOf(luaHook(EventResumed, "h.lua")), map[string]string{"h.lua": `
local list, why = omo.agents()
omo.store_set("r", { none = list == nil, why = why })
`})
			e.load(enabled("p", nil))
			e.m.Dispatch(Event{Name: EventResumed, At: time.Now()})
			eventually(t, "the hook", func() bool { return e.stored("p", "r") != "" })
			var r map[string]any
			_ = json.Unmarshal([]byte(e.stored("p", "r")), &r)
			if r["none"] != true || r["why"] != c.want {
				t.Fatalf("got %v", r)
			}
		})
	}
}
