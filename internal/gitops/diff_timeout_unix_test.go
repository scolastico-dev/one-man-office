//go:build !windows

package gitops

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDiffTimeout(t *testing.T) {
	// The caller's context ends: git and its children go.
	fakeHungGit(t)
	g := New()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	dir := t.TempDir()
	err := returns(t, "diff cancellation", func() error { _, _, err := g.runCapped(ctx, dir, 1<<20, "diff"); return err })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's deadline", err)
	}

	// The package's own timeout: reported as ErrTimeout.
	fakeHungGit(t)
	_, _, err = g.runCappedFor(context.Background(), 300*time.Millisecond, nil, inRepo(t.TempDir()), 1<<20, "diff")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestCanceledDiffKillsChildren(t *testing.T) {
	pidFile := fakeHungGit(t)
	g := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	dir := t.TempDir()
	go func() { _, _, err := g.runCapped(ctx, dir, 1<<20, "diff"); result <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if raw, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(raw)) != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("diff did not start its child")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	err := returns(t, "diff cancellation", func() error { return <-result })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want cancellation", err)
	}
	waitGone(t, pidFile)
}
