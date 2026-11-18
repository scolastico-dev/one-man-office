//go:build windows

package plugins

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processGroup assigns a suspended process to a kill-on-close job before
// resuming it: even a fast child cannot spawn outside supervision.
// All handle access and pre-registration cancellation are synchronized.
type processGroup struct {
	mu       sync.Mutex
	job      windows.Handle
	process  *os.Process
	assigned bool
	killed   bool
}

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
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	cmd.Cancel = func() error { g.kill(); return nil }
	return g, nil
}
func (g *processGroup) started(cmd *exec.Cmd) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.process = cmd.Process
	if g.killed {
		g.killLocked()
		return nil
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(g.job, proc); err != nil {
		return err
	}
	g.assigned = true
	return resumePrimaryThread(uint32(cmd.Process.Pid))
}
func resumePrimaryThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	return errors.New("the suspended program's primary thread is unavailable")
}
func (g *processGroup) killLocked() {
	g.killed = true
	if g.process != nil && !g.assigned {
		_ = g.process.Kill()
	}
	if g.job != 0 {
		_ = windows.TerminateJobObject(g.job, 1)
	}
}
func (g *processGroup) kill() { g.mu.Lock(); defer g.mu.Unlock(); g.killLocked() }
func (g *processGroup) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.killLocked()
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
	}
}
