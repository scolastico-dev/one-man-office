//go:build !windows

package plugins

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A program a hook started, and every program it started, ends with the
// hook's run.
func TestTimeoutEndsTheProgramsAHookStarted(t *testing.T) {
	e := newEnv(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	e.plugin("p", manifestOf(map[string]any{
		"event": EventManual, "name": "spawn", "description": "Spawn", "timeout": "2s",
		"command": helperArgs(t, "spawn", pidFile),
	}), nil)
	e.load(enabled("p", nil))
	start := time.Now()
	_, err := e.m.Trigger(t.Context(), "p", "spawn", nil)
	if err == nil || err.Error() != "time limit reached" {
		t.Fatalf("Trigger = %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("the run took %v", d)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the child to end", func() bool {
		return syscall.Kill(pid, 0) == syscall.ESRCH
	})
}
