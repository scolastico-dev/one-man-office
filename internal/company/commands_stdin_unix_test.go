//go:build !windows

package company

import (
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func verifyCanceledCommandProcess(t *testing.T, events []commandEvent) {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(stdoutData(events)))
	if err != nil || pid < 1 {
		t.Fatalf("missing canceled command pid: %q", stdoutData(events))
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("canceled command process %d survived", pid)
}
