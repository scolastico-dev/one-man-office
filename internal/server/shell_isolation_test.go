//go:build !windows

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestShellFixtureIgnoresPromptAndHistoryOverrides(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("these tests drive a real Bash; install bash (CI installs it in its setup step): %v", err)
	}
	history := filepath.Join(t.TempDir(), "unowned-history")
	t.Setenv("PROMPT_COMMAND", "exit 93")
	t.Setenv("HISTFILE", history)
	s, b := shellServer(t)
	// Exercise Bash's actual interactive prompt and history consumers even
	// on systems where the fixture's usual /bin/sh is dash. An owned wrapper
	// suppresses system rc files too: Ubuntu's bash.bashrc overwrites PS1.
	// Assign PS1 at exec because a Bash /bin/sh resets it in the wrapper.
	wrapper := filepath.Join(t.TempDir(), "bash")
	script := "#!/bin/sh\nPS1='fixture-ready> ' exec '" + strings.ReplaceAll(bash, "'", "'\\''") + "' --norc --noprofile -i\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", wrapper)
	sh := openShell(t, b, s, api.OpenTerminalRequest{Project: addProject(t, s)})
	out := shellConn(t, b, s, sh.ID)
	out.until("fixture-ready> ")
	out.typeLine("history -a; echo FIXTURE$((40+2))")
	out.until("FIXTURE42")
	if _, err := os.Stat(history); !os.IsNotExist(err) {
		t.Fatalf("shell used the inherited history destination: %v", err)
	}
}

func TestShellFixtureIgnoresNoninteractiveBashStartup(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("these tests drive a real Bash; install bash (CI installs it in its setup step): %v", err)
	}
	startup := filepath.Join(t.TempDir(), "startup")
	if err := os.WriteFile(startup, []byte("exit 93\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", startup)
	isolateShells(t)
	cmd := exec.Command(bash, "-c", "printf fixture-ready")
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "fixture-ready" {
		t.Fatalf("fixture Bash: %v, output %q", err, out)
	}
}
