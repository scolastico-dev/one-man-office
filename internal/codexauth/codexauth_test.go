package codexauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
)

func writeAuth(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, "auth.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRefreshStopsCommandOnceCredentialsChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	root := t.TempDir()
	path := writeAuth(t, root, `{"tokens":{"access_token":"stale"}}`)
	marker := filepath.Join(root, "still-running")
	profile := config.Profile{Cmd: "sh", Args: []string{"exec", "--model", "ignored"}, Env: map[string]string{"CODEX_HOME": root}}
	// A refreshing CLI rewrites its credentials shortly after start and then
	// keeps running as an interactive TUI would.
	script := `sleep 0.2; printf '{"tokens":{"access_token":"fresh"}}' > "$CODEX_HOME/auth.json"; sleep 30; touch "$MARKER"`
	r := Refresher{Shell: []string{"sh", "-c", script}, Env: map[string]string{"MARKER": marker}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	if err := r.Refresh(ctx, profile, path); err != nil {
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
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	root := t.TempDir()
	path := writeAuth(t, root, `{"tokens":{"access_token":"stale"}}`)
	r := Refresher{Shell: []string{"sh", "-c", "sleep 30"}, Timeout: 500 * time.Millisecond}
	err := r.Refresh(context.Background(), config.Profile{Cmd: "sh", Env: map[string]string{"CODEX_HOME": root}}, path)
	if !errors.Is(err, ErrUnchanged) {
		t.Fatalf("err = %v", err)
	}
}

func TestRefreshCommandUsesProfileCommandWithoutArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	root := t.TempDir()
	path := writeAuth(t, root, `{}`)
	out := filepath.Join(root, "argv")
	script := filepath.Join(root, "fake-codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'argv=%s TERM=%s' \"$*\" \"$TERM\" > \"$ARGV_OUT\"; printf changed > \"$CODEX_HOME/auth.json\"; sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERM", "")
	profile := config.Profile{Cmd: script, Args: []string{"exec", "%prompt%"}, Env: map[string]string{"CODEX_HOME": root, "ARGV_OUT": out}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := (Refresher{}).Refresh(ctx, profile, path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	if string(raw) != "argv= TERM=xterm-256color" {
		t.Fatalf("argv/env = %q", raw)
	}
}
