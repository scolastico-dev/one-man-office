package plugins

import (
	"runtime"
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
			if got := isAbsolutePath("windows", test.path); got != test.want {
				t.Fatalf("path_is_absolute(%q, windows) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}

func TestLuaPathIsAbsoluteUsesHostPlatform(t *testing.T) {
	path, override := "C:/omo", "windows"
	if runtime.GOOS == "windows" {
		path, override = "/omo", "unix"
	}
	want := isAbsolutePath(runtime.GOOS, path)
	state := lua.NewState()
	defer state.Close()
	state.Push(lua.LString(path))
	state.Push(lua.LString(override))
	if got := luaPathIsAbsolute(state); got != 1 {
		t.Fatalf("return count = %d, want 1", got)
	}
	if got := state.Get(-1) == lua.LTrue; got != want {
		t.Fatalf("path_is_absolute(%q) = %v, want host result %v", path, got, want)
	}
}

func TestIsAbsolutePathUnixSemantics(t *testing.T) {
	for _, test := range []struct {
		name, path string
		want       bool
	}{
		{name: "absolute", path: "/omo", want: true},
		{name: "relative", path: "omo", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isAbsolutePath("unix", test.path); got != test.want {
				t.Fatalf("isAbsolutePath(unix, %q) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}
