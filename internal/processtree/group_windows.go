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
	pi, err := startSuspended(cmd)
	if err == nil {
		defer windows.CloseHandle(pi.Process)
		defer windows.CloseHandle(pi.Thread)
		err = g.assignAndResume(pi)
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

// Go closes the primary thread handle before exec.Cmd.Start returns. Since
// CREATE_SUSPENDED prevents user code from running, the newly created
// process has its initial thread only; Toolhelp recovers a handle to it.
func startSuspended(cmd *exec.Cmd) (*windows.ProcessInformation, error) {
	attr := &syscall.SysProcAttr{}
	if cmd.SysProcAttr != nil {
		*attr = *cmd.SysProcAttr
	}
	attr.CreationFlags |= windows.CREATE_SUSPENDED
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil, err
	}
	thread, err := initialThread(uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(process)
		return nil, err
	}
	return &windows.ProcessInformation{Process: process, Thread: thread, ProcessId: uint32(cmd.Process.Pid)}, nil
}

func initialThread(pid uint32) (windows.Handle, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID == pid {
			return windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		}
	}
	return 0, fmt.Errorf("find suspended process thread: %w", err)
}

// assignAndResume is the single containment gate for exec and ConPTY.
// The caller holds mu and terminates the suspended process on failure.
func (g *Group) assignAndResume(pi *windows.ProcessInformation) error {
	if err := windows.AssignProcessToJobObject(g.job, pi.Process); err != nil {
		return fmt.Errorf("assign process job: %w", err)
	}
	count, err := windows.ResumeThread(pi.Thread)
	if err != nil {
		return fmt.Errorf("resume contained process: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("unexpected initial thread suspend count: %d", count)
	}
	return nil
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
