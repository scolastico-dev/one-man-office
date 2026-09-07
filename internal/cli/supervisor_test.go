package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestSupervisorHelpDescribesLocalDashboardAndAggregateLimit(t *testing.T) {
	cmd := Root("test")
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"supervisor", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--listen", "127.0.0.1:8090", "--max-agents", "--usage-cache-ttl", "--mock", "--unsafe"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %s from help: %s", want, output.String())
		}
	}
}

func TestSupervisorRejectsInvalidLimitsBeforeStartup(t *testing.T) {
	for _, args := range [][]string{{"supervisor", "--max-agents=0"}, {"supervisor", "--usage-cache-ttl=-1s"}} {
		cmd := Root("test")
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
