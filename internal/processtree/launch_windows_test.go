package processtree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"golang.org/x/sys/windows"
)

// The helper spawns its child before doing any other work. The file is a
// handshake, not an assumption that a delay is long enough to spawn.
func TestLaunchHelper(t *testing.T) {
	args := os.Args
	for i, arg := range args {
		if arg != "--tree-helper" {
			continue
		}
		mode, path := args[i+1], args[i+2]
		if mode == "output" {
			fmt.Fprint(os.Stdout, "stdout")
			fmt.Fprint(os.Stderr, "stderr")
			os.Exit(7)
		}
		if mode == "root" || mode == "exit-root" {
			child := helperCommand("child", path)
			if err := child.Start(); err != nil {
				os.Exit(2)
			}
			if err := os.WriteFile(path, []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
				os.Exit(3)
			}
			if mode == "exit-root" {
				os.Exit(0)
			}
		}
		for {
			time.Sleep(time.Hour)
		}
	}
}

func helperCommand(mode, path string) *exec.Cmd {
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestLaunchHelper$", "--", "--tree-helper", mode, path)
	cmd.Env = testEnvironment()
	return cmd
}

func childHandle(t *testing.T, path string) windows.Handle {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			pid, e := strconv.Atoi(string(b))
			if e == nil {
				h, e := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { windows.TerminateProcess(h, 1); windows.CloseHandle(h) })
				return h
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("root never reported its immediate child")
	return 0
}

// assertNeverRan checks that the process has not executed any user-mode code.
// The first code a new process runs is the loader's initialization in ntdll,
// which sets the loader data pointer (PEB.Ldr); until then it is nil. A process
// created suspended and not resumed has not run, however long ago it was
// created, so no interval is waited and no file is expected to stay absent.
func assertNeverRan(t *testing.T, pid int) {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	var info windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil); err != nil {
		t.Fatal(err)
	}
	var ldr uintptr
	at := uintptr(unsafe.Pointer(info.PebBaseAddress)) + unsafe.Offsetof(windows.PEB{}.Ldr)
	if err := windows.ReadProcessMemory(h, at, (*byte)(unsafe.Pointer(&ldr)), unsafe.Sizeof(ldr), nil); err != nil {
		t.Fatal(err)
	}
	if ldr != 0 {
		t.Fatalf("the program ran: its loader is initialized (PEB.Ldr = %#x)", ldr)
	}
}

func assertEnded(t *testing.T, h windows.Handle) {
	t.Helper()
	status, err := windows.WaitForSingleObject(h, 5000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("immediate descendant escaped cleanup: wait=%d, err=%v", status, err)
	}
}

// This controlled widening of the old Start/Assign window is a reproducer,
// not the launch implementation. It proves late assignment misses the child.
func TestLegacyLateAssignmentEscapes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.pid")
	g, err := New(helperCommand("child", path))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	cmd := helperCommand("root", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()
	child := childHandle(t, path)
	root, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(root)
	if err := windows.AssignProcessToJobObject(g.job, root); err != nil {
		t.Fatal(err)
	}
	g.Close()
	status, err := windows.WaitForSingleObject(child, 100)
	if err != nil || status != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("legacy reproducer did not show escape: wait=%d, err=%v", status, err)
	}
}

func TestImmediateDescendantEndsWithRoot(t *testing.T) {
	for _, mode := range []string{"root", "exit-root"} {
		for _, stop := range []string{"kill", "close"} {
			t.Run(mode+"/"+stop, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "child.pid")
				cmd := helperCommand(mode, path)
				g, err := New(cmd)
				if err != nil {
					t.Fatal(err)
				}
				defer g.Close()
				if err := g.Start(cmd); err != nil {
					t.Fatal(err)
				}
				if err := g.Started(cmd); err != nil {
					t.Fatal(err)
				}
				defer func() { g.Close(); _ = cmd.Wait() }()
				child := childHandle(t, path)
				if mode == "exit-root" {
					if err := cmd.Wait(); err != nil {
						t.Fatal(err)
					}
				}
				if stop == "kill" {
					if err := g.Kill(); err != nil {
						t.Fatal(err)
					}
				} else {
					g.Close()
				}
				assertEnded(t, child)
			})
		}
	}
}

