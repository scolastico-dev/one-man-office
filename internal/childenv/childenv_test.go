package childenv

import (
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
)

func TestCleanRemovesSessionVariables(t *testing.T) {
	var env []string
	for k, v := range childenvtest.Samples {
		env = append(env, k+"="+v)
	}
	kept := []string{
		"PATH=/usr/bin", "HOME=/home/u", "TERM=xterm", "LANG=C.UTF-8",
		"NODE_OPTIONS=--max-old-space-size=4096", "GIT_AUTHOR_NAME=me",
		"CLAUDE_CONFIG_DIR=/c", "GEMINI_CLI_HOME=/g", "CODEX_HOME=/x",
		"SSH_AUTH_SOCK=/run/ssh", "TERMINFO=/usr/share/terminfo", "STYLE=x",
	}
	env = append(env, kept...)
	got := Clean(env)
	for _, entry := range got {
		name, _, _ := strings.Cut(entry, "=")
		if _, bad := childenvtest.Samples[name]; bad && name != "NODE_OPTIONS" {
			t.Errorf("%s was kept", name)
		}
	}
	for _, k := range kept {
		if !slices.Contains(got, k) {
			t.Errorf("%s was removed", k)
		}
	}
	if slices.Contains(got, "NODE_OPTIONS="+childenvtest.Samples["NODE_OPTIONS"]) {
		t.Error("the IDE's NODE_OPTIONS was kept")
	}
	// Case does not matter (Windows names are case-insensitive).
	if got := Clean([]string{"vscode_pid=1", "Term_Program=vscode", "Path=x"}); !slices.Equal(got, []string{"Path=x"}) {
		t.Errorf("mixed case: %v", got)
	}
}

func TestDetached(t *testing.T) {
	for name, value := range childenvtest.Samples {
		if !Detached(name, value) {
			t.Errorf("%s is not detached", name)
		}
	}
	if Detached("NODE_OPTIONS", "--max-old-space-size=4096") || Detached("PATH", "/bin") {
		t.Error("ordinary variables are detached")
	}
}

func TestInheritedIsCleanedOwnEnvironment(t *testing.T) {
	for k, v := range childenvtest.Samples {
		t.Setenv(k, v)
	}
	t.Setenv("OMO_CHILDENV_TEST", "kept")
	got := Inherited()
	if !slices.Contains(got, "OMO_CHILDENV_TEST=kept") {
		t.Error("an ordinary variable is missing")
	}
	for _, entry := range got {
		name, value, _ := strings.Cut(entry, "=")
		if Detached(name, value) {
			t.Errorf("%s reached the inherited environment", name)
		}
	}
	if !slices.Contains(Own(), "TERM_PROGRAM=vscode") {
		t.Error("Own lost a variable")
	}
}
