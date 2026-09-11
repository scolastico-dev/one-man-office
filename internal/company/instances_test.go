package company

import (
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/office"
)

// controlledTerminal exposes the two OS timing boundaries exercised here:
// a child which does not consume input, and output still readable after Wait.
type controlledTerminal struct {
	readReady    chan struct{}
	exited       chan struct{}
	closed       chan struct{}
	writeStarted chan struct{}
	output       *strings.Reader
	waitErr      error
	closeOnce    sync.Once
}

func (p *controlledTerminal) Read(b []byte) (int, error) {
	select {
	case <-p.closed:
		return 0, io.EOF
	case <-p.readReady:
		return p.output.Read(b)
	}
}
func (p *controlledTerminal) Write(b []byte) (int, error) {
	select {
	case p.writeStarted <- struct{}{}:
	default:
	}
	<-p.closed
	return 0, io.ErrClosedPipe
}
func (p *controlledTerminal) Resize(uint16, uint16) error { return nil }
func (p *controlledTerminal) Wait() error                 { <-p.exited; return p.waitErr }
func (p *controlledTerminal) Kill() error                 { return p.Close() }
func (p *controlledTerminal) Close() error                { p.closeOnce.Do(func() { close(p.closed) }); return nil }
func terminalFixture() *controlledTerminal {
	return &controlledTerminal{readReady: make(chan struct{}), exited: make(chan struct{}), closed: make(chan struct{}), writeStarted: make(chan struct{}, 1), output: strings.NewReader("last output\n")}
}

func TestBlockedTerminalInputIsBoundedAndDoesNotBlockResize(t *testing.T) {
	p := terminalFixture()
	i := ownInstance("input", "/project", "shell", p, nil)
	defer p.Close()
	defer close(p.exited)
	accepted := make(chan error, 1)
	go func() { accepted <- i.input([]byte("first")) }()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("browser blocked on terminal write")
	}
	<-p.writeStarted
	full := false
	for range 100 {
		if err := i.input([]byte("queued")); err != nil {
			full = true
			break
		}
	}
	if !full {
		t.Fatal("terminal input queue is unbounded")
	}
	resized := make(chan error, 1)
	go func() { resized <- i.resize(30, 100) }()
	select {
	case err := <-resized:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal write blocked resize")
	}
}

func TestProcessExitDrainsFinalTerminalOutput(t *testing.T) {
	p := terminalFixture()
	i := ownInstance("tail", "/project", "shell", p, nil)
	defer p.Close()
	close(p.exited)
	// The process has exited, but its terminal reader has not yet run.
	time.AfterFunc(20*time.Millisecond, func() { close(p.readReady) })
	select {
	case <-i.done:
	case <-time.After(2 * time.Second):
		t.Fatal("exit did not finish")
	}
	output, _, detach := i.subscribe()
	defer detach()
	if string(output) != "last output\n" {
		t.Fatalf("final output was lost: %q", output)
	}
}

func TestProcessExitBoundsDrainForInheritedTerminalHandles(t *testing.T) {
	p := terminalFixture()
	i := ownInstance("drain", "/project", "shell", p, nil)
	defer p.Close()
	close(p.exited)
	select {
	case <-i.done:
	case <-time.After(2 * time.Second):
		t.Fatal("inherited terminal handle blocked exit forever")
	}
}

func TestProcessExitCallbackReceivesWaitResult(t *testing.T) {
	p := terminalFixture()
	called := make(chan error, 1)
	_ = ownInstance("callback", "/project", "setup", p, func(_ *Instance, err error) { called <- err })
	defer p.Close()
	close(p.exited)
	select {
	case err := <-called:
		if err != nil {
			t.Fatalf("wait result = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exit callback was not called")
	}
}

func TestSetupExitNonzeroSetsExactInspectableError(t *testing.T) {
	p := terminalFixture()
	cmd := exec.Command("sh", "-c", "exit 7")
	if err := cmd.Run(); err == nil {
		t.Fatal("exit helper unexpectedly succeeded")
	} else {
		p.waitErr = err
	}
	i := ownInstance("failed-setup", "/project", "setup", p, finishProjectSetup)
	defer p.Close()
	close(p.exited)
	select {
	case <-i.done:
	case <-time.After(2 * time.Second):
		t.Fatal("setup did not exit")
	}
	if got := i.snapshot().Error; got != "Setup exited with status 7; inspect the terminal output" {
		t.Fatalf("setup error = %q", got)
	}
}

func TestSetupExitZeroTrustsCanonicalDestination(t *testing.T) {
	dir := projectHome(t)
	destination := filepath.Join(dir, "new-office")
	if _, err := office.SetupWithAgentCLI(destination, agentcli.Claude); err != nil {
		t.Fatal(err)
	}
	p := terminalFixture()
	i := ownInstance("successful-setup", destination, "setup", p, finishProjectSetup)
	defer p.Close()
	close(p.exited)
	select {
	case <-i.done:
	case <-time.After(2 * time.Second):
		t.Fatal("setup did not exit")
	}
	projects, err := Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Path != destination {
		t.Fatalf("trusted projects: %+v", projects)
	}
}
