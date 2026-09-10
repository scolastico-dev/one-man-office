//go:build windows

package company

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type commandLifecycle struct {
	job windows.Handle
}

func configureCommandCancellation(cmd *exec.Cmd) (*commandLifecycle, error) {
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
	lifecycle := &commandLifecycle{job: job}
	cmd.Cancel = func() error { return windows.TerminateJobObject(job, 1) }
	return lifecycle, nil
}

func (p *commandLifecycle) Started(cmd *exec.Cmd) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	return windows.AssignProcessToJobObject(p.job, process)
}

func (p *commandLifecycle) Close() {
	if p.job != 0 {
		_ = windows.CloseHandle(p.job)
		p.job = 0
	}
}
