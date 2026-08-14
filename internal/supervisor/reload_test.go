package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestReloadAppliesConfigWithoutKillingExistingAgents(t *testing.T) {
	o := newOffice(t, map[string]string{"developer": "ready\nsleep|60s\n"})
	developer, err := o.Sup.Spawn("developer", "developer", 0, o.Dir, "keep running")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "developer ready", func() bool { return agentState(t, o, developer) == "working" })
	sessionBefore, ok := o.Sup.Session(developer)
	if !ok {
		t.Fatal("developer session missing before reload")
	}

	configPath := filepath.Join(o.Dir, ".omo", "omo.yaml")
	if err := os.WriteFile(configPath, []byte(reloadTestConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	var response proto.ConfigReloadResponse
	if err := sockc.Call(o.Sup.SocketPath, "user", "office.reload", nil, &response); err != nil {
		t.Fatal(err)
	}
	if response.Models != 1 || response.Repos != 0 {
		t.Fatalf("reload response = %+v", response)
	}
	if sessionAfter, ok := o.Sup.Session(developer); !ok || sessionAfter != sessionBefore || agentState(t, o, developer) != "working" {
		t.Fatal("reload replaced or killed the existing developer session")
	}
	if model, err := o.Sup.roleProfile("developer", 0); err != nil || model != "refreshed" {
		t.Fatalf("new developer profile = %q, %v", model, err)
	}

	if err := os.WriteFile(configPath, []byte("models: [invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "office.reload", nil, nil); err == nil {
		t.Fatal("invalid config was accepted")
	}
	if model, _ := o.Sup.roleProfile("developer", 0); model != "refreshed" {
		t.Fatalf("failed reload replaced active config with %q", model)
	}
}

func TestReloadRejectsUnauthorizedAgent(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "ready\nsleep|60s\n"})
	if err := os.WriteFile(filepath.Join(o.Dir, ".omo", "omo.yaml"), []byte(reloadTestConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "freelancer ready", func() bool { return agentState(t, o, name) == "working" })
	if err := sockc.Call(o.Sup.SocketPath, name, "office.reload", nil, nil); err == nil {
		t.Fatal("freelancer reloaded the office config")
	}
}

const reloadTestConfig = `repos: {}
models:
  refreshed:
    cmd: "true"
roles:
  ceo: refreshed
  product_manager: refreshed
  developer: refreshed
  reviewer: refreshed
  freelancer: refreshed
  smokealarm: refreshed
  firefighter: refreshed
`
