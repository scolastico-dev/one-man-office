//go:build !windows

package webterm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func waitOutput(t *testing.T, i *Terminal, match string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	initial, stream, detach := i.subscribe()
	defer detach()
	output := bytes.NewBuffer(initial.replay)
	for !strings.Contains(output.String(), match) {
		select {
		case chunk, ok := <-stream:
			if !ok {
				t.Fatalf("terminal ended before %q: %q", match, output.String())
			}
			output.Write(chunk)
		case <-ctx.Done():
			t.Fatalf("missing output %q: %q", match, output.String())
		}
	}
}

func TestTerminalInstancesKeepIndependentInputAndResize(t *testing.T) {
	// An interactive fixture must not source the developer's shell startup
	// files. Keep the inherited choices hostile to guard this boundary.
	startup := filepath.Join(t.TempDir(), "shell-startup")
	if err := os.WriteFile(startup, []byte("exit 93\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/nonexistent/fish")
	t.Setenv("ENV", startup)
	t.Setenv("BASH_ENV", startup)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "SHELL=/bin/sh"}
	a, err := Start(Options{ID: "a", Command: "/bin/sh", Args: []string{"-i"}, Dir: t.TempDir(), Env: env})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Kill()
	b, err := Start(Options{ID: "b", Command: "/bin/sh", Args: []string{"-i"}, Dir: t.TempDir(), Env: env})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Kill()
	if err := a.resize(37, 101); err != nil {
		t.Fatal(err)
	}
	if err := a.input([]byte("printf 'one-'; printf 'only\\n'; stty size\n")); err != nil {
		t.Fatal(err)
	}
	if err := b.input([]byte("printf 'two-'; printf 'only\\n'\n")); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, a, "one-only")
	waitOutput(t, a, "37 101")
	waitOutput(t, b, "two-only")
	initial, _, detach := b.subscribe()
	detach()
	if bytes.Contains(initial.replay, []byte("one-only")) {
		t.Fatal("terminal output leaked across instances")
	}
	if err := a.resize(0, 200); err == nil {
		t.Fatal("accepted invalid dimensions")
	}
	if err := a.Kill(); err != nil {
		t.Fatal(err)
	}
	<-a.done
	if a.Info().State != "exited" {
		t.Fatal("instance remained running")
	}
	if b.Info().State != "running" {
		t.Fatal("killing one instance stopped another")
	}
}

func TestForcedKillStopsChildProcessTree(t *testing.T) {
	i, err := Start(Options{ID: "tree", Command: "sh", Args: []string{"-c", "sleep 300 & echo CHILD:$!; wait"}, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer i.Kill()
	waitOutput(t, i, "\n")
	initial, _, detach := i.subscribe()
	detach()
	line := strings.Split(strings.Split(string(initial.replay), "CHILD:")[1], "\r")[0]
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Kill(); err != nil {
		t.Fatal(err)
	}
	<-i.done
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		stat, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil || strings.Contains(string(stat), ") Z ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("descendant survived forced kill")
}

func TestForcedKillFreezesRootBeforeDescendantSnapshot(t *testing.T) {
	realPS, err := exec.LookPath("ps")
	if err != nil {
		t.Skip("ps unavailable")
	}
	i, err := Start(Options{ID: "freeze", Command: "sh", Args: []string{"-c", "sleep 300 & echo READY; wait"}, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer i.Kill()
	waitOutput(t, i, "READY")
	pid := i.process.(*ptyProcess).cmd.Process.Pid
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state")
	probe := fmt.Sprintf("#!/bin/sh\n%q -o stat= -p %d > %q\nexec %q \"$@\"\n", realPS, pid, statePath, realPS)
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(probe), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := i.Kill(); err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), "T") {
		t.Fatalf("root was still runnable while taking descendant snapshot: %q", state)
	}
}

func TestTerminalProcessCloseIsIdempotent(t *testing.T) {
	p, err := startProcess("sh", []string{"-c", "sleep 300"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Kill()
	_ = p.Kill()
	_ = p.Wait()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestServeWebSocketEchoesThroughPTY(t *testing.T) {
	term, err := Start(Options{ID: "t1", Command: "cat", Dir: t.TempDir(), Env: CleanEnvironment(os.Environ())})
	if err != nil {
		t.Fatal(err)
	}
	defer term.Kill()
	server := serveTerminal(t, term)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/terminal", &websocket.DialOptions{Subprotocols: []string{"omo"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageBinary, []byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	var output []byte
	acked := false
	for !acked || !strings.Contains(string(output), "hello") {
		kind, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v (output %q, acked %v)", err, output, acked)
		}
		switch kind {
		case websocket.MessageBinary:
			output = append(output, data...)
		case websocket.MessageText:
			if string(data) == replayEnd {
				continue
			}
			if string(data) != `{"type":"input-ack"}` {
				t.Fatalf("unexpected text frame %q", data)
			}
			acked = true
		}
	}
	if err := term.Kill(); err != nil {
		t.Fatal(err)
	}
	<-term.Done()
}

func TestServeWebSocketRejectsWhenLimitIsFull(t *testing.T) {
	term := Own("limit", terminalFixture(t), nil)
	limit := NewConnectionLimit(1)
	limit.slots <- struct{}{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		term.ServeWebSocket(r.Context(), w, r, limit)
	}))
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// Resizing and process exit can reach the same PTY descriptor together.
// The race detector checks that ioctl cannot access a descriptor as it closes.
func TestProcessResizeIsSafeDuringClose(t *testing.T) {
	p, err := startProcess("cat", nil, t.TempDir(), nil)
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
	p, err := startProcess("cat", nil, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Kill(); _ = p.Close(); _ = p.Wait() })
	if err := p.Resize(31, 99); err != nil {
		t.Fatal(err)
	}
	raw, err := p.(*ptyProcess).terminal.SyscallConn()
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
