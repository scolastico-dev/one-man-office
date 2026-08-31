package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

func (m *Manager) runLua(ctx context.Context, hook loadedHook, event Event) (Event, error) {
	state := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer state.Close()
	state.SetContext(ctx)
	openSafeLibraries(state)

	state.SetGlobal("event", goToLua(state, map[string]any{
		"event": event.Name, "data": event.Data, "mutable": event.Mutable,
	}))
	api := state.NewTable()
	state.SetFuncs(api, map[string]lua.LGFunction{
		"global_get":    m.luaGet(hook.plugin, "global"),
		"global_set":    m.luaSet(hook.plugin, "global"),
		"global_delete": m.luaDelete(hook.plugin, "global"),
		"local_get":     m.luaGet(hook.plugin, "local"),
		"local_set":     m.luaSet(hook.plugin, "local"),
		"local_delete":  m.luaDelete(hook.plugin, "local"),
		"exec":          m.luaExec(ctx, hook),
	})
	state.SetGlobal("omo", api)
	if err := state.DoFile(filepath.Join(hook.dir, hook.hook.Lua)); err != nil {
		return event, err
	}
	if !event.Mutable {
		return event, nil
	}
	updated, ok := luaToGo(state.GetGlobal("event")).(map[string]any)
	if !ok {
		return event, fmt.Errorf("mutable Lua hook replaced event with a non-table value")
	}
	data, ok := updated["data"].(map[string]any)
	if !ok {
		return event, fmt.Errorf("mutable Lua hook event.data must be a table")
	}
	event.Data = data
	return event, nil
}

func openSafeLibraries(state *lua.LState) {
	for _, lib := range []struct {
		name string
		open lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		state.Push(state.NewFunction(lib.open))
		state.Push(lua.LString(lib.name))
		state.Call(1, 0)
	}
	// File/process access is intentionally exposed only through omo.exec.
	state.SetGlobal("dofile", lua.LNil)
	state.SetGlobal("loadfile", lua.LNil)
}

func (m *Manager) luaGet(plugin, scope string) lua.LGFunction {
	return func(state *lua.LState) int {
		key := state.CheckString(1)
		owner := pluginOwner(plugin, scope)
		var raw string
		err := m.DB.QueryRow(`SELECT value FROM plugin_storage WHERE scope=? AND plugin=? AND key=?`, scope, owner, key).Scan(&raw)
		if err != nil {
			state.Push(lua.LNil)
			return 1
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			state.RaiseError("decode stored value: %v", err)
			return 0
		}
		state.Push(goToLua(state, value))
		return 1
	}
}

func (m *Manager) luaSet(plugin, scope string) lua.LGFunction {
	return func(state *lua.LState) int {
		key := state.CheckString(1)
		if strings.TrimSpace(key) == "" {
			state.RaiseError("storage key must not be empty")
			return 0
		}
		raw, err := json.Marshal(luaToGo(state.CheckAny(2)))
		if err != nil {
			state.RaiseError("encode stored value: %v", err)
			return 0
		}
		owner := pluginOwner(plugin, scope)
		_, err = m.DB.Exec(`INSERT INTO plugin_storage(scope, plugin, key, value, updated_at)
			VALUES(?,?,?,?,datetime('now')) ON CONFLICT(scope,plugin,key)
			DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, scope, owner, key, string(raw))
		if err != nil {
			state.RaiseError("store value: %v", err)
		}
		return 0
	}
}

func (m *Manager) luaDelete(plugin, scope string) lua.LGFunction {
	return func(state *lua.LState) int {
		key := state.CheckString(1)
		_, err := m.DB.Exec(`DELETE FROM plugin_storage WHERE scope=? AND plugin=? AND key=?`, scope, pluginOwner(plugin, scope), key)
		if err != nil {
			state.RaiseError("delete value: %v", err)
		}
		return 0
	}
}

func pluginOwner(plugin, scope string) string {
	if scope == "global" {
		return ""
	}
	return plugin
}

func (m *Manager) luaExec(ctx context.Context, hook loadedHook) lua.LGFunction {
	return func(state *lua.LState) int {
		command := state.CheckString(1)
		args := make([]string, 0, state.GetTop()-1)
		for i := 2; i <= state.GetTop(); i++ {
			args = append(args, state.CheckString(i))
		}
		cmd := exec.CommandContext(ctx, command, args...)
		cmd.Dir = hook.dir
		cmd.Env = append(os.Environ(), "OMO_PLUGIN_NAME="+hook.plugin, "OMO_PLUGIN_EVENT="+hook.hook.Event)
		output, err := cmd.CombinedOutput()
		state.Push(lua.LString(string(output)))
		if err != nil {
			state.Push(lua.LString(err.Error()))
		} else {
			state.Push(lua.LString(""))
		}
		return 2
	}
}

func goToLua(state *lua.LState, value any) lua.LValue {
	switch value := value.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(value)
	case string:
		return lua.LString(value)
	case float64:
		return lua.LNumber(value)
	case float32:
		return lua.LNumber(value)
	case int:
		return lua.LNumber(value)
	case int64:
		return lua.LNumber(value)
	case []any:
		table := state.NewTable()
		for _, item := range value {
			table.Append(goToLua(state, item))
		}
		return table
	case map[string]any:
		table := state.NewTable()
		for key, item := range value {
			table.RawSetString(key, goToLua(state, item))
		}
		return table
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return lua.LNil
		}
		var normalized any
		if json.Unmarshal(raw, &normalized) != nil {
			return lua.LNil
		}
		return goToLua(state, normalized)
	}
}

func luaToGo(value lua.LValue) any {
	switch value := value.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(value)
	case lua.LString:
		return string(value)
	case lua.LNumber:
		return float64(value)
	case *lua.LTable:
		array := make([]any, 0, value.Len())
		isArray := true
		count := 0
		value.ForEach(func(key, item lua.LValue) {
			count++
			if number, ok := key.(lua.LNumber); !ok || int(number) != count {
				isArray = false
			}
			array = append(array, luaToGo(item))
		})
		if isArray && count == value.Len() {
			return array
		}
		object := map[string]any{}
		value.ForEach(func(key, item lua.LValue) { object[key.String()] = luaToGo(item) })
		return object
	default:
		return value.String()
	}
}
