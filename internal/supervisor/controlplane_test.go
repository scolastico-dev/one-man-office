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

func TestControlLeaseCoversBranchNamerAndReleasesOnSessionExit(t *testing.T) {
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	server := controlplane.New(1, nil, time.Minute)
	h := httptest.NewServer(server.Handler())
	defer h.Close()
	attachControl(t, o, server, h.URL, "one")
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.Spawn("branch_namer", "freelancer", 0, o.Dir, "name branch"); !errors.Is(err, controlplane.ErrLimit) {
		t.Fatalf("branch namer bypassed aggregate limit: %v", err)
	}
	sess, _ := o.Sup.Session(name)
	_ = sess.Kill()
	waitFor(t, 3*time.Second, "lease released", func() bool { used, _ := server.Stats(); return used == 0 })
	if _, err := o.Sup.Spawn("branch_namer", "freelancer", 0, o.Dir, "name branch"); err != nil {
		t.Fatal(err)
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
