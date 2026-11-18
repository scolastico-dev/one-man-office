package agents

import (
	"testing"
	"time"
)

// A message from the draft box is one write under the session's input lock,
// so a notification line cannot land inside it, and it counts as typing: it
// holds back notifications like keys do.
func TestAdapterSendsAMessageAsOneUnitAndCountsItAsTyping(t *testing.T) {
	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	term := newFakeTerminal()
	input := &inputClock{}
	adapter := &terminalAdapter{sess: term, out: newOutputPipe(), input: input, now: func() time.Time { return clock }, kill: func() error { return nil }}

	if err := adapter.SendMessage("\x1b[200~one\ntwo\x1b[201~", "\r"); err != nil {
		t.Fatal(err)
	}
	typed, _ := term.snapshot()
	if len(typed) != 1 || typed[0] != "\x1b[200~one\ntwo\x1b[201~\r" {
		t.Fatalf("typed = %q, want the text and its Enter as one unit", typed)
	}
	if !input.lastInput().Equal(clock) {
		t.Fatalf("a message did not count as typing: %v", input.lastInput())
	}
}
