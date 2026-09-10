package company

import (
	"strings"
	"testing"
)

func TestManagedEnvironmentRemovesInheritedIdentityAndDuplicateTerminalSettings(t *testing.T) {
	t.Setenv("TERM", "ansi")
	t.Setenv("COLORTERM", "old")
	t.Setenv("COLORFGBG", "0;15")
	t.Setenv("omo_agent_id", "foreign")
	t.Setenv("omo_socket", "foreign")
	t.Setenv("OMO_CONTROL_TOKEN", "parent-secret")
	counts := map[string]int{}
	for _, entry := range cleanEnvironment() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		counts[key]++
		if key == "OMO_AGENT_ID" || key == "OMO_SOCKET" || strings.HasPrefix(key, "OMO_CONTROL_") {
			t.Fatalf("inherited identity key %s", key)
		}
	}
	for _, key := range []string{"TERM", "COLORTERM", "COLORFGBG"} {
		if counts[key] != 1 {
			t.Errorf("%s has %d values; native Windows environments must not contain duplicate keys", key, counts[key])
		}
	}
}
