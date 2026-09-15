package company

import (
	"bytes"
	"testing"
)

func TestTerminalParserCoversCanonicalStateFamilies(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want terminalVTState
	}{
		{name: "ground", data: []byte("text"), want: terminalVTGround},
		{name: "escape", data: []byte{0x1b}, want: terminalVTEscape},
		{name: "escape-intermediate", data: []byte{0x1b, '('}, want: terminalVTEscapeIntermediate},
		{name: "csi-entry", data: []byte{0x1b, '['}, want: terminalVTCsiEntry},
		{name: "csi-param", data: []byte("\x1b[?1"), want: terminalVTCsiParam},
		{name: "csi-intermediate", data: []byte{0x1b, '[', '!'}, want: terminalVTCsiIntermediate},
		{name: "csi-ignore", data: []byte{0x1b, '[', '<'}, want: terminalVTCsiIgnore},
		{name: "dcs-entry", data: []byte{0x1b, 'P'}, want: terminalVTDcsEntry},
		{name: "dcs-param", data: []byte{0x1b, 'P', '1'}, want: terminalVTDcsParam},
		{name: "dcs-intermediate", data: []byte{0x1b, 'P', ' '}, want: terminalVTDcsIntermediate},
		{name: "dcs-passthrough", data: []byte{0x1b, 'P', '1', 'q'}, want: terminalVTDcsPassthrough},
		{name: "dcs-ignore", data: []byte{0x1b, 'P', 0x01}, want: terminalVTDcsIgnore},
		{name: "osc-string", data: []byte{0x1b, ']'}, want: terminalVTOscString},
		{name: "sos-pm-apc-string", data: []byte{0x1b, '^'}, want: terminalVTSosPmApcString},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser := terminalParser{}
			parser.feed(test.data, nil)
			if parser.state != test.want {
				t.Fatalf("state = %d, want %d", parser.state, test.want)
			}
		})
	}
}

func TestTerminalParserAnywhereTransitions(t *testing.T) {
	for _, cancel := range []byte{0x18, 0x1a} {
		parser := terminalParser{}
		parser.feed(append([]byte{0x1b, 'P', '1', 'q'}, cancel), nil)
		if parser.state != terminalVTGround {
			t.Fatalf("cancel %#x left DCS state %d", cancel, parser.state)
		}
	}
	parser := terminalParser{}
	parser.feed([]byte{0x1b, ']', 'x', 0x1b, '[', '3'}, nil)
	if parser.state != terminalVTCsiParam {
		t.Fatalf("ESC restart left state %d, want CSI param", parser.state)
	}
	for _, opener := range [][]byte{{0x9b}, {0x90}, {0x9d}, {0x98}, {0x9e}, {0x9f}} {
		parser := terminalParser{}
		parser.feed(opener, nil)
		if parser.state == terminalVTGround {
			t.Fatalf("C1 opener %#x did not leave ground", opener)
		}
	}
	parser = terminalParser{}
	parser.feed([]byte{0x1b, 'P', '1', 'q', 0x9c}, nil)
	if parser.state != terminalVTGround {
		t.Fatalf("C1 ST left DCS state %d", parser.state)
	}
}

func TestTerminalParserDispatchesOnlyCompletedModeSequences(t *testing.T) {
	var events []terminalDispatch
	parser := terminalParser{}
	parser.feed([]byte("\x1b[?1002h"), func(event terminalDispatch) { events = append(events, event) })
	if len(events) != 1 || events[0].kind != terminalDispatchPrivateMode || !events[0].set || events[0].paramCount != 1 || events[0].params[0] != 1002 {
		t.Fatalf("events = %+v", events)
	}
	events = nil
	parser.feed([]byte("\x1b[!p\x1bc"), func(event terminalDispatch) { events = append(events, event) })
	if len(events) != 2 || events[0].kind != terminalDispatchDECSTR || events[1].kind != terminalDispatchRIS {
		t.Fatalf("reset events = %+v", events)
	}
}

func TestTerminalParserTracksUTF8CompletenessAtGround(t *testing.T) {
	parser := terminalParser{}
	parser.feed([]byte("a€"), nil)
	if !parser.safeBoundary() {
		t.Fatal("complete UTF-8 text was not left at a safe boundary")
	}
	parser = terminalParser{}
	parser.feed([]byte{0xe2}, nil)
	if parser.safeBoundary() || parser.utf8Remaining != 2 {
		t.Fatalf("incomplete UTF-8 state = %+v", parser.terminalParserSnapshot)
	}
}