func TestSuspendedLaunchDoesNotExecuteBeforeAssignment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.pid")
	cmd := helperCommand("root", path)
	g, err := New(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	process, err := startSuspended(cmd)
	if err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	defer cmd.Wait()
	defer cmd.Process.Kill()
	// The process must be unable to reach user code during an arbitrarily
	// delayed assignment. Losing CREATE_SUSPENDED makes this test fail: the
	// state is read from the process itself, not inferred from a quiet interval.
	assertNeverRan(t, cmd.Process.Pid)
	if err := g.assignAndResume(process, 0); err != nil {
		t.Fatal(err)
	}
	child := childHandle(t, path)
	g.Close()
	assertEnded(t, child)
}

// A program is resumed as a whole, without looking for its thread. Resuming
// must run it exactly once it is in the job, however many programs start at
// the same time: every root reports its child, and every child ends with
// its job.
func TestConcurrentStartsAllRunContained(t *testing.T) {
	const count = 16
	dir := t.TempDir()
	type started struct {
		g    *Group
		cmd  *exec.Cmd
		path string
		err  error
	}
	results := make(chan started, count)
	for i := 0; i < count; i++ {
		go func() {
			path := filepath.Join(dir, strconv.Itoa(i)+".pid")
			cmd := helperCommand("root", path)
			g, err := New(cmd)
			if err == nil {
				err = g.Start(cmd)
			}
			results <- started{g, cmd, path, err}
		}()
	}
	var all []started
	for i := 0; i < count; i++ {
		s := <-results
		if s.err != nil {
			t.Error(s.err)
			if s.g != nil {
				s.g.Close()
			}
			continue
		}
		all = append(all, s)
		t.Cleanup(func() { s.g.Close(); _ = s.cmd.Wait() })
	}
	for _, s := range all {
		child := childHandle(t, s.path)
		s.g.Close()
		assertEnded(t, child)
	}
}

// resumeProcess must report a refusal: Start relies on its error to kill a
// program that stayed suspended. A handle without the right to resume is
// refused, and the program does not run.
func TestResumeReportsFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.pid")
	cmd := helperCommand("root", path)
	process, err := startSuspended(cmd)
	if cmd.Process != nil {
		defer cmd.Wait()
		defer cmd.Process.Kill()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	limited, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(limited)
	if err := resumeProcess(limited); err == nil {
		t.Fatal("resuming without the right to resume reported success")
	}
	assertNeverRan(t, cmd.Process.Pid)
}

func TestFailedAssignmentNeverExecutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.pid")
	cmd := helperCommand("root", path)
	g, err := New(cmd)
	if err != nil {
		t.Fatal(err)
	}
	// An invalid job forces assignment failure after successful creation.
	windows.CloseHandle(g.job)
	g.job = windows.InvalidHandle
	if err := g.Start(cmd); err == nil {
		t.Fatal("start succeeded without containment")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("uncontained program executed: %v", err)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("failed launch was not reaped")
	}
}

func TestImmediatePTYDescendantEndsWithRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.pid")
	p, err := StartPTY(os.Args[0], []string{"-test.run=^TestLaunchHelper$", "--", "--tree-helper", "root", path}, "", testEnvironment(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Drain console output so the console host cannot block teardown.
	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := p.Read(b); err != nil {
				return
			}
		}
	}()
	child := childHandle(t, path)
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	assertEnded(t, child)
	if _, err := p.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// Windows keeps the output of a pseudoconsole open after its programs
