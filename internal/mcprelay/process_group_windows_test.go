package mcprelay

import (
	"os"
	"os/exec"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
)

// A program that cannot be put into its job must not be left running: with
// the job gone, startGrouped fails and no process of the command is alive.
// That a program never runs before it joined its job, and that one whose
// assignment fails after creation is ended and reaped, is tested where the
// launch lives, in internal/processtree.
func TestStartGroupedRefusesWithoutAJob(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), os.Args[0], "process-group-sleeper")
	cmd.Env = childenv.Inherited()
	group, err := newProcessGroup(cmd)
	if err != nil {
		t.Fatal(err)
	}
	group.close()
	if err := startGrouped(cmd, group); err == nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("a program was started without a job")
	}
	if cmd.Process != nil && cmd.ProcessState == nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("the refused program was left running")
	}
}
