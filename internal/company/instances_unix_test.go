//go:build !windows

package company

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitOutput(t *testing.T, i *Instance, match string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	initial, stream, detach := i.subscribe()
	defer detach()
	output := bytes.NewBuffer(initial)
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
	a, err := startInstance("a", "/tmp", "shell", "sh", []string{"-i"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.kill()
	b, err := startInstance("b", "/tmp", "shell", "sh", []string{"-i"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.kill()
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
	if bytes.Contains(initial, []byte("one-only")) {
		t.Fatal("terminal output leaked across instances")
	}
	if err := a.resize(0, 200); err == nil {
		t.Fatal("accepted invalid dimensions")
	}
	if err := a.kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.done:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}
	if a.snapshot().State != "exited" {
		t.Fatal("instance remained running")
	}
	if b.snapshot().State != "running" {
		t.Fatal("killing one instance stopped another")
	}
}

func TestForcedKillStopsChildProcessTree(t *testing.T) {
	i, err := startInstance("tree", t.TempDir(), "shell", "sh", []string{"-c", "sleep 300 & echo CHILD:$!; wait"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer i.kill()
	waitOutput(t, i, "CHILD:")
	initial, _, detach := i.subscribe()
	detach()
	line := strings.Split(strings.Split(string(initial), "CHILD:")[1], "\r")[0]
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	if err := i.kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-i.done:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}
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
	i, err := startInstance("freeze", t.TempDir(), "shell", "sh", []string{"-c", "sleep 300 & echo READY; wait"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer i.kill()
	waitOutput(t, i, "READY")
	pid := i.process.(*ptyProcess).cmd.Process.Pid
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state")
	probe := fmt.Sprintf("#!/bin/sh\n%q -o stat= -p %d > %q\nexec %q \"$@\"\n", realPS, pid, statePath, realPS)
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(probe), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := i.kill(); err != nil {
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
