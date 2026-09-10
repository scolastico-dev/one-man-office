package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompanyCommandIsCanonicalAndSupervisorIsUnknown(t *testing.T) {
	cmd := Root("test")
	if _, _, err := cmd.Find([]string{"company"}); err != nil {
		t.Fatalf("company command missing: %v", err)
	}
	if _, _, err := cmd.Find([]string{"supervisor"}); err == nil {
		t.Fatal("legacy supervisor command is still registered")
	}
}

func TestCompanyHelpDescribesLocalDashboardAndAggregateLimit(t *testing.T) {
	cmd := Root("test")
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"company", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--listen", "127.0.0.1:8090", "--max-agents", "--usage-cache-ttl", "--mock", "--unsafe", "--basic-auth", "--no-origin-check", "--detached", "stop", "autostart"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %s from help: %s", want, output.String())
		}
	}
}

func TestCompanyRejectsInvalidLimitsBeforeStartup(t *testing.T) {
	for _, args := range [][]string{{"company", "--max-agents=0"}, {"company", "--usage-cache-ttl=-1s"}, {"company", "--basic-auth=user"}, {"company", "--basic-auth=user:password", "--unsafe"}, {"company", "-d", "--max-agents=0"}, {"company", "autostart", "register", "--listen=invalid"}, {"company", "autostart", "register", "--basic-auth=user"}} {
		cmd := Root("test")
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestCompanyArgumentSnapshotRoundTripsEverySetting(t *testing.T) {
	first := Root("test")
	register, _, err := first.Find([]string{"company", "autostart", "register"})
	if err != nil {
		t.Fatal(err)
	}
	input := []string{"--listen=127.0.0.1:9182", "--max-agents=7", "--usage-cache-ttl=3m", "--mock=false", "--basic-auth=user:p a'ss\"$HOME`id`%&\\word", "--no-origin-check=true"}
	if err := register.ParseFlags(input); err != nil {
		t.Fatal(err)
	}
	snapshot := companyArgs(register)
	second := Root("test")
	company, _, err := second.Find([]string{"company"})
	if err != nil {
		t.Fatal(err)
	}
	if err := company.ParseFlags(snapshot); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"listen", "max-agents", "usage-cache-ttl", "mock", "basic-auth", "unsafe", "no-origin-check"} {
		if got, want := company.Flags().Lookup(name).Value.String(), register.Flags().Lookup(name).Value.String(); got != want {
			t.Fatalf("%s: %q != %q", name, got, want)
		}
	}
	if len(snapshot) != 7 {
		t.Fatalf("settings omitted: %#v", snapshot)
	}
}
