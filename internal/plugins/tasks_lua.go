package plugins

import (
	"bytes"
	"encoding/json"

	"github.com/scolastico-dev/one-man-office/internal/luabox"
	lua "github.com/yuin/gopher-lua"
)

func pushTaskValue(L *lua.LState, v any) int {
	value, err := luabox.FromGo(L, v)
	if err != nil {
		return fail(L, "the task result exceeds Lua limits")
	}
	L.Push(value)
	return 1
}

func (m *Manager) luaTaskStart(box *luabox.Box, p *plugin) lua.LGFunction {
	return func(L *lua.LState) int {
		value, err := luabox.ToGo(L.CheckTable(1))
		if err != nil {
			return fail(L, "task_start requires a task options table")
		}
		// Empty Lua tables are arrays by convention; env is an object.
		if obj, ok := value.(map[string]any); ok {
			if a, ok := obj["env"].([]any); ok && len(a) == 0 {
				obj["env"] = map[string]any{}
			}
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return fail(L, "invalid task options")
		}
		var req TaskStart
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			return fail(L, "task_start has an unknown field or a value of the wrong type")
		}
		task, err := m.startTask(box.Context(), p.name, p, req, box.Redactor())
		if err != nil {
			return fail(L, err.Error())
		}
		return pushTaskValue(L, task)
	}
}
func (m *Manager) luaTaskList(p *plugin) lua.LGFunction {
	return func(L *lua.LState) int {
		tasks, err := m.Tasks(p.name)
		if err != nil {
			return fail(L, err.Error())
		}
		return pushTaskValue(L, tasks)
	}
}
func (m *Manager) luaTaskStop(p *plugin) lua.LGFunction {
	return func(L *lua.LState) int {
		if err := m.StopTask(p.name, L.CheckString(1)); err != nil {
			return fail(L, err.Error())
		}
		L.Push(lua.LTrue)
		return 1
	}
}
