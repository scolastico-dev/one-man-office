package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitScreen(t *testing.T, s *Session, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if out := s.ScreenANSI(); strings.Contains(out, want) {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("never saw %q; screen:\n%q", want, s.ScreenANSI())
	return ""
}

// The peek view must show the agent's colours: Screen() renders the vt10x
// buffer as plain text, which throws every attribute away.
func TestScreenANSIPreservesColor(t *testing.T) {
	log := filepath.Join(t.TempDir(), "c.log")
	s, err := Start(Options{
		Cmd:     "sh",
		Args:    []string{"-c", `printf '\033[31mRED\033[0m plain\n'`},
		LogPath: log, Rows: 10, Cols: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := waitScreen(t, s, "RED")
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("no SGR codes in rendered screen: %q", out)
	}
	// Red is ANSI colour 1 → SGR 31.
	if !strings.Contains(out, "31m") {
		t.Fatalf("red not rendered: %q", out)
	}
	// Plain text must still be there, and the plain Screen() stays plain.
	if !strings.Contains(out, "plain") {
		t.Fatalf("text lost: %q", out)
	}
	if strings.Contains(s.Screen(), "\x1b[") {
		t.Fatalf("Screen() must stay plain: %q", s.Screen())
	}
}

func TestScreenANSIPreservesTrueColor(t *testing.T) {
	log := filepath.Join(t.TempDir(), "t.log")
	s, err := Start(Options{
		Cmd:     "sh",
		Args:    []string{"-c", `printf '\033[38;2;215;119;87mORANGE\033[0m\n'`},
		LogPath: log, Rows: 10, Cols: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := waitScreen(t, s, "ORANGE")
	if !strings.Contains(out, "38;2;215;119;87") {
		t.Fatalf("truecolor not preserved: %q", out)
	}
}

func TestScreenANSIResetsAtEnd(t *testing.T) {
	log := filepath.Join(t.TempDir(), "r.log")
	s, err := Start(Options{
		Cmd:     "sh",
		Args:    []string{"-c", `printf '\033[41mBG\n'`},
		LogPath: log, Rows: 5, Cols: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := waitScreen(t, s, "BG")
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Fatalf("render must end with a reset, got tail %q", out[max(0, len(out)-12):])
	}
}

// The log on disk must be readable text, not the raw escape stream.
func TestLogIsStripped(t *testing.T) {
	log := filepath.Join(t.TempDir(), "s.log")
	s, err := Start(Options{
		Cmd:     "sh",
		Args:    []string{"-c", `printf '\033[38;2;1;2;3mcolored\033[0m\n'`},
		LogPath: log, Rows: 10, Cols: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-s.Done()
	lines, err := s.TailLog(20)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "\x1b") || strings.Contains(joined, "38;2;") {
		t.Fatalf("log still contains escapes: %q", joined)
	}
	if !strings.Contains(joined, "colored") {
		t.Fatalf("log lost its text: %q", joined)
	}
}
