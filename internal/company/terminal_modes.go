package company

type terminalModeState uint8

const (
	terminalModeUnknown terminalModeState = iota
	terminalModeSet
	terminalModeReset
)

var terminalModeOrder = [...]int{1, 7, 25, 47, 1000, 1002, 1003, 1004, 1005, 1006, 1015, 1016, 1047, 1049, 2004}

type terminalModeTracker struct {
	states             [len(terminalModeOrder)]terminalModeState
	parser             terminalParser
	altState           terminalModeState
	altMode            int
	mouseTrackingState terminalModeState
	mouseTrackingMode  int
	mouseEncodingState terminalModeState
	mouseEncodingMode  int
}

func (t *terminalModeTracker) feed(data []byte) {
	t.parser.feed(data, t.dispatch)
}

func (t *terminalModeTracker) dispatch(event terminalDispatch) {
	switch event.kind {
	case terminalDispatchPrivateMode:
		state := terminalModeReset
		if event.set {
			state = terminalModeSet
		}
		for index := 0; index < event.paramCount; index++ {
			t.commitMode(event.params[index], state)
		}
	case terminalDispatchRIS, terminalDispatchDECSTR:
		t.clearModes()
	}
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

func (t *terminalModeTracker) clearModes() {
	t.states = [len(terminalModeOrder)]terminalModeState{}
	t.altState = terminalModeUnknown
	t.altMode = 0
	t.mouseTrackingState = terminalModeUnknown
	t.mouseTrackingMode = 0
	t.mouseEncodingState = terminalModeUnknown
	t.mouseEncodingMode = 0
}

func (t *terminalModeTracker) resetAll() {
	t.clearModes()
	t.parser = terminalParser{}
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
