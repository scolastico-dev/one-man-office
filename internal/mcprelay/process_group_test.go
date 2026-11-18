package mcprelay

import (
	"context"
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
	if err := cmd.Start(); err != nil {
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
