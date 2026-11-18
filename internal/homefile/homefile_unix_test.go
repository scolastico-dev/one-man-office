//go:build unix

package homefile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// within runs f on its own goroutine and waits for it: a read that waits for a
// writer of a named pipe never returns, and then the test binary's own time
// limit ends the run with a dump of every goroutine (no fixed limit decides).
func within(t *testing.T, what string, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	return <-done
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

func TestAppendRefusesPipeAndLinks(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.log")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := within(t, "a named pipe", func() error { _, err := Append(fifo, 0o600); return err }); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("named pipe: %v", err)
	}
	plain := filepath.Join(dir, "plain.log")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink(plain, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(link, 0o600); !errors.Is(err, ErrLink) {
		t.Fatalf("link to a file: %v", err)
	}
	f, err := Append(filepath.Join(dir, "new.log"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("line\n")
	f.Close()
	f, err = Append(filepath.Join(dir, "new.log"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("more\n")
	f.Close()
	if data, _ := os.ReadFile(filepath.Join(dir, "new.log")); string(data) != "line\nmore\n" {
		t.Fatalf("appended %q", data)
	}
}
