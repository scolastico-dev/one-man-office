//go:build unix

package usage

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRunRefusesAFIFO(t *testing.T) {
	cleanEnv(t)
	home := t.TempDir()
	fifo := filepath.Join(home, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runScript(t, readScript(fifo), RunOptions{Home: home})
	if err == nil || !strings.HasSuffix(err.Error(), "refused: file is not a regular file") {
		t.Fatalf("err = %v", err)
	}
}
