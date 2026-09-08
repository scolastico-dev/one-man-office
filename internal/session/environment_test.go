package session

import (
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

func TestAddEnvironmentExpandsGitIdentityDefaults(t *testing.T) {
	t.Setenv("GIT_COMMITTER_NAME", "existing committer")
	got := AddEnvironment(nil, map[string]string{
		"GIT_AUTHOR_NAME":       "OMO - AI Orchestrator",
		"GIT_AUTHOR_EMAIL":      "omo@scolasti.co",
		"GIT_COMMITTER_NAME":    "${GIT_COMMITTER_NAME:$GIT_AUTHOR_NAME}",
		"GIT_COMMITTER_EMAIL":   "${GIT_COMMITTER_EMAIL:$GIT_AUTHOR_EMAIL}",
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
	if !strings.HasPrefix(values["GIT_CONFIG_PARAMETERS"], "'commit.gpgSign=false'") {
		t.Fatalf("Git config parameters = %q", values["GIT_CONFIG_PARAMETERS"])
	}
}
