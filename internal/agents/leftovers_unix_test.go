//go:build !windows

// These tests start helper processes in process groups of their own and
// signal them, so they are built for Unix only.

package agents

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// leftover starts a helper process in its own process group, the way an
// agent from a crashed omo run would still be running, and reaps it in the
// background.
func leftover(t *testing.T, helper string) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"="+helper)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
	})
	if helper == "stubborn" {
		// Register cleanup before waiting for its signal-handler readiness.
		ready := make(chan error, 1)
		go func() { _, err := bufio.NewReader(stdout).ReadString('\n'); ready <- err }()
		select {
		case err := <-ready:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("helper did not install its signal handler")
		}
	}
	return cmd, exited
}

func record(t *testing.T, runDir, key string, pid int, start string) string {
	t.Helper()
	if err := writeRecord(runDir, processRecord{Key: key, PID: pid, PGID: pid, Start: start}); err != nil {
		t.Fatal(err)
	}
	return recordPath(runDir, key)
}

func gone(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}

func TestEndLeftoversVerifiesBeforeSignalling(t *testing.T) {
	runDir := t.TempDir()

	polite, politeExited := leftover(t, "sleep")
	politeStart, err := processStartTime(polite.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	politeRecord := record(t, runDir, "polite", polite.Process.Pid, politeStart)

	stubborn, stubbornExited := leftover(t, "stubborn")
	stubbornStart, err := processStartTime(stubborn.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	stubbornRecord := record(t, runDir, "stubborn", stubborn.Process.Pid, stubbornStart)

	// A recycled process id: the process with that id started at another
	// time than the recorded one. It must not be signalled.
	stranger, strangerExited := leftover(t, "sleep")
	strangerRecord := record(t, runDir, "recycled", stranger.Process.Pid, politeStart+"-earlier")

	// A process that is gone.
	finished := exec.Command("true")
	if err := finished.Run(); err != nil {
		t.Fatal(err)
	}
	goneRecord := record(t, runDir, "gone", finished.Process.Pid, "linux:1")

	ended, err := EndLeftovers(runDir, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if ended != 2 {
		t.Fatalf("ended %d process groups, want 2", ended)
	}
	for name, ch := range map[string]<-chan struct{}{"SIGTERM": politeExited, "SIGKILL after the grace": stubbornExited} {
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatalf("leftover not ended by %s", name)
		}
	}
	select {
	case <-strangerExited:
		t.Fatal("a process with a recycled id was signalled")
	default:
	}
	if err := syscall.Kill(stranger.Process.Pid, 0); err != nil {
		t.Fatalf("recycled-id process: %v", err)
	}
	for _, path := range []string{politeRecord, stubbornRecord, strangerRecord, goneRecord} {
		if !gone(path) {
			t.Errorf("record %s left behind", filepath.Base(path))
		}
	}
	if ended, err := EndLeftovers(runDir, time.Millisecond); ended != 0 || err != nil {
		t.Fatalf("second sweep: %d, %v", ended, err)
	}
	if ended, err := EndLeftovers(filepath.Join(runDir, "missing"), time.Millisecond); ended != 0 || err != nil {
		t.Fatalf("sweep without records: %d, %v", ended, err)
	}
}

func TestStartRecordsProcessUntilItEnds(t *testing.T) {
	isolate(t)
	runDir := t.TempDir()
	a := start(t, &PTYRunner{RunDir: runDir}, testSpec(t, "cat"))
	if w := a.Warnings(); len(w) != 0 {
		t.Fatalf("warnings: %v", w)
	}
	path := recordPath(runDir, a.TerminalID())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testSessionID) {
		t.Fatal("the process record contains the session id")
	}
	records, err := readRecords(runDir)
	if err != nil {
		t.Fatal(err)
	}
	rec := records[path]
	if rec.PID != a.sess.Pid() || rec.PGID != rec.PID || rec.Start == "" || rec.Key != a.TerminalID() {
		t.Fatalf("record = %+v", rec)
	}
	_ = a.Kill()
	<-a.Done()
	// Done is the end of everything the agent kept: whoever waits for it
	// (the engine at its stop, then omo's exit) must find no record left.
	if !gone(path) {
		t.Fatal("the process record is still there when Done is closed")
	}
}
