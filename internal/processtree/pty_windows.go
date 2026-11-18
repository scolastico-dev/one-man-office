package processtree

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PTY is a Windows pseudoconsole whose root is contained before it runs.
// It owns the job, console, process handle and the parent ends of the pipes.
type PTY struct {
	group         *Group
	console       windows.Handle
	process       windows.Handle
	pid           int
	input, output *os.File
	mu            sync.Mutex
	closed        bool
	closeErr      error
}

func StartPTY(command string, args []string, dir string, env []string, cols, rows int) (*PTY, error) {
	if err := conPTYAvailable(windows.NewLazySystemDLL("kernel32.dll")); err != nil {
		return nil, err
	}
	g, err := newJob()
	if err != nil {
		return nil, err
	}
	p := &PTY{group: g}
	success := false
	defer func() {
		if !success {
			p.Close()
		}
	}()
	var inRead, inWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, err
	}
	defer windows.CloseHandle(inRead)
	p.input = os.NewFile(uintptr(inWrite), "conpty-input")
	var outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		return nil, err
	}
	defer windows.CloseHandle(outWrite)
	p.output = os.NewFile(uintptr(outRead), "conpty-output")
	if err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inRead, outWrite, 0, &p.console); err != nil {
		return nil, err
	}
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	// Unlike other attributes, PSEUDOCONSOLE takes the handle value itself.
	update := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
	ok, _, callErr := update.Call(uintptr(unsafe.Pointer(attrs.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(p.console), unsafe.Sizeof(p.console), 0, 0)
	if ok == 0 {
		return nil, callErr
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{command}, args...)))
	if err != nil {
		return nil, err
	}
	var cwd *uint16
	if dir != "" {
		cwd, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			return nil, err
		}
	}
	block, err := environmentBlock(env)
	if err != nil {
		return nil, err
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(nil, line, nil, nil, false, flags, &block[0], cwd, &si.StartupInfo, &pi); err != nil {
		return nil, fmt.Errorf("create console process: %w", err)
	}
	defer windows.CloseHandle(pi.Thread)
	p.process = pi.Process
	p.pid = int(pi.ProcessId)
	g.mu.Lock()
	err = g.assignAndResume(&pi)
	g.mu.Unlock()
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		return nil, err
	}
	success = true
	return p, nil
}

func environmentBlock(env []string) ([]uint16, error) {
	var block []uint16
	for _, entry := range env {
		if strings.ContainsRune(entry, 0) {
			return nil, fmt.Errorf("environment contains NUL")
		}
		value, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, value...)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func (p *PTY) Pid() int                    { return p.pid }
func (p *PTY) Read(b []byte) (int, error)  { return p.output.Read(b) }
func (p *PTY) Write(b []byte) (int, error) { return p.input.Write(b) }
func (p *PTY) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return os.ErrClosed
	}
	return windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}
func (p *PTY) Kill() error { return p.group.Kill() }
func (p *PTY) Wait(ctx context.Context) (uint32, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, os.ErrClosed
	}
	var process windows.Handle
	err := windows.DuplicateHandle(windows.CurrentProcess(), p.process, windows.CurrentProcess(), &process, 0, false, windows.DUPLICATE_SAME_ACCESS)
	p.mu.Unlock()
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(process)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		status, err := windows.WaitForSingleObject(process, 100)
		if err != nil {
			return 0, err
		}
		if status == windows.WAIT_OBJECT_0 {
			var code uint32
			err := windows.GetExitCodeProcess(process, &code)
			return code, err
		}
	}
}
func (p *PTY) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return p.closeErr
	}
	p.closed = true
	// Kill descendants before closing the console, so no descendant can keep
	// console handles alive. The consumer drains output concurrently.
	p.group.Close()
	if p.console != 0 {
		windows.ClosePseudoConsole(p.console)
	}
	if p.input != nil {
		p.closeErr = p.input.Close()
	}
	if p.output != nil {
		if err := p.output.Close(); p.closeErr == nil {
			p.closeErr = err
		}
	}
	if p.process != 0 {
		if err := windows.CloseHandle(p.process); p.closeErr == nil {
			p.closeErr = err
		}
	}
	return p.closeErr
}

// The generated x/sys wrappers panic if an export is absent. Keep terminal
// startup an ordinary error on Windows versions without pseudoconsole APIs.
func conPTYAvailable(kernel32 *windows.LazyDLL) error {
	for _, name := range []string{"CreatePseudoConsole", "ResizePseudoConsole", "ClosePseudoConsole", "InitializeProcThreadAttributeList", "UpdateProcThreadAttribute"} {
		if err := kernel32.NewProc(name).Find(); err != nil {
			return fmt.Errorf("Windows pseudoconsole is unavailable: %w", err)
		}
	}
	return nil
}
