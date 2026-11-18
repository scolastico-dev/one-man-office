//go:build unix

package mcprelay

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A named pipe or a link at a server's log path is neither read nor
// written through: reading the log answers at once with an error, and a
// line is not appended to the link's target.
func TestTheServerLogRefusesPipesAndLinks(t *testing.T) {
	f := newFixture(t, nil)
	if err := os.MkdirAll(f.logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(f.logDir, "mcp-piped.log")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.pool.Log("piped", 10)
		done <- err
	}()
	if err := <-done; err == nil {
		t.Fatal("a named pipe was read as the log")
	}

	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.logDir, "mcp-linked.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Log("linked", 10); err == nil {
		t.Fatal("a link was read as the log")
	}
	l := newServerLog(f.logDir, "linked", &redactor{})
	l.printf("a line")
	l.close()
	if data, _ := os.ReadFile(target); string(data) != "kept\n" {
		t.Fatalf("the log was written through a link: %q", data)
	}
}
