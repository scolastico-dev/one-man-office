package plugins

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"unicode/utf8"

	lua "github.com/yuin/gopher-lua"

	officedb "github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/luabox"
)

// Bounds of a plugin's stored values.
const (
	// MaxStoreKeys is the number of keys one plugin may store.
	MaxStoreKeys = 1000
	// MaxStoreKeyBytes bounds one key.
	MaxStoreKeyBytes = 200
	// MaxStoreValueBytes bounds one value, as JSON.
	MaxStoreValueBytes = 64 << 10
)

// hookFunction is the global function a Lua hook's source becomes: the
// script's top level is its body, so a "return" of the script is the
// hook's answer. The name is not one a sandbox global can have.
const hookFunction = "omo_plugin_hook__"

// runLua runs a Lua hook in a fresh sandbox: the globals event (the event
// as plugins see it) and config (the plugin's configuration), the helpers of
// the package doc, the hook's time limit. It returns the script's answer,
// the run's log lines (cleaned) and "" or the cleaned error text.
func (m *Manager) runLua(ctx context.Context, p *plugin, h *hook, ev Event) (any, []string, string) {
	box := luabox.New(ctx, luabox.Limits{Timeout: h.timeout, MaxSource: len(h.source) + 64})
	defer box.Close()
	red := box.Redactor()
	for _, s := range p.secrets {
		red.Add(s)
	}
	box.Set("http", (&luabox.HTTP{Client: m.o.HTTP}).Func(box))
	box.Set("exec", m.luaExec(box, p, ev.Name))
	box.Set("sleep", box.Sleep())
	box.Set("store_get", m.luaStoreGet(p.name))
	box.Set("store_set", m.luaStoreSet(p.name))
	box.Set("store_delete", m.luaStoreDelete(p.name))
	box.Set("store_keys", m.luaStoreKeys(p.name))
	box.Set("data_dir", func(L *lua.LState) int {
		dir, err := m.dataDir(p.name)
		if err != nil {
			return fail(L, err.Error())
		}
		L.Push(lua.LString(dir))
		return 1
	})

	run := func() (any, error) {
		if err := box.SetGlobal("event", ev.wire()); err != nil {
			return nil, errors.New("the event cannot be handed to Lua: " + err.Error())
		}
		if err := box.SetGlobal("config", p.config); err != nil {
			return nil, errors.New("the configuration cannot be handed to Lua: " + err.Error())
		}
		// The source is the body of one function; the line numbers stay
		// those of the file.
		if err := box.Load("function "+hookFunction+"(...) "+h.source+"\nend", h.chunk); err != nil {
			return nil, err
		}
		return box.Call(hookFunction)
	}
	result, err := run()
	var lines []string
	for _, l := range box.Log() {
		lines = append(lines, cutRunes(l, maxLogLineRunes))
	}
	if err != nil {
		return nil, lines, luabox.ErrorText(err, red)
	}
	return result, lines, ""
}

// fail returns nil and a fixed phrase to Lua, the way helpers fail.
func fail(L *lua.LState, phrase string) int {
	L.Push(lua.LNil)
	L.Push(lua.LString(phrase))
	return 2
}

// luaExec is omo.exec(program, args…): runs program with the arguments as
// they are (no shell), in the plugin's folder with the plugin environment,
// bounded by the run's time limit. It returns the program's output
// (standard output and error, at most 1 MiB) and "" or a fixed error
// phrase.
func (m *Manager) luaExec(box *luabox.Box, p *plugin, event string) lua.LGFunction {
	return func(L *lua.LState) int {
		top := L.GetTop()
		if top < 1 || top > maxArgs {
			L.RaiseError("omo.exec takes a program and at most %d arguments", maxArgs-1)
		}
		args := make([]string, 0, top)
		for i := 1; i <= top; i++ {
			s := L.CheckString(i)
			if len(s) > maxArgBytes || containsNUL(s) {
				L.RaiseError("omo.exec: argument %d is longer than %d bytes or holds a NUL character", i, maxArgBytes)
			}
			args = append(args, s)
		}
		if args[0] == "" {
			L.RaiseError("omo.exec names no program")
		}
		env, err := m.environment(p, event)
		if err != nil {
			L.Push(lua.LString(""))
			L.Push(lua.LString(err.Error()))
			return 2
		}
		ctx := box.Context()
		out := newHeadBuffer(maxOutputBytes)
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = p.dir
		cmd.Env = env
		cmd.Stdout = out
		cmd.Stderr = out
		text := runProgram(ctx, cmd)
		if text == "" && out.overflow {
			text = fmt.Sprintf("output exceeds %d bytes", maxOutputBytes)
		}
		L.Push(lua.LString(out.Bytes()))
		L.Push(lua.LString(text))
		return 2
	}
}

func containsNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

// storeKey checks a key of omo.store_*.
func storeKey(L *lua.LState) string {
	key := L.CheckString(1)
	if key == "" || len(key) > MaxStoreKeyBytes || !utf8.ValidString(key) {
		L.RaiseError("a storage key is 1 to %d bytes of text", MaxStoreKeyBytes)
	}
	return key
}

// luaStoreGet is omo.store_get(key): the stored value or nil.
func (m *Manager) luaStoreGet(plugin string) lua.LGFunction {
	return func(L *lua.LState) int {
		key := storeKey(L)
		if m.o.DB == nil {
			return fail(L, "storage is not available")
		}
		raw, ok, err := officedb.PluginStoreGet(m.o.DB, plugin, key)
		if err != nil {
			return fail(L, "storage failed")
		}
		if !ok {
			L.Push(lua.LNil)
			return 1
		}
		v, err := luabox.DecodeJSON(raw, nil)
		if err != nil {
			return fail(L, "the stored value cannot be read")
		}
		lv, err := luabox.FromGo(L, v)
		if err != nil {
			return fail(L, "the stored value cannot be read")
		}
		L.Push(lv)
		return 1
	}
}

// luaStoreSet is omo.store_set(key, value): true, or nil and a phrase.
// value is anything omo.json_encode takes, at most 64 KiB as JSON; a plugin
// has at most 1000 keys.
func (m *Manager) luaStoreSet(plugin string) lua.LGFunction {
	return func(L *lua.LState) int {
		key := storeKey(L)
		v, err := luabox.ToGo(L.CheckAny(2))
		if err != nil {
			return fail(L, "the value cannot be stored")
		}
		raw, err := luabox.EncodeJSON(v)
		if err != nil {
			return fail(L, "the value cannot be stored")
		}
		if len(raw) > MaxStoreValueBytes {
			return fail(L, fmt.Sprintf("the value exceeds %d bytes", MaxStoreValueBytes))
		}
		if m.o.DB == nil {
			return fail(L, "storage is not available")
		}
		if err := officedb.PluginStoreSet(m.o.DB, plugin, key, raw, MaxStoreKeys); err != nil {
			if errors.Is(err, officedb.ErrPluginStoreFull) {
				return fail(L, fmt.Sprintf("the plugin stores at most %d keys", MaxStoreKeys))
			}
			return fail(L, "storage failed")
		}
		L.Push(lua.LTrue)
		return 1
	}
}

// luaStoreDelete is omo.store_delete(key): true, or nil and a phrase.
func (m *Manager) luaStoreDelete(plugin string) lua.LGFunction {
	return func(L *lua.LState) int {
		key := storeKey(L)
		if m.o.DB == nil {
			return fail(L, "storage is not available")
		}
		if err := officedb.PluginStoreDelete(m.o.DB, plugin, key); err != nil {
			return fail(L, "storage failed")
		}
		L.Push(lua.LTrue)
		return 1
	}
}

// luaStoreKeys is omo.store_keys(): the plugin's keys in order.
func (m *Manager) luaStoreKeys(plugin string) lua.LGFunction {
	return func(L *lua.LState) int {
		if m.o.DB == nil {
			return fail(L, "storage is not available")
		}
		keys, err := officedb.PluginStoreKeys(m.o.DB, plugin)
		if err != nil {
			return fail(L, "storage failed")
		}
		t := L.CreateTable(len(keys), 0)
		for _, k := range keys {
			t.Append(lua.LString(k))
		}
		L.Push(t)
		return 1
	}
}
