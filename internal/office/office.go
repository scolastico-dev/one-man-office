// Package office assembles a running office: config, database, socket
// server, supervisor loops, restart recovery and the CEO session.
package office

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
	"github.com/scolastico-dev/one-man-office/internal/supervisor"
	"github.com/scolastico-dev/one-man-office/internal/transport"
	"github.com/scolastico-dev/one-man-office/internal/verbs"
)

// ConfigPath is the office config, relative to the office root.
const ConfigPath = ".omo/omo.yaml"

type Office struct {
	Dir      string
	Cfg      *config.Config
	DB       *sql.DB
	Sup      *supervisor.Supervisor
	Srv      *sockd.Server
	Warnings []string
	SafeMode bool
	ReadOnly bool

	cancel           context.CancelFunc
	transportCleanup func()
	instanceLock     *instanceLock
}

// OpenReadOnly assembles the query side of an existing office without
// claiming ownership, running recovery, creating transports, or loading
// mutable prompt templates.
func OpenReadOnly(dir string) (*Office, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadReadOnly(filepath.Join(abs, ConfigPath))
	if err != nil {
		return nil, err
	}
	d, err := db.OpenReadOnly(filepath.Join(abs, ".omo", "omo.db"))
	if err != nil {
		return nil, err
	}
	sup := supervisor.New(cfg, d, gitops.New(), abs, nil)
	return &Office{Dir: abs, Cfg: cfg, DB: d, Sup: sup, ReadOnly: true}, nil
}

func Open(dir string, mock bool) (*Office, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(filepath.Join(abs, ConfigPath))
	if err != nil {
		return nil, err
	}
	if mock {
		mockProfiles(cfg)
	}
	usageClient := &modelusage.Client{}
	preflightTimeout := time.Duration(cfg.Startup.CheckTimeout)
	if preflightTimeout <= 0 {
		preflightTimeout = 5 * time.Second
	}
	preflightCtx, cancelPreflight := context.WithTimeout(context.Background(), preflightTimeout)
	err = modelusage.Preflight(preflightCtx, cfg, usageClient)
	cancelPreflight()
	if err != nil {
		return nil, err
	}
	msgs, err := messages.Load(abs)
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"extensions", "logs", "storage", "worktrees"} {
		if err := os.MkdirAll(filepath.Join(abs, ".omo", sub), 0o755); err != nil {
			return nil, err
		}
	}
	lock, err := claimInstance(abs)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			lock.release()
		}
	}()
	d, err := db.Open(filepath.Join(abs, ".omo", "omo.db"))
	if err != nil {
		return nil, err
	}
	socketPath, socketDisplay, cleanupTransport, err := transport.Endpoint(abs)
	if err != nil {
		d.Close()
		return nil, err
	}
	sup := supervisor.New(cfg, d, gitops.New(), abs, msgs)
	sup.Usage = usageClient
	sup.SocketPath = socketPath
	sup.SocketDisplay = socketDisplay
	srv := sockd.New(sup.SocketPath, sup.Auth)
	verbs.RegisterMail(srv, sup.Mail)
	sup.Register(srv)
	if err := srv.Listen(); err != nil {
		d.Close()
		cleanupTransport()
		return nil, err
	}
	if err := lock.setEndpoint(socketPath); err != nil {
		srv.Close()
		d.Close()
		cleanupTransport()
		return nil, fmt.Errorf("write instance lock: %w", err)
	}
	o := &Office{Dir: abs, Cfg: cfg, DB: d, Sup: sup, Srv: srv, transportCleanup: cleanupTransport, instanceLock: lock}
	if err := o.recover(); err != nil {
		srv.Close()
		d.Close()
		cleanupTransport()
		return nil, err
	}
	o.Warnings = append(o.Warnings, o.excludeOfficeState()...)
	failed = false
	return o, nil
}

// excludeOfficeState keeps omo out of the user's repository. When the office
// runs inside a repo (the single-repo layout), .omo — database, logs and the
// per-job worktrees — sits in that repo's working tree, where it would show
// up in `git status` and be swept into a developer's `git add .`.
func (o *Office) excludeOfficeState() []string {
	var warnings []string
	for key, path := range o.Cfg.Repos {
		if !within(o.Dir, path) {
			continue
		}
		rel, err := filepath.Rel(path, filepath.Join(o.Dir, ".omo"))
		if err != nil {
			continue
		}
		if err := o.Sup.Git.Exclude(path, "/"+filepath.ToSlash(rel)+"/"); err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"could not hide the office state from repo %q (%v) — add %s to its .git/info/exclude by hand",
				key, err, rel))
		}
	}
	return warnings
}

