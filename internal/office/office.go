// Package office assembles a running office: config, database, socket
// server, supervisor loops, restart recovery and the CEO session.
package office

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/messages"
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

	cancel           context.CancelFunc
	transportCleanup func()
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
	msgs, err := messages.Load(abs)
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"logs", "worktrees"} {
		if err := os.MkdirAll(filepath.Join(abs, ".omo", sub), 0o755); err != nil {
			return nil, err
		}
	}
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
	sup.SocketPath = socketPath
	sup.SocketDisplay = socketDisplay
	srv := sockd.New(sup.SocketPath, sup.Auth)
	verbs.RegisterMail(srv, sup.Mail)
	sup.Register(srv)
	o := &Office{Dir: abs, Cfg: cfg, DB: d, Sup: sup, Srv: srv, transportCleanup: cleanupTransport}
	if err := o.recover(); err != nil {
		d.Close()
		cleanupTransport()
		return nil, err
	}
	o.Warnings = append(o.Warnings, o.excludeOfficeState()...)
	o.Warnings = append(o.Warnings, superpowersWarnings(cfg)...)
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
		cfg.Roles[role] = key
	}
}

// recover implements the intentionally dumb restart recovery from the spec.
func (o *Office) recover() error {
	if err := db.MarkAllAgentsDead(o.DB); err != nil {
		return err
	}
	nonTerminal := []queue.State{
		queue.StateAssigned, queue.StateWorking, queue.StateReview, queue.StateMerging, queue.StateRework,
	}
	jobs, err := o.Sup.Jobs.List(nonTerminal...)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		o.Sup.Jobs.SetNote(j.ID, o.Sup.Msgs.RestartNote())
		o.Sup.Jobs.SetAssignee(j.ID, "")
		if err := o.Sup.Jobs.Transition(j.ID, queue.StateQueued); err != nil {
			return fmt.Errorf("recover job %d: %w", j.ID, err)
		}
	}
	db.AppendEvent(o.DB, "office_started", "", 0, fmt.Sprintf("recovered %d jobs", len(jobs)))
	o.Sup.BeginSession()
	return nil
}

// superpowersWarnings warns when a claude-based profile is configured but
// the superpowers plugin cannot be found under ~/.claude/plugins.
func superpowersWarnings(cfg *config.Config) []string {
	usesClaude := false
	for _, p := range cfg.Models {
		if strings.Contains(filepath.Base(p.Cmd), "claude") {
			usesClaude = true
			break
		}
	}
	if !usesClaude {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".claude", "plugins")
	found := false
	filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return fs.SkipDir
		}
		if e.IsDir() && strings.HasPrefix(e.Name(), "superpowers") {
			found = true
			return fs.SkipAll
		}
		if e.IsDir() && strings.Count(strings.TrimPrefix(path, root), string(os.PathSeparator)) > 4 {
			return fs.SkipDir
		}
		return nil
	})
	if !found {
		return []string{"superpowers plugin not found under ~/.claude/plugins — role prompts mandate its skills; install it for the configured claude CLI"}
	}
	return nil
}

// Start launches the socket server, the supervisor loops and the CEO.
func (o *Office) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	o.cancel = cancel
	go o.Srv.ListenAndServe()
	go o.Sup.DispatchLoop(ctx)
	go o.Sup.SmokeLoop(ctx)
	go o.Sup.NudgeLoop(ctx)
	go o.Sup.CleanupLoop(ctx)
	go o.Sup.CEOActivityLoop(ctx)
	o.Sup.PruneInactiveLogs()
	_, err := o.Sup.Spawn("ceo", o.Cfg.Roles["ceo"], 0, o.Dir, o.Sup.Msgs.CEOGoal())
	return err
}

func (o *Office) Close() {
	if o.cancel != nil {
		o.cancel()
	}
	o.Sup.KillAll()
	o.Srv.Close()
	o.DB.Close()
	if o.transportCleanup != nil {
		o.transportCleanup()
	}
}
