//go:build !windows

package cli

import (
	"context"
	"os"
	"syscall"
	"testing"
)

// TestStopSignals: the first interrupt ends the context (a graceful stop),
// the second closes hurry (the stop goes on without waiting for handoffs).
func TestStopSignals(t *testing.T) {
	ctx, hurry, stop := stopSignals(context.Background())
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	select {
	case <-hurry:
		t.Fatal("the first signal hurried the stop")
	default:
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	<-hurry
}
