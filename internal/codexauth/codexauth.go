// Package codexauth refreshes stale Codex OAuth credentials by briefly running
// the Codex CLI in a private PTY. Codex rewrites its own auth.json on startup
// when the stored access token has expired; omo only waits for that write and
// then stops the process. omo never writes the credential file.
package codexauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

// ErrUnchanged reports that the CLI exited or the deadline passed before the
// credential file changed.
var ErrUnchanged = errors.New("codex did not refresh its credentials")

// MaxAuthFile is the largest credential file Refresh compares.
const MaxAuthFile = 1 << 20

const (
	defaultTimeout = 45 * time.Second
	pollInterval   = 200 * time.Millisecond
	stopGrace      = 5 * time.Second
)

// Refresher launches a command with no arguments so the interactive TUI
// performs its own token refresh without receiving a prompt.
type Refresher struct {
	// Shell replaces the launched argv; tests use it to stand in for Codex.
	Shell []string
	// Env adds literal environment entries after env.
	Env map[string]string
	// Timeout bounds one refresh attempt; zero selects the default.
	Timeout time.Duration
}

var (
	refreshMu    sync.Mutex
	refreshPaths = map[string]*sync.Mutex{}
)

func pathLock(path string) *sync.Mutex {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	mu := refreshPaths[path]
	if mu == nil {
		mu = &sync.Mutex{}
		refreshPaths[path] = mu
	}
	return mu
}

// Refresh runs command (the profile's own program, without arguments) with
// env appended to omo's environment until authPath changes, then stops it.
// Concurrent refreshes of one credential file are serialized within the
// process. The credential file is only read (through homefile.Read, so a
// link, a pipe or a file over MaxAuthFile never counts as a change), never
// written. Error texts name no path and no file content.
func (r Refresher) Refresh(ctx context.Context, command string, env []string, authPath string) error {
	authPath = filepath.Clean(authPath)
	mu := pathLock(authPath)
	if !lockContext(ctx, mu) {
		return fmt.Errorf("codex credential refresh: %w", ctx.Err())
	}
	defer mu.Unlock()

	before, _ := homefile.Read(authPath, MaxAuthFile)
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	argv := r.Shell
	if len(argv) == 0 {
		if command == "" {
			return errors.New("codex credential refresh: the profile has no command")
		}
		argv = []string{command}
	}
	workdir, err := os.MkdirTemp("", "omo-codex-refresh-")
	if err != nil {
		return fmt.Errorf("codex credential refresh: %w", err)
	}
	defer os.RemoveAll(workdir)

	s, err := session.Start(session.Options{
		Cmd:     argv[0],
		Args:    append([]string(nil), argv[1:]...),
		Env:     r.environment(env),
		Dir:     workdir,
		LogPath: filepath.Join(workdir, "refresh.log"),
		Rows:    40,
		Cols:    120,
	})
	if err != nil {
		return errors.New("codex credential refresh: the command could not be started")
	}
	defer func() {
		_ = s.Kill()
		select {
		case <-s.Done():
		case <-time.After(stopGrace):
		}
	}()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	changed := func() bool {
		after, err := homefile.Read(authPath, MaxAuthFile)
		return err == nil && len(after) > 0 && !bytes.Equal(before, after)
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("codex credential refresh: %w", ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("codex credential refresh timed out after %s: %w", timeout, ErrUnchanged)
		case <-s.Done():
			if changed() {
				return nil
			}
			return fmt.Errorf("codex credential refresh: the command exited: %w", ErrUnchanged)
		case <-ticker.C:
			if changed() {
				return nil
			}
		}
	}
}

// lockContext takes mu, or gives up when ctx ends first.
func lockContext(ctx context.Context, mu *sync.Mutex) bool {
	for {
		if mu.TryLock() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// environment is env plus a TERM for the PTY when env sets none, then r.Env.
func (r Refresher) environment(env []string) []string {
	out := append(make([]string, 0, len(env)+len(r.Env)+1), env...)
	hasTerm := false
	for _, entry := range env {
		if key, _, _ := strings.Cut(entry, "="); key == "TERM" {
			hasTerm = true
		}
	}
	if !hasTerm {
		// The refresh runs on a real PTY even when omo itself was started
		// without a terminal; Codex refuses to start behind a dumb TERM.
		out = append(out, "TERM=xterm-256color")
	}
	for key, value := range r.Env {
		out = append(out, key+"="+value)
	}
	return out
}
