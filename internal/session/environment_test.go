package session

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestAgentEnvironmentRemovesParentControlCredentials(t *testing.T) {
	t.Setenv("OMO_CONTROL_URL", "http://127.0.0.1:1234")
	t.Setenv("OMO_CONTROL_TOKEN", "parent-secret")
	t.Setenv("OMO_SOCKET", "office-socket")
	env := processEnvironment([]string{"OMO_AGENT_ID=developer-test", "OMO_CONTROL_TOKEN=profile-secret"})
	joined := "\n" + strings.Join(env, "\n")
	if strings.Contains(joined, "OMO_CONTROL_") {
		t.Fatal("control credentials leaked to agent")
	}
	if !strings.Contains(joined, "\nOMO_AGENT_ID=developer-test") || !strings.Contains(joined, "\nOMO_SOCKET=office-socket") {
		t.Fatal("agent identity or socket stripped")
	}
}

func TestProcessEnvironmentExportsCredentialScrubbedEnvironment(t *testing.T) {
	t.Setenv("OMO_CONTROL_TOKEN", "parent-secret")
	t.Setenv("GIT_AUTHOR_NAME", "parent")
	env := ProcessEnvironment([]string{"GIT_AUTHOR_NAME=configured"})
	values := environmentMap(env)
	if values["GIT_AUTHOR_NAME"] != "configured" {
		t.Fatalf("GIT_AUTHOR_NAME = %q, want configured", values["GIT_AUTHOR_NAME"])
	}
	if _, ok := values["OMO_CONTROL_TOKEN"]; ok {
		t.Fatal("control token leaked into exported process environment")
	}
}

func TestMergeEnvironmentExpandsFallbacksAndPreservesInheritedCommitter(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "existing committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'safe.directory=/workspace'")
	got := MergeEnvironment(map[string]string{
		"GIT_AUTHOR_NAME":       "OMO - AI Orchestrator",
		"GIT_AUTHOR_EMAIL":      "omo@scolasti.co",
		"GIT_COMMITTER_NAME":    "${GIT_COMMITTER_NAME:${GIT_AUTHOR_NAME:-}}",
		"GIT_COMMITTER_EMAIL":   "${GIT_COMMITTER_EMAIL:${GIT_AUTHOR_EMAIL:-}}",
		"GIT_CONFIG_PARAMETERS": "'commit.gpgSign=false' ${GIT_CONFIG_PARAMETERS:-}",
	})
	values := map[string]string{}
	for _, item := range got {
		key, value, _ := strings.Cut(item, "=")
		values[key] = value
	}
	if values["GIT_COMMITTER_NAME"] != "existing committer" || values["GIT_COMMITTER_EMAIL"] != "omo@scolasti.co" {
		t.Fatalf("expanded Git identity = %#v", values)
	}
	if values["GIT_CONFIG_PARAMETERS"] != "'commit.gpgSign=false' 'safe.directory=/workspace'" {
		t.Fatalf("Git config parameters = %q", values["GIT_CONFIG_PARAMETERS"])
	}
}

func TestMergeEnvironmentInterpolatesCommandInNestedFallback(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "")
	t.Setenv("GIT_AUTHOR_NAME", "")
	var commands []string
	got := mergeEnvironmentWithExecutor(false, map[string]string{
		"GIT_COMMITTER_NAME": "${GIT_COMMITTER_NAME:${GIT_AUTHOR_NAME:`git config user.name`}}",
	}, func(command string) string {
		commands = append(commands, command)
		return "Configured Git User\n\n"
	})
	values := environmentMap(got)
	if values["GIT_COMMITTER_NAME"] != "Configured Git User" {
		t.Fatalf("GIT_COMMITTER_NAME = %q", values["GIT_COMMITTER_NAME"])
	}
	if !reflect.DeepEqual(commands, []string{"git config user.name"}) {
		t.Fatalf("commands = %q", commands)
	}
}

func TestMergeEnvironmentSkipsCommandWhenFallbackIsNotNeeded(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "Existing User")
	called := false
	got := mergeEnvironmentWithExecutor(false, map[string]string{
		"GIT_COMMITTER_NAME": "${GIT_COMMITTER_NAME:`should not run`}",
	}, func(string) string {
		called = true
		return "unexpected"
	})
	if called {
		t.Fatal("fallback command ran despite an existing value")
	}
	if value := environmentMap(got)["GIT_COMMITTER_NAME"]; value != "Existing User" {
		t.Fatalf("GIT_COMMITTER_NAME = %q", value)
	}
}

func TestMergeEnvironmentKeepsProfileCommandsLiteral(t *testing.T) {
	called := false
	got := mergeEnvironmentWithExecutor(false,
		map[string]string{"SHARED": "plain"},
		func(string) string {
			called = true
			return "unexpected"
		},
		map[string]string{"PROFILE_TOKEN": "secret`still literal`"},
	)
	if called {
		t.Fatal("profile command was interpolated")
	}
	if value := environmentMap(got)["PROFILE_TOKEN"]; value != "secret`still literal`" {
		t.Fatalf("PROFILE_TOKEN = %q", value)
	}
}

