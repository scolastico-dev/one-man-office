//go:build !windows

package session

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	var pid int
	waitFor(t, 5*time.Second, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && pid > 1
	})
	return pid
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	waitFor(t, 5*time.Second, func() bool {
		err := syscall.Kill(pid, 0)
		return errors.Is(err, syscall.ESRCH)
	})
}

func TestKillReapsBackgroundProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s, err := Start(Options{
		Cmd: "sh", Args: []string{"-c", `sleep 60 & echo $! > "$1"; wait`, "sh", pidFile},
		LogPath: filepath.Join(t.TempDir(), "agent.log"), Rows: 24, Cols: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	childPID := waitForPIDFile(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })
	if err := s.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not exit after Kill")
	}
	waitForProcessExit(t, childPID)
}
