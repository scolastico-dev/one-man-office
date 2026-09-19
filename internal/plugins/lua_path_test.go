package plugins

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestLuaPathIsAbsoluteWindowsSemantics(t *testing.T) {
	for _, test := range []struct {
		name, path string
		want       bool
	}{
		{name: "drive rooted forward slash", path: "C:/omo", want: true},
		{name: "drive rooted backslash", path: `C:\omo`, want: true},
		{name: "UNC backslash", path: `\\server\share`, want: true},
		{name: "UNC forward slash", path: "//server/share", want: true},
		{name: "drive relative", path: "C:omo", want: false},
		{name: "root relative backslash", path: `\omo`, want: false},
		{name: "root relative forward slash", path: "/omo", want: false},
		{name: "relative", path: "omo", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := lua.NewState()
			defer state.Close()
			state.Push(lua.LString(test.path))
			state.Push(lua.LString("windows"))
			if got := luaPathIsAbsolute(state); got != 1 {
				t.Fatalf("return count = %d, want 1", got)
			}
			if got := state.Get(-1) == lua.LTrue; got != test.want {
				t.Fatalf("path_is_absolute(%q, windows) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}
