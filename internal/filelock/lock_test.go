package filelock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLockCoordinatesProcessesAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.lock")
	held, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	child := func(want string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestLockChildProcess$")
		cmd.Env = append(os.Environ(), "OMO_LOCK_TEST_PATH="+path, "OMO_LOCK_TEST_WANT="+want)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v %s", err, out)
		}
	}
	child("timeout")
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	child("acquired")
}

func TestLockChildProcess(t *testing.T) {
	path := os.Getenv("OMO_LOCK_TEST_PATH")
	if path == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	lock, err := Acquire(ctx, path)
	if os.Getenv("OMO_LOCK_TEST_WANT") == "timeout" {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("lock did not wait for owner: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}
