package plugins

import (
	"context"
	"errors"
	"strings"

	lua "github.com/yuin/gopher-lua"

	"github.com/scolastico-dev/one-man-office/internal/luabox"
)

// MaxAgentTextBytes bounds the text omo.send_to_agent takes. omo types at
// most agents.MaxNotifyLength characters of it.
const MaxAgentTextBytes = 4 << 10

// ErrNoSuchAgent is the error of a message for a terminal no running agent
// has.
var ErrNoSuchAgent = errors.New("no running agent has this terminal")

// RunningAgent is one agent session omo runs right now, as omo.agents
// shows it. Terminal is the random id of its web terminal (the one the
// user's sidebar opens), never its session id.
type RunningAgent struct {
	Terminal string
	Name     string
	Role     string
	Model    string
	Purpose  string
	Project  string
	// Ticket is the ticket's number; 0 for the CEO.
	Ticket int
}

// AgentDesk is what omo.agents and omo.send_to_agent reach: the agents the
// engine runs. Its methods must honour ctx.
type AgentDesk interface {
	// RunningAgents lists the agent sessions that run with a terminal.
	RunningAgents(ctx context.Context) ([]RunningAgent, error)
	// SendToAgent hands text to the agent with terminal the way omo's own
	// notifications reach agents: one line, "omo: " in front, typed into
	// its terminal and submitted (or the answer of a parked omo ai wait).
	// ErrNoSuchAgent when no running agent has the terminal.
	SendToAgent(ctx context.Context, terminal, text string) error
}

// luaAgents is omo.agents(): a list of the running agents, each a table
// {terminal, name, role, model, purpose, project, ticket} (ticket nil for
// the CEO), or nil and a phrase.
func (m *Manager) luaAgents(box *luabox.Box) lua.LGFunction {
	return func(L *lua.LState) int {
		if m.o.Agents == nil {
			return fail(L, "omo runs no agents")
		}
		list, err := m.o.Agents.RunningAgents(box.Context())
		if err != nil {
			return fail(L, "the running agents cannot be read")
		}
		t := L.CreateTable(len(list), 0)
		for _, a := range list {
			row := L.CreateTable(0, 7)
			row.RawSetString("terminal", lua.LString(a.Terminal))
			row.RawSetString("name", lua.LString(cutText(a.Name, maxNameBytes)))
			row.RawSetString("role", lua.LString(a.Role))
			row.RawSetString("model", lua.LString(cutText(a.Model, maxNameBytes)))
			row.RawSetString("purpose", lua.LString(a.Purpose))
			row.RawSetString("project", lua.LString(cutText(a.Project, maxNameBytes)))
			if a.Ticket > 0 {
				row.RawSetString("ticket", lua.LNumber(a.Ticket))
			}
			t.Append(row)
		}
		L.Push(t)
		return 1
	}
}

// luaSendToAgent is omo.send_to_agent(terminal, text): true, or nil and a
// phrase. text is one line of at most MaxAgentTextBytes; line breaks become
// spaces.
func (m *Manager) luaSendToAgent(box *luabox.Box) lua.LGFunction {
	return func(L *lua.LState) int {
		terminal := L.CheckString(1)
		text := L.CheckString(2)
		if len(text) > MaxAgentTextBytes {
			L.RaiseError("omo.send_to_agent: the text is longer than %d bytes", MaxAgentTextBytes)
		}
		if strings.TrimSpace(text) == "" {
			return fail(L, "the text is empty")
		}
		if m.o.Agents == nil {
			return fail(L, "omo runs no agents")
		}
		err := m.o.Agents.SendToAgent(box.Context(), terminal, text)
		switch {
		case errors.Is(err, ErrNoSuchAgent):
			return fail(L, ErrNoSuchAgent.Error())
		case err != nil:
			return fail(L, "the text could not be sent")
		}
		L.Push(lua.LTrue)
		return 1
	}
}
