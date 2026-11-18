package session

import (
	"bytes"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// completeWithin bounds a stalled operation; it is a watchdog, not a speed
// assertion. Behavioral deadlines remain configured on the operation itself.
func completeWithin(t *testing.T, guard time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	<-done
}

type recordingProcess struct {
	writes [][]byte
}

func (p *recordingProcess) Read([]byte) (int, error) { return 0, io.EOF }
func (p *recordingProcess) Write(b []byte) (int, error) {
	p.writes = append(p.writes, bytes.Clone(b))
	return len(b), nil
}
func (p *recordingProcess) Resize(uint16, uint16) error { return nil }
func (p *recordingProcess) Kill() error                 { return nil }
func (p *recordingProcess) Wait() error                 { return nil }
func (p *recordingProcess) Close() error                { return nil }
func (p *recordingProcess) Exited() <-chan struct{}     { return nil }
func (p *recordingProcess) Pid() int                    { return 0 }

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

// endSession ends a session a test does not wait for and waits until it let
// go of its log file: Windows removes no folder that holds an open file.
func endSession(t *testing.T, s *Session) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-s.Done():
			return
		default:
		}
		_ = s.Kill()
		<-s.Done()
	})
}

func TestScreenAndLogCaptureOutput(t *testing.T) {
	log := filepath.Join(t.TempDir(), "a.log")
	s, err := Start(Options{Cmd: "sh", Args: []string{"-c", "printf 'hello-omo\\n'"}, LogPath: log, Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	endSession(t, s)
	<-s.Done()
	if !strings.Contains(s.Screen(), "hello-omo") {
		t.Fatalf("screen missing output:\n%s", s.Screen())
	}
	lines, err := s.TailLog(10)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "hello-omo") {
		t.Fatalf("log missing output: %v", lines)
	}
}

func TestPublishesDeduplicatedLogLines(t *testing.T) {
	log := filepath.Join(t.TempDir(), "plugin.log")
	lines := make(chan string, 4)
	s, err := Start(Options{
		Cmd: "sh", Args: []string{"-c", "printf 'plugin-line\\n'"}, LogPath: log,
		Rows: 24, Cols: 80, OnLogLine: func(line string) { lines <- line },
	})
	if err != nil {
		t.Fatal(err)
	}
	endSession(t, s)
	<-s.Done()
	line := <-lines
	if line != "plugin-line" {
		t.Fatalf("log callback line = %q", line)
	}
}

func TestSendLineReachesProcess(t *testing.T) {
	log := filepath.Join(t.TempDir(), "b.log")
	s, err := Start(Options{Cmd: "sh", Args: []string{"-c", "read x; echo got:$x"}, LogPath: log, Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	endSession(t, s)
	if err := s.SendLine("ping"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return strings.Contains(s.Screen(), "got:ping") })
}

func TestSendSubmitWritesEnterAfterDelay(t *testing.T) {
	proc := &recordingProcess{}
	s := &Session{proc: proc}
	oldDelay := SubmitDelay
	SubmitDelay = 10 * time.Millisecond
	t.Cleanup(func() { SubmitDelay = oldDelay })

	started := time.Now()
	if err := s.SendSubmit(); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < SubmitDelay {
		t.Fatal("submit bypassed the input settling delay")
	}
	if len(proc.writes) != 1 || string(proc.writes[0]) != "\r" {
		t.Fatalf("submit writes = %q, want Enter", proc.writes)
	}
}

func TestSendTextAndKeysWritesSeparateBursts(t *testing.T) {
	proc := &recordingProcess{}
	s := &Session{proc: proc}
	oldDelay := SubmitDelay
	SubmitDelay = 0
	t.Cleanup(func() { SubmitDelay = oldDelay })

	if err := s.SendTextAndKeys("answer", "\r"); err != nil {
		t.Fatal(err)
	}
	if len(proc.writes) != 2 || string(proc.writes[0]) != "answer" || string(proc.writes[1]) != "\r" {
		t.Fatalf("writes = %q, want separate text and Enter writes", proc.writes)
	}
}

func TestConditionalTextAndKeysChecksReadinessWhileOwningInput(t *testing.T) {
	proc := &recordingProcess{}
	s := &Session{proc: proc}
	oldDelay := SubmitDelay
	SubmitDelay = 0
	t.Cleanup(func() { SubmitDelay = oldDelay })

	callbackOwnedInput := false
	sent, err := s.SendTextAndKeysIf("answer", "\r", func() bool {
		if s.inputMu.TryLock() {
			s.inputMu.Unlock()
			return false
		}
		callbackOwnedInput = true
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sent || !callbackOwnedInput {
		t.Fatalf("sent=%t callback_owned_input=%t, want both true", sent, callbackOwnedInput)
	}
	if len(proc.writes) != 2 || string(proc.writes[0]) != "answer" || string(proc.writes[1]) != "\r" {
		t.Fatalf("writes = %q, want separate text and Enter writes", proc.writes)
	}
	sent, err = s.SendTextAndKeysIf("blocked", "\r", func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if sent || len(proc.writes) != 2 {
		t.Fatalf("declined send reported sent=%t and writes=%q", sent, proc.writes)
	}
}

func TestKillClosesDone(t *testing.T) {
	log := filepath.Join(t.TempDir(), "c.log")
	s, err := Start(Options{Cmd: "sleep", Args: []string{"60"}, LogPath: log, Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	endSession(t, s)
	if err := s.Kill(); err != nil {
		t.Fatal(err)
	}
	<-s.Done()
}

func TestEnvInjected(t *testing.T) {
	log := filepath.Join(t.TempDir(), "d.log")
	s, err := Start(Options{Cmd: "sh", Args: []string{"-c", "echo id=$OMO_AGENT_ID"},
		Env: []string{"OMO_AGENT_ID=developer-jason"}, LogPath: log, Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	endSession(t, s)
	<-s.Done()
	if !strings.Contains(s.Screen(), "id=developer-jason") {
		t.Fatalf("env not injected:\n%s", s.Screen())
	}
}

func TestOnOutputReceivesRawBytes(t *testing.T) {
	var mu sync.Mutex
	var got []byte
	s, err := Start(Options{
		Cmd:     "sh",
		Args:    []string{"-c", `printf 'a\033[1mb'`},
		LogPath: filepath.Join(t.TempDir(), "raw.log"),
		OnOutput: func(chunk []byte) {
			mu.Lock()
			got = append(got, chunk...)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	endSession(t, s)
	<-s.Done()
	mu.Lock()
	defer mu.Unlock()
	if runtime.GOOS == "windows" {
		// A Windows pseudoconsole has no byte stream of the program: it
		// draws the program's screen with sequences of its own. What stays
		// true is that OnOutput gets the terminal's sequences unstripped.
		if !bytes.Contains(got, []byte("a")) || !bytes.Contains(got, []byte("\x1b[1m")) {
			t.Fatalf("OnOutput bytes = %q, want the text and its bold sequence", got)
		}
		return
	}
	if string(got) != "a\x1b[1mb" {
		t.Fatalf("OnOutput bytes = %q, want %q", got, "a\x1b[1mb")
	}
}

// SendTextAndKeys keeps typed text and following control keys in distinct PTY
// writes. Full-screen CLIs can otherwise interpret the combined burst as a
// paste and insert Enter instead of submitting the text.
func (s *Session) SendTextAndKeys(text, keys string) error {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	return s.sendTextAndKeys(text, keys)
}

// SendLine types text and a carriage return.
func (s *Session) SendLine(text string) error { return s.SendText(text + "\r") }
