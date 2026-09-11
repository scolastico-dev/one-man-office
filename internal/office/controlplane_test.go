package office

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/company/controlplane"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
	"gopkg.in/yaml.v3"
)

type remoteUsageFixture struct{}

func (remoteUsageFixture) Fetch(context.Context, string, config.Profile) (modelusage.Snapshot, error) {
	return modelusage.Snapshot{UsedPercent: 17}, nil
}

func TestOpenSupervisedOfficePublishesLiveStateProvider(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
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
	o, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-ada", Role: "developer", Profile: "mock", JobID: 7}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "developer-ada", "working"); err != nil {
		t.Fatal(err)
	}
	o.Sup.SetTUIState("peek", "developer-ada")
	if err := o.Sup.Control.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := s.Snapshot("child")
	if len(state.Agents) != 1 || state.Agents[0].Name != "developer-ada" || state.TUI.Mode != "peek" {
		t.Fatalf("supervised live state = %#v", state)
	}
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

func TestSupervisedOfficeExemptsInitialCEOFromCapacity(t *testing.T) {
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
		t.Fatalf("office failed to start capacity-exempt CEO: %v", err)
	}
	if n, _ := db.CountLivingByRole(o.DB, "ceo"); n != 1 {
		t.Fatalf("capacity-exempt CEO did not start: %d living", n)
	}
	if used, _ := s.Stats(); used != 1 {
		t.Fatalf("capacity-exempt CEO changed lease use to %d", used)
	}
	if err := c.Release(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
}
