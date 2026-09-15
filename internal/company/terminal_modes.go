package company

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
	states             [len(terminalModeOrder)]terminalModeState
	parser             terminalModeParser
	altState           terminalModeState
	altMode            int
	mouseTrackingState terminalModeState
	mouseTrackingMode  int
	mouseEncodingState terminalModeState
	mouseEncodingMode  int
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
		} else if b == 0x1b {
			t.parser.state = terminalParserEscape
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
	if isMouseTrackingMode(mode) {
		if state == terminalModeSet {
			t.mouseTrackingState = terminalModeSet
			t.mouseTrackingMode = mode
		} else if mode == t.mouseTrackingMode {
			t.mouseTrackingState = terminalModeReset
			t.mouseTrackingMode = 0
		}
	}
	if isMouseEncodingMode(mode) {
		if state == terminalModeSet {
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

func (t *terminalModeTracker) resetAll() {
	t.states = [len(terminalModeOrder)]terminalModeState{}
	t.altState = terminalModeUnknown
	t.altMode = 0
	t.mouseTrackingState = terminalModeUnknown
	t.mouseTrackingMode = 0
	t.mouseEncodingState = terminalModeUnknown
	t.mouseEncodingMode = 0
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
		if isMouseTrackingMode(mode) || isMouseEncodingMode(mode) {
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

type replaySkipKind uint8

const (
	replaySkipNone replaySkipKind = iota
	replaySkipUTF8
	replaySkipEscape
	replaySkipEscapeIntermediate
	replaySkipCSI
	replaySkipString
)

type replaySkipState struct {
	kind          replaySkipKind
	remainingUTF8 int
	stringEscape  bool
	replayPrefix  []byte
}

func (s replaySkipState) active() bool {
	return s.kind != replaySkipNone
}

func (s *replaySkipState) consume(data []byte) int {
	index := 0
	for index < len(data) && s.active() {
		b := data[index]
		switch s.kind {
		case replaySkipUTF8:
			if b&0xc0 != 0x80 {
				s.kind = replaySkipNone
				continue
			}
			index++
			s.remainingUTF8--
			if s.remainingUTF8 == 0 {
				s.kind = replaySkipNone
			}
		case replaySkipEscape:
			if b == 0x1b {
				s.kind = replaySkipNone
				return index
			}
			index++
			switch b {
			case '[':
				s.kind = replaySkipCSI
			case ']', 'P', '^', '_':
				s.kind = replaySkipString
			case 0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f:
				s.kind = replaySkipEscapeIntermediate
			default:
				s.kind = replaySkipNone
			}
		case replaySkipEscapeIntermediate:
			if b == 0x1b {
				s.kind = replaySkipNone
				return index
			}
			index++
			if b >= 0x30 && b <= 0x7e {
				s.kind = replaySkipNone
			} else if b < 0x20 || b > 0x2f {
				s.kind = replaySkipNone
			}
		case replaySkipCSI:
			if b == 0x1b {
				s.kind = replaySkipNone
				return index
			}
			index++
			if (b >= 0x40 && b <= 0x7e) || b == 0x18 || b == 0x1a {
				s.kind = replaySkipNone
			}
		case replaySkipString:
			if s.stringEscape {
				if b == '\\' {
					index++
					s.stringEscape = false
					s.kind = replaySkipNone
					continue
				}
				s.stringEscape = false
				s.kind = replaySkipNone
				s.replayPrefix = []byte{'\x1b'}
				return index
			}
			if b == 0x1b {
				if index+1 < len(data) && data[index+1] == '\\' {
					index += 2
					s.kind = replaySkipNone
					continue
				}
				index++
				if index == len(data) {
					s.stringEscape = true
					continue
				}
				s.kind = replaySkipNone
				s.replayPrefix = []byte{'\x1b'}
				return index
			}
			index++
			if b == 0x07 || b == 0x18 || b == 0x1a {
				s.kind = replaySkipNone
			}
		}
	}
	return index
}

func safeReplayTail(data []byte, limit int) []byte {
	tail, _ := safeReplayTailState(data, limit)
	return tail
}

func safeReplayTailState(data []byte, limit int) ([]byte, replaySkipState) {
	if limit <= 0 || len(data) == 0 {
		return nil, replaySkipState{}
	}
	if len(data) <= limit {
		return appendSafeReplayTail(data, 0), replaySkipState{}
	}
	target := len(data) - limit
	for index := 0; index < len(data); {
		end, complete, skip := replayUnit(data, index)
		if !complete {
			if index < target {
				return nil, skip
			}
			return appendSafeReplayTail(data, index), replaySkipState{}
		}
		if end >= target {
			return appendSafeReplayTail(data, end), replaySkipState{}
		}
		index = end
	}
	return nil, replaySkipState{}
}

func appendSafeReplayTail(data []byte, start int) []byte {
	for start < len(data) && data[start]&0xc0 == 0x80 {
		start++
	}
	return append([]byte(nil), data[start:]...)
}

func replayUnit(data []byte, start int) (end int, complete bool, skip replaySkipState) {
	if data[start] == 0x1b {
		return replayEscapeUnit(data, start)
	}
	if data[start] == 0x9b {
		return replayCSIUnit(data, start+1)
	}
	if data[start] == 0x9d || data[start] == 0x90 || data[start] == 0x98 || data[start] == 0x9e || data[start] == 0x9f {
		return replayStringUnit(data, start+1)
	}
	if data[start]&0xc0 == 0x80 {
		end = start + 1
		for end < len(data) && data[end]&0xc0 == 0x80 {
			end++
		}
		return end, true, replaySkipState{}
	}
	width := utf8Width(data[start])
	if width <= 1 {
		return start + 1, true, replaySkipState{}
	}
	for offset := 1; offset < width && start+offset < len(data); offset++ {
		if data[start+offset]&0xc0 != 0x80 {
			return start + offset, true, replaySkipState{}
		}
	}
	if start+width > len(data) {
		return len(data), false, replaySkipState{kind: replaySkipUTF8, remainingUTF8: width - (len(data) - start)}
	}
	return start + width, true, replaySkipState{}
}

func utf8Width(lead byte) int {
	switch {
	case lead < 0x80:
		return 1
	case lead >= 0xc2 && lead <= 0xdf:
		return 2
	case lead >= 0xe0 && lead <= 0xef:
		return 3
	case lead >= 0xf0 && lead <= 0xf4:
		return 4
	default:
		return 1
	}
}

func replayEscapeUnit(data []byte, start int) (int, bool, replaySkipState) {
	if start+1 >= len(data) {
		return len(data), false, replaySkipState{kind: replaySkipEscape}
	}
	switch data[start+1] {
	case '[', ']':
		if data[start+1] == ']' {
			return replayStringUnit(data, start+2)
		}
		return replayCSIUnit(data, start+2)
	case 'P', '^', '_':
		return replayStringUnit(data, start+2)
	}
	index := start + 1
	if data[index] >= 0x20 && data[index] <= 0x2f {
		for index < len(data) && data[index] >= 0x20 && data[index] <= 0x2f {
			index++
		}
		if index == len(data) {
			return index, false, replaySkipState{kind: replaySkipEscapeIntermediate}
		}
		if data[index] == 0x1b {
			return index, true, replaySkipState{}
		}
		if data[index] >= 0x30 && data[index] <= 0x7e {
			return index + 1, true, replaySkipState{}
		}
		return index + 1, true, replaySkipState{}
	}
	return start + 2, true, replaySkipState{}
}

func replayCSIUnit(data []byte, start int) (int, bool, replaySkipState) {
	for index := start; index < len(data); index++ {
		if data[index] == 0x1b {
			return index, true, replaySkipState{}
		}
		if data[index] == 0x18 || data[index] == 0x1a {
			return index + 1, true, replaySkipState{}
		}
		if data[index] >= 0x40 && data[index] <= 0x7e {
			return index + 1, true, replaySkipState{}
		}
	}
	return len(data), false, replaySkipState{kind: replaySkipCSI}
}

func replayStringUnit(data []byte, start int) (int, bool, replaySkipState) {
	for index := start; index < len(data); index++ {
		if data[index] == 0x07 {
			return index + 1, true, replaySkipState{}
		}
		if data[index] == 0x18 || data[index] == 0x1a {
			return index + 1, true, replaySkipState{}
		}
		if data[index] == 0x1b {
			if index+1 < len(data) && data[index+1] == '\\' {
				return index + 2, true, replaySkipState{}
			}
			return index, true, replaySkipState{}
		}
	}
	return len(data), false, replaySkipState{kind: replaySkipString, stringEscape: len(data) > start && data[len(data)-1] == 0x1b}
}
