// Package luabox is the only place omo runs Lua: usage scripts and plugin
// hooks run in a Box. A Box is one fresh gopher-lua state with a fixed set
// of libraries, the helpers its caller registers and limits on time, size
// and depth. Nothing is shared between boxes, so the package is safe for
// concurrent use; one Box is used by one goroutine at a time.
//
// What a script can reach: the base functions without loaders, environment
// functions or garbage collector control (assert, error, getmetatable,
// ipairs, next, pairs, pcall, print, rawequal, rawget, rawset, select,
// setmetatable, tonumber, tostring, type, unpack, xpcall, _G, _VERSION);
// string without dump; table; math; os.time, os.date and os.clock;
// coroutine; and the omo table. print writes to the run's log, not to omo's
// output. There is no io, no os.execute, os.getenv, os.exit or other os
// function, no debug, no package, no require, no load, loadstring, dofile
// or loadfile, no getfenv or setfenv, and no way to load bytecode. Helpers
// that reach anything outside (network, files, programs) exist only where
// the caller registers them with Set.
//
// Bounds held hard:
//
//   - wall time: the whole run (Load and every Call) ends at Limits.Timeout.
//     The interpreter checks the deadline before every instruction, and the
//     string functions that loop in Go (find, match, gmatch, gsub) and the
//     comparator of table.sort check it too, so a tight loop, a pcall that
//     swallows the error and a pattern that backtracks all end. A helper
//     registered with Set must honour Context(); one that blocks without
//     looking at it holds the run (the HTTP helper honours it).
//
//   - call depth: 200 Lua and Go frames per coroutine, at most 100
//     coroutines per run, pattern recursion 200, conversion depth 64, JSON
//     depth 64.
//
//   - stack values: the registry of each coroutine holds at most 256 Ki
//     values (unpack and string.byte of huge ranges fail).
//
//   - sizes: source 64 KiB; strings made by string.rep, string.format,
//     string.gsub, table.concat, os.date and omo.json_encode at most 1 MiB; JSON text
//     decoded at most 1 MiB; values converted between Go and Lua at most
//     1 Mi entries; log at most Limits.MaxLogLines lines of 500 runes;
//     error texts one line of at most 300 runes.
//
//   - integer keys: only keys below arrayIndexLimit go to a table's array
//     part (gopher-lua fills the array up to the key it sets, so one
//     assignment like t[67108863] = true would allocate about 1 GiB inside a
//     single instruction); larger keys go to the hash part like any other
//     key (the length operator and ipairs see only the array part, so a
//     list filled by index stops at arrayIndexLimit-1 entries; table.insert
//     without a position still appends to the array part). This is
//     gopher-lua's package variable MaxArrayIndex, set once
//     when the package is loaded; luabox is the only place omo creates
//     gopher-lua states.
//
// Bounded only approximately: memory. gopher-lua cannot cap its heap. The
// limits above stop the amplifying library calls; the .. operator and table
// growth are not capped per value. A watchdog samples the process heap
// every 20 ms and ends the run when it has grown by more than
// Limits.MaxMemory since the box was opened. The heap is the whole
// process's, so other work running at the same time counts too, and a
// script can overshoot by what it allocates within one sample (repeated
// doubling with .. can reach gigabytes within a few instructions). The
// deadline ends anything the watchdog misses.
package luabox

