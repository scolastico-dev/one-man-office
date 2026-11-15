package luabox

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"

	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// minSecret is the shortest text the redactor takes as a secret. Shorter
// texts would remove ordinary words from logs and error messages.
const minSecret = 6

// Redacted replaces every secret in cleaned text.
const Redacted = "[redacted]"

// maxErrorRunes is the length of an error text.
const maxErrorRunes = 300

// Redactor collects the secrets one run touched and removes them from text
// that leaves the run. It is safe for concurrent use.
//
// It finds a secret only where it appears whole and unchanged: a part of a
// secret, or a secret the script encoded (escaped, reversed, base64), is not
// found.
type Redactor struct {
	mu       sync.Mutex
	secrets  map[string]struct{}
	marked   map[string]struct{}
	replacer *strings.Replacer // nil when secrets changed since the last build
}

// NewRedactor returns an empty redactor.
func NewRedactor() *Redactor {
	return &Redactor{secrets: map[string]struct{}{}, marked: map[string]struct{}{}}
}

// Add registers a secret. Texts shorter than 6 bytes are ignored.
func (r *Redactor) Add(secret string) {
	if len(secret) < minSecret {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.secrets[secret]; ok {
		return
	}
	r.secrets[secret] = struct{}{}
	r.replacer = nil
}

// MarkSource registers text as a secret and marks it as coming from a
// secret source (a credential file, a keychain entry): every string value
// omo.json_decode later decodes from exactly this text is registered too.
func (r *Redactor) MarkSource(text string) {
	r.Add(text)
	if text == "" {
		return
	}
	r.mu.Lock()
	r.marked[text] = struct{}{}
	r.mu.Unlock()
}

// isMarked reports whether text was marked with MarkSource.
func (r *Redactor) isMarked(text string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.marked[text]
	return ok
}

// Clean replaces every registered secret in s by "[redacted]", the longest
// secret first, and then writes every character textsafe.IsHidden names as
// \u{N}, so the result is one line that cannot steer a terminal or hide
// text.
func (r *Redactor) Clean(s string) string {
	if r != nil {
		if rep := r.currentReplacer(); rep != nil {
			s = rep.Replace(s)
		}
	}
	return escapeHidden(s)
}

func (r *Redactor) currentReplacer() *strings.Replacer {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.secrets) == 0 {
		return nil
	}
	if r.replacer == nil {
		list := make([]string, 0, len(r.secrets))
		for s := range r.secrets {
			list = append(list, s)
		}
		// strings.Replacer prefers the earlier pair where two match at the
		// same place: longest first, so a secret containing another goes
		// whole.
		sort.Slice(list, func(i, j int) bool {
			if len(list[i]) != len(list[j]) {
				return len(list[i]) > len(list[j])
			}
			return list[i] < list[j]
		})
		pairs := make([]string, 0, 2*len(list))
		for _, s := range list {
			pairs = append(pairs, s, Redacted)
		}
		r.replacer = strings.NewReplacer(pairs...)
	}
	return r.replacer
}

// escapeHidden writes the characters textsafe.IsHidden names, line breaks
// and tabs included, and invalid UTF-8 bytes as \u{N}.
func escapeHidden(s string) string {
	clean := true
	for _, c := range s {
		if c == utf8.RuneError || textsafe.IsHidden(c) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case c == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x{%X}`, s[i])
		case textsafe.IsHidden(c):
			fmt.Fprintf(&b, `\u{%X}`, c)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// cutRunes cuts s to at most n runes, ending in "…" when cut.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n-1 {
			return s[:i] + "…"
		}
		count++
	}
	return s
}

// ScriptError is an error a script raised, a limit it hit, or a refusal of
// its source. Its Error text is cleaned with the redactor of the run.
type ScriptError struct {
	// Line is the script line the error points at, 0 when unknown.
	Line int
	// msg is the raw message, possibly holding secrets. Never shown as is.
	msg string
	red *Redactor
}

func (e *ScriptError) Error() string { return ErrorText(e, e.red) }

// ErrorText is the one way a script error becomes text: "line N: message"
// (or the message alone when no line is known), cleaned by r, one line, at
// most 300 runes. Errors of other types are treated the same way.
func ErrorText(err error, r *Redactor) string {
	if err == nil {
		return ""
	}
	var se *ScriptError
	text := ""
	if errors.As(err, &se) {
		text = se.msg
		if se.Line > 0 {
			text = "line " + strconv.Itoa(se.Line) + ": " + text
		}
	} else {
		text = err.Error()
	}
	return cutRunes(r.Clean(text), maxErrorRunes)
}

// Fixed messages of the limits.
const (
	msgTimeLimit   = "time limit reached"
	msgMemoryLimit = "memory limit reached"
	msgCanceled    = "run canceled"
	msgInternal    = "internal error in the sandbox"
)

// positionPrefix matches the "<chunk>:<line>: " gopher-lua puts in front of
// a runtime error.
var positionPrefix = regexp.MustCompile(`^(?s)(.*?):(\d+): (.*)$`)

// scriptError turns an error of gopher-lua into a ScriptError for the chunk
// names the box loaded. Messages that could echo the source (a syntax
// error's token) are reduced to their kind.
func (b *Box) scriptError(err error) *ScriptError {
	if reason := b.stopReason(); reason != "" {
		return &ScriptError{msg: reason, red: b.red}
	}
	var se *ScriptError
	if errors.As(err, &se) {
		return se
	}
	var aerr *lua.ApiError
	if errors.As(err, &aerr) && aerr.Cause != nil {
		err = aerr.Cause
	}
	var perr *parse.Error
	if errors.As(err, &perr) {
		msg := strings.TrimSpace(perr.Message)
		if msg == "" {
			msg = "syntax error"
		}
		line := perr.Pos.Line
		if line < 0 {
			line = 0
		}
		return &ScriptError{Line: line, msg: msg, red: b.red}
	}
	var cerr *lua.CompileError
	if errors.As(err, &cerr) {
		return &ScriptError{Line: cerr.Line, msg: cerr.Message, red: b.red}
	}
	if errors.As(err, &aerr) {
		if aerr.Type == lua.ApiErrorPanic {
			return &ScriptError{msg: msgInternal, red: b.red}
		}
		obj := aerr.Object
		if obj == nil {
			return &ScriptError{msg: "script failed", red: b.red}
		}
		s, ok := obj.(lua.LString)
		if !ok {
			if n, isNum := obj.(lua.LNumber); isNum {
				return &ScriptError{msg: n.String(), red: b.red}
			}
			return &ScriptError{msg: "script raised a " + obj.Type().String() + " as its error", red: b.red}
		}
		return b.splitPosition(string(s))
	}
	return &ScriptError{msg: err.Error(), red: b.red}
}

// splitPosition takes "<chunk>:<line>: text" apart when chunk is one the box
// loaded.
func (b *Box) splitPosition(s string) *ScriptError {
	if m := positionPrefix.FindStringSubmatch(s); m != nil {
		for _, name := range b.chunks {
			if m[1] == name {
				line, _ := strconv.Atoi(m[2])
				return &ScriptError{Line: line, msg: m[3], red: b.red}
			}
		}
	}
	return &ScriptError{msg: s, red: b.red}
}
