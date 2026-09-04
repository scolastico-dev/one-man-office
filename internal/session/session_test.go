package session

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type submitRecordingProcess struct {
	writes []string
}

func (p *submitRecordingProcess) Read([]byte) (int, error) { return 0, io.EOF }
func (p *submitRecordingProcess) Write(b []byte) (int, error) {
	p.writes = append(p.writes, string(b))
	return len(b), nil
}
func (p *submitRecordingProcess) Resize(uint16, uint16) error { return nil }
func (p *submitRecordingProcess) Kill() error                 { return nil }
func (p *submitRecordingProcess) Wait() error                 { return nil }
func (p *submitRecordingProcess) Close() error                { return nil }

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

func TestScreenAndLogCaptureOutput(t *testing.T) {
	log := filepath.Join(t.TempDir(), "a.log")
	s, err := Start(Options{Cmd: "sh", Args: []string{"-c", "printf 'hello-omo\\n'"}, LogPath: log, Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
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
	<-s.Done()
	select {
	case line := <-lines:
		if line != "plugin-line" {
			t.Fatalf("log callback line = %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("log callback was not invoked")
	}
}

func TestSendLineReachesProcess(t *testing.T) {
	log := filepath.Join(t.TempDir(), "b.log")
	s, err := Start(Options{Cmd: "sh", Args: []string{"-c", "read x; echo got:$x"}, LogPath: log, Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SendLine("ping"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return strings.Contains(s.Screen(), "got:ping") })
}

func TestSendSubmitWritesEnterAfterDelay(t *testing.T) {
	proc := &submitRecordingProcess{}
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
	if len(proc.writes) != 1 || proc.writes[0] != "\r" {
		t.Fatalf("submit writes = %q, want Enter", proc.writes)
	}
}

func TestKillClosesDone(t *testing.T) {
	log := filepath.Join(t.TempDir(), "c.log")
	s, err := Start(Options{Cmd: "sleep", Args: []string{"60"}, LogPath: log, Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed after Kill")
	}
}

func TestEnvInjected(t *testing.T) {
	log := filepath.Join(t.TempDir(), "d.log")
	s, err := Start(Options{Cmd: "sh", Args: []string{"-c", "echo id=$OMO_AGENT_ID"},
		Env: []string{"OMO_AGENT_ID=developer-jason"}, LogPath: log, Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	<-s.Done()
	if !strings.Contains(s.Screen(), "id=developer-jason") {
		t.Fatalf("env not injected:\n%s", s.Screen())
	}
}