// within reports whether dir is inside (or equal to) root.
func within(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

// MockSelf overrides the executable used for mock profiles (tests set it to
// a freshly built omo binary — os.Executable() would be the test binary).
var MockSelf string

// mockProfiles replaces every role's profile with the omo binary running
// the embedded fake-agent scenario for that role (keys: mock-<role>).
func mockProfiles(cfg *config.Config) {
	self := MockSelf
	if self == "" {
		if exe, err := os.Executable(); err == nil {
			self = exe
		} else {
			self = "omo"
		}
	}
	// The mock scenarios create a developer job, so they need a real repo
	// key from this office — there is no guarantee one is called "demo".
	env := map[string]string{}
	if keys := SortedKeys(cfg.Repos); len(keys) > 0 {
		env["OMO_MOCK_REPO"] = keys[0]
	}
	for _, role := range config.AllRoles {
		key := "mock-" + role
		cfg.Models[key] = config.Profile{
			Cmd: self, Args: []string{"fake-agent", "--auto-role", role}, Env: env,
		}
		cfg.Roles[role] = config.RoleModels{Models: []string{key}, Assignment: config.AssignmentRoundRobin}
	}
}

// recover implements the intentionally dumb restart recovery from the spec.
func (o *Office) recover() error {
	if err := db.MarkAllAgentsDead(o.DB); err != nil {
		return err
	}
	closedIncidents, err := db.CloseOpenIncidentsForRestart(o.DB)
	if err != nil {
		return fmt.Errorf("close restart incidents: %w", err)
	}
	nonTerminal := []queue.State{
		queue.StateAssigned, queue.StateWorking, queue.StateReview, queue.StateMerging, queue.StateRework,
	}
	jobs, err := o.Sup.Jobs.List(nonTerminal...)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		note := o.Sup.Msgs.RestartNote()
		if j.State == queue.StateReview {
			note = o.Sup.Msgs.RestartReviewNote()
		}
		o.Sup.Jobs.SetNote(j.ID, note)
		o.Sup.Jobs.SetAssignee(j.ID, "")
		if err := o.Sup.Jobs.Retry(j.ID); err != nil {
			return fmt.Errorf("recover job %d: %w", j.ID, err)
		}
	}
	if closedIncidents > 0 {
		db.AppendEvent(o.DB, "incidents_closed_on_restart", "", 0, fmt.Sprintf("closed %d open incidents", closedIncidents))
	}
	db.AppendEvent(o.DB, "office_started", "", 0, fmt.Sprintf("recovered %d jobs; closed %d incidents", len(jobs), closedIncidents))
	o.Sup.BeginSession()
	return nil
}

// Start launches the socket server, the supervisor loops and the CEO.
func (o *Office) Start() error {
	if o.SafeMode {
		o.Sup.EnterSafeMode()
	}
	ctx, cancel := context.WithCancel(context.Background())
	o.cancel = cancel
	go o.Srv.Serve()
	go o.Sup.DispatchLoop(ctx)
	go o.Sup.SmokeLoop(ctx)
	go o.Sup.NudgeLoop(ctx)
	go o.Sup.StatusLoop(ctx)
	go o.Sup.CleanupLoop(ctx)
	go o.Sup.CEOActivityLoop(ctx)
	go o.Sup.StatisticsLoop(ctx)
	o.Sup.PruneInactiveLogs()
	goal := o.Sup.Msgs.CEOGoal()
	if o.SafeMode {
		goal += "\n\n" + o.Sup.Msgs.SafeModeGoal()
	}
	_, err := o.Sup.SpawnConfiguredRole("ceo", 0, o.Dir, goal, 0)
	return err
}

func (o *Office) Close() {
	if o.ReadOnly {
		if o.DB != nil {
			o.DB.Close()
		}
		return
	}
	if o.cancel != nil {
		o.cancel()
	}
	o.Sup.KillAll()
	_ = o.Sup.PersistOverallStatistics()
	o.Srv.Close()
	o.DB.Close()
	if o.transportCleanup != nil {
		o.transportCleanup()
	}
	if o.instanceLock != nil {
		o.instanceLock.release()
	}
}
