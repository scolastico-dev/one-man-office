package session

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hinshun/vt10x"
)

func rotatedFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// A long-lived agent must not fill the disk with one unbounded transcript.
func TestLogRotatesAtMaxSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	w, err := newRotatingWriter(path, 1, 2) // 1 KB, keep 2
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	chunk := []byte(strings.Repeat("x", 300) + "\n")
	for i := 0; i < 20; i++ { // ~6 KB total
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	files := rotatedFiles(t, dir)
	// Live log plus at most Keep rotated files.
	if len(files) > 3 {
		t.Fatalf("kept %d files, want at most 3: %v", len(files), files)
	}
	if len(files) < 2 {
		t.Fatalf("never rotated: %v", files)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > 2*1024 {
		t.Fatalf("live log is %d bytes, rotation is not bounding it", fi.Size())
	}
}

func TestLogRotationDisabledWhenMaxSizeZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.log")
	w, err := newRotatingWriter(path, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for i := 0; i < 50; i++ {
		w.Write([]byte(strings.Repeat("y", 500) + "\n"))
	}
	if files := rotatedFiles(t, dir); len(files) != 1 {
		t.Fatalf("rotation must be off, got %v", files)
	}
}

func TestRotationKeepsMostRecentContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.log")
	w, _ := newRotatingWriter(path, 1, 1)
	defer w.Close()
	w.Write([]byte(strings.Repeat("old", 500) + "\n"))
	w.Write([]byte("NEWEST-LINE\n"))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "NEWEST-LINE") {
		t.Fatalf("live log lost the newest write: %q", string(raw))
	}
}

// Full-screen output is captured as line-oriented screen deltas, rather than
// one continuous ANSI-stripped string.
func TestSessionWritesReadableScreenLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.log")
	s, err := Start(Options{
		Cmd:     "sh",
		Args:    []string{"-c", `printf '\033[Hhello'; read reply; printf '\033[2Hworld'`},
		LogPath: path, Rows: 10, Cols: 80,
		LogMaxSizeKB: 1, LogKeep: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanupSession(t, s)
	waitFor(t, 5*time.Second, func() bool {
		lines, err := s.TailLog(10)
		return err == nil && strings.Contains(strings.Join(lines, "\n"), "hello")
	})
	if err := s.SendLine("continue"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end after releasing output")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hello\n") || !strings.Contains(string(raw), "world\n") {
		t.Fatalf("session transcript is not line-oriented: %q", raw)
	}
}

func TestTailLogReadsAcrossARotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	s := &Session{
		opts:    Options{LogPath: path},
		term:    vt10x.New(vt10x.WithSize(80, 24)),
		logOut:  io.Discard,
		logSeen: map[string]time.Time{},
	}
	var older strings.Builder
	for i := 1; i <= 5000; i++ { // more than one read block
		fmt.Fprintf(&older, "line %d %s\n", i, strings.Repeat("-", 20))
	}
	if err := os.WriteFile(path+".1", []byte(older.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	// Right after the rotation the live file is empty.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := s.TailLog(3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "|") != "line 4998 "+strings.Repeat("-", 20)+"|line 4999 "+strings.Repeat("-", 20)+"|line 5000 "+strings.Repeat("-", 20) {
		t.Fatalf("tail after rotation = %q", lines)
	}

	if err := os.WriteFile(path, []byte("new 1\nnew 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err = s.TailLog(4)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 4 || lines[0] != "line 4999 "+strings.Repeat("-", 20) || lines[3] != "new 2" {
		t.Fatalf("tail across the rotated file = %q", lines)
	}
	lines, err = s.TailLog(1)
	if err != nil || len(lines) != 1 || lines[0] != "new 2" {
		t.Fatalf("tail of the live file = %q, %v", lines, err)
	}

	// Without a rotated file the live file is all there is.
	if err := os.Remove(path + ".1"); err != nil {
		t.Fatal(err)
	}
	if lines, err = s.TailLog(10); err != nil || len(lines) != 2 {
		t.Fatalf("tail without a rotated file = %q, %v", lines, err)
	}
}
