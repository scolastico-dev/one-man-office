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
	for _, want := range []string{"--listen", "127.0.0.1:8090", "--max-agents", "--usage-cache-ttl", "--mock", "--unsafe", "--basic-auth", "--no-origin-check", "--detached", "stop", "autostart"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %s from help: %s", want, output.String())
		}
	}
}

func TestSupervisorRejectsInvalidLimitsBeforeStartup(t *testing.T) {
	for _, args := range [][]string{{"supervisor", "--max-agents=0"}, {"supervisor", "--usage-cache-ttl=-1s"}, {"supervisor", "--basic-auth=user"}, {"supervisor", "--basic-auth=user:password", "--unsafe"}, {"supervisor", "-d", "--max-agents=0"}, {"supervisor", "autostart", "register", "--listen=invalid"}, {"supervisor", "autostart", "register", "--basic-auth=user"}} {
		cmd := Root("test")
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestSupervisorArgumentSnapshotRoundTripsEverySetting(t *testing.T) {
	first := Root("test")
	register, _, err := first.Find([]string{"supervisor", "autostart", "register"})
	if err != nil {
		t.Fatal(err)
	}
	input := []string{"--listen=127.0.0.1:9182", "--max-agents=7", "--usage-cache-ttl=3m", "--mock=false", "--basic-auth=user:p a'ss\"$HOME`id`%&\\word", "--no-origin-check=true"}
	if err := register.ParseFlags(input); err != nil {
		t.Fatal(err)
	}
	snapshot := supervisorArgs(register)
	second := Root("test")
	supervisor, _, err := second.Find([]string{"supervisor"})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.ParseFlags(snapshot); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"listen", "max-agents", "usage-cache-ttl", "mock", "basic-auth", "unsafe", "no-origin-check"} {
		if got, want := supervisor.Flags().Lookup(name).Value.String(), register.Flags().Lookup(name).Value.String(); got != want {
			t.Fatalf("%s: %q != %q", name, got, want)
		}
	}
	if len(snapshot) != 7 {
		t.Fatalf("settings omitted: %#v", snapshot)
	}
}
