//go:build !windows

// The fake git is a shell script, which Windows cannot run; the timeout
// itself is platform independent, so this file is skipped there.
package gitops

import (
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

func TestTimeoutSendsSigtermFirst(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "term")
	pidFile := fakeGit(t, "trap 'echo term > \""+marker+"\"; exit 1' TERM")
	g := New()
	g.query = 300 * time.Millisecond
	if _, err := g.CurrentBranch(t.TempDir()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("git did not get SIGTERM before it was killed")
	}
	waitGone(t, pidFile)
}

func TestTimeoutKillsWhatIgnoresSigterm(t *testing.T) {
	pidFile := fakeGit(t, "trap '' TERM")
	g := New()
	g.query = 300 * time.Millisecond
	g.grace = 500 * time.Millisecond
	start := time.Now()
	if _, err := g.CurrentBranch(t.TempDir()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if d := time.Since(start); d < 700*time.Millisecond || d > 5*time.Second {
		t.Fatalf("returned after %s, want the timeout plus the grace period", d)
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
	fakeGit(t, "case \" $* \" in *\" merge \"*) trap '' TERM; : > '"+lock+"';; *) exec '"+realGit+"' \"$@\";; esac")
	g.op = time.Second
	g.grace = 300 * time.Millisecond
	err = g.AdvanceBranch(repo, "main", from, res.Commit)
	if !errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), lock+" remains") {
		t.Fatalf("err = %v, want a timeout naming %s", err, lock)
	}
}
