//go:build windows

package mcprelay

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processGroup ends a program together with everything it started: the
// program runs in a job object that kills its processes when it is closed
// or the command is cancelled.
type processGroup struct {
	job windows.Handle
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
	cmd.Cancel = func() error { return windows.TerminateJobObject(job, 1) }
	return g, nil
}

func (g *processGroup) started(cmd *exec.Cmd) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	return windows.AssignProcessToJobObject(g.job, process)
}

// close kills what is left of the job and releases it.
func (g *processGroup) close() {
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
	}
}
