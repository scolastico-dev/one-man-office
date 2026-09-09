//go:build windows

package session

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"unsafe"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

type conptyProcess struct {
	pty      *conpty.ConPty
	job      windows.Handle
	mu       sync.Mutex
	closed   bool
	closeErr error
}

func startProcess(o Options) (terminalProcess, error) {
	argv := append([]string{o.Cmd}, o.Args...)
	for i := range argv {
		argv[i] = windows.EscapeArg(argv[i])
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	p, err := conpty.Start(strings.Join(argv, " "),
		conpty.ConPtyDimensions(int(o.Cols), int(o.Rows)),
		conpty.ConPtyWorkDir(o.Dir),
		conpty.ConPtyEnv(processEnvironment(o.Env)))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid()))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, proc)
		windows.CloseHandle(proc)
	}
	if err != nil {
		p.Close()
		windows.CloseHandle(job)
		return nil, err
	}
	return &conptyProcess{pty: p, job: job}, nil
}

func (p *conptyProcess) Read(b []byte) (int, error)  { return p.pty.Read(b) }
func (p *conptyProcess) Write(b []byte) (int, error) { return p.pty.Write(b) }
func (p *conptyProcess) Resize(rows, cols uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return os.ErrClosed
	}
	return p.pty.Resize(int(cols), int(rows))
}
func (p *conptyProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	return windows.TerminateJobObject(p.job, 1)
}
func (p *conptyProcess) Wait() error {
	code, err := p.pty.Wait(context.Background())
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("process exited with code %d", code)
	}
	return nil
}
func (p *conptyProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		// Job closure kills descendants before ConPTY closes its pipes.
		_ = windows.CloseHandle(p.job)
		p.closeErr = p.pty.Close()
	}
	return p.closeErr
}
