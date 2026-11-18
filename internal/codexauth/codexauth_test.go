package codexauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeAuth(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, "auth.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
}

func TestRefreshStopsCommandOnceCredentialsChange(t *testing.T) {
	skipWindows(t)
	root := t.TempDir()
	path := writeAuth(t, root, `{"tokens":{"access_token":"stale"}}`)
	pidFile := filepath.Join(root, "pid")
	// Publish the PID before the atomic credential replacement. Refresh
	// must reap this same process, which stays alive until omo stops it.
	script := `printf '%s' "$$" > "$PID_FILE"; printf '{"tokens":{"access_token":"fresh"}}' > "$CODEX_HOME/auth.next"; mv "$CODEX_HOME/auth.next" "$CODEX_HOME/auth.json"; exec sleep 60`
	r := Refresher{Shell: []string{"sh", "-c", script}, Env: map[string]string{"PID_FILE": pidFile}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := r.Refresh(ctx, "sh", []string{"CODEX_HOME=" + root}, path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != `{"tokens":{"access_token":"fresh"}}` {
		t.Fatalf("credentials = %s", raw)
	}
	rawPID, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(rawPID))
	if err != nil || pid <= 0 {
		t.Fatalf("invalid refresh PID %q: %v", rawPID, err)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release()
	if err := p.Signal(syscall.Signal(0)); err == nil {
		_ = p.Kill()
		t.Fatal("refresh returned before its command ended")
	}
}

func TestRefreshFailsWhenCredentialsStayUnchanged(t *testing.T) {
	skipWindows(t)
	root := t.TempDir()
	path := writeAuth(t, root, `{"tokens":{"access_token":"stale"}}`)
	r := Refresher{Shell: []string{"sh", "-c", "sleep 30"}, Timeout: 500 * time.Millisecond}
	err := r.Refresh(context.Background(), "sh", []string{"CODEX_HOME=" + root}, path)
	if !errors.Is(err, ErrUnchanged) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("error names the path: %v", err)
	}
}

func TestRefreshEndsWithItsContext(t *testing.T) {
	skipWindows(t)
	root := t.TempDir()
	path := writeAuth(t, root, `{}`)
	started := filepath.Join(root, "started")
	r := Refresher{Shell: []string{"sh", "-c", `printf started > "$STARTED"; exec sleep 60`}, Env: map[string]string{"STARTED": started}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Refresh(ctx, "sh", nil, path) }()
	finished := false
	t.Cleanup(func() {
		cancel()
		if !finished {
			<-done
		}
	})
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("refresh command did not start")
		case err := <-done:
			finished = true
			t.Fatalf("refresh ended before cancellation: %v", err)
		}
	}
	cancel()
	select {
	case err := <-done:
		finished = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-deadline.C:
		t.Fatal("refresh did not stop after cancellation")
	}
}

func TestRefreshIgnoresALinkInPlaceOfTheFile(t *testing.T) {
	skipWindows(t)
	root := t.TempDir()
	path := writeAuth(t, root, `{"tokens":{"access_token":"stale"}}`)
	other := filepath.Join(root, "other.json")
	// The command swaps the credential file for a link to another file:
	// omo reads no links, so that is no refresh.
	script := `printf '{"x":1}' > "$OTHER"; rm -f "$CODEX_HOME/auth.json"; ln -s "$OTHER" "$CODEX_HOME/auth.json"; sleep 30`
	r := Refresher{Shell: []string{"sh", "-c", script}, Timeout: time.Second}
	err := r.Refresh(context.Background(), "sh", []string{"CODEX_HOME=" + root, "OTHER=" + other}, path)
	if !errors.Is(err, ErrUnchanged) {
		t.Fatalf("err = %v", err)
	}
}

func TestRefreshCommandRunsWithoutArguments(t *testing.T) {
	skipWindows(t)
	root := t.TempDir()
	path := writeAuth(t, root, `{}`)
	out := filepath.Join(root, "argv")
	script := filepath.Join(root, "fake-codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'argv=%s TERM=%s' \"$*\" \"$TERM\" > \"$ARGV_OUT\"; printf changed > \"$CODEX_HOME/auth.json\"; sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERM", "")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := (Refresher{}).Refresh(ctx, script, []string{"CODEX_HOME=" + root, "ARGV_OUT=" + out}, path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	if string(raw) != "argv= TERM=xterm-256color" {
		t.Fatalf("argv/env = %q", raw)
	}
}

func TestRefreshWithoutCommandFails(t *testing.T) {
	root := t.TempDir()
	path := writeAuth(t, root, `{}`)
	if err := (Refresher{}).Refresh(context.Background(), "", nil, path); err == nil {
		t.Fatal("refresh without a command succeeded")
	}
}
