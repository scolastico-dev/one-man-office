//go:build !windows

// The fake git is a shell script, which Windows cannot run; the timeout
// itself is platform independent, so this file is skipped there.
package gitops

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

func fakeHungGit(t *testing.T) (pidFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "child.pid")
	script := "#!/bin/sh\nsleep 60 &\necho $! > '" + pidFile + "'\nwait\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return pidFile
}

func TestHungGitIsKilledWithItsChildren(t *testing.T) {
	pidFile := fakeHungGit(t)
	g := New()
	g.query = 300 * time.Millisecond
	start := time.Now()
	_, err := g.CurrentBranch(t.TempDir())
	if !errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("returned after %s", time.Since(start))
	}
	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatal(rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d of the fake git still runs", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHungOperationUsesOperationTimeout(t *testing.T) {
	fakeHungGit(t)
	g := New()
	g.query = time.Hour
	g.op = 300 * time.Millisecond
	if err := g.AddWorktree(t.TempDir(), filepath.Join(t.TempDir(), "w"), "b"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want a timeout", err)
	}
}
