//go:build windows

package plugins

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type luaCommandLifecycle struct {
	job windows.Handle
}

func configureLuaCommandCancellation(cmd *exec.Cmd) (*luaCommandLifecycle, error) {
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
	lifecycle := &luaCommandLifecycle{job: job}
	cmd.Cancel = func() error { return windows.TerminateJobObject(job, 1) }
	return lifecycle, nil
}

func (p *luaCommandLifecycle) Started(cmd *exec.Cmd) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	return windows.AssignProcessToJobObject(p.job, process)
}

func (p *luaCommandLifecycle) Close() {
	if p.job != 0 {
		_ = windows.CloseHandle(p.job)
		p.job = 0
	}
}