import (
	"context"
	"errors"
	"fmt"
	"runtime/metrics"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Defaults of Limits.
const (
	DefaultTimeout     = 60 * time.Second
	DefaultMaxSource   = 64 << 10
	DefaultMaxLogLines = 50
	DefaultMaxMemory   = 256 << 20
)

// arrayIndexLimit is the gopher-lua MaxArrayIndex of every state.
const arrayIndexLimit = 1 << 16

func init() { lua.MaxArrayIndex = arrayIndexLimit }

// Fixed limits of every box.
const (
	// MaxString is the size of a string the library functions that amplify
	// (string.rep, string.format, string.gsub, table.concat,
	// omo.json_encode) may make, and of a JSON text omo.json_decode reads.
	MaxString = 1 << 20
	// MaxDepth is the nesting depth of values converted between Go and Lua
	// and of JSON.
	MaxDepth = 64
	// MaxItems is the number of values (table entries counted one by one)
	// one conversion between Go and Lua handles.
	MaxItems = 1 << 20
	// MaxLogRunes is the length of one log line.
	MaxLogRunes = 500

	callStackSize   = 200
	registrySize    = 2 << 10
	registryMaxSize = 256 << 10
	// registryGrowStep keeps growing the registry cheap: gopher-lua grows
	// it by required size plus this step, and copies it each time.
	registryGrowStep = 16 << 10
	maxCoroutines    = 100
	memorySample     = 20 * time.Millisecond
)

// Limits are the bounds of one run. Zero fields take the defaults.
type Limits struct {
	Timeout     time.Duration // whole run; 0 = 60s
	MaxSource   int           // bytes of all loaded source; 0 = 64 KiB
	MaxLogLines int           // 0 = 50
	MaxMemory   int64         // heap growth, bytes; 0 = 256 MiB (approximate, see the package doc)
}

func (l Limits) withDefaults() Limits {
	if l.Timeout <= 0 {
		l.Timeout = DefaultTimeout
	}
	if l.MaxSource <= 0 {
		l.MaxSource = DefaultMaxSource
	}
	if l.MaxLogLines <= 0 {
		l.MaxLogLines = DefaultMaxLogLines
	}
	if l.MaxMemory <= 0 {
		l.MaxMemory = DefaultMaxMemory
	}
	return l
}

var (
	errMemory = errors.New(msgMemoryLimit)
	errClosed = errors.New("sandbox closed")
)

// Box is one sandboxed Lua state. Open it with New, register helpers, Load
// the script, Call its functions, read Log and Close it. A Box is not safe
// for concurrent use; abort a running call by cancelling the context given
// to New.
type Box struct {
	L      *lua.LState
	lim    Limits
	ctx    context.Context
	cancel context.CancelCauseFunc
	stopT  context.CancelFunc
	red    *Redactor
	omo    *lua.LTable
	chunks []string
	loaded int

	logMu sync.Mutex
	log   []string

	coroutines int
	closers    []func()
	watch      sync.WaitGroup
	closed     bool
}

// New opens a fresh sandbox under ctx with lim. The run's deadline starts
// now. The omo table holds json_decode, json_encode, sha256_hex, nfc,
// time_parse, now and log.
func New(ctx context.Context, lim Limits) *Box {
	lim = lim.withDefaults()
	causeCtx, cancel := context.WithCancelCause(ctx)
	runCtx, stopT := context.WithTimeout(causeCtx, lim.Timeout)
	b := &Box{lim: lim, ctx: runCtx, cancel: cancel, stopT: stopT, red: NewRedactor()}
	b.L = lua.NewState(lua.Options{
		CallStackSize:    callStackSize,
		RegistrySize:     registrySize,
		RegistryMaxSize:  registryMaxSize,
		RegistryGrowStep: registryGrowStep,
		SkipOpenLibs:     true,
	})
	b.openLibs()
	b.L.SetContext(runCtx)
	b.watch.Add(1)
	go b.watchMemory(heapBytes())
	return b
}

// Context is the run's context; it ends at the deadline, on cancellation of
// the parent and on Close. Helpers pass it to everything that can block.
func (b *Box) Context() context.Context { return b.ctx }

// Redactor is the run's redactor. Helpers register what they read or send
// as secrets with it.
func (b *Box) Redactor() *Redactor { return b.red }

// Close ends the run and frees the state. It waits for the box's own
// goroutines; it is safe to call more than once.
func (b *Box) Close() {
	if b.closed {
		return
	}
	b.closed = true
	b.cancel(errClosed)
	b.stopT()
	b.watch.Wait()
	for i := len(b.closers) - 1; i >= 0; i-- {
		b.closers[i]()
	}
	b.L.Close()
}

// onClose registers f to run at Close.
func (b *Box) onClose(f func()) { b.closers = append(b.closers, f) }

// stopReason is the fixed message of a run that ended by a limit or by
// cancellation, "" while it runs.
func (b *Box) stopReason() string {
	if b.ctx.Err() == nil {
		return ""
	}
	cause := context.Cause(b.ctx)
	switch {
	case errors.Is(cause, errMemory):
		return msgMemoryLimit
	case errors.Is(cause, context.DeadlineExceeded):
		return msgTimeLimit
	default:
		return msgCanceled
	}
}

// checkStop raises the run's stop reason as a Lua error once the run ended.
// Go functions that loop call it.
func (b *Box) checkStop(L *lua.LState) {
	if b.ctx.Err() != nil {
		L.RaiseError("%s", b.stopReason())
	}
}

// heapBytes is the size of the heap objects of the process.
func heapBytes() int64 {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return int64(s[0].Value.Uint64())
}

// watchMemory ends the run when the process heap grew by more than
// MaxMemory over base. It returns when the run ends.
func (b *Box) watchMemory(base int64) {
	defer b.watch.Done()
	t := time.NewTicker(memorySample)
	defer t.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-t.C:
			if heapBytes()-base > b.lim.MaxMemory {
				b.cancel(errMemory)
				return
			}
		}
	}
}

