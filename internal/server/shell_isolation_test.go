//go:build !windows

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestShellFixtureDropsInheritedConfiguration(t *testing.T) {
	startup := filepath.Join(t.TempDir(), "startup")
	if err := os.WriteFile(startup, []byte("exit 93\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"BASH_ENV", "ZDOTDIR", "SHELLOPTS", "BASHOPTS", "POSIXLY_CORRECT", "BASH_COMPAT", "BASH_FUNC_printf%%", "PROMPT_COMMAND", "PS1", "HISTFILE", "EDITOR", "VISUAL", "CDPATH", "ZLE_RPROMPT_INDENT"} {
		t.Setenv(key, startup)
	}
	t.Setenv("INPUTRC", startup)
	isolateShells(t)
	for _, key := range []string{"BASH_ENV", "ZDOTDIR", "SHELLOPTS", "BASHOPTS", "POSIXLY_CORRECT", "BASH_COMPAT", "BASH_FUNC_printf%%", "PROMPT_COMMAND", "PS1", "HISTFILE", "EDITOR", "VISUAL", "CDPATH", "ZLE_RPROMPT_INDENT"} {
		if _, ok := os.LookupEnv(key); ok {
			t.Errorf("shell fixture inherits %s", key)
		}
	}
	if os.Getenv("INPUTRC") != os.DevNull {
		t.Error("shell fixture does not own its Readline configuration")
	}
}

func TestShellFixtureIgnoresPromptAndHistoryOverrides(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	history := filepath.Join(t.TempDir(), "unowned-history")
	t.Setenv("PROMPT_COMMAND", "exit 93")
	t.Setenv("HISTFILE", history)
	s, b := shellServer(t)
	// Exercise Bash's actual interactive prompt and history consumers even
	// on systems where the fixture's usual /bin/sh is dash.
	t.Setenv("SHELL", bash)
	t.Setenv("PS1", "fixture-ready> ")
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
		t.Skip("bash is not installed")
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
