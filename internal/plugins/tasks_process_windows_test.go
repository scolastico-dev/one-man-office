package plugins

import (
	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func taskProcessAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	state, err := windows.WaitForSingleObject(h, 0)
	return err == nil && state == uint32(windows.WAIT_TIMEOUT)
}

// The program must not run any code before started assigns its job, otherwise
// a short-lived parent can leave descendants outside the job.
func TestProcessGroupSuspendsUntilAssigned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ran")
	args := helperArgs(t, "record", path)
	cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
	cmd.Env = childenv.Inherited()
	g, err := newProcessGroup(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("program executed before job assignment")
	}
	if err := g.started(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("program did not resume:", err)
	}
}
