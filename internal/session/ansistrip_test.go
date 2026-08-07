package session

import (
	"bytes"
	"strings"
	"testing"
)

func stripAll(t *testing.T, chunks ...string) string {
	t.Helper()
	var buf bytes.Buffer
	w := newStripWriter(&buf)
	for _, c := range chunks {
		if _, err := w.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	return buf.String()
}

func TestStripRemovesColorsAndCursorMoves(t *testing.T) {
	// A line lifted from a real Claude Code session log.
	in := "\x1b[1B\x1b[38;2;215;119;87m╭───\x1b[6GClaude Code\x1b[18G\x1b[38;2;153;153;153mv2.1.223\x1b[39m\n"
	got := stripAll(t, in)
	want := "╭───Claude Codev2.1.223\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStripHandlesOSCAndPrivateModes(t *testing.T) {
	in := "\x1b]0;✳ Claude Code\x07\x1b[?25l\x1b[?2004hhello\x1b[?1049h\x1b\\world\n"
	got := stripAll(t, in)
	if got != "helloworld\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStripWorksAcrossChunkBoundaries(t *testing.T) {
	// The escape is split across three writes, as a PTY read easily would.
	got := stripAll(t, "abc\x1b[3", "8;2;1;2;3m", "def\n")
	if got != "abcdef\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStripKeepsNewlinesAndTabsDropsCarriageReturns(t *testing.T) {
	got := stripAll(t, "a\tb\r\nc\rd\n")
	if got != "a\tb\ncd\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStripLeavesPlainTextUntouched(t *testing.T) {
	in := "omo: step \"ready\": dial /tmp/x.sock\nplain output\n"
	if got := stripAll(t, in); got != in {
		t.Fatalf("got %q, want %q", got, in)
	}
}

func TestStripDoesNotBufferUnboundedOnLoneEscape(t *testing.T) {
	// A stray ESC must not swallow the rest of the stream forever.
	got := stripAll(t, "\x1b"+strings.Repeat("x", 200)+"\n")
	if !strings.Contains(got, strings.Repeat("x", 100)) {
		t.Fatalf("stray ESC swallowed output: %q", got)
	}
}
