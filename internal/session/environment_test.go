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
