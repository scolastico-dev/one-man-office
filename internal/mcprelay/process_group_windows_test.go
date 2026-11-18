package mcprelay

import (
	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"testing"
)

func TestStartGroupedReapsSuspendedAssignmentFailure(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), os.Args[0], "process-group-sleeper")
	cmd.Env = childenv.Inherited()
	group, err := newProcessGroup(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer group.close()
	// Invalidate the job without marking cancellation: assigning the suspended
	// child must fail, and the caller must terminate and reap that child.
	if err := windows.CloseHandle(group.job); err != nil {
		t.Fatal(err)
	}
	group.job = 0
	if err := startGrouped(cmd, group); err == nil {
		t.Fatal("invalid job assignment accepted")
	}
	if cmd.ProcessState == nil {
		t.Fatal("suspended assignment failure was not reaped")
	}
}
