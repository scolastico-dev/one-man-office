package supervisor

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/company/controlplane"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"gopkg.in/yaml.v3"
)

func attachControl(t *testing.T, o *office, server *controlplane.Server, url, id string) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Models = o.Sup.Config().Models
	cfg.Roles = o.Sup.Config().Roles
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Dir, ".omo", "omo.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	token, err := server.Register(id, o.Dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := controlplane.NewClient(url, token)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Control = c
}

func TestCompanyCapacityExemptsCoordinationRoles(t *testing.T) {
	tests := []struct {
		role    string
		profile string
	}{
		{role: "ceo", profile: "ceo"},
		{role: "reviewer", profile: "reviewer"},
		{role: "smokealarm", profile: "smokealarm"},
		{role: "firefighter", profile: "firefighter"},
		{role: "branch_namer", profile: "smokealarm"},
	}
	for _, test := range tests {
		t.Run(test.role, func(t *testing.T) {
			o := newOffice(t, map[string]string{test.profile: "ready\nsleep|60s\n"})
			control := capacityControl(t, o, 1)
			lease, err := o.Sup.Control.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = o.Sup.Control.Release(context.Background(), lease) })
			if _, err := o.Sup.Spawn(test.role, test.profile, 0, o.Dir, "work"); err != nil {
				t.Fatalf("capacity-exempt spawn failed: %v", err)
			}
			if used, _ := control.Stats(); used != 1 {
				t.Fatalf("capacity-exempt spawn changed lease use to %d", used)
			}
		})
	}
}

func TestCompanyCapacityCountsWorkRoles(t *testing.T) {
	for _, role := range []string{"product_manager", "developer", "freelancer"} {
		t.Run(role, func(t *testing.T) {
			o := newOffice(t, map[string]string{role: "ready\nsleep|60s\n"})
			control := capacityControl(t, o, 1)
			if _, err := o.Sup.Spawn(role, role, 0, o.Dir, "work"); err != nil {
				t.Fatal(err)
			}
			if used, _ := control.Stats(); used != 1 {
				t.Fatalf("counted spawn used %d leases, want 1", used)
			}
			if _, err := o.Sup.Spawn(role, role, 0, o.Dir, "more work"); !errors.Is(err, controlplane.ErrLimit) {
				t.Fatalf("second counted spawn error = %v, want capacity limit", err)
			}
		})
	}
}

func TestCompanyCapacityRoleSet(t *testing.T) {
	want := map[string]bool{
		"product_manager": true,
		"developer":       true,
		"freelancer":      true,
	}
	for _, role := range config.AllRoles {
		if got := roleConsumesCompanyCapacity(role); got != want[role] {
			t.Fatalf("role %q capacity classification = %t, want %t", role, got, want[role])
		}
	}
	if roleConsumesCompanyCapacity("branch_namer") {
		t.Fatal("branch_namer consumes company capacity")
	}
}

func TestControlLeaseReleasedAfterLaunchFailure(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.Cfg.Models["freelancer"] = o.Sup.Cfg.Models["ceo"]
	p := o.Sup.Cfg.Models["freelancer"]
	p.Cmd = filepath.Join(t.TempDir(), "nonexistent")
	o.Sup.Cfg.Models["freelancer"] = p
	server := controlplane.New(1, nil, time.Minute)
	h := httptest.NewServer(server.Handler())
	defer h.Close()
	attachControl(t, o, server, h.URL, "one")
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work"); err == nil {
		t.Fatal("invalid executable started")
	}
	if used, _ := server.Stats(); used != 0 {
		t.Fatalf("failed spawn retained %d leases", used)
	}
}

func TestControlParentDeathRequestsEmergencyStop(t *testing.T) {
	o := newOffice(t, nil)
	server := controlplane.New(1, nil, time.Minute)
	h := httptest.NewServer(server.Handler())
	attachControl(t, o, server, h.URL, "one")
	h.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.WatchControl(ctx)
	select {
	case <-o.Sup.EmergencyStop():
	case <-time.After(2 * time.Second):
		t.Fatal("parent death did not emergency-stop child")
	}
	if o.Sup.ExitReason() == "" {
		t.Fatal("parent loss lacked user reason")
	}
	if _, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "work"); !errors.Is(err, ErrSpawningHalted) {
		t.Fatalf("spawn after parent death: %v", err)
	}
}
