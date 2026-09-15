package company

import (
	"bytes"
	"testing"
)

func TestTerminalModeTrackerParsesRequiredModes(t *testing.T) {
	const required = "1;7;25;47;1000;1002;1003;1004;1005;1006;1015;1016;1047;1049;2004"
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?" + required + "h"))

	for _, mode := range []int{1, 7, 25, 47, 1000, 1002, 1003, 1004, 1005, 1006, 1015, 1016, 1047, 1049, 2004} {
		if got := tracker.state(mode); got != terminalModeSet {
			t.Errorf("mode %d state = %v, want set", mode, got)
		}
	}
	if !tracker.alternate() {
		t.Fatal("alternate screen was not tracked")
	}
}

func TestTerminalModeTrackerParsesResetForEveryRequiredMode(t *testing.T) {
	for _, mode := range []int{1, 7, 25, 47, 1000, 1002, 1003, 1004, 1005, 1006, 1015, 1016, 1047, 1049, 2004} {
		tracker := terminalModeTracker{}
		tracker.feed([]byte("\x1b[?" + string(appendInt(nil, mode)) + "h\x1b[?" + string(appendInt(nil, mode)) + "l"))
		if got := tracker.state(mode); got != terminalModeReset {
			t.Errorf("mode %d reset state = %v, want reset", mode, got)
		}
	}
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?47h\x1b[?1047h\x1b[?1049h\x1b[?1049l"))
	if tracker.alternate() {
		t.Fatal("latest alternate reset was not applied")
	}
}

func TestTerminalModeTrackerSplitAtEveryByte(t *testing.T) {
	sequence := []byte("\x1b[?1002;1006;1049;2004h")
	want := terminalModeTracker{}
	want.feed(sequence)
	for split := 0; split <= len(sequence); split++ {
		tracker := terminalModeTracker{}
		tracker.feed(sequence[:split])
		tracker.feed(sequence[split:])
		if got, expected := string(tracker.prefix()), string(want.prefix()); got != expected {
			t.Fatalf("split %d prefix = %q, want %q", split, got, expected)
		}
	}
}

func TestTerminalModeTrackerHandlesResetAndUnknownModes(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?1002h\x1b[?1006h\x1b[?1049h\x1b[?2004h"))
	before := append([]byte(nil), tracker.prefix()...)
	tracker.feed([]byte("\x1b[?9999h\x1b[?9999l"))
	if got := tracker.prefix(); !bytes.Equal(got, before) {
		t.Fatalf("unknown modes changed prefix: got %q, want %q", got, before)
	}
	tracker.feed([]byte("\x1b[!p"))
	if got := tracker.prefix(); len(got) != 0 || tracker.alternate() {
		t.Fatalf("DECSTR did not reset tracker: prefix %q alternate=%v", got, tracker.alternate())
	}
	tracker.feed([]byte("\x1b[?1002h\x1bc\x1b[?2004h"))
	if got := tracker.state(1002); got != terminalModeUnknown {
		t.Fatalf("RIS did not reset mode 1002: %v", got)
	}
	if got := tracker.state(2004); got != terminalModeSet {
		t.Fatalf("mode after RIS = %v, want set", got)
	}
}

func TestTerminalModeTrackerTracksExplicitDefaultResets(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?25l\x1b[?7l"))
	if got, want := string(tracker.prefix()), "\x1b[?7l\x1b[?25l"; got != want {
		t.Fatalf("default-difference prefix = %q, want %q", got, want)
	}
	tracker.feed([]byte("\x1b[?25h\x1b[?7h"))
	if got, want := string(tracker.prefix()), "\x1b[?7h\x1b[?25h"; got != want {
		t.Fatalf("restored defaults prefix = %q, want %q", got, want)
	}
}

func TestTerminalModeTrackerUsesLatestMouseSelectors(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?1003h\x1b[?1000h\x1b[?1016h\x1b[?1006h"))
	if got, want := string(tracker.prefix()), "\x1b[?1000h\x1b[?1006h"; got != want {
		t.Fatalf("latest selector prefix = %q, want %q", got, want)
	}
	tracker.feed([]byte("\x1b[?1002l\x1b[?1016l"))
	if got, want := string(tracker.prefix()), "\x1b[?1000h\x1b[?1006h"; got != want {
		t.Fatalf("non-active selector resets changed active modes: %q", got)
	}
	tracker.feed([]byte("\x1b[?1000l\x1b[?1006l"))
	if got := tracker.prefix(); len(got) != 0 {
		t.Fatalf("active selector resets retained modes: %q", got)
	}
}

func TestTerminalModeTrackerIgnoresMalformedSequences(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?1002;hello"))
	if got := tracker.state(1002); got != terminalModeUnknown {
		t.Fatalf("malformed sequence changed mode: %v", got)
	}
	tracker.feed([]byte("\x9b?1006h"))
	if got := tracker.state(1006); got != terminalModeSet {
		t.Fatalf("C1 CSI mode = %v, want set", got)
	}
}

func TestReconnectPrefixPlacesAlternateScreenFirst(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?2004h\x1b[?1006h\x1b[?1049h\x1b[?25l"))
	got := tracker.prefix()
	if want := []byte("\x1b[?1049h\x1b[?25l\x1b[?2004h\x1b[?1006h"); !bytes.Equal(got, want) {
		t.Fatalf("prefix = %q, want %q", got, want)
	}
}

func TestReconnectPrefixPlacesMouseModesAfterIndependentModes(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?1006h\x1b[?1002h\x1b[?2004h\x1b[?1004h"))
	if got, want := string(tracker.prefix()), "\x1b[?1004h\x1b[?2004h\x1b[?1002h\x1b[?1006h"; got != want {
		t.Fatalf("semantic mode ordering = %q, want %q", got, want)
	}
}

func TestAlternateModeUsesLatestAliasTransition(t *testing.T) {
	tracker := terminalModeTracker{}
	tracker.feed([]byte("\x1b[?47h\x1b[?1049l"))
	if tracker.alternate() {
		t.Fatal("older alternate alias remained active")
	}
	tracker.feed([]byte("\x1b[?1047h"))
	if got, want := string(tracker.prefix()), "\x1b[?1047h"; got != want {
		t.Fatalf("latest alternate alias prefix = %q, want %q", got, want)
	}
}

func TestSafeReplayTailAvoidsEscapeAndUTF8Boundaries(t *testing.T) {
	data := append(bytes.Repeat([]byte("x"), 8), []byte("\x1b[?1049h€tail")...)
	got := safeReplayTail(data, 10)
	if len(got) > 10 {
		t.Fatalf("safe replay length = %d, want <= 10", len(got))
	}
	if bytes.HasPrefix(got, []byte("\x1b")) || bytes.HasPrefix(got, []byte("[?")) || (len(got) > 0 && got[0]&0xc0 == 0x80) {
		t.Fatalf("safe replay starts inside a sequence or UTF-8 code point: %q", got)
	}
	if !bytes.HasSuffix(got, []byte("tail")) {
		t.Fatalf("safe replay lost complete tail: %q", got)
	}
}

func TestSafeReplayTailPreservesEveryUTF8AndEscapeBoundary(t *testing.T) {
	tests := []struct {
		name  string
		data  []byte
		start int
	}{
		{name: "two-byte", data: []byte("head-¢-tail"), start: len("head-")},
		{name: "three-byte", data: []byte("head-€-tail"), start: len("head-")},
		{name: "four-byte", data: []byte("head-🎉-tail"), start: len("head-")},
		{name: "escape-intermediate", data: []byte("head-\x1b(B-tail"), start: len("head-")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sequenceEnd := test.start + len(test.data[test.start:]) - len("-tail")
			for cut := 1; cut < len(test.data); cut++ {
				wantCut := cut
				if cut > test.start && cut < sequenceEnd {
					wantCut = sequenceEnd
				}
				got := safeReplayTail(test.data, len(test.data)-cut)
				if want := test.data[wantCut:]; !bytes.Equal(got, want) {
					t.Fatalf("cut %d returned %q, want %q", cut, got, want)
				}
			}
		})
	}
}

func TestSafeReplayTailPreservesRestartedEscapeInInitialOverflow(t *testing.T) {
	data := append([]byte{'\x1b'}, bytes.Repeat([]byte{'('}, replayLimit+8)...)
	data = append(data, []byte("\x1b[31mTAIL")...)
	if got, want := string(safeReplayTail(data, replayLimit)), "\x1b[31mTAIL"; got != want {
		t.Fatalf("initial overflow replay = %q, want %q", got, want)
	}
}

func TestSafeReplayTailPreservesRestartedEscapeInStringOverflow(t *testing.T) {
	for _, opener := range [][]byte{{'\x1b', ']'}, {'\x1b', 'P'}, {'\x1b', '^'}, {'\x1b', '_'}} {
		data := append(append([]byte(nil), opener...), bytes.Repeat([]byte{'x'}, replayLimit+8)...)
		data = append(data, []byte("\x1b[31mTAIL")...)
		if got, want := string(safeReplayTail(data, replayLimit)), "\x1b[31mTAIL"; got != want {
			t.Fatalf("%q string overflow replay = %q, want %q", opener, got, want)
		}
	}
}

func TestSafeReplayTailHonorsStringCancellationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		opener []byte
		cancel byte
	}{
		{name: "esc-osc-can", opener: []byte{'\x1b', ']'}, cancel: 0x18},
		{name: "c1-osc-sub", opener: []byte{0x9d}, cancel: 0x1a},
		{name: "esc-dcs-can", opener: []byte{'\x1b', 'P'}, cancel: 0x18},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := append(append([]byte(nil), test.opener...), bytes.Repeat([]byte{'x'}, replayLimit+8)...)
			data = append(data, test.cancel)
			data = append(data, []byte("\x1b[31mTAIL")...)
			if got, want := string(safeReplayTail(data, replayLimit)), "\x1b[31mTAIL"; got != want {
				t.Fatalf("cancelled string replay = %q, want %q", got, want)
			}
		})
	}
}

func TestSafeReplayTailHonorsCSICancellationBoundaries(t *testing.T) {
	for _, cancel := range []byte{0x18, 0x1a} {
		data := append([]byte{'\x1b', '['}, bytes.Repeat([]byte{'0'}, replayLimit+8)...)
		data = append(data, cancel)
		data = append(data, []byte("\x1b[31mTAIL")...)
		if got, want := string(safeReplayTail(data, replayLimit)), "\x1b[31mTAIL"; got != want {
			t.Fatalf("cancel byte %#x replay = %q, want %q", cancel, got, want)
		}
	}
}
