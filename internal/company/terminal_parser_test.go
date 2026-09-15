package company

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	ansiparser "github.com/charmbracelet/x/ansi/parser"
)

func TestTerminalModeTrackerRecordsAnsiParserPreChunkState(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b["))
	snapshot := tracker.parserSnapshot()
	if snapshot.state != ansiparser.CsiEntryState || snapshot.safe {
		t.Fatalf("pre-chunk snapshot = %+v, want CSI entry and unsafe", snapshot)
	}
	tracker.feed([]byte("?1002h"))
	if got := tracker.state(1002); got != terminalModeSet {
		t.Fatalf("split mode state = %v, want set", got)
	}
}

func TestTerminalModeTrackerUsesAnsiParserForControlBoundaries(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?1002\x00h"))
	if got := tracker.state(1002); got != terminalModeUnknown {
		t.Fatalf("mode after C0 inside CSI = %v, want x/ansi no-update semantics", got)
	}
	tracker.feed([]byte("\x1bP1qpayload\x1b\\\x1b[?1006h"))
	if got := tracker.state(1006); got != terminalModeSet {
		t.Fatalf("mode after DCS passthrough = %v, want set", got)
	}
}

func TestTerminalModeTrackerUsesDecodedUTF8AndC1Sequence(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\xc2\x9c"))
	if snapshot := tracker.parserSnapshot(); snapshot.state != ansiparser.GroundState || !snapshot.safe {
		t.Fatalf("decoded C1 snapshot = %+v, want safe ground", snapshot)
	}
	tracker.feed([]byte{0x9b, '?', '2', '0', '0', '4', 'h'})
	if got := tracker.state(2004); got != terminalModeSet {
		t.Fatalf("C1 CSI mode = %v, want set", got)
	}
}

func TestTerminalModeTrackerResetsOnRISAndDECSTR(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?1049h\x1b[?1002h\x1b[!p"))
	if tracker.alternate() || tracker.state(1002) != terminalModeUnknown {
		t.Fatalf("DECSTR retained state: alternate=%v mode=%v", tracker.alternate(), tracker.state(1002))
	}
	tracker.feed([]byte("\x1b[?2004h\x1bc"))
	if got := tracker.state(2004); got != terminalModeUnknown {
		t.Fatalf("RIS retained mode = %v", got)
	}
}

func TestTerminalModeTrackerUnknownAndSubparametersDoNotChangeModes(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?1002:1;1006h\x1b[?9999h"))
	if tracker.state(1002) != terminalModeUnknown || tracker.state(1006) != terminalModeSet {
		t.Fatalf("subparameter/unknown handling = 1002:%v 1006:%v", tracker.state(1002), tracker.state(1006))
	}
}

// decodeTokens is an independent whole-stream oracle. It deliberately uses
// DecodeSequence rather than any parser or replay helper from this package.
func decodeTokens(stream []byte) [][]byte {
	var tokens [][]byte
	state := ansi.NormalState
	parser := ansi.NewParser()
	for len(stream) > 0 {
		seq, _, n, next := ansi.DecodeSequence(stream, state, parser)
		if n == 0 {
			break
		}
		tokens = append(tokens, append([]byte(nil), seq...))
		stream = stream[n:]
		state = next
	}
	return tokens
}

func TestTerminalReplayTailMatchesIndependentDecodeSuffix(t *testing.T) {
	stream := append([]byte("head€"), []byte("\x1b[?1002hbody\x1b]0;title\x07tail")...)
	tail := safeReplayTail(stream, 12)
	if len(tail) > 12 || !bytes.HasSuffix(stream, tail) {
		t.Fatalf("tail = %q, stream suffix invariant failed", tail)
	}
	if len(tail) == 0 {
		t.Fatal("oracle fixture unexpectedly produced empty tail")
	}
	all := decodeTokens(stream)
	suffix := decodeTokens(tail)
	if len(suffix) == 0 || !bytes.Equal(suffix[len(suffix)-1], all[len(all)-1]) {
		t.Fatalf("decoded tail does not end at same token: tail=%q", tail)
	}
}

type oracleToken struct {
	start int
	end   int
	data  []byte
}

func decodeOracleTokens(stream []byte) []oracleToken {
	var tokens []oracleToken
	state := ansi.NormalState
	parser := ansi.NewParser()
	for len(stream) > 0 {
		seq, _, n, next := ansi.DecodeSequence(stream, state, parser)
		if n == 0 {
			break
		}
		start := 0
		if len(tokens) > 0 {
			start = tokens[len(tokens)-1].end
		}
		tokens = append(tokens, oracleToken{start: start, end: start + n, data: append([]byte(nil), seq...)})
		stream = stream[n:]
		state = next
	}
	return tokens
}

type oracleModeFold struct {
	states map[int]bool
	alt    int
	track  int
	encode int
}

func (f *oracleModeFold) reset() {
	f.states = make(map[int]bool)
	f.alt, f.track, f.encode = 0, 0, 0
}

func (f *oracleModeFold) fold(token []byte) {
	if bytes.Equal(token, []byte("\x1bc")) || bytes.Equal(token, []byte("\x1b[!p")) {
		f.reset()
		return
	}
	if !bytes.HasPrefix(token, []byte("\x1b[?")) || len(token) < 5 {
		return
	}
	final := token[len(token)-1]
	if final != 'h' && final != 'l' {
		return
	}
	set := final == 'h'
	for _, raw := range strings.Split(string(token[3:len(token)-1]), ";") {
		mode, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		switch mode {
		case 1, 7, 25, 47, 1000, 1002, 1003, 1004, 1005, 1006, 1015, 1016, 1047, 1049, 2004:
			f.states[mode] = set
		}
		if mode == 47 || mode == 1047 || mode == 1049 {
			if set {
				f.alt = mode
			} else {
				f.alt = 0
			}
		}
		if mode == 1000 || mode == 1002 || mode == 1003 {
			if set {
				f.track = mode
			} else if f.track == mode {
				f.track = 0
			}
		}
		if mode == 1005 || mode == 1006 || mode == 1015 || mode == 1016 {
			if set {
				f.encode = mode
			} else if f.encode == mode {
				f.encode = 0
			}
		}
	}
}

func (f *oracleModeFold) prefix() []byte {
	var out []byte
	appendMode := func(mode int, set bool) {
		out = append(out, '\x1b', '[', '?')
		out = strconv.AppendInt(out, int64(mode), 10)
		if set {
			out = append(out, 'h')
		} else {
			out = append(out, 'l')
		}
	}
	if f.alt != 0 {
		appendMode(f.alt, true)
	}
	for _, mode := range []int{1, 7, 25, 1004, 2004} {
		if set, ok := f.states[mode]; ok && (set || !set && (mode == 7 || mode == 25)) {
			appendMode(mode, set)
		}
	}
	if f.track != 0 {
		appendMode(f.track, true)
	}
	if f.encode != 0 {
		appendMode(f.encode, true)
	}
	return out
}

func TestTerminalModeTrackerMatchesIndependentModeFold(t *testing.T) {
	stream := []byte("\x1b[?2004h\x1b[?1006h\x1b[?1049h\x1b[?25lbody\x1b[?1006l")
	tracker := terminalModeTracker{}
	tracker.feed(stream)
	oracle := oracleModeFold{}
	oracle.reset()
	for _, token := range decodeOracleTokens(stream) {
		oracle.fold(token.data)
	}
	if got, want := tracker.prefix(), oracle.prefix(); !bytes.Equal(got, want) {
		t.Fatalf("product prefix %q differs from independent fold %q", got, want)
	}
}

func FuzzTerminalReplayUsesDecodeSequenceOracle(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("plain text"),
		[]byte("\x1b[?1002hhello\x1b[?1006h"),
		[]byte("\x1b]long\x1b[31mrestart\x18tail"),
		[]byte("\x1b[!\x1b[?1002h\x1bc"),
		[]byte{0xe2, 0x82, 0xac, '\x1b', '[', '?', '2', '0', '0', '0', 'h'},
		[]byte("\x1bP1qdata\x1b\\\x1b[?1004h"),
		[]byte("\x1bP1qdata\x1b[?1004h\x1b\\"),
		[]byte("\x1b]0;\xc2\x80\x07\x1b[?1004h"),
		[]byte("\x1b[?1002h\x00\x1b[?1006h"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		stream := fuzzTerminalStreamForOracle(input)
		tail := safeReplayTail(stream, 96)
		if len(tail) > 96 || !bytes.HasSuffix(stream, tail) {
			t.Fatalf("tail is not bounded stream suffix: %x", tail)
		}
		if len(tail) == 0 {
			// An unterminated control string may have no safe suffix, but its
			// mode fold is still checked below.
		}
		cut := len(stream) - len(tail)
		all := decodeOracleTokens(stream)
		start := -1
		for index, token := range all {
			if token.start == cut {
				start = index
				break
			}
		}
		if start < 0 {
			t.Fatalf("tail cut %d is not an oracle token boundary", cut)
		}
		tailTokens := decodeOracleTokens(tail)
		if len(tailTokens) != len(all)-start {
			t.Fatalf("tail token count=%d, want=%d", len(tailTokens), len(all)-start)
		}
		for index := range tailTokens {
			if !bytes.Equal(tailTokens[index].data, all[start+index].data) {
				t.Fatalf("tail token %d=%x, want=%x", index, tailTokens[index].data, all[start+index].data)
			}
		}
		tracker := terminalModeTracker{}
		tracker.feed(stream)
		oracle := oracleModeFold{}
		oracle.reset()
		for _, token := range all {
			oracle.fold(token.data)
		}
		if got, want := tracker.prefix(), oracle.prefix(); !bytes.Equal(got, want) {
			t.Fatalf("mode fold mismatch: got=%q want=%q", got, want)
		}
	})
}

func fuzzTerminalStreamForOracle(input []byte) []byte {
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
			stream = append(stream, []byte("\x1b[?2004h")...)
		case 6:
			stream = append(stream, []byte("\x1b]x\x1b[31m")...)
		default:
			stream = append(stream, 'a'+byte(index%26), byte('0'+index%10))
		}
	}
	return stream
}
