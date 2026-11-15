//go:build unix

package homefile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// within fails when f does not return within a second: a read that waits
// for a writer of a named pipe never returns.
func within(t *testing.T, what string, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatalf("%s blocked", what)
		return nil
	}
}

func TestReadRefusesPipeAndLinks(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := within(t, "a named pipe", func() error { _, err := Read(fifo, 10); return err }); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("named pipe: %v", err)
	}

	zero := filepath.Join(dir, "zero.yaml")
	if err := os.Symlink("/dev/zero", zero); err != nil {
		t.Fatal(err)
	}
	if err := within(t, "a link to /dev/zero", func() error { _, err := Read(zero, 10); return err }); !errors.Is(err, ErrLink) {
		t.Fatalf("link to /dev/zero: %v", err)
	}
	if _, err := Read("/dev/zero", 10); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("/dev/zero: %v", err)
	}

	// A link to a regular file is refused too.
	plain := filepath.Join(dir, "plain.yaml")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(plain, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); !errors.Is(err, ErrLink) {
		t.Fatalf("link to a file: %v", err)
	}
}
