package company

import (
	"bytes"
	"testing"
)

func TestTerminalReplayStartsWithSanitizedModes(t *testing.T) {
	initial := []byte("tail beginning after the retained output")
	got := terminalReplay(initial)
	wantPrefix := []byte("\x1b[0m\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1004l\x1b[?2004l")
	if !bytes.HasPrefix(got, wantPrefix) {
		t.Fatalf("replay prefix = %q, want %q", got[:min(len(got), len(wantPrefix))], wantPrefix)
	}
	if !bytes.Equal(got[len(wantPrefix):], initial) {
		t.Fatalf("replay payload changed: %q", got[len(wantPrefix):])
	}
}
