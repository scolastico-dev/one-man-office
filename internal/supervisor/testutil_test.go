package supervisor

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
	"github.com/scolastico-dev/one-man-office/internal/verbs"
)

var omoBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "omo-bin-*")
	if err != nil {
		panic(err)
	}
	omoBin = filepath.Join(dir, "omo")
	cmd := exec.Command("go", "build", "-o", omoBin, "../../cmd/omo")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic(string(out))
	}
	// Fast tests: short delays, isolated git.
	StartPromptDelay = 10 * time.Millisecond
	ReadyTimeout = 3 * time.Second
	GraceBeforeReap = 50 * time.Millisecond
	os.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	os.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	os.Setenv("GIT_AUTHOR_NAME", "omo-test")
	os.Setenv("GIT_AUTHOR_EMAIL", "omo@test")
	os.Setenv("GIT_COMMITTER_NAME", "omo-test")
	os.Setenv("GIT_COMMITTER_EMAIL", "omo@test")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type office struct {
	Dir string
	DB  *sql.DB
	Sup *Supervisor
	Srv *sockd.Server
}

// newOffice wires a kernel whose every profile runs the fake agent with the
// given scenario file content (one profile per role, all identical cmd).
func newOffice(t *testing.T, scenarios map[string]string) *office {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".omo", "logs"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".omo", "worktrees"), 0o755)
	cfg := &config.Config{
		Repos:  map[string]string{},
		Models: map[string]config.Profile{},
		Roles:  map[string]config.RoleModels{},
		Limits: config.Limits{MaxDevelopers: 4, MaxFreelancers: 2},
	}
	for role, scenario := range scenarios {
		p := filepath.Join(dir, role+".scenario")
		os.WriteFile(p, []byte(scenario), 0o644)
		cfg.Models[role] = config.Profile{Cmd: omoBin, Args: []string{"fake-agent", "--scenario", p}}
		cfg.Roles[role] = config.RoleModels{Models: []string{role}, Assignment: config.AssignmentRoundRobin}
	}
	for _, role := range config.AllRoles { // roles without a scenario still need a profile
		if _, ok := cfg.Roles[role]; !ok {
			cfg.Models[role] = config.Profile{Cmd: "true"}
			cfg.Roles[role] = config.RoleModels{Models: []string{role}, Assignment: config.AssignmentRoundRobin}
		}
	}
	d, err := db.Open(filepath.Join(dir, ".omo", "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	msgs, err := messages.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	sup := New(cfg, d, gitops.New(), dir, msgs)
	srv := sockd.New(sup.SocketPath, sup.Auth)
	verbs.RegisterMail(srv, sup.Mail)
	sup.Register(srv)
	go srv.ListenAndServe()
	t.Cleanup(func() { srv.Close(); sup.KillAll() })
	time.Sleep(50 * time.Millisecond)
	return &office{Dir: dir, DB: d, Sup: sup, Srv: srv}
}

func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", desc)
}

func agentState(t *testing.T, o *office, name string) string {
	t.Helper()
	a, err := db.GetAgent(o.DB, name)
	if err != nil {
		return ""
	}
	return a.State
}

func onlyAgentOfRole(t *testing.T, o *office, role string) string {
	t.Helper()
	as, _ := db.LivingByRole(o.DB, role)
	if len(as) != 1 {
		return ""
	}
	return as[0].Name
}

var _ = bus.PrioNormal // keep the import used until later tests need it
