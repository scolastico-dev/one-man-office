//go:build !windows

package plugins

import (
	"context"
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
func TestCancellationEndsTheProgramsAHookStarted(t *testing.T) {
	e := newEnv(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	e.plugin("p", manifestOf(map[string]any{
		"event": EventManual, "name": "spawn", "description": "Spawn",
		"command": helperArgs(t, "spawn", pidFile),
	}), nil)
	e.load(enabled("p", nil))
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { _, err := e.m.Trigger(ctx, "p", "spawn", nil); done <- err }()
	eventually(t, "the hook's child to start", func() bool {
		raw, err := os.ReadFile(pidFile)
		_, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil && parseErr == nil
	})
	cancel()
	select {
	case err := <-done:
		if err == nil || err.Error() != "run canceled" {
			t.Fatalf("Trigger = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the canceled hook did not finish")
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
