package processtree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	handles, err := startSuspended(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handles.Process)
	defer windows.CloseHandle(handles.Thread)
	defer cmd.Wait()
	defer cmd.Process.Kill()
	// The process must be unable to reach user code during an arbitrarily
	// delayed assignment. Losing CREATE_SUSPENDED makes this test fail.
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("program ran before containment: %v", err)
	}
	if err := g.assignAndResume(handles); err != nil {
		t.Fatal(err)
	}
	child := childHandle(t, path)
	g.Close()
	assertEnded(t, child)
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
