package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		Args:    []string{"-c", `printf '\033[Hhello'; sleep 0.3; printf '\033[2Hworld'`},
		LogPath: path, Rows: 10, Cols: 80,
		LogMaxSizeKB: 1, LogKeep: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-s.Done()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hello\n") || !strings.Contains(string(raw), "world\n") {
		t.Fatalf("session transcript is not line-oriented: %q", raw)
	}
}
