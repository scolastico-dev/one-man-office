// Package office assembles a running office: config, database, socket
// server, supervisor loops, restart recovery and the CEO session.
package office

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/company/controlplane"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/exporter"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
	"github.com/scolastico-dev/one-man-office/internal/supervisor"
	"github.com/scolastico-dev/one-man-office/internal/transport"
	"github.com/scolastico-dev/one-man-office/internal/verbs"
	bundledplugins "github.com/scolastico-dev/one-man-office/plugins"
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
	runtimeWG        sync.WaitGroup
	closeOnce        sync.Once
}

// OpenReadOnly assembles the query side of an existing office without
// claiming ownership, running recovery, creating transports, or loading
// mutable prompt templates.
func OpenReadOnly(dir string) (*Office, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	home, err := globalhome.Open()
	if err != nil {
		return nil, err
	}
	cfg, err := loadOfficeConfig(abs, home, false)
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
	home, err := globalhome.Open()
	if err != nil {
		return nil, err
	}
	cfg, err := loadOfficeConfig(abs, home, true)
	if err != nil {
		return nil, err
	}
	if mock {
		mockProfiles(cfg)
	}
	cacheTTL := time.Duration(cfg.Usage.RefreshInterval)
	if cacheTTL <= 0 {
		cacheTTL = modelusage.DefaultCacheTTL
	}
	var usageClient modelusage.Fetcher = modelusage.NewCache(&modelusage.Client{}, cacheTTL)
	control, err := controlplane.ClientFromEnv()
	if err != nil {
		return nil, err
	}
	if control != nil {
		usageClient = control
	}
	preflightTimeout := time.Duration(cfg.Startup.CheckTimeout)
	if preflightTimeout <= 0 {
		preflightTimeout = 5 * time.Second
	}
	preflightCtx, cancelPreflight := context.WithTimeout(context.Background(), preflightTimeout)
	if control != nil {
		err = control.Ping(preflightCtx)
	}
	if err == nil {
		err = modelusage.Preflight(preflightCtx, cfg, usageClient)
	}
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
	installed, err := ensureBundledPlugins(abs, filepath.Join(abs, ConfigPath), home)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("install bundled plugins: %w", err)
	}
	for _, name := range installed {
		if name == bundledplugins.ToolsName {
			cfg.Plugins.Installed[name] = config.Plugin{Source: "builtin:tools", Enabled: true}
		}
	}
	pluginSettings := make(map[string]plugins.Settings, len(cfg.Plugins.Installed))
	for name, plugin := range cfg.Plugins.Installed {
		pluginSettings[name] = plugins.Settings{Enabled: plugin.Enabled, Config: plugin.Config}
	}
	globalSettings := make(map[string]plugins.Settings, len(home.Config.Plugins.Installed))
	for name, plugin := range home.Config.Plugins.Installed {
		globalSettings[name] = plugins.Settings{Enabled: plugin.Enabled, Config: plugin.Config}
	}
	pluginManager, err := plugins.LoadSourcesWithOptions(abs, d, plugins.Options{LogLines: cfg.Plugins.LogLines},
		plugins.Source{Root: filepath.Join(home.Dir, "plugins"), Configured: globalSettings, Shared: true},
		plugins.Source{Root: filepath.Join(abs, plugins.Dir), Configured: pluginSettings})
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("load plugins: %w", err)
	}
	defer func() {
		if failed {
			_ = pluginManager.Close()
		}
	}()
	socketPath, socketDisplay, cleanupTransport, err := transport.Endpoint(abs)
	if err != nil {
		d.Close()
		return nil, err
	}
	sup := supervisor.New(cfg, d, gitops.New(), abs, msgs)
	sup.Usage = usageClient
	sup.Control = control
	sup.Plugins = pluginManager
	pluginManager.Snapshot = sup.PluginSnapshot
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
	if !o.Cfg.GitIntegration {
		o.Warnings = append(o.Warnings, o.excludeOfficeState()...)
	}
	failed = false
	return o, nil
}

