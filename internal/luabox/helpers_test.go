package luabox

import lua "github.com/yuin/gopher-lua"

// Short names for tests.
type luaState = lua.LState

func luaString(s string) lua.LValue { return lua.LString(s) }
