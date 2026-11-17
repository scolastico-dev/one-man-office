//go:build unix

package serverconfig

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// TestLoadersRefusePipesAndLinks: server.yaml and the token file replaced by
// a named pipe or a link to /dev/zero are refused at once.
func TestLoadersRefusePipesAndLinks(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "server.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "token")
	if err := os.Symlink("/dev/zero", token); err != nil {
		t.Fatal(err)
	}
	done := make(chan [2]error, 1)
	go func() {
		_, cerr := Load(fifo)
		_, terr := LoadOrCreateToken(token)
		done <- [2]error{cerr, terr}
	}()
	select {
	case errs := <-done:
		if !errors.Is(errs[0], homefile.ErrNotRegular) {
			t.Errorf("server.yaml: %v", errs[0])
		}
		if !errors.Is(errs[1], homefile.ErrLink) {
			t.Errorf("token: %v", errs[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a loader blocked")
	}
}
