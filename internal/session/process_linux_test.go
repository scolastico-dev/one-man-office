//go:build linux

package session

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestNaturalAgentExitReapsDetachedProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "detached.pid")
	s, err := Start(Options{
		Cmd: "sh", Args: []string{"-c", `setsid sh -c 'exec sleep 60 </dev/null >/dev/null 2>&1' & echo $! > "$1"`, "sh", pidFile},
		LogPath: filepath.Join(t.TempDir(), "agent.log"), Rows: 24, Cols: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	childPID := waitForPIDFile(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not finish after agent exited")
	}
	waitForProcessExit(t, childPID)
}
