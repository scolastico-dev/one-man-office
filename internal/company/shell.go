package company

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/scolastico-dev/one-man-office/internal/company/controlplane"
)

// RunShell is the managed shell child. A second PTY keeps control characters
// directed at the shell, while this wrapper watches the parent's liveness.
// No control credentials are inherited by the interactive shell or its commands.
func RunShell(ctx context.Context) error {
	client, err := controlplane.ClientFromEnv()
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("company-shell requires a company control channel")
	}
	if err := client.Ping(ctx); err != nil {
		return err
	}
	state, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		return err
	}
	defer term.Restore(os.Stdin.Fd(), state)
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	command, args := shellCommand()
	p, err := startProcess(command, args, dir, cleanEnvironment())
	if err != nil {
		return err
	}
	defer p.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go client.Watch(ctx, func(error) { _ = p.Kill() })
	go func() { _, _ = io.Copy(p, os.Stdin); _ = p.Kill() }()
	outputDone := make(chan struct{})
	go func() { defer close(outputDone); _, _ = io.Copy(os.Stdout, p) }()
	go func() {
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		var previousRows, previousCols int
		for {
			cols, rows, err := term.GetSize(os.Stdin.Fd())
			if err == nil && rows > 0 && cols > 0 && (previousRows != rows || previousCols != cols) {
				_ = p.Resize(uint16(rows), uint16(cols))
				previousRows, previousCols = rows, cols
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	err = p.Wait()
	cancel()
	select {
	case <-outputDone:
	case <-time.After(terminalDrainGrace):
	}
	_ = p.Close()
	<-outputDone
	return err
}
