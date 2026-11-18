//go:build windows

package mcprelay

import (
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processGroup ends a program together with everything it started: the
// program runs in a job object that kills its processes when it is closed
// or the command is cancelled. A program that could not be put into the
// job is killed on its own.
type processGroup struct {
	mu       sync.Mutex
	job      windows.Handle
	process  *os.Process
	assigned bool
	stopped  bool
}

// newProcessGroup prepares cmd to run in its own job object. Call started
// after cmd.Start and close once cmd ended.
func newProcessGroup(cmd *exec.Cmd) (*processGroup, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	g := &processGroup{job: job}
	cmd.Cancel = g.kill
	return g, nil
}

func (g *processGroup) started(cmd *exec.Cmd) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.process = cmd.Process
	if g.stopped {
		// A cancel or close before registration must still end the child.
		_ = g.process.Kill()
		return nil
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(g.job, process); err != nil {
		return err
	}
	g.assigned = true
	return nil
}

// kill remembers cancellation even before the program is registered.
func (g *processGroup) kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	if g.process != nil && !g.assigned {
		_ = g.process.Kill()
	}
	if g.job != 0 {
		return windows.TerminateJobObject(g.job, 1)
	}
	return nil
}

// close kills what is left of the job and releases it.
func (g *processGroup) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	if g.process != nil && !g.assigned {
		_ = g.process.Kill()
	}
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
	}
}
