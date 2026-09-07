package office

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
	"github.com/scolastico-dev/one-man-office/internal/websupervisor/controlplane"
	"gopkg.in/yaml.v3"
)

type remoteUsageFixture struct{}

func (remoteUsageFixture) Fetch(context.Context, string, config.Profile) (modelusage.Snapshot, error) {
	return modelusage.Snapshot{UsedPercent: 17}, nil
}

func TestOpenSupervisedOfficeUsesRemoteUsage(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	for key, p := range cfg.Models {
		p.Env = map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(dir, "no-credentials")}
		p.Cmd = "claude"
		p.Provider = "claude"
		cfg.Models[key] = p
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigPath), raw, 0600); err != nil {
		t.Fatal(err)
	}
	s := controlplane.New(2, remoteUsageFixture{}, time.Minute)
	token, err := s.Register("child", dir)
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	t.Setenv("OMO_CONTROL_URL", h.URL)
	t.Setenv("OMO_CONTROL_TOKEN", token)
	o, err := Open(dir, false)
	if err != nil {
		t.Fatalf("remote usage preflight: %v", err)
	}
	defer o.Close()
	if _, ok := o.Sup.Usage.(*controlplane.Client); !ok {
		t.Fatalf("local usage fetcher active: %T", o.Sup.Usage)
	}
}

func TestOpenSupervisedOfficeCannotIgnoreMissingParent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(controlplane.New(1, nil, time.Minute).Handler())
	h.Close()
	t.Setenv("OMO_CONTROL_URL", h.URL)
	t.Setenv("OMO_CONTROL_TOKEN", "missing")
	if o, err := Open(dir, true); err == nil {
		o.Close()
		t.Fatal("mock office ignored missing parent")
	}
	if _, err := os.Stat(filepath.Join(dir, LockPath)); !os.IsNotExist(err) {
		t.Fatal("missing-parent startup claimed runtime lock")
	}
}

func TestSupervisedOfficeWaitsForInitialCEOCapacity(t *testing.T) {
	o, _ := mockOffice(t)
	s := controlplane.New(1, nil, time.Minute)
	token, err := s.Register("child", o.Dir)
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	defer o.Close()
	c, err := controlplane.NewClient(h.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Control = c
	lease, err := c.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Start(); err != nil {
		t.Fatalf("office exited instead of waiting for CEO capacity: %v", err)
	}
	if n, _ := db.CountLivingByRole(o.DB, "ceo"); n != 0 {
		t.Fatal("CEO bypassed process cap")
	}
	if err := c.Release(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 4*time.Second, "CEO starts after capacity becomes available", func() bool { n, _ := db.CountLivingByRole(o.DB, "ceo"); return n == 1 })
}