// loadOfficeConfig applies the optional global partial template after the
// office's own configuration has been loaded. Auto-sync persists the merged
// result; the normal enabled mode remains an in-memory overlay.
func loadOfficeConfig(abs string, home *globalhome.Home, write bool) (*config.Config, error) {
	path := filepath.Join(abs, ConfigPath)
	var cfg *config.Config
	var err error
	if write {
		cfg, err = config.Load(path)
	} else {
		cfg, err = config.LoadReadOnly(path)
	}
	if err != nil {
		return nil, err
	}
	if !home.Config.Template.Enabled {
		return cfg, nil
	}
	template, err := home.PrepareTemplate(abs)
	if err != nil {
		return nil, fmt.Errorf("prepare global template: %w", err)
	}
	override, ok := template.ConfigOverride()
	if !ok {
		return cfg, nil
	}
	if write && home.Config.Template.AutoSync {
		if err := applyTemplateConfigOverride(path, override); err != nil {
			return nil, fmt.Errorf("auto-sync global config template: %w", err)
		}
		return config.Load(path)
	}
	merged, err := mergedTemplateConfig(path, override)
	if err != nil {
		return nil, fmt.Errorf("merge global config template: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".omo-config-overlay-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(merged); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	return config.LoadReadOnly(tmpPath)
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
	if err := o.Sup.CleanupTerminalWorktrees(); err != nil {
		db.AppendEvent(o.DB, "cleanup_error", "", 0, "startup worktree reconciliation: "+err.Error())
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
	if o.Sup.Control != nil {
		o.startRuntime(func() { o.Sup.WatchControl(ctx) })
	}
	o.startRuntime(func() { o.Sup.DispatchLoop(ctx) })
	o.startRuntime(func() { o.Sup.SmokeLoop(ctx) })
	o.startRuntime(func() { o.Sup.CleanupLoop(ctx) })
	o.startRuntime(func() { o.Sup.CEOActivityLoop(ctx) })
	o.startRuntime(func() { o.Sup.StatisticsLoop(ctx) })
	o.startRuntime(func() { o.Sup.UsageLoop(ctx) })
	if o.Sup.Plugins != nil {
		o.startRuntime(func() { o.Sup.Plugins.Run(ctx) })
	}
	o.Sup.PruneInactiveLogs()
	goal := o.Sup.Msgs.CEOGoal()
	if o.SafeMode {
		goal += "\n\n" + o.Sup.Msgs.SafeModeGoal()
	}
	_, err := o.Sup.SpawnConfiguredRole("ceo", 0, o.Dir, goal, 0)
	if o.Sup.Plugins != nil {
		snapshot := o.Sup.PluginSnapshot()
		_, _ = o.Sup.Plugins.EmitLifecycle(context.Background(), plugins.Event{
			Name: plugins.EventStartup,
			Data: map[string]any{
				"office_path":            o.Dir,
				"office_started_at_unix": snapshot["office_started_at_unix"],
			},
		})
	}
	if errors.Is(err, controlplane.ErrLimit) {
		return nil
	}
	return err
}

func (o *Office) startRuntime(run func()) {
	o.runtimeWG.Add(1)
	go func() {
		defer o.runtimeWG.Done()
		run()
	}()
}

func (o *Office) Close() {
	o.closeOnce.Do(func() {
		if o.ReadOnly {
			if o.DB != nil {
				o.DB.Close()
			}
			return
		}
		if o.Sup != nil {
			o.Sup.EmitShutdown(false)
		}
		if o.cancel != nil {
			o.cancel()
		}
		// Stop accepting commands before waiting for dispatch and the other
		// runtime loops. In particular, dispatch may still be changing a repo's
		// git metadata when cancellation arrives; the database and office files
		// must stay alive until that operation has returned.
		o.Srv.Close()
		o.Sup.Plugins.Close()
		o.runtimeWG.Wait()
		o.Sup.KillAll()
		_ = o.Sup.CleanupTerminalWorktrees()
		_ = o.Sup.PersistOverallStatistics()
		if o.Cfg.GitIntegration {
			_, _ = exporter.Git(o.Dir, o.DB)
		}
		if o.Sup.Plugins != nil {
			_ = o.Sup.Plugins.Close()
		}
		o.DB.Close()
		if o.transportCleanup != nil {
			o.transportCleanup()
		}
		if o.instanceLock != nil {
			o.instanceLock.release()
		}
	})
}
