package company

import "unicode/utf8"

type terminalModeState uint8

const (
	terminalModeUnknown terminalModeState = iota
	terminalModeSet
	terminalModeReset
)

var terminalModeOrder = [...]int{1, 7, 25, 47, 1000, 1002, 1003, 1004, 1005, 1006, 1015, 1016, 1047, 1049, 2004}

type terminalParserState uint8

const (
	terminalParserGround terminalParserState = iota
	terminalParserEscape
	terminalParserCSI
	terminalParserDECSTR
)

type terminalModeTracker struct {
	states   [len(terminalModeOrder)]terminalModeState
	parser   terminalModeParser
	altState terminalModeState
	altMode  int
}

type terminalModeParser struct {
	state       terminalParserState
	private     bool
	invalid     bool
	params      [16]int
	paramCount  int
	current     int
	hasCurrent  bool
	lastWasSemi bool
}

func (t *terminalModeTracker) feed(data []byte) {
	for _, b := range data {
		t.feedByte(b)
	}
}

func (t *terminalModeTracker) feedByte(b byte) {
	switch t.parser.state {
	case terminalParserGround:
		if b == 0x1b {
			t.parser.state = terminalParserEscape
		} else if b == 0x9b {
			t.beginCSI()
		}
	case terminalParserEscape:
		switch b {
		case '[':
			t.beginCSI()
		case 'c':
			t.resetAll()
		case 0x1b:
			// Keep waiting for the next escape sequence.
		default:
			t.parser.state = terminalParserGround
		}
	case terminalParserDECSTR:
		if b == 'p' {
			t.resetAll()
		} else {
			t.parser.state = terminalParserGround
		}
	case terminalParserCSI:
		t.feedCSIByte(b)
	}
}

func (t *terminalModeTracker) beginCSI() {
	t.parser = terminalModeParser{state: terminalParserCSI}
}

func (t *terminalModeTracker) feedCSIByte(b byte) {
	p := &t.parser
	if p.paramCount == 0 && !p.hasCurrent && !p.private && !p.invalid {
		switch b {
		case '?':
			p.private = true
			return
		case '!':
			p.state = terminalParserDECSTR
			return
		}
	}
	if b >= '0' && b <= '9' {
		if p.current > 9999999 {
			p.invalid = true
		} else {
			p.current = p.current*10 + int(b-'0')
			p.hasCurrent = true
			p.lastWasSemi = false
		}
		return
	}
	if b == ';' {
		if !p.hasCurrent || p.paramCount >= len(p.params) {
			p.invalid = true
		} else {
			p.params[p.paramCount] = p.current
			p.paramCount++
			p.current = 0
			p.hasCurrent = false
		}
		p.lastWasSemi = true
		return
	}
	if b >= 0x40 && b <= 0x7e {
		if p.hasCurrent && p.paramCount < len(p.params) {
			p.params[p.paramCount] = p.current
			p.paramCount++
		} else if p.lastWasSemi || p.paramCount == 0 {
			p.invalid = true
		}
		if p.private && !p.invalid && (b == 'h' || b == 'l') {
			state := terminalModeSet
			if b == 'l' {
				state = terminalModeReset
			}
			for index := 0; index < p.paramCount; index++ {
				t.commitMode(p.params[index], state)
			}
		}
		p.state = terminalParserGround
		return
	}
	if b == 0x1b {
		p.state = terminalParserEscape
		return
	}
	// A malformed CSI is consumed through its final byte, without applying
	// any partially parsed parameters.
	p.invalid = true
}

func (t *terminalModeTracker) commitMode(mode int, state terminalModeState) {
	index := terminalModeIndex(mode)
	if index < 0 {
		return
	}
	t.states[index] = state
	if mode == 47 || mode == 1047 || mode == 1049 {
		t.altState = state
		t.altMode = mode
	}
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

func (t *terminalModeTracker) resetAll() {
	t.states = [len(terminalModeOrder)]terminalModeState{}
	t.altState = terminalModeUnknown
	t.altMode = 0
	t.parser = terminalModeParser{}
}

func (t *terminalModeTracker) prefix() []byte {
	var prefix []byte
	if t.alternate() {
		prefix = appendModeSequence(prefix, t.altMode, terminalModeSet)
	}
	for _, mode := range terminalModeOrder {
		if mode == 47 || mode == 1047 || mode == 1049 {
			continue
		}
		state := t.state(mode)
		if state == terminalModeSet || (state == terminalModeReset && (mode == 7 || mode == 25)) {
			prefix = appendModeSequence(prefix, mode, state)
		}
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

func safeReplayTail(data []byte, limit int) []byte {
	if limit <= 0 || len(data) == 0 {
		return nil
	}
	if len(data) <= limit {
		return append([]byte(nil), data...)
	}
	target := len(data) - limit
	boundaries := make([]bool, len(data)+1)
	boundaries[0] = true
	for index := 0; index < len(data); {
		start := index
		if data[index] == 0x1b {
			index = replayEscapeEnd(data, index)
		} else if data[index] == 0x9b || data[index] == 0x9d || data[index] == 0x90 || data[index] == 0x98 || data[index] == 0x9e || data[index] == 0x9f {
			index = replayC1End(data, index)
		} else if data[index]&0xc0 == 0x80 {
			index++
			for index < len(data) && data[index]&0xc0 == 0x80 {
				index++
			}
		} else if data[index] >= 0xc2 && data[index] <= 0xf4 {
			runeSize := utf8.RuneLen(rune(data[index]))
			if runeSize > 1 && index+runeSize <= len(data) && utf8.Valid(data[index:index+runeSize]) {
				index += runeSize
			} else {
				index++
			}
		} else {
			index++
		}
		if index > start && index <= len(data) {
			boundaries[index] = true
		}
	}
	for cut := target; cut <= len(data); cut++ {
		if boundaries[cut] {
			return append([]byte(nil), data[cut:]...)
		}
	}
	return nil
}

func replayEscapeEnd(data []byte, start int) int {
	if start+1 >= len(data) {
		return len(data)
	}
	switch data[start+1] {
	case '[', ']':
		if data[start+1] == ']' {
			return replayStringEnd(data, start+2)
		}
		return replayCSIEnd(data, start+2)
	case 'P', '^', '_':
		return replayStringEnd(data, start+2)
	default:
		return start + 2
	}
}

func replayCSIEnd(data []byte, start int) int {
	for index := start; index < len(data); index++ {
		if data[index] >= 0x40 && data[index] <= 0x7e {
			return index + 1
		}
	}
	return len(data)
}

func replayC1End(data []byte, start int) int {
	switch data[start] {
	case 0x9b:
		return replayCSIEnd(data, start+1)
	default:
		return replayStringEnd(data, start+1)
	}
}

func replayStringEnd(data []byte, start int) int {
	for index := start; index < len(data); index++ {
		if data[index] == 0x07 {
			return index + 1
		}
		if data[index] == 0x1b && index+1 < len(data) && data[index+1] == '\\' {
			return index + 2
		}
	}
	return len(data)
}