// Set adds omo.<name>. A Go panic in fn is turned into the Lua error
// "omo.<name> failed"; Lua errors fn raises pass as they are. fn must
// honour Context() wherever it can block.
func (b *Box) Set(name string, fn lua.LGFunction) {
	b.omo.RawSetString(name, b.L.NewFunction(guard("omo."+name, fn)))
}

// guard wraps fn so a Go panic cannot leave it except as a Lua error with a
// fixed text.
func guard(name string, fn lua.LGFunction) lua.LGFunction {
	return func(L *lua.LState) (n int) {
		defer func() {
			if r := recover(); r != nil {
				if apiErr, ok := r.(*lua.ApiError); ok {
					panic(apiErr)
				}
				L.RaiseError("%s failed", name)
			}
		}()
		return fn(L)
	}
}

// SetGlobal sets the global name to v converted with FromGo. It refuses
// names the sandbox defines itself.
func (b *Box) SetGlobal(name string, v any) error {
	if _, ok := allowedGlobals[name]; ok || name == "" {
		return fmt.Errorf("global %q is the sandbox's own", name)
	}
	lv, err := FromGo(b.L, v)
	if err != nil {
		return err
	}
	b.L.SetGlobal(name, lv)
	return nil
}

// Load compiles chunk, a script's source text, under the name source and
// runs its top level. All chunks loaded into a box together are at most
// MaxSource bytes. Only source text loads; precompiled chunks are refused.
func (b *Box) Load(chunk, source string) error {
	if b.closed {
		return &ScriptError{msg: errClosed.Error(), red: b.red}
	}
	if b.loaded+len(chunk) > b.lim.MaxSource {
		return &ScriptError{msg: fmt.Sprintf("script exceeds %d bytes", b.lim.MaxSource), red: b.red}
	}
	if strings.HasPrefix(chunk, "\x1bLua") {
		return &ScriptError{msg: "precompiled chunks are not accepted", red: b.red}
	}
	if err := b.ctx.Err(); err != nil {
		return b.scriptError(err)
	}
	b.loaded += len(chunk)
	b.chunks = append(b.chunks, source)
	fn, err := b.L.Load(strings.NewReader(chunk), source)
	if err != nil {
		return b.scriptError(err)
	}
	b.L.Push(fn)
	if err := b.L.PCall(0, 0, nil); err != nil {
		return b.scriptError(err)
	}
	return nil
}

// Call calls the global function fn with args (converted with FromGo) and
// returns its first result converted with ToGo.
func (b *Box) Call(fn string, args ...any) (any, error) {
	if b.closed {
		return nil, &ScriptError{msg: errClosed.Error(), red: b.red}
	}
	if err := b.ctx.Err(); err != nil {
		return nil, b.scriptError(err)
	}
	f, ok := b.L.GetGlobal(fn).(*lua.LFunction)
	if !ok {
		return nil, &ScriptError{msg: "no function " + fn, red: b.red}
	}
	top := b.L.GetTop()
	defer b.L.SetTop(top)
	b.L.Push(f)
	for i, a := range args {
		lv, err := FromGo(b.L, a)
		if err != nil {
			return nil, &ScriptError{msg: fmt.Sprintf("argument %d: %s", i+1, err), red: b.red}
		}
		b.L.Push(lv)
	}
	if err := b.L.PCall(len(args), 1, nil); err != nil {
		return nil, b.scriptError(err)
	}
	if reason := b.stopReason(); reason != "" {
		return nil, &ScriptError{msg: reason, red: b.red}
	}
	out, err := ToGo(b.L.Get(-1))
	if err != nil {
		return nil, &ScriptError{msg: "result: " + err.Error(), red: b.red}
	}
	return out, nil
}

