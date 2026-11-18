package processtree

import (
	"bytes"
	"errors"
	"os/exec"
)

// Output and CombinedOutput retain os/exec's output and exit-error contract
// while routing the actual creation through Group.Start.
func Output(cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	capture := cmd.Stderr == nil
	stderr := &stderrPrefix{remaining: 32 << 10}
	if capture {
		cmd.Stderr = stderr
	}
	err := Run(cmd)
	if capture {
		if exit, ok := err.(*exec.ExitError); ok {
			exit.Stderr = stderr.Bytes()
		}
	}
	return out.Bytes(), err
}
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if cmd.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := Run(cmd)
	return out.Bytes(), err
}

// exec.Cmd.Output also bounds captured stderr. Retain a bounded diagnostic
// prefix here; stdout keeps the normal Output contract.
type stderrPrefix struct {
	bytes.Buffer
	remaining int
}

func (w *stderrPrefix) Write(p []byte) (int, error) {
	n := len(p)
	keep := min(n, w.remaining)
	_, _ = w.Buffer.Write(p[:keep])
	w.remaining -= keep
	return n, nil
}
