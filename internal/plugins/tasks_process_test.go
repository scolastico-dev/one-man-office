package plugins

import (
	"context"
	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Catches killing only a parent, and waiting forever on inherited output pipes
// when that parent exits naturally before its child.
func TestTaskProcessTreeCleanup(t *testing.T) {
	for _, mode := range []string{"spawn", "spawn-exit"} {
		t.Run(mode, func(t *testing.T) {
			e := taskPlugin(t)
			pidfile := filepath.Join(t.TempDir(), "child.pid")
			task, err := e.m.StartTask(t.Context(), "p", taskRequest(t, "tree", mode, pidfile))
			if err != nil {
				t.Fatal(err)
			}
			var pid int
			eventually(t, "child pid", func() bool {
				raw, err := os.ReadFile(pidfile)
				if err != nil {
					return false
				}
				pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
				return pid > 0
			})
			if mode == "spawn" {
				if err := e.m.StopTask("p", "tree"); err != nil {
					t.Fatal(err)
				}
			}
			eventually(t, "parent and descendants terminated", func() bool {
				ts, _ := e.m.Tasks("p")
				ended := false
				for _, x := range ts {
					if x.ID == task.ID {
						ended = x.State == "exited"
						if ended && mode == "spawn-exit" && x.Error != "" {
							t.Errorf("successful parent exit: %s", x.Error)
						}
					}
				}
				return ended && !taskProcessAlive(pid)
			})
		})
	}
}

// Cancellation can arrive between cmd.Start and group.started. Remembering it
// prevents a child escaping because the group's pid was not published yet.
func TestProcessGroupCancellationBeforeRegistration(t *testing.T) {
	args := helperArgs(t, "sleep")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = childenv.Inherited()
	group, err := newProcessGroup(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer group.close()
	if err := group.Start(cmd); err != nil {
		t.Fatal(err)
	}
	// Exercise cancellation before started deterministically, independent of
	// the exec watcher's scheduling.
	group.kill()
	if err := group.started(cmd); err != nil {
		group.close()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("cancellation lost before registration")
		group.close()
		<-done
	}
}