// Log is the run's log (omo.log and print), cleaned by the redactor at the
// time of reading, each line at most 500 runes.
func (b *Box) Log() []string {
	b.logMu.Lock()
	lines := append([]string(nil), b.log...)
	b.logMu.Unlock()
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = cutRunes(b.red.Clean(l), MaxLogRunes)
	}
	return out
}

// addLog keeps a line whole (cut only when read, after redaction, so no
// part of a secret survives a cut) until MaxLogLines lines are kept.
func (b *Box) addLog(line string) {
	b.logMu.Lock()
	defer b.logMu.Unlock()
	if len(b.log) < b.lim.MaxLogLines {
		b.log = append(b.log, line)
	}
}

// allowedGlobals is every global of a fresh box, by name. The test
// TestGlobalsAllowList keeps this list and the state in step.
var allowedGlobals = map[string]struct{}{
	"_G": {}, "_VERSION": {}, "assert": {}, "error": {}, "getmetatable": {},
	"ipairs": {}, "next": {}, "pairs": {}, "pcall": {}, "print": {},
	"rawequal": {}, "rawget": {}, "rawset": {}, "select": {},
	"setmetatable": {}, "tonumber": {}, "tostring": {}, "type": {},
	"unpack": {}, "xpcall": {},
	"string": {}, "table": {}, "math": {}, "os": {}, "coroutine": {}, "omo": {},
}

// removedBase are the base functions of gopher-lua a script never gets.
var removedBase = []string{
	"collectgarbage", "dofile", "getfenv", "load", "loadfile", "loadstring",
	"module", "newproxy", "require", "setfenv", "_printregs", "_GOPHER_LUA_VERSION",
}

// openLibs opens the libraries of the package doc and nothing else.
func (b *Box) openLibs() {
	L := b.L
	for _, lib := range []struct {
		name string
		fn   lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
		{lua.OsLibName, lua.OpenOs},
		{lua.CoroutineLibName, lua.OpenCoroutine},
	} {
		L.Push(L.NewFunction(lib.fn))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}
	for _, name := range removedBase {
		L.SetGlobal(name, lua.LNil)
	}
	L.SetGlobal("print", L.NewFunction(b.luaPrint))

	// os: a new table with only the clock functions.
	osLib := L.GetGlobal("os").(*lua.LTable)
	keep := L.NewTable()
	for _, name := range []string{"time", "clock"} {
		keep.RawSetString(name, osLib.RawGetString(name))
	}
	keep.RawSetString("date", L.NewFunction(osDate(osLib.RawGetString("date").(*lua.LFunction))))
	L.SetGlobal("os", keep)

	// string: the same table is the __index of the string metatable, so
	// changing it here changes ("").rep and friends too.
	str := L.GetGlobal("string").(*lua.LTable)
	str.RawSetString("dump", lua.LNil)
	str.RawSetString("rep", L.NewFunction(b.strRep))
	str.RawSetString("format", L.NewFunction(b.strFormat(str.RawGetString("format").(*lua.LFunction))))
	str.RawSetString("find", L.NewFunction(b.strFind))
	str.RawSetString("match", L.NewFunction(b.strMatch))
	gmatch := L.NewFunction(b.strGmatch)
	str.RawSetString("gmatch", gmatch)
	str.RawSetString("gfind", gmatch)
	str.RawSetString("gsub", L.NewFunction(b.strGsub))

	tbl := L.GetGlobal("table").(*lua.LTable)
	tbl.RawSetString("sort", L.NewFunction(b.tableSort(tbl.RawGetString("sort").(*lua.LFunction))))
	tbl.RawSetString("concat", L.NewFunction(b.tableConcat))

	co := L.GetGlobal("coroutine").(*lua.LTable)
	co.RawSetString("create", L.NewFunction(b.counted(co.RawGetString("create").(*lua.LFunction))))
	co.RawSetString("wrap", L.NewFunction(b.counted(co.RawGetString("wrap").(*lua.LFunction))))

	b.omo = L.NewTable()
	L.SetGlobal("omo", b.omo)
	b.Set("json_decode", b.jsonDecode)
	b.Set("json_encode", b.jsonEncode)
	b.Set("sha256_hex", luaSHA256)
	b.Set("nfc", luaNFC)
	b.Set("time_parse", luaTimeParse)
	b.Set("now", luaNow)
	b.Set("log", b.luaLog)
}

