//go:build windows

package plugins

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
	job windows.Handle

	mu       sync.Mutex
	process  *os.Process
	assigned bool
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
	cmd.Cancel = func() error {
		g.killUnassigned()
		return windows.TerminateJobObject(job, 1)
	}
	return g, nil
}

func (g *processGroup) started(cmd *exec.Cmd) error {
	g.mu.Lock()
	g.process = cmd.Process
	g.mu.Unlock()
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(g.job, process); err != nil {
		return err
	}
	g.mu.Lock()
	g.assigned = true
	g.mu.Unlock()
	return nil
}

// killUnassigned kills the program when it is not in the job, which would
// not end it.
func (g *processGroup) killUnassigned() {
	g.mu.Lock()
	process, assigned := g.process, g.assigned
	g.mu.Unlock()
	if process != nil && !assigned {
		_ = process.Kill()
	}
}

// close kills what is left of the job and releases it.
func (g *processGroup) close() {
	g.killUnassigned()
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
	}
}
