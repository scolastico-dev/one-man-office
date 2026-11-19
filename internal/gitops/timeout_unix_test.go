//go:build !windows

// The fake git is a shell script, which Windows cannot run; the timeout
// itself is platform independent, so this file is skipped there.
package gitops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fakeHungGit(t *testing.T) (pidFile string) {
	t.Helper()
	return fakeGit(t, "")
}

// fakeGit puts a git on PATH that runs prelude, then starts a child and
// waits for it forever. It returns the file holding the child's pid.
func fakeGit(t *testing.T, prelude string) (pidFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "child.pid")
	script := "#!/bin/sh\n" + prelude + "\nsleep 60 &\necho $! > '" + pidFile + "'\nwait\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return pidFile
}

// waitGone fails unless the process pid ends within a few seconds.
func waitGone(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
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

func TestHungGitTimesOut(t *testing.T) {
	fakeHungGit(t)
	g := New()
	g.query = 300 * time.Millisecond
	dir := t.TempDir()
	err := returns(t, "hung git", func() error {
		_, err := g.CurrentBranch(dir)
		return err
	})
	if !errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}

}

// cancelReadyGit starts git with a long deadline and cancels only once its
// child exists. Process-tree assertions cannot depend on startup beating a
// short wall-clock timeout.
func cancelReadyGit(t *testing.T, g *Git, pidFile string) (error, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	dir := t.TempDir()
	go func() { _, err := g.run(ctx, time.Hour, dir, "branch", "--show-current"); result <- err }()
	for {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 1 {
				t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
				break
			}
		}

		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	cancel()
	err := returns(t, "git cancellation", func() error { return <-result })
	return err, time.Since(start)
}

func TestCanceledGitIsKilledWithItsChildren(t *testing.T) {
	pidFile := fakeHungGit(t)
	if err, _ := cancelReadyGit(t, New(), pidFile); err == nil {
		t.Fatal("canceled git succeeded")
	}
	waitGone(t, pidFile)
}

func TestHungOperationUsesOperationTimeout(t *testing.T) {
	fakeHungGit(t)
	g := New()
	g.query = time.Hour
	g.op = 300 * time.Millisecond
	if _, err := g.opRun(t.TempDir(), "worktree", "add", "--end-of-options", filepath.Join(t.TempDir(), "w"), "b"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

func TestCancellationSendsSigtermFirst(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "term")
	pidFile := fakeGit(t, "trap 'echo term > \""+marker+"\"; exit 1' TERM")
	g := New()
	if err, _ := cancelReadyGit(t, g, pidFile); err == nil {
		t.Fatal("canceled git succeeded")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("git did not get SIGTERM before it was killed")
	}
	waitGone(t, pidFile)
}

func TestCancellationKillsWhatIgnoresSigterm(t *testing.T) {
	pidFile := fakeGit(t, "trap '' TERM")
	g := New()
	g.grace = 500 * time.Millisecond
	err, elapsed := cancelReadyGit(t, g, pidFile)
	if err == nil {
		t.Fatal("canceled git succeeded")
	}
	if elapsed < g.grace {
		t.Fatalf("returned after %s, want the grace period %s", elapsed, g.grace)
	}
	waitGone(t, pidFile)
}

// A fast-forward that hangs is reported with the index.lock it leaves.
func TestFastForwardTimeoutReportsIndexLock(t *testing.T) {
	repo := newTestRepo(t)
	g := testGit(t)
	feature(t, repo)
	from := rev(t, repo, "main")
	res := prepare(t, g, repo, "main", "feature", StyleMerge, "m")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(repo, ".git", "index.lock")
	// Install the leftover before starting the timed command; the assertion
	// must not require that a child wins a race against the operation timeout.
	g.pause = func(point string) {
		if point == "fast-forward" {
			if err := os.WriteFile(lock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	fakeGit(t, "case \" $* \" in *\" merge \"*) trap '' TERM;; *) exec '"+realGit+"' \"$@\";; esac")
	g.op = time.Second
	g.grace = 300 * time.Millisecond
	err = g.AdvanceBranch(repo, "main", from, res.Commit)
	if !errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), lock+" remains") {
		t.Fatalf("err = %v, want a timeout naming %s", err, lock)
	}
}
