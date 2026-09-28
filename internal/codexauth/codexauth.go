// Package codexauth refreshes stale Codex OAuth credentials by briefly running
// the configured Codex CLI in a private PTY. Codex rewrites its own auth.json
// on startup when the stored access token has expired; omo only waits for that
// write and then stops the process. omo never writes the credential file.
package codexauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

// ErrUnchanged reports that the CLI exited or the deadline passed before the
// credential file changed.
var ErrUnchanged = errors.New("codex did not refresh its credentials")

const (
	defaultTimeout = 45 * time.Second
	pollInterval   = 200 * time.Millisecond
	stopGrace      = 5 * time.Second
)

// Refresher launches the profile command with no arguments so the interactive
// TUI performs its own token refresh without receiving a prompt.
type Refresher struct {
	// Shell replaces the launched argv; tests use it to stand in for Codex.
	Shell []string
	// Env adds literal environment entries after the profile environment.
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

// Refresh runs the CLI until authPath changes, then stops it. Concurrent
// refreshes of one credential file are serialized within the process.
func (r Refresher) Refresh(ctx context.Context, profile config.Profile, authPath string) error {
	authPath = filepath.Clean(authPath)
	mu := pathLock(authPath)
	mu.Lock()
	defer mu.Unlock()

	before, _ := os.ReadFile(authPath)
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	workdir, err := os.MkdirTemp("", "omo-codex-refresh-")
	if err != nil {
		return fmt.Errorf("codex credential refresh: %w", err)
	}
	defer os.RemoveAll(workdir)

	argv := r.Shell
	if len(argv) == 0 {
		argv = []string{profile.Cmd}
	}
	s, err := session.Start(session.Options{
		Cmd:     argv[0],
		Args:    append([]string(nil), argv[1:]...),
		Env:     r.environment(profile),
		Dir:     workdir,
		LogPath: filepath.Join(workdir, "refresh.log"),
		Rows:    40,
		Cols:    120,
	})
	if err != nil {
		return fmt.Errorf("codex credential refresh: start %s: %w", profile.Cmd, err)
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
		after, err := os.ReadFile(authPath)
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
			if exitErr := s.ExitErr(); exitErr != nil {
				return fmt.Errorf("codex credential refresh: %s exited: %v: %w", profile.Cmd, exitErr, ErrUnchanged)
			}
			return fmt.Errorf("codex credential refresh: %s exited: %w", profile.Cmd, ErrUnchanged)
		case <-ticker.C:
			if changed() {
				return nil
			}
		}
	}
}

func (r Refresher) environment(profile config.Profile) []string {
	env := make([]string, 0, len(profile.Env)+len(r.Env)+1)
	for key, value := range profile.Env {
		env = append(env, key+"="+value)
	}
	if _, configured := profile.Env["TERM"]; !configured {
		// The refresh runs on a real PTY even when omo itself was started
		// without a terminal; Codex refuses to start behind a dumb TERM.
		env = append(env, "TERM=xterm-256color")
	}
	for key, value := range r.Env {
		env = append(env, key+"="+value)
	}
	return env
}