// counted wraps coroutine.create or coroutine.wrap so a run makes at most
// maxCoroutines coroutines. Each one has its own call stack and registry;
// the count bounds their memory and the Go stack of nested resumes.
func (b *Box) counted(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		if b.coroutines >= maxCoroutines {
			L.RaiseError("too many coroutines (at most %d)", maxCoroutines)
		}
		b.coroutines++
		return callOriginal(L, orig)
	}
}

// callOriginal calls the Go library function orig with the current
// arguments and returns all its results.
func callOriginal(L *lua.LState, orig *lua.LFunction) int {
	n := L.GetTop()
	L.Insert(orig, 1)
	L.Call(n, lua.MultRet)
	return L.GetTop()
}

// tableSort checks the deadline after every call of a comparator, so a
// comparator that swallows the deadline with pcall cannot keep the sort
// going.
func (b *Box) tableSort(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		if L.GetTop() >= 2 && L.Get(2) != lua.LNil {
			cmp := L.CheckFunction(2)
			L.Replace(2, L.NewFunction(func(L *lua.LState) int {
				L.Push(cmp)
				L.Push(L.Get(1))
				L.Push(L.Get(2))
				L.Call(2, 1)
				b.checkStop(L)
				return 1
			}))
		}
		return callOriginal(L, orig)
	}
}

// tableConcat is table.concat of Lua 5.1, built in Go without the stack
// (so long lists work) and refusing a result over MaxString.
func (b *Box) tableConcat(L *lua.LState) int {
	t := L.CheckTable(1)
	sep := L.OptString(2, "")
	i := L.OptInt(3, 1)
	j := L.OptInt(4, t.Len())
	var out strings.Builder
	for k := i; k <= j; k++ {
		var part string
		switch v := t.RawGetInt(k).(type) {
		case lua.LString:
			part = string(v)
		case lua.LNumber:
			part = v.String()
		default:
			L.RaiseError("invalid value (at index %d) in table for 'concat'", k)
		}
		if out.Len()+len(part)+len(sep) > MaxString {
			L.RaiseError("string result exceeds %d bytes", MaxString)
		}
		out.WriteString(part)
		if k < j {
			out.WriteString(sep)
		}
		if k&0x3ff == 0 {
			b.checkStop(L)
		}
	}
	L.Push(lua.LString(out.String()))
	return 1
}

// luaPrint writes its arguments, separated by spaces, to the run's log.
func (b *Box) luaPrint(L *lua.LState) int {
	parts := make([]string, L.GetTop())
	for i := range parts {
		parts[i] = L.ToStringMeta(L.Get(i + 1)).String()
	}
	b.addLog(strings.Join(parts, " "))
	return 0
}

// luaLog is omo.log(text).
func (b *Box) luaLog(L *lua.LState) int {
	b.addLog(L.ToStringMeta(L.CheckAny(1)).String())
	return 0
}

// maxDateDirective is more than the longest text one directive of os.date
// expands to.
const maxDateDirective = 64

// osDate is os.date refusing a format whose result could exceed MaxString
// before anything is formatted.
func osDate(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		if L.GetTop() >= 1 {
			f := L.CheckString(1)
			if len(f)+maxDateDirective*strings.Count(f, "%") > MaxString {
				L.RaiseError("string result exceeds %d bytes", MaxString)
			}
		}
		return orig.GFunction(L)
	}
}
