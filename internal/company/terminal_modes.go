package company

import (
	"github.com/charmbracelet/x/ansi"
	ansiparser "github.com/charmbracelet/x/ansi/parser"
)

type terminalModeState uint8

const (
	terminalModeUnknown terminalModeState = iota
	terminalModeSet
	terminalModeReset
)

var terminalModeOrder = [...]int{1, 7, 25, 47, 1000, 1002, 1003, 1004, 1005, 1006, 1015, 1016, 1047, 1049, 2004}

// terminalParserSnapshot records library parser state at a chunk boundary.
// The parser's private parameter/data buffers are not copied: replay cuts are
// recorded at observed safe boundaries while bytes are streamed, so no
// product state machine reconstructs parser transitions.
type terminalParserSnapshot struct {
	state ansiparser.State
	safe  bool
}

type terminalModeTracker struct {
	states             [len(terminalModeOrder)]terminalModeState
	parser             *ansi.Parser
	parserSafe         bool
	altState           terminalModeState
	altMode            int
	mouseTrackingState terminalModeState
	mouseTrackingMode  int
	mouseEncodingState terminalModeState
	mouseEncodingMode  int
}

func (t *terminalModeTracker) ensureParser() {
	if t.parser != nil {
		return
	}
	t.parser = ansi.NewParser()
	t.parser.SetParamsSize(ansiparser.MaxParamsSize)
	t.parserSafe = true
	t.parser.SetHandler(ansi.Handler{
		HandleCsi: func(cmd ansi.Cmd, params ansi.Params) {
			if cmd.Prefix() == 0 && cmd.Intermediate() == '!' && cmd.Final() == 'p' {
				t.resetAllModes()
				return
			}
			if cmd.Prefix() != '?' || cmd.Intermediate() != 0 {
				return
			}
			switch cmd.Final() {
			case 'h', 'l':
				set := cmd.Final() == 'h'
				params.ForEach(0, func(_ int, mode int, hasMore bool) {
					if !hasMore {
						t.commitMode(mode, set)
					}
				})
			}
		},
		HandleEsc: func(cmd ansi.Cmd) {
			if cmd.Final() == 'c' && cmd.Intermediate() == 0 {
				t.resetAllModes()
			}
		},
	})
}

// feed records boundaries from ansi.Parser.Advance. A boundary is the
// library parser's ground state after a complete dispatch, excluding a
// partially collected UTF-8 rune (Utf8State).
func (t *terminalModeTracker) feed(data []byte, callbacks ...func(int)) {
	t.ensureParser()
	var boundary func(int)
	if len(callbacks) > 0 {
		boundary = callbacks[0]
	}
	for offset, b := range data {
		action := t.parser.Advance(b)
		t.parserSafe = t.parser.State() == ansiparser.GroundState && action != ansiparser.CollectAction
		if t.parserSafe && boundary != nil {
			boundary(offset + 1)
		}
	}
}

func (t *terminalModeTracker) parserSnapshot() terminalParserSnapshot {
	t.ensureParser()
	return terminalParserSnapshot{state: t.parser.State(), safe: t.parserSafe}
}

func (t *terminalModeTracker) commitMode(mode int, set bool) {
	state := terminalModeReset
	if set {
		state = terminalModeSet
	}
	index := terminalModeIndex(mode)
	if index < 0 {
		return
	}
	t.states[index] = state
	if mode == 47 || mode == 1047 || mode == 1049 {
		t.altState = state
		t.altMode = mode
	}
	if isMouseTrackingMode(mode) {
		if set {
			t.mouseTrackingState = terminalModeSet
			t.mouseTrackingMode = mode
		} else if mode == t.mouseTrackingMode {
			t.mouseTrackingState = terminalModeReset
			t.mouseTrackingMode = 0
		}
	}
	if isMouseEncodingMode(mode) {
		if set {
			t.mouseEncodingState = terminalModeSet
			t.mouseEncodingMode = mode
		} else if mode == t.mouseEncodingMode {
			t.mouseEncodingState = terminalModeReset
			t.mouseEncodingMode = 0
		}
	}
}

func isMouseTrackingMode(mode int) bool {
	return mode == 1000 || mode == 1002 || mode == 1003
}

func isMouseEncodingMode(mode int) bool {
	return mode == 1005 || mode == 1006 || mode == 1015 || mode == 1016
}

func terminalModeIndex(mode int) int {
	for index, candidate := range terminalModeOrder {
		if candidate == mode {
			return index
		}
	}
	return -1
}

func (t *terminalModeTracker) state(mode int) terminalModeState {
	index := terminalModeIndex(mode)
	if index < 0 {
		return terminalModeUnknown
	}
	return t.states[index]
}

func (t *terminalModeTracker) alternate() bool {
	return t.altState == terminalModeSet
}

func (t *terminalModeTracker) clearModes() {
	t.states = [len(terminalModeOrder)]terminalModeState{}
	t.altState = terminalModeUnknown
	t.altMode = 0
	t.mouseTrackingState = terminalModeUnknown
	t.mouseTrackingMode = 0
	t.mouseEncodingState = terminalModeUnknown
	t.mouseEncodingMode = 0
}

func (t *terminalModeTracker) resetAllModes() {
	t.clearModes()
}

func (t *terminalModeTracker) resetAll() {
	t.clearModes()
	if t.parser != nil {
		t.parser.Reset()
	}
	t.parserSafe = true
}

func (t *terminalModeTracker) prefix() []byte {
	var prefix []byte
	if t.alternate() {
		prefix = appendModeSequence(prefix, t.altMode, terminalModeSet)
	}
	for _, mode := range terminalModeOrder {
		if mode == 47 || mode == 1047 || mode == 1049 || isMouseTrackingMode(mode) || isMouseEncodingMode(mode) {
			continue
		}
		state := t.state(mode)
		if state == terminalModeSet || (state == terminalModeReset && (mode == 7 || mode == 25)) {
			prefix = appendModeSequence(prefix, mode, state)
		}
	}
	if t.mouseTrackingState == terminalModeSet {
		prefix = appendModeSequence(prefix, t.mouseTrackingMode, terminalModeSet)
	}
	if t.mouseEncodingState == terminalModeSet {
		prefix = appendModeSequence(prefix, t.mouseEncodingMode, terminalModeSet)
	}
	return prefix
}

func appendModeSequence(prefix []byte, mode int, state terminalModeState) []byte {
	prefix = append(prefix, '\x1b', '[', '?')
	prefix = appendInt(prefix, mode)
	if state == terminalModeReset {
		return append(prefix, 'l')
	}
	return append(prefix, 'h')
}

func appendInt(dst []byte, value int) []byte {
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	if index == len(digits) {
		index--
		digits[index] = '0'
	}
	return append(dst, digits[index:]...)
}
