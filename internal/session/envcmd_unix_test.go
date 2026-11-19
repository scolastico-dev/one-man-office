//go:build !windows

// This test uses sh, sleep and signals, so it is built for Unix only.

package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExpandEnvironmentTimesOutAndKillsTheCommand(t *testing.T) {
	// Test the configured deadline independently of process startup speed.
	command := "echo printed-secret; sleep 60"
	var err error
	completeWithin(t, 5*time.Second, func() {
		_, err = ExpandEnvironment(300*time.Millisecond, map[string]string{
			"FAST":           "plain",
			"PASSWORD_VALUE": "`" + command + "`",
		})
	})
	var timeout *CommandTimeoutError
	if !errors.As(err, &timeout) || timeout.Variable != "PASSWORD_VALUE" {
		t.Fatalf("err = %v, want a timeout naming PASSWORD_VALUE", err)
	}
	if !strings.Contains(err.Error(), "PASSWORD_VALUE") || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error text = %q", err)
	}
	if strings.Contains(err.Error(), "printed-secret") || strings.Contains(err.Error(), "sleep") {
		t.Fatalf("error repeats the command or its output: %q", err)
	}

}

func TestExpandEnvironmentCancellationKillsTheCommandTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := ExpandEnvironmentContext(ctx, time.Hour, map[string]string{
			"VALUE": "`sleep 60 & echo $! > " + pidFile + "; wait`",
		})
		result <- err
	}()
	pid := waitForPIDFile(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	cancel()
	var err error
	completeWithin(t, 5*time.Second, func() { err = <-result })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want cancellation", err)
	}
	waitFor(t, 10*time.Second, func() bool { return processGone(pid) })
}

// processGone reports whether pid no longer runs (a zombie counts as gone).
func processGone(pid int) bool {
	if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	_, after, _ := strings.Cut(string(stat), ") ")
	return strings.HasPrefix(after, "Z")
}

func TestCommandExitStatusFailsOnlyTheCheck(t *testing.T) {
	env := map[string]string{"SILENT_FAIL": "`exit 3`", "LOUD_FAIL": "`echo out; exit 4`", "OK": "`true`"}
	// Agents get the empty output.
	got, err := ExpandEnvironment(5*time.Second, env)
	if err != nil {
		t.Fatal(err)
	}
	if m := environmentMap(got); m["SILENT_FAIL"] != "" || m["LOUD_FAIL"] != "out" {
		t.Fatalf("expanded %v", m)
	}
	// The check fails on the command that printed nothing.
	_, err = CheckEnvironmentContext(t.Context(), 5*time.Second, env)
	var ee *CommandExitError
	if !errors.As(err, &ee) || ee.Variable != "SILENT_FAIL" || ee.Status != 3 {
		t.Fatalf("check: %v", err)
	}
	if strings.Contains(err.Error(), "exit 3") {
		t.Fatalf("the error quotes the command: %v", err)
	}
	delete(env, "SILENT_FAIL")
	if _, err := CheckEnvironmentContext(t.Context(), 5*time.Second, env); err != nil {
		t.Fatalf("check without it: %v", err)
	}
}
