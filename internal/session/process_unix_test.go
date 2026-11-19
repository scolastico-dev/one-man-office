//go:build !windows

package session

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	var pid int
	waitFor(t, 5*time.Second, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && pid > 1
	})
	return pid
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	waitFor(t, 5*time.Second, func() bool {
		err := syscall.Kill(pid, 0)
		return errors.Is(err, syscall.ESRCH)
	})
}

func TestKillReapsBackgroundProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s, err := Start(Options{
		Cmd: "sh", Args: []string{"-c", `sleep 60 & echo $! > "$1"; wait`, "sh", pidFile},
		LogPath: filepath.Join(t.TempDir(), "agent.log"), Rows: 24, Cols: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	endSession(t, s)
	childPID := waitForPIDFile(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })
	if err := s.Kill(); err != nil {
		t.Fatal(err)
	}
	<-s.Done()
	waitForProcessExit(t, childPID)
}

// Resizing and process exit can reach the same PTY descriptor together.
// The race detector checks that ioctl cannot access a descriptor as it closes.
func TestProcessResizeIsSafeDuringClose(t *testing.T) {
	p, err := startProcess(Options{Cmd: "cat", Dir: t.TempDir(), Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill(); _ = p.Close(); _ = p.Wait() })
	if err := p.Resize(31, 99); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	closed := make(chan struct{})
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			<-ready
			for {
				select {
				case <-closed:
					return
				default:
					_ = p.Resize(32, 100)
				}
			}
		})
	}
	workers.Go(func() { <-ready; _ = p.Close(); close(closed) })
	close(ready)
	workers.Wait()
	if err := p.Resize(33, 101); err == nil {
		t.Fatal("resize succeeded after the PTY closed")
	}
}

// An in-flight Read can retain the descriptor after File.Close returns because
// pty's File.Fd call switched it to blocking mode. Resize must respect the
// process's closed state even while that descriptor is still valid in the OS.
func TestClosedProcessCannotResizeDescriptorHeldByRead(t *testing.T) {
	p, err := startProcess(Options{Cmd: "cat", Dir: t.TempDir(), Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill(); _ = p.Close(); _ = p.Wait() })
	if err := p.Resize(31, 99); err != nil {
		t.Fatal(err)
	}
	raw, err := p.(*ptyProcess).ptmx.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	reading := make(chan struct{})
	release := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		readDone <- raw.Read(func(uintptr) bool {
			close(reading)
			<-release
			return true
		})
	}()
	t.Cleanup(func() {
		close(release)
		err := <-readDone
		if err != nil {
			t.Error(err)
		}
	})
	<-reading
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Resize(33, 101); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("resize after close = %v, want os.ErrClosed", err)
	}
}
