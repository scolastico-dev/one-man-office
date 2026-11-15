//go:build !windows

package gitops

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDiffTimeout(t *testing.T) {
	// The caller's context ends: git and its children go.
	pidFile := fakeHungGit(t)
	g := New()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := g.runCapped(ctx, t.TempDir(), 1<<20, "diff")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's deadline", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("returned after %s", time.Since(start))
	}
	waitGone(t, pidFile)

	// The package's own timeout: reported as ErrTimeout.
	pidFile = fakeHungGit(t)
	_, _, err = g.runCappedFor(context.Background(), 300*time.Millisecond, nil, inRepo(t.TempDir()), 1<<20, "diff")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	waitGone(t, pidFile)
}
