package childenvtest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWithoutOmoKeepsBuildInputs(t *testing.T) {
	got := WithoutOmo([]string{"OMO_AGENT_ID=fixture", "omo_socket=fixture", "OmO_FUTURE=fixture",
		"GOCACHE=/fixture/cache", "GOMODCACHE=/fixture/modules", "PATH=/fixture/bin"})
	if !slices.Equal(got, []string{"GOCACHE=/fixture/cache", "GOMODCACHE=/fixture/modules", "PATH=/fixture/bin"}) {
		t.Fatal("build environment retained omo state or lost build inputs")
	}
}

func TestShellEnvDropsConfigurationAndOwnsHome(t *testing.T) {
	home := t.TempDir()
	keys := strings.Fields(`ENV BASH_ENV BASH_COMPAT BASH_XTRACEFD BASH_FUNC_probe%%
		INPUTRC ZDOTDIR SHELLOPTS BASHOPTS POSIXLY_CORRECT EDITOR VISUAL FCEDIT
		KEYMAP KEYTIMEOUT CDPATH FPATH GLOBIGNORE FIGNORE PS0 PS1 PS2 PS3 PS4
		RPS1 RPS2 PSVAR PROMPT PROMPT_COMMAND RPROMPT SPROMPT HISTFILE HISTSIZE
		HISTCONTROL SAVEHIST ZLE_RPROMPT_INDENT IFS MAIL MAILPATH MAILCHECK
		TMOUT TIMEOUT READNULLCMD NULLCMD SHELL OMO_AGENT_ID omo_socket OmO_FUTURE bAsH_env zDoTdIr`)
	base := []string{"PATH=/fixture/bin", "SystemRoot=C:\\Windows", "GIT_CONFIG_GLOBAL=fixture-git", "KEEP=fixture"}
	for _, key := range keys {
		base = append(base, key+"=hostile")
	}
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		base = append(base, key+"=unowned")
	}
	env := ShellEnv(base, home)
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if _, duplicate := values[key]; duplicate {
			t.Errorf("duplicate %s", key)
		}
		values[key] = value
	}
	for _, key := range keys {
		if key != "INPUTRC" {
			if _, present := values[key]; present {
				t.Errorf("inherited %s", key)
			}
		}
	}
	for key, want := range map[string]string{
		"PATH": "/fixture/bin", "SystemRoot": "C:\\Windows", "GIT_CONFIG_GLOBAL": "fixture-git", "KEEP": "fixture",
		"HOME": home, "USERPROFILE": home, "APPDATA": filepath.Join(home, "appdata"),
		"LOCALAPPDATA": filepath.Join(home, "localappdata"), "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME": filepath.Join(home, ".local", "share"), "XDG_CACHE_HOME": filepath.Join(home, ".cache"), "INPUTRC": os.DevNull,
	} {
		if values[key] != want {
			t.Errorf("wrong fixture value for %s", key)
		}
	}
}

func TestClearShellRestoresCallerConfiguration(t *testing.T) {
	t.Setenv("BASH_ENV", "test-owned-startup")
	t.Setenv("INPUTRC", "test-owned-input")
	t.Run("fixture", func(t *testing.T) {
		ClearShell(t)
		if _, present := os.LookupEnv("BASH_ENV"); present || os.Getenv("INPUTRC") != os.DevNull {
			t.Fatal("fixture retained caller configuration")
		}
	})
	if os.Getenv("BASH_ENV") != "test-owned-startup" || os.Getenv("INPUTRC") != "test-owned-input" {
		t.Fatal("fixture did not restore caller configuration")
	}
}
