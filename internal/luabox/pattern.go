package luabox

import (
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// The pattern functions of the string library (find, match, gmatch, gsub)
// are a port of Lua 5.1's lstrlib.c matcher instead of gopher-lua's, whose
// backtracking cannot be interrupted: a pattern like ("a"):rep(30)..".-.-.-b"
// would hold a run for hours inside one Go call. This one counts its steps,
// checks the deadline every few thousand of them and refuses a single call
// over patternSteps steps; its recursion is bounded by maxMatchDepth.
const (
	patternSteps  = 1 << 25
	stopEvery     = 1 << 12
	maxMatchDepth = 200
	maxCaptures   = 32
	capUnfinished = -1
	capPosition   = -2
	specials      = "^$*+?.([%-"
)

type capture struct{ init, len int }

type matchState struct {
	b       *Box
	L       *lua.LState
	src     string
	pat     string
	level   int
	depth   int
	steps   int
	capture [maxCaptures]capture
}

// step counts one unit of matching work.
func (ms *matchState) step() {
	ms.steps++
	if ms.steps&(stopEvery-1) == 0 {
		ms.b.checkStop(ms.L)
		if ms.steps > patternSteps {
			ms.L.RaiseError("pattern too complex")
		}
	}
}

// pc returns the pattern byte at i, or -1 past the end (C's '\0').
func (ms *matchState) pc(i int) int {
	if i >= len(ms.pat) {
		return -1
	}
	return int(ms.pat[i])
}

func (ms *matchState) checkCapture(l int) int {
	l -= '1'
	if l < 0 || l >= ms.level || ms.capture[l].len == capUnfinished {
		ms.L.RaiseError("invalid capture index")
	}
	return l
}

func (ms *matchState) captureToClose() int {
	level := ms.level - 1
	for ; level >= 0; level-- {
		if ms.capture[level].len == capUnfinished {
			return level
		}
	}
	ms.L.RaiseError("invalid pattern capture")
	return 0
}

func (ms *matchState) classEnd(p int) int {
	c := ms.pc(p)
	p++
	if c == '%' {
		if ms.pc(p) == -1 {
			ms.L.RaiseError("malformed pattern (ends with '%%')")
		}
		return p + 1
	}
	if c == '[' {
		if ms.pc(p) == '^' {
			p++
		}
		for {
			if ms.pc(p) == -1 {
				ms.L.RaiseError("malformed pattern (missing ']')")
			}
			cc := ms.pc(p)
			p++
			if cc == '%' && ms.pc(p) != -1 {
				p++
			}
			if ms.pc(p) == ']' {
				break
			}
		}
		return p + 1
	}
	return p
}

func isalpha(c int) bool  { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isdigit(c int) bool  { return c >= '0' && c <= '9' }
func islower(c int) bool  { return c >= 'a' && c <= 'z' }
func isupper(c int) bool  { return c >= 'A' && c <= 'Z' }
func isspace(c int) bool  { return c == ' ' || c >= '\t' && c <= '\r' }
func iscntrl(c int) bool  { return c < 32 || c == 127 }
func isalnum(c int) bool  { return isalpha(c) || isdigit(c) }
func isxdigit(c int) bool { return isdigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
func ispunct(c int) bool  { return c > 32 && c < 127 && !isalnum(c) }
func tolower(c int) int {
	if isupper(c) {
		return c + 32
	}
	return c
}

func matchClass(c, cl int) bool {
	var res bool
	switch tolower(cl) {
	case 'a':
		res = isalpha(c)
	case 'c':
		res = iscntrl(c)
	case 'd':
		res = isdigit(c)
	case 'l':
		res = islower(c)
	case 'p':
		res = ispunct(c)
	case 's':
		res = isspace(c)
	case 'u':
		res = isupper(c)
	case 'w':
		res = isalnum(c)
	case 'x':
		res = isxdigit(c)
	case 'z':
		res = c == 0
	default:
		return cl == c
	}
	if isupper(cl) {
		return !res
	}
	return res
}

// matchBracketClass matches c against the class [..] from p (at '[') to ec
// (at ']').
func (ms *matchState) matchBracketClass(c, p, ec int) bool {
	sig := true
	if ms.pc(p+1) == '^' {
		sig = false
		p++
	}
	for p++; p < ec; p++ {
		ms.step()
		if ms.pc(p) == '%' {
			p++
			if matchClass(c, ms.pc(p)) {
				return sig
			}
		} else if ms.pc(p+1) == '-' && p+2 < ec {
			p += 2
			if ms.pc(p-2) <= c && c <= ms.pc(p) {
				return sig
			}
		} else if ms.pc(p) == c {
			return sig
		}
	}
	return !sig
}

func (ms *matchState) singleMatch(c, p, ep int) bool {
	ms.step()
	switch ms.pc(p) {
	case '.':
		return true
	case '%':
		return matchClass(c, ms.pc(p+1))
	case '[':
		return ms.matchBracketClass(c, p, ep-1)
	default:
		return ms.pc(p) == c
	}
}

func (ms *matchState) matchBalance(s, p int) int {
	if ms.pc(p) == -1 || ms.pc(p+1) == -1 {
		ms.L.RaiseError("unbalanced pattern")
	}
	if s >= len(ms.src) || int(ms.src[s]) != ms.pc(p) {
		return -1
	}
	b, e := ms.pc(p), ms.pc(p+1)
	cont := 1
	for s++; s < len(ms.src); s++ {
		ms.step()
		c := int(ms.src[s])
		if c == e {
			cont--
			if cont == 0 {
				return s + 1
			}
		} else if c == b {
			cont++
		}
	}
	return -1
}

func (ms *matchState) maxExpand(s, p, ep int) int {
	i := 0
	for s+i < len(ms.src) && ms.singleMatch(int(ms.src[s+i]), p, ep) {
		i++
	}
	for ; i >= 0; i-- {
		if res := ms.match(s+i, ep+1); res != -1 {
			return res
		}
	}
	return -1
}

func (ms *matchState) minExpand(s, p, ep int) int {
	for {
		if res := ms.match(s, ep+1); res != -1 {
			return res
		}
		if s < len(ms.src) && ms.singleMatch(int(ms.src[s]), p, ep) {
			s++
		} else {
			return -1
		}
	}
}

func (ms *matchState) startCapture(s, p, what int) int {
	level := ms.level
	if level >= maxCaptures {
		ms.L.RaiseError("too many captures")
	}
	ms.capture[level] = capture{init: s, len: what}
	ms.level = level + 1
	res := ms.match(s, p)
	if res == -1 {
		ms.level--
	}
	return res
}

func (ms *matchState) endCapture(s, p int) int {
	l := ms.captureToClose()
	ms.capture[l].len = s - ms.capture[l].init
	res := ms.match(s, p)
	if res == -1 {
		ms.capture[l].len = capUnfinished
	}
	return res
}

func (ms *matchState) matchCapture(s, l int) int {
	l = ms.checkCapture(l)
	c := ms.capture[l]
	if len(ms.src)-s >= c.len && ms.src[c.init:c.init+c.len] == ms.src[s:s+c.len] {
		return s + c.len
	}
	return -1
}

// match returns the end of the match of the pattern from p against the
// subject from s, or -1.
func (ms *matchState) match(s, p int) int {
	ms.depth++
	defer func() { ms.depth-- }()
	if ms.depth > maxMatchDepth {
		ms.L.RaiseError("pattern too complex")
	}
	for {
		ms.step()
		switch ms.pc(p) {
		case '(':
			if ms.pc(p+1) == ')' {
				return ms.startCapture(s, p+2, capPosition)
			}
			return ms.startCapture(s, p+1, capUnfinished)
		case ')':
			return ms.endCapture(s, p+1)
		case -1:
			return s
		case '$':
			if ms.pc(p+1) == -1 {
				if s == len(ms.src) {
					return s
				}
				return -1
			}
		case '%':
			switch ms.pc(p + 1) {
			case 'b':
				s = ms.matchBalance(s, p+2)
				if s == -1 {
					return -1
				}
				p += 4
				continue
			case 'f':
				p += 2
				if ms.pc(p) != '[' {
					ms.L.RaiseError("missing '[' after '%%f' in pattern")
				}
				ep := ms.classEnd(p)
				prev, cur := 0, 0
				if s > 0 {
					prev = int(ms.src[s-1])
				}
				if s < len(ms.src) {
					cur = int(ms.src[s])
				}
				if ms.matchBracketClass(prev, p, ep-1) || !ms.matchBracketClass(cur, p, ep-1) {
					return -1
				}
				p = ep
				continue
			default:
				if isdigit(ms.pc(p + 1)) {
					s = ms.matchCapture(s, ms.pc(p+1))
					if s == -1 {
						return -1
					}
					p += 2
					continue
				}
			}
		}
		// default: a single character class, maybe with a repetition.
		ep := ms.classEnd(p)
		m := s < len(ms.src) && ms.singleMatch(int(ms.src[s]), p, ep)
		switch ms.pc(ep) {
		case '?':
			if m {
				if res := ms.match(s+1, ep+1); res != -1 {
					return res
				}
			}
			p = ep + 1
			continue
		case '*':
			return ms.maxExpand(s, p, ep)
		case '+':
			if m {
				return ms.maxExpand(s+1, p, ep)
			}
			return -1
		case '-':
			return ms.minExpand(s, p, ep)
		default:
			if !m {
				return -1
			}
			s++
			p = ep
		}
	}
}

// oneCapture returns capture i as a Lua value; s and e delimit the whole
// match, used when there are no captures.
func (ms *matchState) oneCapture(i, s, e int) lua.LValue {
	if i >= ms.level {
		if i == 0 {
			return lua.LString(ms.src[s:e])
		}
		ms.L.RaiseError("invalid capture index")
	}
	c := ms.capture[i]
	switch c.len {
	case capUnfinished:
		ms.L.RaiseError("unfinished capture")
	case capPosition:
		return lua.LNumber(c.init + 1)
	}
	return lua.LString(ms.src[c.init : c.init+c.len])
}

// pushCaptures pushes the captures (or the whole match when s >= 0 and
// there are none) and returns how many.
func (ms *matchState) pushCaptures(s, e int) int {
	n := ms.level
	if n == 0 && s >= 0 {
		n = 1
	}
	for i := 0; i < n; i++ {
		ms.L.Push(ms.oneCapture(i, s, e))
	}
	return n
}

func posRelat(pos, n int) int {
	if pos < 0 {
		pos += n + 1
	}
	if pos < 0 {
		return 0
	}
	return pos
}

func (b *Box) newMatch(L *lua.LState, src, pat string) *matchState {
	return &matchState{b: b, L: L, src: src, pat: pat}
}

func (b *Box) findAux(L *lua.LState, find bool) int {
	s := L.CheckString(1)
	p := L.CheckString(2)
	init := posRelat(L.OptInt(3, 1), len(s)) - 1
	if init < 0 {
		init = 0
	} else if init > len(s) {
		init = len(s)
	}
	if find && (L.ToBool(4) || !strings.ContainsAny(p, specials)) {
		if i := strings.Index(s[init:], p); i >= 0 {
			L.Push(lua.LNumber(init + i + 1))
			L.Push(lua.LNumber(init + i + len(p)))
			return 2
		}
		L.Push(lua.LNil)
		return 1
	}
	ms := b.newMatch(L, s, p)
	pstart := 0
	anchor := len(p) > 0 && p[0] == '^'
	if anchor {
		pstart = 1
	}
	for s1 := init; ; s1++ {
		ms.level = 0
		if e := ms.match(s1, pstart); e != -1 {
			if find {
				L.Push(lua.LNumber(s1 + 1))
				L.Push(lua.LNumber(e))
				return ms.pushCaptures(-1, 0) + 2
			}
			return ms.pushCaptures(s1, e)
		}
		if s1 >= len(s) || anchor {
			break
		}
	}
	L.Push(lua.LNil)
	return 1
}

// strFind is string.find of Lua 5.1, bounded.
func (b *Box) strFind(L *lua.LState) int { return b.findAux(L, true) }

// strMatch is string.match of Lua 5.1, bounded.
func (b *Box) strMatch(L *lua.LState) int { return b.findAux(L, false) }

// strGmatch is string.gmatch of Lua 5.1, bounded per call of the iterator.
func (b *Box) strGmatch(L *lua.LState) int {
	s := L.CheckString(1)
	p := L.CheckString(2)
	pos := 0
	L.Push(L.NewFunction(func(L *lua.LState) int {
		ms := b.newMatch(L, s, p)
		for src := pos; src <= len(s); src++ {
			ms.level = 0
			if e := ms.match(src, 0); e != -1 {
				pos = e
				if e == src {
					pos++
				}
				return ms.pushCaptures(src, e)
			}
		}
		pos = len(s) + 1
		return 0
	}))
	return 1
}

// strGsub is string.gsub of Lua 5.1, bounded in work and in the size of
// its result.
func (b *Box) strGsub(L *lua.LState) int {
	src := L.CheckString(1)
	p := L.CheckString(2)
	repl := L.Get(3)
	switch repl.Type() {
	case lua.LTNumber, lua.LTString, lua.LTTable, lua.LTFunction:
	default:
		L.ArgError(3, "string/function/table expected")
	}
	maxN := L.OptInt(4, len(src)+1)
	anchor := len(p) > 0 && p[0] == '^'
	pstart := 0
	if anchor {
		pstart = 1
	}
	ms := b.newMatch(L, src, p)
	var out strings.Builder
	add := func(s string) {
		if out.Len()+len(s) > MaxString {
			L.RaiseError("string result exceeds %d bytes", MaxString)
		}
		out.WriteString(s)
	}
	s, n := 0, 0
	for n < maxN {
		ms.level = 0
		e := ms.match(s, pstart)
		if e != -1 {
			n++
			b.addValue(ms, add, repl, s, e)
		}
		if e != -1 && e > s {
			s = e
		} else if s < len(src) {
			add(src[s : s+1])
			s++
		} else {
			break
		}
		if anchor {
			break
		}
	}
	add(src[s:])
	L.Push(lua.LString(out.String()))
	L.Push(lua.LNumber(n))
	return 2
}

func (b *Box) addValue(ms *matchState, add func(string), repl lua.LValue, s, e int) {
	L := ms.L
	var val lua.LValue
	switch r := repl.(type) {
	case lua.LString, lua.LNumber:
		news := lua.LVAsString(r)
		for i := 0; i < len(news); i++ {
			ms.step()
			if news[i] != '%' {
				add(news[i : i+1])
				continue
			}
			i++
			if i >= len(news) || !isdigit(int(news[i])) {
				if i < len(news) {
					add(news[i : i+1])
				}
				continue
			}
			if news[i] == '0' {
				add(ms.src[s:e])
				continue
			}
			add(lua.LVAsString(ms.oneCapture(int(news[i]-'1'), s, e)))
		}
		return
	case *lua.LTable:
		val = L.GetTable(r, ms.oneCapture(0, s, e))
	case *lua.LFunction:
		top := L.GetTop()
		L.Push(r)
		n := ms.pushCaptures(s, e)
		L.Call(n, 1)
		val = L.Get(-1)
		L.SetTop(top)
	}
	b.checkStop(L)
	switch v := val.(type) {
	case *lua.LNilType:
		add(ms.src[s:e])
	case lua.LBool:
		if !bool(v) {
			add(ms.src[s:e])
			return
		}
		L.RaiseError("invalid replacement value (a %s)", val.Type().String())
	case lua.LString:
		add(string(v))
	case lua.LNumber:
		add(v.String())
	default:
		L.RaiseError("invalid replacement value (a %s)", val.Type().String())
	}
}

// strRep is string.rep, refusing results over MaxString.
func (b *Box) strRep(L *lua.LState) int {
	s := L.CheckString(1)
	n := L.CheckInt(2)
	if n <= 0 || s == "" {
		L.Push(lua.LString(""))
		return 1
	}
	if n > MaxString/len(s) {
		L.RaiseError("string result exceeds %d bytes", MaxString)
	}
	L.Push(lua.LString(strings.Repeat(s, n)))
	return 1
}

// strFormat accepts only the conversions of Lua 5.1 with at most two
// digits of width and precision, refuses Go's own verbs and argument
// indexes, and refuses a result that could exceed MaxString before making
// it. The formatting itself is gopher-lua's.
func (b *Box) strFormat(orig *lua.LFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		f := L.CheckString(1)
		size := int64(len(f))
		arg := 2
		for i := 0; i < len(f); i++ {
			if f[i] != '%' {
				continue
			}
			i++
			if i < len(f) && f[i] == '%' {
				continue
			}
			start := i
			for i < len(f) && strings.IndexByte("-+ #0", f[i]) >= 0 {
				i++
			}
			if i-start > 5 {
				L.RaiseError("invalid format (repeated flags)")
			}
			width := 0
			for i < len(f) && isdigit(int(f[i])) {
				i++
				width++
			}
			precision := 0
			if i < len(f) && f[i] == '.' {
				i++
				for i < len(f) && isdigit(int(f[i])) {
					i++
					precision++
				}
			}
			if width > 2 || precision > 2 {
				L.RaiseError("invalid format (width or precision too long)")
			}
			if i >= len(f) || strings.IndexByte("cdiouxXeEfgGqs", f[i]) < 0 {
				L.RaiseError("invalid option to 'format'")
			}
			size += 512
			if arg <= L.GetTop() {
				if v, ok := L.Get(arg).(lua.LString); ok {
					size += 2*int64(len(v)) + 2
				}
			}
			arg++
			if size > MaxString {
				L.RaiseError("string result exceeds %d bytes", MaxString)
			}
		}
		return callOriginal(L, orig)
	}
}
