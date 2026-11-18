package mcprelay

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

// Cancellation can run after Start but before started publishes the process.
// A request made in that gap must still end the actual child once registered.
// The test waits for the child's exit itself; the test binary's own timeout
// is the only bound, so a lost stop fails by hanging, never by a clock.
func TestProcessGroupRemembersStopBeforeRegistration(t *testing.T) {
	for _, stop := range []string{"cancel", "kill", "close", "close then cancel"} {
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
			if err := group.Start(cmd); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			t.Cleanup(func() {
				// Ends the child on every path and waits for its exit, so a
				// failed test leaves no process behind.
				group.close()
				_ = cmd.Process.Kill()
			})
			// Invoke the same callback used by CommandContext, with registration
			// deliberately held back so the ordering is deterministic.
			switch stop {
			case "kill":
				group.kill()
			case "close", "close then cancel":
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
			if err := <-exited; err == nil {
				t.Error("stopped child exited successfully")
			}
		})
	}
}

// The OS may reject job assignment or resuming a suspended Windows child.
// Exercise real process cleanup with that registration result injected.
type rejectedProcessGroup struct{ *processGroup }

func (g rejectedProcessGroup) started(cmd *exec.Cmd) error {
	if err := g.processGroup.started(cmd); err != nil {
		return err
	}
	return errors.New("registration failed")
}

func TestStartGroupedReapsRegistrationFailure(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), os.Args[0], "process-group-sleeper")
	cmd.Env = childenv.Inherited()
	group, err := newProcessGroup(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer group.close()
	if err := startGrouped(cmd, rejectedProcessGroup{group}); err == nil {
		t.Fatal("registration failure accepted")
	}
	if cmd.ProcessState == nil {
		t.Fatal("failed registration did not reap the child")
	}
}
