package mcprelay

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
)

func TestProcessGroupCancellationBeforeRegistration(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "process-group-sleeper")
	cmd.Env = childenv.Inherited()
	group, err := newProcessGroup(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer group.close()
	if err := group.Start(cmd); err != nil {
		t.Fatal(err)
	}
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
