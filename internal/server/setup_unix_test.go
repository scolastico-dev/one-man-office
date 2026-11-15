//go:build !windows

package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCloseEndsEnvCheck answers an environment check at its limit while a
// command still runs, then closes the server: Close ends the command and
// waits for the check, within a bound.
func TestCloseEndsEnvCheck(t *testing.T) {
	s := newTestServer(t)
	s.setup.commandTimeout = time.Minute
	s.setup.checkLimit = 200 * time.Millisecond
	b := newBrowser(t, s)
	pidFile := filepath.Join(t.TempDir(), "pid")
	resp, raw := postEnvCheck(t, s, b, map[string]string{"A": "`echo $$ > " + pidFile + "; exec sleep 60`"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := checks(t, raw); got["A"].State != "failed" {
		t.Fatalf("answer = %s", raw)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close waits for the environment check's command")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("the check's command still runs after Close (kill 0: %v)", err)
	}
	// The slot was released: nothing holds it after Close.
	if !s.setup.envCheck.TryLock() {
		t.Fatal("the environment check still holds its slot")
	}
	s.setup.envCheck.Unlock()
}
