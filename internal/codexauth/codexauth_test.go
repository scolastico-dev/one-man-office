package codexauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	marker := filepath.Join(root, "still-running")
	// A refreshing CLI rewrites its credentials shortly after start and then
	// keeps running as an interactive TUI would.
	script := `sleep 0.2; printf '{"tokens":{"access_token":"fresh"}}' > "$CODEX_HOME/auth.json"; sleep 30; touch "$MARKER"`
	r := Refresher{Shell: []string{"sh", "-c", script}, Env: map[string]string{"MARKER": marker}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	if err := r.Refresh(ctx, "sh", []string{"CODEX_HOME=" + root}, path); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("refresh waited %s after the credentials changed", time.Since(start))
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != `{"tokens":{"access_token":"fresh"}}` {
		t.Fatalf("credentials = %s", raw)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("refresh command kept running after credentials changed")
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
	r := Refresher{Shell: []string{"sh", "-c", "sleep 30"}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.Refresh(ctx, "sh", nil, path)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("refresh took %s after its context ended", time.Since(start))
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
