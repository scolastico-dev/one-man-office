//go:build !windows

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// An agent replaced its worktree's index by a named pipe: every read of the
// uncommitted changes fails at once and gives its diff slot back.
func TestDiffRouteIndexPipeReleasesSlot(t *testing.T) {
	e := newDiffEnv(t)
	task := newTask(t, e.u, e.project, "work")
	wt := e.cut(e.repo, e.project, task.Number, "omo/1-work", "main")
	out, err := exec.Command("git", "-C", wt, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	gitDir := strings.TrimSpace(string(out))
	index := filepath.Join(gitDir, "index")
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(index, 0o600); err != nil {
		t.Fatal(err)
	}
	// Bound each request as a watchdog; wall time spent on all the git and
	// HTTP requests together is not evidence that a pipe read blocked.
	e.u.b.srv.Client().Timeout = streamTimeout
	for i := 0; i < diffSlots+2; i++ {
		p := e.diff(e.u, task.Number).Projects[0]
		if !strings.Contains(p.Message, "could not be read") {
			t.Fatalf("round %d: message %q", i, p.Message)
		}
		status, _ := e.u.do("GET", fileURL(task.Number, e.project, "base.txt", "true"), nil)
		if status == 200 {
			t.Fatalf("round %d: the file route answered 200", i)
		}
	}
	// Every request completed, including more than the available slots. A
	// pipe read that blocked or leaked its slot would prevent these results.
}
