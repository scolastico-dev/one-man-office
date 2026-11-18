package luabox

import (
	"context"
	"errors"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// completeWithin bounds a stalled operation; it is a watchdog, not a speed
// assertion. Behavioral deadlines remain configured on the operation itself.
func completeWithin(t *testing.T, guard time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	<-done
}

// run loads src into a fresh box with lim and returns the error text of
// the load ("" when it succeeded) and the box (closed by the test's
// cleanup).
func run(t *testing.T, lim Limits, src string) (string, *Box) {
	t.Helper()
	b := New(context.Background(), lim)
	t.Cleanup(b.Close)
	if err := b.Load(src, "test"); err != nil {
		return err.Error(), b
	}
	return "", b
}

// eval returns the value of the Lua expression expr.
func eval(t *testing.T, expr string) any {
	t.Helper()
	b := New(context.Background(), Limits{})
	defer b.Close()
	if err := b.Load("function f() return "+expr+" end", "test"); err != nil {
		t.Fatalf("load %s: %v", expr, err)
	}
	v, err := b.Call("f")
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return v
}

// mustFail asserts that src fails and that the error text holds want.
func mustFail(t *testing.T, src, want string) string {
	t.Helper()
	msg, _ := run(t, Limits{Timeout: 5 * time.Second}, src)
	if msg == "" {
		t.Fatalf("%q ran without error", src)
	}
	if !strings.Contains(msg, want) {
		t.Fatalf("%q: error %q, want it to hold %q", src, msg, want)
	}
	return msg
}

func TestSandboxHasNoShellFileOrLoader(t *testing.T) {
	for _, name := range []string{
		"io", "os.execute", "os.getenv", "os.remove", "os.rename", "os.exit", "os.tmpname", "os.setlocale", "os.difftime",
		"load", "loadstring", "dofile", "loadfile", "require", "module", "package", "debug", "channel",
		"string.dump", "collectgarbage", "getfenv", "setfenv", "newproxy", "_printregs", "_GOPHER_LUA_VERSION",
	} {
		if got := eval(t, name+" == nil"); got != true {
			t.Errorf("%s is reachable", name)
		}
	}
	// Through the string metatable too.
	if got := eval(t, `getmetatable("").__index.dump == nil and getmetatable("").__index == string`); got != true {
		t.Error("string.dump is reachable through the string metatable")
	}
	if got := eval(t, "type(os.time()) == 'number' and type(os.date('%Y')) == 'string' and type(os.clock()) == 'number'"); got != true {
		t.Error("the os clock functions do not work")
	}
}

// TestGlobalsAllowList is the guard: every global and every library entry
// of a fresh box is listed here. A new global or library function fails
// the test until it is added on purpose.
func TestGlobalsAllowList(t *testing.T) {
	want := map[string][]string{
		"_G":     keys(allowedGlobals),
		"string": {"byte", "char", "find", "format", "gfind", "gmatch", "gsub", "len", "lower", "match", "rep", "reverse", "sub", "upper", "__index"},
		"table":  {"concat", "getn", "insert", "maxn", "remove", "sort"},
		"math": {"abs", "acos", "asin", "atan", "atan2", "ceil", "cos", "cosh", "deg", "exp", "floor", "fmod", "frexp", "huge", "ldexp",
			"log", "log10", "max", "min", "mod", "modf", "pi", "pow", "rad", "random", "randomseed", "sin", "sinh", "sqrt", "tan", "tanh"},
		"os":        {"clock", "date", "time"},
		"coroutine": {"create", "resume", "running", "status", "wrap", "yield"},
		"omo":       {"json_decode", "json_encode", "log", "nfc", "now", "sha256_hex", "time_parse"},
	}
	b := New(context.Background(), Limits{})
	defer b.Close()
	g := b.L.Get(lua.GlobalsIndex).(*lua.LTable)
	for lib, names := range want {
		tbl := g
		if lib != "_G" {
			tbl = g.RawGetString(lib).(*lua.LTable)
		}
		var got []string
		tbl.ForEach(func(k, _ lua.LValue) { got = append(got, k.String()) })
		sort.Strings(got)
		sort.Strings(names)
		if strings.Join(got, ",") != strings.Join(names, ",") {
			t.Errorf("%s holds\n  %v\nwant\n  %v", lib, got, names)
		}
	}
	// The string metatable is the string table itself and nothing else.
	mt := b.L.GetMetatable(lua.LString(""))
	if mt != g.RawGetString("string") {
		t.Error("the string metatable is not the sandbox's string table")
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestKnownEscapesFail(t *testing.T) {
	for _, src := range []string{
		`load("return 1")`,
		`loadstring("return 1")()`,
		`dofile("/etc/passwd")`,
		`require("io")`,
		`io.open("/etc/passwd")`,
		`os.execute("true")`,
		`os.exit(1)`,
		`os.getenv("HOME")`,
		`os.remove("/tmp/x")`,
		`debug.getregistry()`,
		`package.loadlib("x", "y")`,
		`setfenv(1, {})`,
		`getfenv(0)`,
		`string.dump(function() end)`,
		`("x"):dump()`,
		`collectgarbage("count")`,
		`newproxy(true)`,
		`_G.io.write("x")`,
		`rawget(_G, "loadstring")("x")`,
	} {
		if msg, _ := run(t, Limits{Timeout: 2 * time.Second}, src); msg == "" {
			t.Errorf("%s ran", src)
		}
	}
}

func TestBytecodeRefused(t *testing.T) {
	msg, _ := run(t, Limits{}, "\x1bLua\x51\x00\x01\x04\x08\x04\x08\x00")
	if !strings.Contains(msg, "precompiled") {
		t.Fatalf("bytecode: %q", msg)
	}
}

func TestPrintGoesToLog(t *testing.T) {
	_, b := run(t, Limits{}, `print("a", 1, true) omo.log("b")`)
	if got := strings.Join(b.Log(), "|"); got != "a 1 true|b" {
		t.Fatalf("log %q", got)
	}
}

func TestRunTimeLimit(t *testing.T) {
	for name, src := range map[string]string{
		"loop":          `while true do end`,
		"pcall loop":    `while true do pcall(function() while true do end end) end`,
		"nested pcall":  `pcall(function() while true do pcall(function() while true do end end) end end) while true do end`,
		"tail pcall":    `local function f() return pcall(function() while true do end end) end while true do f() end`,
		"xpcall":        `while true do xpcall(function() while true do end end, function() while true do end end) end`,
		"error handler": `while true do xpcall(function() error("x") end, function() while true do end end) end`,
		"sort":          `local t = {} for i = 1, 200000 do t[i] = i end while true do table.sort(t, function(a, b) return pcall(function() while true do end end) end) end`,
		"gsub callback": `local s = ("a"):rep(1000000) while true do s:gsub(".", function() return pcall(function() while true do end end) end) end`,
		"coroutine":     `local co = coroutine.wrap(function() while true do pcall(function() while true do end end) end end) co()`,
		"tostring":      `local t = setmetatable({}, {__tostring = function() while true do end end}) while true do pcall(tostring, t) end`,
		"tail call":     `local function f() return f() end f()`,
		"pattern":       `while true do pcall(string.find, ("a"):rep(5000), ".-.-.-.-.-b") end`,
	} {
		t.Run(name, func(t *testing.T) {
			var msg string
			completeWithin(t, 2*time.Second, func() { msg, _ = run(t, Limits{Timeout: 200 * time.Millisecond}, src) })
			if msg != msgTimeLimit {
				t.Fatalf("error %q, want %q", msg, msgTimeLimit)
			}
		})
	}
}

func TestBlockedHelperEndsAtDeadline(t *testing.T) {
	b := New(context.Background(), Limits{Timeout: 200 * time.Millisecond})
	defer b.Close()
	b.Set("block", func(L *lua.LState) int {
		<-b.Context().Done()
		return 0
	})
	var err error
	completeWithin(t, 2*time.Second, func() { err = b.Load(`while true do pcall(omo.block) end`, "test") })
	if err == nil || err.Error() != msgTimeLimit {
		t.Fatalf("err %v", err)
	}
}

func TestCancelEndsRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(ctx, Limits{})
	defer b.Close()
	entered := make(chan struct{})
	b.Set("entered", func(L *lua.LState) int { close(entered); return 0 })
	result := make(chan error, 1)
	go func() { result <- b.Load(`omo.entered(); while true do end`, "test") }()
	<-entered
	cancel()
	var err error
	err = <-result
	if err == nil || err.Error() != msgCanceled {
		t.Fatalf("err %v", err)
	}
	// A call after the end fails at once.
	if _, err := b.Call("f"); err == nil || err.Error() != msgCanceled {
		t.Fatalf("call after cancel: %v", err)
	}
}

func TestDeepRecursionRefused(t *testing.T) {
	for _, src := range []string{
		`local function f() return 1 + f() end f()`,
		`local function f() pcall(f) error("deep") end f()`,
		`local t = setmetatable({}, {}) getmetatable(t).__index = function(t, k) return t[k] end local x = t.a`,
		`local function f() return tostring(setmetatable({}, {__tostring = f})) end f()`,
		`local function f() local t = {3, 2, 1} table.sort(t, function(a, b) f() return a < b end) end f()`,
		`local function f() return ("x"):gsub("x", f) end f()`,
	} {
		msg, _ := run(t, Limits{Timeout: 5 * time.Second}, src)
		if msg == "" {
			t.Errorf("%s ran", src)
		}
	}
	mustFail(t, `local function f() return 1 + f() end f()`, "stack overflow")
}

func TestCoroutinesBounded(t *testing.T) {
	mustFail(t, `for i = 1, 1000 do coroutine.create(function() end) end`, "too many coroutines")
	mustFail(t, `for i = 1, 1000 do coroutine.wrap(function() end) end`, "too many coroutines")
	// Nested resumes end at the coroutine count, not in a Go stack overflow.
	mustFail(t, `local function nest() coroutine.wrap(nest)() end nest()`, "too many coroutines")
	// Ordinary use works.
	if got := eval(t, `(function() local co = coroutine.wrap(function(a) local b = coroutine.yield(a + 1) return b * 2 end) return co(1) + co(5) end)()`); got != 12.0 {
		t.Fatalf("coroutine: %v", got)
	}
}

func TestRegistryBounded(t *testing.T) {
	mustFail(t, `local t = {} unpack(t, 1, 10000000)`, "")
	mustFail(t, `local s = ("x"):rep(1000000) string.byte(s, 1, -1)`, "")
	mustFail(t, `local t = {} for i = 1, 300000 do table.insert(t, i) end print(unpack(t))`, "")
}

func TestStringRepCapped(t *testing.T) {
	mustFail(t, `string.rep("x", 1048577)`, "exceeds")
	mustFail(t, `("xx"):rep(2^30)`, "exceeds")
	mustFail(t, `getmetatable("").__index.rep("x", 2^40)`, "exceeds")
	if got := eval(t, `#string.rep("ab", 3)`); got != 6.0 {
		t.Fatalf("rep: %v", got)
	}
	if got := eval(t, `string.rep("ab", -1)`); got != "" {
		t.Fatalf("rep -1: %v", got)
	}
}

func TestAmplifyingFunctionsCapped(t *testing.T) {
	mustFail(t, `local s = ("x"):rep(1000000) s:gsub(".", "%0%0")`, "exceeds")
	mustFail(t, `local s = ("x"):rep(1000) local t = {} for i = 1, 2000 do t[i] = s end table.concat(t)`, "exceeds")
	mustFail(t, `string.format("%99999d", 1)`, "too long")
	mustFail(t, `string.format("%[1]*d", 1000000, 1)`, "invalid option")
	mustFail(t, `string.format("%v", 1)`, "invalid option")
	mustFail(t, `local s = ("x"):rep(600000) string.format("%s", s)`, "exceeds")
	if got := eval(t, `string.format("%5.2f|%s|%d|%x", 3.14159, "a", 42, 255)`); got != " 3.14|a|42|ff" {
		t.Fatalf("format: %q", got)
	}
	if got := eval(t, `table.concat({1, "b", 3}, ",")`); got != "1,b,3" {
		t.Fatalf("concat: %q", got)
	}
	if got := eval(t, `table.concat({1, 2, 3}, "-", 2, 3)`); got != "2-3" {
		t.Fatalf("concat range: %q", got)
	}
	if got := eval(t, `table.concat({})`); got != "" {
		t.Fatalf("concat empty: %q", got)
	}
	mustFail(t, `table.concat({1, {}, 3})`, "invalid value")
}

func TestPatternBlowUpBounded(t *testing.T) {
	// The step bound, not the 30 s timeout, ends the search, well before it
	// (four times later under the race detector, still below the timeout).
	var msg string
	completeWithin(t, raceSlowdown*5*time.Second, func() {
		msg, _ = run(t, Limits{Timeout: 30 * time.Second}, `string.find(("a"):rep(5000), ".-.-.-.-.-b")`)
	})
	if msg == "" {
		t.Fatal("pattern completed without its complexity error")
	}
	if !strings.Contains(msg, "pattern too complex") {
		t.Fatalf("error %q", msg)
	}
	// Patterns nested deeper than the recursion bound are refused.
	mustFail(t, `string.match(("a"):rep(300), ("a?"):rep(300))`, "pattern too complex")
}

func TestPatternsWork(t *testing.T) {
	for expr, want := range map[string]any{
		`string.find("hello world", "o w")`:                            5.0,
		`string.find("a.b", ".", 1, true)`:                             2.0,
		`select(2, string.find("hello", "l+"))`:                        4.0,
		`string.match("key=value", "(%w+)=(%w+)")`:                     "key",
		`select(2, string.match("key=value", "(%w+)=(%w+)"))`:          "value",
		`string.match("  x  ", "^%s*(.-)%s*$")`:                        "x",
		`string.match("2026-11-15", "(%d+)-(%d+)")`:                    "2026",
		`string.match("f(a(b)c)", "%b()")`:                             "(a(b)c)",
		`string.match("THE (quick) fox", "%f[%a]%a+")`:                 "THE",
		`string.match("hello", "()ll()")`:                              3.0,
		`string.match("abcabc", "(abc)%1")`:                            "abc",
		`string.match("x", "[%]]")`:                                    nil,
		`string.match("]", "[]]")`:                                     "]",
		`string.match("a-b", "[a-]+")`:                                 "a-",
		`(string.gsub("hello world", "o", "0"))`:                       "hell0 w0rld",
		`select(2, string.gsub("hello world", "o", "0"))`:              2.0,
		`(string.gsub("abc", "%w", "%0%0"))`:                           "aabbcc",
		`(string.gsub("hello world", "(%w+)", "<%1>"))`:                "<hello> <world>",
		`(string.gsub("abc", "", "-"))`:                                "-a-b-c-",
		`(string.gsub("$name is $age", "%$(%w+)", {name = "x"}))`:      "x is $age",
		`(string.gsub("a b", "%w", function(c) return c:upper() end))`: "A B",
		`(string.gsub("aaa", "a", "b", 2))`:                            "bba",
		`(string.gsub("hello", "^h", "H"))`:                            "Hello",
		`(function() local t = {} for k, v in string.gmatch("a=1, b=2", "(%w+)=(%w+)") do t[#t+1] = k .. v end return table.concat(t, ";") end)()`: "a1;b2",
		`(function() local n = 0 for w in ("one two three"):gmatch("%a+") do n = n + 1 end return n end)()`:                                        3.0,
	} {
		if got := eval(t, expr); got != want {
			t.Errorf("%s = %#v, want %#v", expr, got, want)
		}
	}
	mustFail(t, `string.find("a", "[a")`, "malformed pattern")
	mustFail(t, `string.find("a", "%")`, "malformed pattern")
	mustFail(t, `string.match("a", "(a")`, "unfinished capture")
	mustFail(t, `string.gsub("a", "a", "%2")`, "invalid capture index")
}

func TestSourceSizeCapped(t *testing.T) {
	msg, _ := run(t, Limits{MaxSource: 100}, "-- "+strings.Repeat("x", 200))
	if !strings.Contains(msg, "exceeds 100 bytes") {
		t.Fatalf("error %q", msg)
	}
	// The default is 64 KiB.
	msg, _ = run(t, Limits{}, "-- "+strings.Repeat("x", 64<<10))
	if !strings.Contains(msg, "exceeds 65536 bytes") {
		t.Fatalf("error %q", msg)
	}
}

func TestSyntaxErrorDoesNotEchoSource(t *testing.T) {
	msg, _ := run(t, Limits{}, "local x = 1\nlocal y = secretvalue123 secretvalue456 +")
	if strings.Contains(msg, "secretvalue") {
		t.Fatalf("error echoes the source: %q", msg)
	}
	if !strings.HasPrefix(msg, "line 2: ") {
		t.Fatalf("error %q", msg)
	}
}

func TestErrorText(t *testing.T) {
	msg := mustFail(t, "\n\nerror('broken')", "broken")
	if msg != "line 3: broken" {
		t.Fatalf("error %q", msg)
	}
	msg = mustFail(t, "error('a\\nb\\27[31m' .. ('x'):rep(1000))", "")
	if strings.ContainsAny(msg, "\n\x1b") || len([]rune(msg)) > 300 {
		t.Fatalf("error not one short line: %q", msg)
	}
	if !strings.Contains(msg, `a\u{A}b\u{1B}[31m`) {
		t.Fatalf("error %q", msg)
	}
	msg = mustFail(t, "error({})", "")
	if msg != "script raised a table as its error" {
		t.Fatalf("error %q", msg)
	}
	msg = mustFail(t, "local x = nil; x.y = 1", "")
	if !strings.HasPrefix(msg, "line 1: ") {
		t.Fatalf("error %q", msg)
	}
}

func TestHelperPanicIsAnError(t *testing.T) {
	b := New(context.Background(), Limits{})
	defer b.Close()
	b.Set("boom", func(L *lua.LState) int {
		var m map[string]int
		m["secret-data-value"] = 1 // panics
		return 0
	})
	b.Set("raise", func(L *lua.LState) int {
		L.RaiseError("helper refused")
		return 0
	})
	err := b.Load(`omo.boom()`, "test")
	if err == nil || err.Error() != "line 1: omo.boom failed" {
		t.Fatalf("panic: %v", err)
	}
	err = b.Load(`local ok, e = pcall(omo.boom) assert(not ok) omo.raise()`, "test2")
	if err == nil || err.Error() != "line 1: helper refused" {
		t.Fatalf("raise: %v", err)
	}
}

func TestCallAndGlobals(t *testing.T) {
	b := New(context.Background(), Limits{})
	defer b.Close()
	if err := b.SetGlobal("event", map[string]any{"name": "x", "n": 2, "list": []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetGlobal("string", 1); err == nil {
		t.Fatal("SetGlobal replaced a library")
	}
	if err := b.Load(`function f(a, b) return {name = event.name, sum = a + b + event.n, first = event.list[1]} end`, "test"); err != nil {
		t.Fatal(err)
	}
	v, err := b.Call("f", 1, 2.5)
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["name"] != "x" || m["sum"] != 5.5 || m["first"] != "a" {
		t.Fatalf("result %#v", m)
	}
	if _, err := b.Call("missing"); err == nil || err.Error() != "no function missing" {
		t.Fatalf("missing: %v", err)
	}
	if err := b.Load(`function g() return function() end end`, "t2"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Call("g"); err == nil || !strings.Contains(err.Error(), "cannot be converted") {
		t.Fatalf("function result: %v", err)
	}
}

func TestLogBounded(t *testing.T) {
	_, b := run(t, Limits{MaxLogLines: 3}, `for i = 1, 10 do omo.log(("x"):rep(1000)) end`)
	lines := b.Log()
	if len(lines) != 3 {
		t.Fatalf("%d lines", len(lines))
	}
	for _, l := range lines {
		if n := len([]rune(l)); n > MaxLogRunes {
			t.Fatalf("line of %d runes", n)
		}
	}
	_, b = run(t, Limits{}, `for i = 1, 100 do print(i) end`)
	if len(b.Log()) != DefaultMaxLogLines {
		t.Fatalf("%d lines", len(b.Log()))
	}
}

func TestFreshStatePerBox(t *testing.T) {
	_, b1 := run(t, Limits{}, `shared = 1 string.upper = nil getmetatable("").__index.lower = nil`)
	_ = b1
	if got := eval(t, `shared == nil and string.upper ~= nil and ("A"):lower() == "a"`); got != true {
		t.Fatal("state leaked between boxes")
	}
}

func TestConcurrentBoxes(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b := New(context.Background(), Limits{Timeout: 5 * time.Second})
			defer b.Close()
			if err := b.Load(`function f(n) local s = 0 for i = 1, n do s = s + i end return omo.json_encode({s = s}) end`, "c"); err != nil {
				errs <- err
				return
			}
			v, err := b.Call("f", 1000)
			if err != nil {
				errs <- err
				return
			}
			if v != `{"s":500500}` {
				errs <- errors.New("wrong result " + v.(string))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestMemoryWatchdog(t *testing.T) {
	start := time.Now()
	msg, _ := run(t, Limits{Timeout: 30 * time.Second, MaxMemory: 64 << 20},
		`local t = {} local i = 0 while true do i = i + 1 t[i] = {i, i, i} end`)
	if msg != msgMemoryLimit {
		t.Fatalf("error %q after %v", msg, time.Since(start))
	}
	runtime.GC()
}

// boxGoroutines counts the goroutines that run code of this package or hold
// an HTTP client connection, by their stacks.
func boxGoroutines() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		ours := strings.Contains(g, "internal/luabox.") && !strings.Contains(g, "luabox.boxGoroutines") && !strings.Contains(g, "testing.tRunner")
		// Client connections of omo.http (the test servers' own goroutines
		// are (*conn).serve and do not count).
		if ours || strings.Contains(g, "net/http.(*persistConn)") {
			count++
		}
	}
	return count
}

func TestNoGoroutineLeftAfterTimeout(t *testing.T) {
	for i := 0; i < 5; i++ {
		b := New(context.Background(), Limits{Timeout: 50 * time.Millisecond})
		_ = b.Load(`local co = coroutine.wrap(function() while true do end end) co()`, "leak")
		b.Close()
	}
	// A box that is never closed stops its own goroutine at the deadline.
	b := New(context.Background(), Limits{Timeout: 50 * time.Millisecond})
	_ = b.Load(`while true do end`, "leak")
	deadline := time.Now().Add(2 * time.Second)
	for boxGoroutines() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := boxGoroutines(); n != 0 {
		t.Fatalf("%d goroutines left", n)
	}
	b.Close()
}

// allocated runs src in a fresh box and returns the run's error text and
// the bytes the process allocated meanwhile.
func allocated(t *testing.T, src string) (string, uint64) {
	t.Helper()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	msg, _ := run(t, Limits{Timeout: 10 * time.Second}, src)
	runtime.ReadMemStats(&after)
	return msg, after.TotalAlloc - before.TotalAlloc
}

func TestSparseIntegerKeysStaySmall(t *testing.T) {
	for _, src := range []string{
		`local t = {} t[67108863] = true assert(t[67108863] == true)`,
		`local t = {} table.insert(t, 67108863, "x") assert(t[67108863] == "x")`,
		`local t = {} rawset(t, 67108000, 1) assert(rawget(t, 67108000) == 1)`,
		`local t = {} for i = 1, 100 do t[67108863 - i * 1000] = i end`,
	} {
		var msg string
		var alloc uint64
		completeWithin(t, 2*time.Second, func() { msg, alloc = allocated(t, src) })
		if msg != "" {
			t.Errorf("%s: %s", src, msg)
		}
		if alloc > 64<<20 {
			t.Errorf("%s: allocated %d MiB", src, alloc>>20)
		}
	}
}

func TestArraysUpToTheIndexLimitWork(t *testing.T) {
	n := lua.MaxArrayIndex - 1
	if n != 1<<16-1 {
		t.Fatalf("MaxArrayIndex is %d", lua.MaxArrayIndex)
	}
	src := `
local n = ` + strconv.Itoa(n) + `
local a, b = {}, {}
for i = 1, n do a[i] = i end
for i = 1, n do table.insert(b, i) end
assert(#a == n and #b == n, "length")
local sum = 0
for _, v in ipairs(a) do sum = sum + v end
assert(sum == n * (n + 1) / 2, "ipairs")
assert(a[n] == n and b[n] == n and a[n + 1] == nil, "index")
-- Keys past the limit still work, through the hash part.
a[n + 1], a[n + 10] = "x", "y"
assert(a[n + 1] == "x" and a[n + 10] == "y", "hash part")`
	if msg, _ := run(t, Limits{Timeout: 20 * time.Second}, src); msg != "" {
		t.Fatal(msg)
	}
}

func TestOsDateCapped(t *testing.T) {
	mustFail(t, `local f = ("%c"):rep(50000) os.date(f, 0)`, "exceeds")
	if got := eval(t, `os.date("!%Y-%m-%d", 86400)`); got != "1970-01-02" {
		t.Fatalf("os.date: %v", got)
	}
	if got := eval(t, `os.date("!*t", 0).year`); got != 1970.0 {
		t.Fatalf("os.date *t: %v", got)
	}
	if got := eval(t, `type(os.date())`); got != "string" {
		t.Fatalf("os.date(): %v", got)
	}
}
