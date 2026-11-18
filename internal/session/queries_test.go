package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Programs that link bubbletea/termenv (including omo's own CLI verbs) probe
// the terminal for its background colour and cursor position at startup and
// block on a 5s timeout when nothing answers. omo owns these PTYs, so it
// must answer like a real terminal.
func TestSessionAnswersTerminalQueries(t *testing.T) {
	log := filepath.Join(t.TempDir(), "q.log")
	// Emit an OSC 11 background query, then consume one byte of the reply.
	// Raw mode as termenv itself uses: an OSC reply carries no newline, so a
	// canonical-mode read would never see it. The START line before all of
	// it is only for the failure message: with it on the screen or in the
	// log the script ran and the query or its reply got lost; without it the
	// program wrote nothing at all.
	s, err := Start(Options{
		Cmd:     "sh",
		Args:    []string{"-c", `echo START; stty raw; printf '\033]11;?\007'; head -c 1 >/dev/null; stty sane; echo GOT:reply`},
		LogPath: log, Rows: 24, Cols: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	endSession(t, s)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.Screen(), "GOT:") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The log holds what the terminal's program wrote: it shows whether the
	// script ran (START) and whether the query reached omo at all (a Windows
	// pseudoconsole may keep it).
	written, rerr := os.ReadFile(log)
	t.Fatalf("no reply to OSC 11 query; screen:\n%s\nlog (read error %v): %q", strings.TrimSpace(s.Screen()), rerr, written)
}

func TestAnswerQueriesCarriesSplitSequences(t *testing.T) {
	var got []byte
	s := &Session{writeInput: func(b []byte) { got = append(got, b...) }}

	// A device-status query split across two reads must still be answered
	// exactly once, and only after it is complete.
	carry := s.answerQueries([]byte("hello\x1b["))
	if len(got) != 0 {
		t.Fatalf("answered an incomplete query: %q", got)
	}
	if string(carry) != "\x1b[" {
		t.Fatalf("carry = %q, want the partial escape", carry)
	}
	carry = s.answerQueries(append(carry, []byte("6n")...))
	if string(got) != "\x1b[1;1R" {
		t.Fatalf("DSR reply = %q", got)
	}
	if len(carry) != 0 {
		t.Fatalf("carry after complete query = %q", carry)
	}
}

func TestAnswerQueriesBackgroundAndForeground(t *testing.T) {
	var got []byte
	s := &Session{writeInput: func(b []byte) { got = append(got, b...) }}
	s.answerQueries([]byte("\x1b]11;?\x07\x1b]10;?\x1b\\"))
	reply := string(got)
	if !strings.Contains(reply, "\x1b]11;rgb:") {
		t.Errorf("missing background reply in %q", reply)
	}
	if !strings.Contains(reply, "\x1b]10;rgb:") {
		t.Errorf("missing foreground reply in %q", reply)
	}
}

func TestAnswerQueriesDropsUnboundedCarry(t *testing.T) {
	s := &Session{writeInput: func([]byte) {}}
	// Plain output must never accumulate: only a trailing partial escape is
	// worth keeping.
	carry := s.answerQueries([]byte(strings.Repeat("ordinary output\n", 100)))
	if len(carry) != 0 {
		t.Fatalf("carry = %d bytes, want 0", len(carry))
	}
}