func TestMergeEnvironmentExecutesCommandWithScrubbedEnvironment(t *testing.T) {
	t.Setenv("OMO_CONTROL_TOKEN", "must-not-leak")
	command := `printf command-value:%s "${OMO_CONTROL_TOKEN-}"`
	if runtime.GOOS == "windows" {
		command = `<nul set /p "=command-value:" & set OMO_CONTROL_TOKEN`
	}
	got := environmentMap(MergeEnvironment(map[string]string{"VALUE": "`" + command + "`"}))["VALUE"]
	if got != "command-value:" {
		t.Fatalf("command interpolation = %q, want %q", got, "command-value:")
	}
}

func TestMergeEnvironmentUsesSpecificProfileValuesAndProtectsAgentIdentity(t *testing.T) {
	got := MergeEnvironment(
		map[string]string{
			"GIT_AUTHOR_NAME":    "OMO - AI Orchestrator",
			"GIT_COMMITTER_NAME": "${GIT_COMMITTER_NAME:${GIT_AUTHOR_NAME:-}}",
			"OMO_AGENT_ID":       "configured-override",
		},
		map[string]string{
			"GIT_AUTHOR_NAME": "Project Bot",
			"OMO_AGENT_ID":    "profile-override",
		},
		map[string]string{
			"OMO_AGENT_ID": "developer-ada",
			"OMO_SOCKET":   "/tmp/omo.sock",
		},
	)
	values := map[string]string{}
	for _, item := range got {
		key, value, _ := strings.Cut(item, "=")
		values[key] = value
	}
	if values["GIT_AUTHOR_NAME"] != "Project Bot" || values["GIT_COMMITTER_NAME"] != "Project Bot" {
		t.Fatalf("profile Git identity did not override shared defaults: %#v", values)
	}
	if values["OMO_AGENT_ID"] != "developer-ada" || values["OMO_SOCKET"] != "/tmp/omo.sock" {
		t.Fatalf("supervisor identity was overridden: %#v", values)
	}
}

func TestMergeEnvironmentKeepsProfileValuesLiteralAndCannotExpandControlSecrets(t *testing.T) {
	t.Setenv("OMO_CONTROL_TOKEN", "private-control-token")
	got := MergeEnvironment(
		map[string]string{"COPIED_TOKEN": "$OMO_CONTROL_TOKEN"},
		map[string]string{"PROFILE_TOKEN": "abc$UNSET", "PROFILE_HOME": "$HOME"},
	)
	values := environmentMap(got)
	if values["COPIED_TOKEN"] != "" {
		t.Fatalf("agents.env copied a supervisor credential: %q", values["COPIED_TOKEN"])
	}
	if values["PROFILE_TOKEN"] != "abc$UNSET" || values["PROFILE_HOME"] != "$HOME" {
		t.Fatalf("profile values were expanded: %#v", values)
	}
}

func TestMergeEnvironmentResolvesCyclesDeterministically(t *testing.T) {
	t.Setenv("A", "parent-a")
	t.Setenv("B", "parent-b")
	configured := map[string]string{"A": "$B", "B": "$A"}
	for i := 0; i < 100; i++ {
		values := environmentMap(mergeEnvironment(false, configured))
		if values["A"] != "parent-a" || values["B"] != "parent-a" {
			t.Fatalf("cyclic expansion changed with map iteration: A=%q B=%q", values["A"], values["B"])
		}
	}
}

func TestMergeEnvironmentIsCaseInsensitiveOnWindows(t *testing.T) {
	got := mergeEnvironment(true,
		map[string]string{"GIT_AUTHOR_NAME": "default", "git_author_name": "explicit", "omo_agent_id": "configured"},
		map[string]string{"Git_Author_Name": "profile", "OMO_AGENT_ID": "developer-ada"},
	)
	values := map[string]string{}
	for _, item := range got {
		key, value, _ := strings.Cut(item, "=")
		folded := strings.ToUpper(key)
		if _, exists := values[folded]; exists {
			t.Fatalf("duplicate case-insensitive environment key in %q", got)
		}
		values[folded] = value
	}
	if values["GIT_AUTHOR_NAME"] != "profile" || values["OMO_AGENT_ID"] != "developer-ada" {
		t.Fatalf("Windows environment precedence = %#v", values)
	}
}

func TestProcessEnvironmentDeduplicatesWindowsKeysWithExtraWinning(t *testing.T) {
	got := processEnvironmentForPlatform(
		[]string{"=D:=D:\\work", "Path=parent", "=C:=C:\\repo", "OMO_CONTROL_TOKEN=private", "KEEP=yes"},
		[]string{"PATH=agent", "keep=override"},
		true,
	)
	if !reflect.DeepEqual(got, []string{"=C:=C:\\repo", "=D:=D:\\work", "keep=override", "PATH=agent"}) {
		t.Fatalf("Windows process environment = %q", got)
	}
}
