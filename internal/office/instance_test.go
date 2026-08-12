package office

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestOpenRejectsSecondLiveInstanceAndRecordsEndpoint(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	first, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	raw, err := os.ReadFile(filepath.Join(dir, LockPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != first.Sup.SocketPath {
		t.Fatalf("lock endpoint = %q, want %q", got, first.Sup.SocketPath)
	}
	if !sockc.Probe(first.Sup.SocketPath, instanceProbeTimeout) {
		t.Fatal("bound office endpoint is not probeable")
	}

	_, err = Open(dir, true)
	var running *AlreadyRunningError
	if !errors.As(err, &running) {
		t.Fatalf("second Open error = %v, want AlreadyRunningError", err)
	}
	if running.Endpoint != first.Sup.SocketPath {
		t.Fatalf("running endpoint = %q, want %q", running.Endpoint, first.Sup.SocketPath)
	}
}

func TestOpenReplacesStaleInstanceLock(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, LockPath)
	if err := os.WriteFile(lockPath, []byte(filepath.Join(dir, "missing.sock")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	o, err := Open(dir, true)
	if err != nil {
		t.Fatalf("Open with stale lock: %v", err)
	}
	endpoint := o.Sup.SocketPath
	o.Close()
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("instance lock remains after close: %v", err)
	}
	if sockc.Probe(endpoint, instanceProbeTimeout) {
		t.Fatal("transport remains reachable after close")
	}
}
