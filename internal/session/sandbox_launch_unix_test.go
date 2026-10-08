//go:build !windows

package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLauncherGetsPTYAndFailureDoesNotStartCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	called := false
	_, err := Start(Options{Cmd: "/bin/sh", Args: []string{"-c", "touch " + marker}, Dir: t.TempDir(), LogPath: filepath.Join(t.TempDir(), "log"),
		PrepareLauncher: func(tty string) (Launch, error) {
			called = true
			if !strings.HasPrefix(tty, "/dev/") {
				t.Errorf("PTY path = %q", tty)
			}
			if _, statErr := os.Stat(tty); statErr != nil {
				t.Errorf("PTY unavailable: %v", statErr)
			}
			return Launch{}, errors.New("policy refused")
		},
	})
	if !called || err == nil || !strings.Contains(err.Error(), "policy refused") {
		t.Fatalf("launcher result called=%v err=%v", called, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unsandboxed command ran: %v", err)
	}
}

func TestLauncherCleanupRunsWhenLogCannotOpen(t *testing.T) {
	cleaned := false
	_, err := Start(Options{Cmd: "/bin/true", Dir: t.TempDir(), LogPath: filepath.Join(t.TempDir(), "missing", "log"), Cleanup: func() { cleaned = true }})
	if err == nil || !cleaned {
		t.Fatalf("log failure cleanup: err=%v cleaned=%v", err, cleaned)
	}
}

func TestPreparedLauncherDoesNotReinheritParentSecrets(t *testing.T) {
	t.Setenv("GH_TOKEN", "parent-secret")
	sess, err := Start(Options{Cmd: "/bin/false", Dir: t.TempDir(), LogPath: filepath.Join(t.TempDir(), "log"),
		PrepareLauncher: func(string) (Launch, error) {
			return Launch{Cmd: "/bin/sh", Args: []string{"-c", "test -z \"$GH_TOKEN\""}, Env: []string{"PATH=/usr/bin:/bin"}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	<-sess.Done()
	if err := sess.ExitErr(); err != nil {
		t.Fatalf("launcher inherited GH_TOKEN: %v", err)
	}
}
