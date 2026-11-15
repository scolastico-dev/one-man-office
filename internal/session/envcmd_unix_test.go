//go:build !windows

// This test uses sh, sleep and signals, so it is built for Unix only.

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

func TestExpandEnvironmentTimesOutAndKillsTheCommand(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	// The command starts a background sleeper (a grandchild in the same
	// group) and then waits: both must be gone after the timeout.
	command := "sleep 60 & echo $! > " + pidFile + "; echo printed-secret; wait"
	started := time.Now()
	_, err := ExpandEnvironment(300*time.Millisecond, map[string]string{
		"FAST":           "plain",
		"PASSWORD_VALUE": "`" + command + "`",
	})
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("expansion took %v", elapsed)
	}
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
	raw, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if convErr != nil {
		t.Fatal(convErr)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !processGone(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("background process %d survived the timeout", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
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