// ended. Once the tree is gone, Hangup must end the output, with everything
// the program wrote before it: a reader that waits for the end of the output
// (an agent session) would otherwise wait for ever.
func TestPTYOutputEndsAfterHangup(t *testing.T) {
	p, err := StartPTY("cmd.exe", []string{"/d", "/c", "echo %OMO_TREE_TEST_VALUE%"}, "", append(testEnvironment(), "OMO_TREE_TEST_VALUE=last words"), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	type result struct {
		output string
		err    error
	}
	read := make(chan result, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := p.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				read <- result{b.String(), err}
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if code, err := p.Wait(ctx); err != nil || code != 0 {
		t.Fatalf("wait: code %d, %v", code, err)
	}
	if err := p.WaitTree(ctx); err != nil {
		t.Fatal(err)
	}
	p.Hangup()
	select {
	case got := <-read:
		if got.err != io.EOF {
			t.Errorf("output ended with %v, want io.EOF", got.err)
		}
		if !strings.Contains(got.output, "last words") {
			t.Errorf("output before the end = %q", got.output)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the output did not end after Hangup")
	}
	if err := p.Resize(100, 30); err == nil {
		t.Error("a closed pseudoconsole was resized")
	}
}

func TestPTYArgumentsEnvironmentAndResize(t *testing.T) {
	// cmd.exe supplies a real console program and a simple environment check.
	p, err := StartPTY("cmd.exe", []string{"/d", "/c", "echo %OMO_TREE_TEST_VALUE%"}, "", append(testEnvironment(), "OMO_TREE_TEST_VALUE=hello tree"), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	output := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := p.Read(buf)
			b.Write(buf[:n])
			if strings.Contains(b.String(), "hello tree") || err != nil {
				output <- b.String()
				return
			}
		}
	}()
	select {
	case got := <-output:
		if !strings.Contains(got, "hello tree") {
			t.Fatal(fmt.Sprintf("environment not passed: %q", got))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no console output")
	}
}

// Helpers must never inherit the live worker's omo identity or socket.
func testEnvironment() []string {
	var env []string
	for _, entry := range childenv.Inherited() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "OMO_") {
			env = append(env, entry)
		}
	}
	return env
}

func TestHelperEnvironmentIsIsolated(t *testing.T) {
	childenvtest.SetOmo(t)
	childenvtest.CheckOmo(t, testEnvironment(), nil)
}

func TestOutputRetainsExitStatus(t *testing.T) {
	cmd := helperCommand("output", "")
	out, err := Output(cmd)
	var exit *exec.ExitError
	if string(out) != "stdout" || !errors.As(err, &exit) || exit.ExitCode() != 7 || string(exit.Stderr) != "stderr" {
		t.Fatalf("output=%q error=%v exit=%+v", out, err, exit)
	}
	// Run also accepts exec.Command, whose Cancel must remain nil.
	cmd = helperCommand("output", "")
	plain := exec.Command(cmd.Path, cmd.Args[1:]...)
	plain.Env = cmd.Env
	out, err = CombinedOutput(plain)
	if !strings.Contains(string(out), "stdout") || !strings.Contains(string(out), "stderr") || !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("combined output=%q error=%v", out, err)
	}
}

func TestCancellationEndsImmediateDescendant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.pid")
	base := helperCommand("root", path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, base.Path, base.Args[1:]...)
	cmd.Env = base.Env
	g, err := New(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if err := g.Start(cmd); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	reaped := false
	defer func() {
		g.Close()
		if !reaped {
			<-waited
		}
	}()
	child := childHandle(t, path)
	cancel()
	select {
	case err := <-waited:
		reaped = true
		if err == nil {
			t.Fatal("cancelled command succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not reap root")
	}
	assertEnded(t, child)
}

func TestUnavailableConPTYReturnsError(t *testing.T) {
	// A missing DLL takes the same Find error path as a missing export on an
	// older Windows version. Calling the generated wrapper directly panics.
	if err := conPTYAvailable(windows.NewLazySystemDLL("omo-nonexistent-conpty-test.dll")); err == nil {
		t.Fatal("missing ConPTY support was accepted")
	}
}
