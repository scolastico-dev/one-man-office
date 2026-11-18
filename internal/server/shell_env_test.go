package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
)

// This file has no build tag: the environment boundary of the shell fixture
// is a map of variables and holds on every supported platform. Only the
// tests that start a real Bash or a POSIX shell are Unix-only.

// isolateShells keeps the user shells a test opens away from the
// developer's home and shell configuration.
func isolateShells(t *testing.T) {
	t.Helper()
	isolateAgents(t)
	childenvtest.ClearShell(t)
	t.Setenv("SHELL", "/bin/sh")
}

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
