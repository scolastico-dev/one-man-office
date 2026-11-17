//go:build !windows

package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// A transcript path that is a link is refused, and the file it points at
// stays untouched.
func TestLogRefusesALink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.log")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := newRotatingWriter(path, 1, 1); !errors.Is(err, homefile.ErrLink) {
		t.Fatalf("start on a link: %v", err)
	}

	if raw, _ := os.ReadFile(target); string(raw) != "keep" {
		t.Fatalf("linked file changed: %q", raw)
	}
}
