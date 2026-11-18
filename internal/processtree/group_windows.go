package processtree

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Group owns a non-inheritable kill-on-close job. The mutex orders launch,
// cancellation and closure; cancellation cannot miss a suspended child.
type Group struct {
	mu  sync.Mutex
	job windows.Handle
}

func New(cmd *exec.Cmd) (*Group, error) {
	g, err := newJob()
	if err != nil {
		return nil, err
	}
	cmd.Cancel = g.Kill
	return g, nil
}

func newJob() (*Group, error) {
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
	return &Group{job: job}, nil
}

// Start creates a suspended process and never returns a running process that
// is outside its job. Any failure kills and reaps the suspended root.
func (g *Group) Start(cmd *exec.Cmd) error {
	g.mu.Lock()
	if g.job == 0 {
		g.mu.Unlock()
		return fmt.Errorf("process job is closed")
	}
	process, err := startSuspended(cmd)
	if err == nil {
		err = g.assignAndResume(process, 0)
		windows.CloseHandle(process)
	}
	// Wait may wait for exec's cancellation goroutine, which calls Kill.
	// Release mu before reaping so that goroutine can finish.
	g.mu.Unlock()
	if err != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return err
}

// startSuspended starts cmd with every thread suspended and returns a handle
// that may join it to a job, resume it and end it. A failure after creation
// leaves cmd.Process set: the caller kills and reaps it.
func startSuspended(cmd *exec.Cmd) (windows.Handle, error) {
	attr := &syscall.SysProcAttr{}
	if cmd.SysProcAttr != nil {
		*attr = *cmd.SysProcAttr
	}
	attr.CreationFlags |= windows.CREATE_SUSPENDED
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	// cmd.Process keeps its own handle, so the id cannot name another process.
	return windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid))
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// resumeProcess resumes every thread of a process once. Go closes the
// primary thread handle before exec.Cmd.Start returns, so a process started
// through os/exec is resumed as a whole. Looking its thread up again in a
// snapshot of all threads of the system costs time that grows with the load
// of the machine, on every program start, and can pick a thread somebody
// else put into the still suspended process.
func resumeProcess(process windows.Handle) error {
	if err := ntResumeProcess.Find(); err != nil {
		return err
	}
	// An NTSTATUS has 32 bits; the rest of the register is not defined.
	status, _, _ := ntResumeProcess.Call(uintptr(process))
	if uint32(status) != 0 {
		return windows.NTStatus(uint32(status))
	}
	return nil
}

// assignAndResume is the single containment gate for exec and ConPTY: the
// process joins the job first and only then runs. thread is the primary
// thread where the caller still has it, or 0 to resume the whole process.
// The caller holds mu and terminates the suspended process on failure.
func (g *Group) assignAndResume(process, thread windows.Handle) error {
	if err := windows.AssignProcessToJobObject(g.job, process); err != nil {
		return fmt.Errorf("assign process job: %w", err)
	}
	if thread == 0 {
		if err := resumeProcess(process); err != nil {
			return fmt.Errorf("resume contained process: %w", err)
		}
		return nil
	}
	count, err := windows.ResumeThread(thread)
	if err != nil {
		return fmt.Errorf("resume contained process: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("unexpected initial thread suspend count: %d", count)
	}
	return nil
}

// jobAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// active is the number of processes still running in the job; a closed job
// has none.
func (g *Group) active() (uint32, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return 0, nil
	}
	var info jobAccounting
	if err := windows.QueryInformationJobObject(g.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return 0, err
	}
	return info.ActiveProcesses, nil
}

func (g *Group) Started(*exec.Cmd) error { return nil }
func (g *Group) Kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(g.job, 1)
}
func (g *Group) Close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
	}
}

func Run(cmd *exec.Cmd) error {
	cancelable := cmd.Cancel != nil
	g, err := New(cmd)
	if err != nil {
		return err
	}
	if !cancelable {
		cmd.Cancel = nil
	}
	defer g.Close()
	if err := g.Start(cmd); err != nil {
		return err
	}
	return cmd.Wait()
}
