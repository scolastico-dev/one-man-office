package childenvtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shellKey identifies inherited configuration consumed by sh, Bash, zsh
// and their line editors. Production environments deliberately retain it.
func shellKey(key string) bool {
	key = strings.ToUpper(key)
	for _, prefix := range []string{"BASH_", "HIST", "PROMPT", "RPROMPT", "ZLE_"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	switch key {
	case "SHELL", "ENV", "INPUTRC", "ZDOTDIR", "SHELLOPTS", "BASHOPTS", "POSIXLY_CORRECT",
		"PS0", "PS1", "PS2", "PS3", "PS4", "RPS1", "RPS2", "SPROMPT", "SAVEHIST",
		"EDITOR", "VISUAL", "FCEDIT", "KEYMAP", "CDPATH", "FPATH", "GLOBIGNORE", "FIGNORE",
		"KEYTIMEOUT", "PSVAR", "IFS", "MAIL", "MAILPATH", "MAILCHECK", "TIMEOUT", "TMOUT", "READNULLCMD", "NULLCMD":
		return true
	}
	return false
}

// ShellEnv gives fixture descendants an owned home and configuration, without
// inherited omo identity or shell startup, option, prompt and input overrides.
// PATH, platform runtime variables and git isolation are preserved. Add any
// configuration a test deliberately exercises after calling this helper.
func ShellEnv(base []string, home string) []string {
	env := make([]string, 0, len(base)+9)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "OMO_") || shellKey(key) {
			continue
		}
		switch key {
		case "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME":
			continue
		}
		env = append(env, entry)
	}
	return append(env, "HOME="+home, "USERPROFILE="+home,
		"APPDATA="+filepath.Join(home, "appdata"), "LOCALAPPDATA="+filepath.Join(home, "localappdata"),
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"), "INPUTRC="+os.DevNull)
}

// ClearShell removes inherited shell configuration inside a fixture that
// already owns its home. Cleanup restores the caller's environment.
func ClearShell(t testing.TB) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if shellKey(key) {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("remove shell configuration %s", key)
			}
		}
	}
	t.Setenv("INPUTRC", os.DevNull)
}

// Run isolates a test process before m.Run, covering indirect shell scripts
// (including programs found on PATH) as well as explicit shell invocations.
// Call after any TestMain branch acting as an intentionally configured child.
func Run(m *testing.M) int {
	home, err := os.MkdirTemp("", "omo-shell-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create fixture home:", err)
		return 1
	}
	defer os.RemoveAll(home)
	IsolateShell(home)
	return m.Run()
}

// IsolateShell installs ShellEnv in a suite's TestMain. The caller owns home
// and its cleanup. Tests needing per-test restoration should use ClearShell.
func IsolateShell(home string) {
	base := os.Environ()
	env := ShellEnv(base, home)
	keep := make(map[string]bool, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		keep[key] = true
	}
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if key != "" && !keep[key] {
			if err := os.Unsetenv(key); err != nil {
				panic("remove fixture environment key: " + key)
			}
		}
	}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		// Windows drive-current-directory entries are preserved in place.
		if key != "" {
			if err := os.Setenv(key, value); err != nil {
				panic("set fixture environment key: " + key)
			}
		}
	}
}
