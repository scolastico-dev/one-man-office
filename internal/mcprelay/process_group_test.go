package mcprelay

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

// Cancellation can run after Start but before started publishes the process.
// A request made in that gap must still end the actual child once registered.
func TestProcessGroupRemembersStopBeforeRegistration(t *testing.T) {
	for _, stop := range []string{"cancel", "close", "close then cancel"} {
		t.Run(stop, func(t *testing.T) {
			f := newFixture(t, nil)
			cmd := exec.CommandContext(context.Background(), exe())
			cmd.Env = childEnvironment(f.env, map[string]string{
				fakemcp.EnvEnable: "1",
				fakemcp.EnvDelay:  "1h",
			})
			childenvtest.CheckOmo(t, cmd.Env, nil)
			group, err := newProcessGroup(cmd)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(group.close)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			waited := false
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				if !waited {
					select {
					case <-exited:
					case <-time.After(5 * time.Second):
						t.Error("child did not exit during cleanup")
					}
				}
			})
			// Invoke the same callback used by CommandContext, with registration
			// deliberately held back so the ordering is deterministic.
			if stop == "close" || stop == "close then cancel" {
				group.close()
			}
			if stop == "cancel" || stop == "close then cancel" {
				if err := cmd.Cancel(); err != nil {
					t.Fatal(err)
				}
			}
			if err := group.started(cmd); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-exited:
				waited = true
				if err == nil {
					t.Error("stopped child exited successfully")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("stop before registration left the child running")
			}
		})
	}
}
