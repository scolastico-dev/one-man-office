package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Programs that link bubbletea/termenv (including omo's own CLI verbs) probe
// the terminal for its background colour and cursor position at startup and
// block on a 5s timeout when nothing answers. omo owns these PTYs, so it
// must answer like a real terminal.
func TestSessionAnswersTerminalQueries(t *testing.T) {
	log := filepath.Join(t.TempDir(), "q.log")
	// Everything the program writes, as omo receives it: it tells whether
	// the query reached omo at all.
	var mu sync.Mutex
	var raw []byte
	// Emit an OSC 11 background query, then consume one byte of the reply.
	// Raw mode as termenv itself uses: an OSC reply carries no newline, so a
	// canonical-mode read would never see it. The START line before all of
	// it shows in a failure that the script ran.
	s, err := Start(Options{
		Cmd:     "sh",
		Args:    []string{"-c", `echo START; stty raw; printf '\033]11;?\007'; head -c 1 >/dev/null; stty sane; echo GOT:reply`},
		LogPath: log, Rows: 24, Cols: 80,
		OnOutput: func(b []byte) {
			mu.Lock()
			raw = append(raw, b...)
			mu.Unlock()
		},
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
	mu.Lock()
	got := string(raw)
	mu.Unlock()
	// A Windows pseudoconsole reads the program's output itself and passes
	// on what it decides to: the console that Windows ships keeps this
	// query, so omo never sees it and cannot answer. That is skipped here
	// only when it is what happened: the script ran and no query arrived.
	// A query that did arrive and stayed unanswered fails on every platform.
	if runtime.GOOS == "windows" && strings.Contains(got, "START") && !strings.Contains(got, "\x1b]11;?") {
		t.Skipf("the Windows pseudoconsole did not pass the OSC 11 query on to omo; it received %q", got)
	}
	written, rerr := os.ReadFile(log)
	t.Fatalf("no reply to OSC 11 query; screen:\n%s\nreceived: %q\nlog (read error %v): %q", strings.TrimSpace(s.Screen()), got, rerr, written)
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
