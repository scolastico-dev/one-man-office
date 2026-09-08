package session

import (
	"reflect"
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
