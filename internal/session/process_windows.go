//go:build windows

package session

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

type conptyProcess struct{ pty *conpty.ConPty }

func startProcess(o Options) (terminalProcess, error) {
	argv := append([]string{o.Cmd}, o.Args...)
	for i := range argv {
		argv[i] = windows.EscapeArg(argv[i])
	}
	p, err := conpty.Start(strings.Join(argv, " "),
		conpty.ConPtyDimensions(int(o.Cols), int(o.Rows)),
		conpty.ConPtyWorkDir(o.Dir),
		conpty.ConPtyEnv(append(os.Environ(), o.Env...)))
	if err != nil {
		return nil, err
	}
	return &conptyProcess{pty: p}, nil
}

func (p *conptyProcess) Read(b []byte) (int, error)  { return p.pty.Read(b) }
func (p *conptyProcess) Write(b []byte) (int, error) { return p.pty.Write(b) }
func (p *conptyProcess) Resize(rows, cols uint16) error {
	return p.pty.Resize(int(cols), int(rows))
}
func (p *conptyProcess) Kill() error {
	proc, err := os.FindProcess(p.pty.Pid())
	if err != nil {
		return err
	}
	return proc.Kill()
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
func (p *conptyProcess) Close() error { return p.pty.Close() }
