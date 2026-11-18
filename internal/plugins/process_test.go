package plugins

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// exec can invoke Cancel as soon as Start launches the process, before
// runProgram has published the process to its group.
func TestProcessGroupRemembersCancellationBeforeStarted(t *testing.T) {
	args := helperArgs(t, "sleep")
	cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
	cmd.Env = os.Environ()
	group, err := newProcessGroup(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(group.close)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() { done <- cmd.Wait(); close(exited) }()
	t.Cleanup(func() { group.close(); <-exited })
	// Deliver cancellation in the exact gap before PID/job publication.
	_ = cmd.Cancel()
	if err := group.started(cmd); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the canceled sleeping program exited successfully")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation before group publication left the program running")
	}
}