func FuzzTerminalReplay(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("plain text"),
		[]byte("\x1b[?1002hhello\x1b[?1006h"),
		[]byte("\x1b]0;title\x07\x1bP1;2qdata\x1b\\"),
		[]byte("\x1b]long\x1b[31mrestart\x18tail"),
		[]byte{0xe2, 0x82, 0xac, 0x1b, '[', '?', '2', '0', '0', '4', 'h'},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 256 {
			input = input[:256]
		}
		stream := fuzzTerminalStream(input)
		const limit = 96
		actual := terminalModeTracker{}
		reference := terminalModeTracker{}
		var chunks []terminalReplayChunk
		total := 0
		var seen []byte
		position := 0
		for position < len(stream) {
			width := 1
			if len(input) > 0 {
				width += int(input[position%len(input)] % 17)
			}
			if width > len(stream)-position {
				width = len(stream) - position
			}
			chunk := append([]byte(nil), stream[position:position+width]...)
			chunks = append(chunks, terminalReplayChunk{data: chunk, before: actual.parser.snapshot()})
			total += len(chunk)
			seen = append(seen, chunk...)
			actual.feed(chunk)
			reference.feed(chunk)
			chunks, total = trimTerminalReplay(chunks, total, limit)
			tail := terminalReplayTail(chunks, total, limit)
			if !bytes.HasSuffix(seen, tail) {
				t.Fatalf("tail is not a stream suffix: %x", tail)
			}
			cut := len(seen) - len(tail)
			referenceBoundary := referenceReplayParser{}
			referenceBoundary.feed(seen[:cut])
			if len(tail) > 0 && !referenceBoundary.safeBoundary() && (cut == len(seen) || seen[cut] != 0x1b) {
				t.Fatalf("tail starts at unsafe offset %d reference=%d/%d around=%x tail=%x", cut, referenceBoundary.state, referenceBoundary.utf8Remaining, seen[max(0, cut-12):min(len(seen), cut+12)], tail)
			}
			if got, want := string(actual.prefix()), string(reference.prefix()); got != want {
				t.Fatalf("mode state diverged: got %q want %q", got, want)
			}
			position += width
		}
	})
}

type referenceReplayState uint8

const (
	referenceGround referenceReplayState = iota
	referenceEscape
	referenceCSI
	referenceString
	referenceDCS
	referenceDCSPassthrough
	referenceDCSIgnore
)

// referenceReplayParser is intentionally independent from terminalParser. It
// models only the boundary contract used by the fuzz property, so a replay
// cut is checked by a fresh stream-from-start run rather than by replay code.
type referenceReplayParser struct {
	state         referenceReplayState
	utf8Remaining int
}

func (p *referenceReplayParser) feed(data []byte) {
	for _, b := range data {
		p.feedByte(b)
	}
}

func (p referenceReplayParser) safeBoundary() bool {
	return p.state == referenceGround && p.utf8Remaining == 0
}

func (p *referenceReplayParser) feedByte(b byte) {
	if p.state == referenceGround && p.utf8Remaining > 0 {
		if b >= 0x80 && b <= 0xbf {
			p.utf8Remaining--
			return
		}
		p.utf8Remaining = 0
	}
	if b == 0x1b {
		p.state = referenceEscape
		return
	}
	if b == 0x18 || b == 0x1a {
		p.state = referenceGround
		p.utf8Remaining = 0
		return
	}
	if b == 0x9c && (p.state == referenceString || p.state == referenceDCS || p.state == referenceDCSPassthrough || p.state == referenceDCSIgnore) {
		p.state = referenceGround
		return
	}
	if p.state == referenceGround {
		switch b {
		case 0x9b:
			p.state = referenceCSI
		case 0x90:
			p.state = referenceDCS
		case 0x9d, 0x98, 0x9e, 0x9f:
			p.state = referenceString
		default:
			if width := terminalUTF8Width(b); width > 1 {
				p.utf8Remaining = width - 1
			}
		}
		return
	}
	switch p.state {
	case referenceEscape:
		switch b {
		case '[':
			p.state = referenceCSI
		case 'P':
			p.state = referenceDCS
		case ']', 'X', '^', '_':
			p.state = referenceString
		default:
			p.state = referenceGround
		}
	case referenceCSI:
		if b >= 0x40 && b <= 0x7e {
			p.state = referenceGround
		}
	case referenceDCS:
		switch {
		case b >= 0x30 && b <= 0x3f, b >= 0x20 && b <= 0x2f, b >= 0x40 && b <= 0x7e:
			if b >= 0x40 && b <= 0x7e {
				p.state = referenceDCSPassthrough
			}
		default:
			p.state = referenceDCSIgnore
		}
	case referenceDCSPassthrough:
		// DCS data remains in passthrough until ST; the anywhere ESC/CAN/SUB
		// transitions and C1 ST check above still apply.
	case referenceDCSIgnore:
		if b >= 0x40 && b <= 0x7e {
			p.state = referenceGround
		}
	case referenceString:
		if b == 0x07 || b == 0x9c {
			p.state = referenceGround
		}
	}
}

func fuzzTerminalStream(input []byte) []byte {
	if len(input) == 0 {
		return []byte("plain")
	}
	var stream []byte
	for index, b := range input {
		switch b % 8 {
		case 0:
			stream = append(stream, []byte("text€")...)
		case 1:
			stream = append(stream, []byte("\x1b[?1002h")...)
		case 2:
			stream = append(stream, []byte("\x1b[?1006h")...)
		case 3:
			stream = append(stream, []byte("\x1b]0;title\x07")...)
		case 4:
			stream = append(stream, []byte("\x1bP1qdata\x1b\\")...)
		case 5:
			stream = append(stream, []byte{'\x1b', '[', '?', '2', '0', '0', '4', 'h'}...)
		case 6:
			stream = append(stream, []byte{'\x1b', ']', 'x', '\x1b', '[', '3', '1', 'm'}...)
		default:
			literal := b
			if literal < 0x20 || literal >= 0x80 {
				literal = 'a' + byte(index%26)
			}
			stream = append(stream, literal, byte(index))
		}
	}
	return stream
}
