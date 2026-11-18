// Package plugins runs the plugins the user installed on the board's events.
//
// A plugin is a folder below the plugin root (<home>/plugins/<name>) with a
// manifest, plugin.json, that names its hooks. A hook reacts to one event
// and is either a Lua script, run in a fresh luabox sandbox for every run,
// or a command, run with the manifest's argument list. Every plugin has one
// scope: the whole omo, as the user.
//
// What reaches a plugin from the board is data only. An event is JSON, the
// standard input of a command hook and the global event of a Lua hook; the
// text people and agents write (titles, comments, attention texts, names)
// is a string value in it, never an argument, an environment value, a file
// name or source text, and never a key that selects anything. See events.go
// for the payloads.
//
// What a plugin can do:
//   - a command hook is a program the plugin names, run in the plugin's
//     folder with omo's environment minus every OMO_ variable plus
//     OMO_SOCKET (so "omo ai" from it acts as the user, through the same
//     API and checks as the user's own requests), OMO_PLUGIN_NAME,
//     OMO_PLUGIN_EVENT, OMO_PLUGIN_CONFIG and OMO_PLUGIN_DATA;
//   - a Lua hook has the sandbox of luabox (no io, no os.execute, no
//     loaders) plus omo.exec (a program with an argument list, no shell, in
//     the plugin's folder with the environment above), omo.http (luabox's
//     bounded helper), omo.sleep (luabox's: at most 10 s, never past the
//     run's time limit), omo.store_get/set/delete/keys (the plugin's own
//     values in omo's database, bounded), omo.data_dir (the plugin's own
//     folder below <root>/.data), omo.agents (the running agents, named
//     by their terminal id), omo.send_to_agent (one line typed into a
//     running agent's terminal through omo's own notification path; see
//     agents.go) and luabox's own helpers (json_decode,
//     json_encode, sha256_hex, nfc, time_parse, now, log).
//
// Bounds: every run has its hook's time limit (30 s unless the manifest says
// otherwise, at most 5 minutes). Events are delivered to each plugin in
// order by one goroutine of that plugin, through a queue of 256 events; a
// full queue drops the event for that plugin and the plugin's log says how
// many were dropped. One kind of event runs a plugin's hooks at most 60
// times a minute (DefaultHookRunsPerMinute); more are dropped for that
// plugin with one log line per minute. A slow or failing plugin never holds the caller of
// Dispatch or another plugin. Errors are kept per plugin (state, message,
// log), cleaned of the plugin's configuration values and of hidden
// characters, and never raised to the caller. Close stops everything and
// waits; nothing runs after it.
package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/luabox"
)

// Bounds of manifests, hooks and runs.
const (
	// ManifestFile is the manifest's name in a plugin's folder.
	ManifestFile = "plugin.json"
	// MaxManifestBytes bounds plugin.json.
	MaxManifestBytes = 64 << 10
	// MaxLuaBytes bounds the source of one Lua hook.
	MaxLuaBytes = luabox.DefaultMaxSource
	// QueueSize is the number of events waiting for one plugin.
	QueueSize = 256
	// DefaultHookRunsPerMinute is how often one kind of event runs a
	// plugin's hooks within a minute; further events of that kind are
	// dropped for the plugin until the minute is over. omo cannot tell
	// which comments or moves a plugin itself caused (its requests reach
	// the socket as the user's), so this is what keeps a hook that causes
	// its own event (a comment_written hook that writes a comment) from
	// spinning for ever.
	DefaultHookRunsPerMinute = 60
	// DefaultTimeout is a hook's time limit when its manifest names none.
	DefaultTimeout = 30 * time.Second
	// MaxTimeout is the longest time limit a manifest may name.
	MaxTimeout = 5 * time.Minute
	// MinTimeout is the shortest time limit a manifest may name.
	MinTimeout = time.Second
	// MinInterval is the shortest interval of a timer hook.
	MinInterval = time.Minute
	// LogLines is the number of log lines kept per plugin.
	LogLines = 500

	maxHooks       = 64
	maxArgs        = 64
	maxArgBytes    = 4 << 10
	maxDescription = 1000
	maxActionDesc  = 200
)

// Values of api.PluginInfo.State.
const (
	StateReady        = "ready"
	StateRunning      = "running"
	StateError        = "error"
	StateDisabled     = "disabled"
	StateNotInstalled = "not_installed"
	StateInvalid      = "invalid"
)

// Scopes of a manual hook.
const (
	ScopeGlobal = "global"
	ScopeTicket = "ticket"
)

var (
	// ErrNotInstalled is the error of a plugin whose folder or manifest is
	// missing.
	ErrNotInstalled = errors.New("not installed: the plugin's folder or its plugin.json is missing")
	// ErrClosed is the error of a call after Close.
	ErrClosed = errors.New("plugins are stopped")
	// ErrUnknownPlugin is the error of an action of a plugin that is not
	// loaded.
	ErrUnknownPlugin = errors.New("no such plugin is installed and enabled")
	// ErrUnknownAction is the error of an action the plugin does not have.
	ErrUnknownAction = errors.New("the plugin has no such action")
	// ErrTicketRequired is the error of a ticket action without a ticket.
	ErrTicketRequired = errors.New("this action needs a ticket")
	// ErrBusy is the error of an action while another action of the same
	// plugin runs.
	ErrBusy = errors.New("an action of this plugin is already running")
)

var (
	namePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	actionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ValidName reports whether name can name a plugin: one folder name of
// letters, digits, '.', '_' and '-' that starts with a letter or digit.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// Manifest is a plugin's plugin.json.
type Manifest struct {
	Name          string                  `json:"name,omitempty"`
	Version       string                  `json:"version,omitempty"`
	Description   string                  `json:"description,omitempty"`
	DefaultConfig DefaultConfig           `json:"default_config,omitempty"`
	Hooks         []Hook                  `json:"hooks"`
	Buttons       []api.PluginButton      `json:"buttons,omitempty"`
	Frontend      api.PluginFrontend      `json:"frontend,omitzero"`
	ConfigFields  []api.PluginConfigField `json:"config_fields,omitempty"`
	contentID     string
	manifestHash  [32]byte
	frontendFiles map[string]frontendFile // immutable per-file digests computed at load
}

var (
	errConfigObject = errors.New("default_config must be a JSON object")
	errConfigJSON   = errors.New("default_config is not valid JSON")
)

// DefaultConfig is the configuration a plugin starts with: a JSON object.
type DefaultConfig map[string]any

// UnmarshalJSON accepts a JSON object only and keeps numbers exact.
func (c *DefaultConfig) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return errConfigObject
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return errConfigJSON
	}
	*c = m
	return nil
}

// Hook is one hook of a manifest.
type Hook struct {
	Event string `json:"event"`
	// Lua is the path of a Lua script in the plugin's folder; Command is a
	// program and its arguments. A hook has exactly one of them.
	Lua     string   `json:"lua,omitempty"`
	Command []string `json:"command,omitempty"`
	// Timeout is the run's time limit (a duration, default 30s, 1s to 5m).
	Timeout string `json:"timeout,omitempty"`
	// Interval (a duration of at least 1 minute) or IntervalConfig (the key
	// of the configuration that holds it) are for timer hooks only.
	Interval       string `json:"interval,omitempty"`
	IntervalConfig string `json:"interval_config,omitempty"`
	// Name, Description and Scope (ticket or global, default global) are
	// for manual hooks only.
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Scope       string `json:"scope,omitempty"`
	// Hidden suppresses automatic manual-action controls; explicit buttons and
	// direct invocation remain available. It is presentation, not authorization.
	Hidden bool `json:"hidden,omitempty"`
}

// removedFields are manifest fields the engine had once, with what to say
// about them. A manifest that uses one does not load.
var removedFields = map[string]string{
	"startup":     "use hooks with event omo_started",
	"load":        "use hooks with event omo_started",
	"unload":      "there is no shutdown event; use omo_started for start-up",
	"shutdown":    "there is no shutdown event; use omo_started for start-up",
	"requires":    "plugins have no dependencies any more; install each plugin by itself",
	"roles":       "only the user runs manual actions",
	"manual_args": "manual actions take no arguments",
	"javascript":  "use frontend.script and frontend.assets",
	"files":       "use frontend.script and frontend.assets",
}

// removedEvents are events the engine had once, with what to use instead.
var removedEvents = map[string]string{
	"prompt_render":    "plugins cannot change prompts",
	"company_load":     "use frontend.script and frontend.assets",
	"agent_log_line":   "plugins do not see agents' output",
	"job_create":       "use agent_started",
	"agent_start":      "use agent_started",
	"cron":             "use timer",
	"chron":            "use timer",
	"load":             "use omo_started for start-up; there is no shutdown event",
	"unload":           "use omo_started for start-up; there is no shutdown event",
	"startup":          "use omo_started for start-up; there is no shutdown event",
	"shutdown":         "use omo_started for start-up; there is no shutdown event",
	"company_startup":  "use omo_started for start-up; there is no shutdown event",
	"company_shutdown": "use omo_started for start-up; there is no shutdown event",
}

var manifestFields = map[string]bool{"name": true, "version": true, "description": true, "default_config": true, "hooks": true, "buttons": true, "frontend": true, "config_fields": true}

var hookFields = map[string]bool{
	"event": true, "lua": true, "command": true, "timeout": true, "interval": true,
	"interval_config": true, "name": true, "description": true, "scope": true, "hidden": true,
}

// quoted is text from a manifest as it may appear in an error: cut, quoted
// and with every character that is not printable ASCII escaped.
func quoted(s string) string { return strconv.QuoteToASCII(cutRunes(s, 40)) }

// ReadManifest reads and checks dir/plugin.json: a regular file (no link)
// of at most 64 KiB, strict JSON with the declared nested schema only.
// Public frontend files must exist and pass confinement and resource checks.
// Fields and events the engine no longer has are refused with a message
// that names them. A missing manifest gives an error that wraps
// ErrNotInstalled.
func ReadManifest(dir string) (Manifest, error) {
	raw, err := homefile.Read(filepath.Join(dir, ManifestFile), MaxManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, ErrNotInstalled
	}
	if err != nil {
		if text, ok := homefile.Refusal(err, MaxManifestBytes); ok {
			return Manifest{}, errors.New("plugin.json: " + text)
		}
		return Manifest{}, errors.New("plugin.json cannot be read")
	}
	man, err := ParseManifest(raw)
	if err != nil {
		return Manifest{}, err
	}
	if err := man.readFrontend(dir, raw); err != nil {
		return Manifest{}, err
	}
	return man, nil
}

// ParseManifest checks a manifest's text and path syntax. ReadManifest also
// validates the frontend files on disk and computes the content identity.
func ParseManifest(raw []byte) (Manifest, error) {
	if len(raw) > MaxManifestBytes {
		return Manifest{}, errors.New("plugin.json is larger than 64 KiB")
	}
	var fields map[string]json.RawMessage
	top := json.NewDecoder(bytes.NewReader(raw))
	if err := top.Decode(&fields); err != nil || fields == nil {
		return Manifest{}, errors.New("plugin.json is not a JSON object")
	}
	if err := top.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, errors.New("plugin.json has data after its object")
	}
	if err := checkFields(fields, manifestFields, ""); err != nil {
		return Manifest{}, err
	}
	var hookList []map[string]json.RawMessage
	if h, ok := fields["hooks"]; ok {
		if err := json.Unmarshal(h, &hookList); err != nil {
			return Manifest{}, errors.New("hooks must be a list of objects")
		}
	}
	for i, h := range hookList {
		if h == nil {
			return Manifest{}, fmt.Errorf("hook %d is not an object", i+1)
		}
		if err := checkFields(h, hookFields, fmt.Sprintf("hook %d: ", i+1)); err != nil {
			return Manifest{}, err
		}
		if hidden, ok := h["hidden"]; ok {
			value := string(bytes.TrimSpace(hidden))
			if value != "true" && value != "false" {
				return Manifest{}, fmt.Errorf("hook %d: hidden must be a boolean", i+1)
			}
			var event string
			if err := json.Unmarshal(h["event"], &event); err == nil && event != EventManual {
				return Manifest{}, fmt.Errorf("hook %d: hidden is for manual hooks only", i+1)
			}
		}
	}
	if err := checkDeclarationJSON(fields); err != nil {
		return Manifest{}, err
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, errConfigObject) || errors.Is(err, errConfigJSON) {
			return Manifest{}, err
		}
		return Manifest{}, errors.New("plugin.json has a value of the wrong type")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, errors.New("plugin.json has data after its object")
	}
	if m.Name != "" && !ValidName(m.Name) {
		return Manifest{}, errors.New("name must be letters, digits, '.', '_' or '-', starting with a letter or digit, at most 64 characters")
	}
	if m.Version != "" {
		if err := validVersion(m.Version); err != nil {
			return Manifest{}, err
		}
	}
	if len(m.Description) > maxDescription {
		return Manifest{}, fmt.Errorf("description is longer than %d bytes", maxDescription)
	}
	if len(m.Hooks) > maxHooks {
		return Manifest{}, fmt.Errorf("a plugin has at most %d hooks", maxHooks)
	}
	actions := map[string]bool{}
	for i := range m.Hooks {
		h := &m.Hooks[i]
		if err := checkHook(h); err != nil {
			return Manifest{}, fmt.Errorf("hook %d: %w", i+1, err)
		}
		if h.Event == EventManual {
			if actions[h.Name] {
				return Manifest{}, fmt.Errorf("hook %d: another manual hook has the name %s", i+1, quoted(h.Name))
			}
			actions[h.Name] = true
		}
	}
	if err := checkDeclarations(&m, fields); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// checkFields refuses removed and unknown fields.
func checkFields(fields map[string]json.RawMessage, allowed map[string]bool, prefix string) error {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if allowed[name] {
			continue
		}
		if why, ok := removedFields[name]; ok {
			return fmt.Errorf("%s%q is no longer available: %s", prefix, name, why)
		}
		return fmt.Errorf("%sunknown field %s", prefix, quoted(name))
	}
	return nil
}

// checkHook checks one hook and fills in its defaults (scope).
func checkHook(h *Hook) error {
	if why, ok := removedEvents[h.Event]; ok {
		return fmt.Errorf("event %q is no longer available: %s", h.Event, why)
	}
	known := false
	for _, e := range Events {
		known = known || e == h.Event
	}
	if !known {
		return fmt.Errorf("unknown event %s; the events are %s", quoted(h.Event), strings.Join(Events, ", "))
	}
	if (h.Lua == "") == (len(h.Command) == 0) {
		return errors.New("a hook has exactly one of lua and command")
	}
	if h.Lua != "" {
		if _, err := luaPath(h.Lua); err != nil {
			return err
		}
	}
	if len(h.Command) > 0 {
		if len(h.Command) > maxArgs {
			return fmt.Errorf("command has more than %d parts", maxArgs)
		}
		if strings.TrimSpace(h.Command[0]) == "" {
			return errors.New("command names no program")
		}
		for _, a := range h.Command {
			if len(a) > maxArgBytes || strings.ContainsRune(a, 0) {
				return fmt.Errorf("a part of command is longer than %d bytes or holds a NUL character", maxArgBytes)
			}
		}
	}
	if h.Timeout != "" {
		d, err := time.ParseDuration(h.Timeout)
		if err != nil || d < MinTimeout || d > MaxTimeout {
			return errors.New("timeout must be a duration from 1s to 5m")
		}
	}
	if h.Event == EventTimer {
		if (h.Interval == "") == (h.IntervalConfig == "") {
			return errors.New("a timer hook has exactly one of interval and interval_config")
		}
		if h.Interval != "" {
			d, err := time.ParseDuration(h.Interval)
			if err != nil || d < MinInterval {
				return errors.New("interval must be a duration of at least 1m")
			}
		}
		if len(h.IntervalConfig) > 100 {
			return errors.New("interval_config is longer than 100 bytes")
		}
	} else if h.Interval != "" || h.IntervalConfig != "" {
		return errors.New("interval and interval_config are for timer hooks only")
	}
	if h.Event == EventManual {
		if !actionPattern.MatchString(h.Name) {
			return errors.New("a manual hook's name is letters, digits, '.', '_' or '-', starting with a letter or digit, at most 64 characters")
		}
		if strings.TrimSpace(h.Description) == "" || len([]rune(h.Description)) > maxActionDesc {
			return fmt.Errorf("a manual hook needs a description of at most %d characters", maxActionDesc)
		}
		switch h.Scope {
		case "":
			h.Scope = ScopeGlobal
		case ScopeGlobal, ScopeTicket:
		default:
			return errors.New("scope is ticket or global")
		}
	} else if h.Name != "" || h.Description != "" || h.Scope != "" {
		return errors.New("name, description and scope are for manual hooks only")
	}
	return nil
}

// luaPath is a hook's Lua path as a clean relative path inside the plugin.
func luaPath(p string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(p, "\\", "/"))
	if strings.HasPrefix(clean, "/") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
		filepath.IsAbs(p) || filepath.VolumeName(p) != "" || strings.ContainsRune(p, 0) {
		return "", errors.New("lua must be a path inside the plugin's folder")
	}
	return clean, nil
}

// Options configure a Manager.
type Options struct {
	// Root is the plugin root, <home>/plugins: plugin <name> is in
	// Root/<name>, its data folder is Root/.data/<name>.
	Root string
	// DB holds the plugins' runtime records, logs and stored values.
	DB *sql.DB
	// Socket returns the endpoint plugin programs reach omo on
	// (OMO_SOCKET); nil or "" leaves OMO_SOCKET unset.
	Socket func() string
	// HTTP is the client of omo.http; nil uses a fresh client per run.
	HTTP *http.Client
	// ArtifactHTTP supplies a trusted artifact transport; Lua HTTP limits stay separate.
	ArtifactHTTP *http.Client
	// Log receives the manager's own problems (a failed database write);
	// nil discards them. Plugin output never goes there.
	Log *log.Logger
	// Changed, when set, is called with a plugin's info after its state
	// changed. It must not block.
	Changed func(api.PluginInfo)
	// Agents is what omo.agents and omo.send_to_agent reach; nil makes
	// both answer that omo runs no agents.
	Agents   AgentDesk
	Forwards ForwardDesk
	// Unloaded releases plugin resources after hooks have been cancelled.
	Unloaded func(plugin, reason string)
	// TaskTicketLive checks that a ticket exists and is nonterminal; nil
	// refuses every ticket-bound task. Called before and after registration.
	TaskTicketLive func(context.Context, int) bool
	// TaskExited is called exactly once per successful start after reaping,
	// outside locks, before Stop returns. It must not wait for its own task
	// or call Close. Configure callbacks before using the manager.
	TaskExited func(string, api.PluginTask)
	// TasksChanged invalidates a plugin's task snapshot on start/exit.
	// It may be concurrent and must not block.
	TasksChanged func(string)
	// TaskOutputChanged invalidates a changed visible output tail. Adapters
	// should throttle these separately from lifecycle changes.
	TaskOutputChanged func(string)
}

// Manager runs the loaded plugins. Its methods are safe for concurrent use.
type Manager struct {
	o      Options
	ctx    context.Context
	cancel context.CancelFunc

	loadMu sync.Mutex // one Load at a time

	mu         sync.Mutex
	ready      bool
	closed     bool
	closedDone chan struct{}
	entries    []*entry // by name, every plugin of the last Load
	known      map[string]db.PluginRuntime

	artifactsWG    sync.WaitGroup
	artifactsMu    sync.Mutex
	artifacts      map[string]map[string]*artifactJob
	artifactLimits *artifactLimits // tests override finite production bounds
	tasksWG        sync.WaitGroup
	tasksMu        sync.Mutex
	tasks          map[string]*task
	taskSequence   uint64
	exitSequence   uint64
	manual         sync.WaitGroup // running manual actions

	now   func() time.Time
	after func(time.Duration) <-chan time.Time
	// perMinute is DefaultHookRunsPerMinute; tests change it before Load.
	perMinute int
}

// entry is one plugin of the settings and what omo knows about it. Its
// fields after p are guarded by Manager.mu.
type entry struct {
	settings api.PluginSettings
	manifest Manifest
	p        *plugin // nil unless loaded

	state       string
	message     string
	lastEvent   string
	lastRun     *time.Time
	running     int
	started     bool
	startQueued bool
}

// plugin is a loaded plugin: its hooks, configuration, queue and the
// goroutines that deliver to it.
type plugin struct {
	// forwardMu joins resource operations with unload cleanup after cancellation.
	forwardMu  sync.Mutex
	name       string
	dir        string
	entry      *entry
	config     map[string]any
	configJSON string
	codeID     string
	secrets    []string
	hooks      []*hook

	startup chan job
	queue   chan job
	dropped atomic.Int64
	busy    atomic.Bool // a manual action runs
	// rates counts the runs per kind of event in the current minute; only
	// the plugin's deliver goroutine uses it.
	rates map[string]*rateWindow

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// rateWindow is one minute of runs of one kind of event.
type rateWindow struct {
	start time.Time
	runs  int
	told  bool // the drop was logged
}

// allowRun reports whether an event of kind may run p's hooks now (see
// DefaultHookRunsPerMinute) and counts it. The first event dropped in a
// minute writes one line to the plugin's log.
func (m *Manager) allowRun(p *plugin, kind string) bool {
	now := m.now()
	if p.rates == nil {
		p.rates = map[string]*rateWindow{}
	}
	w := p.rates[kind]
	if w == nil || now.Before(w.start) || now.Sub(w.start) >= time.Minute {
		w = &rateWindow{start: now}
		p.rates[kind] = w
	}
	if w.runs < m.perMinute {
		w.runs++
		return true
	}
	if !w.told {
		w.told = true
		m.logLine(p.name, fmt.Sprintf("%s events are dropped for the rest of the minute: the plugin's hooks ran %d times for them within a minute", kind, m.perMinute))
	}
	return false
}

// hook is a checked hook of a loaded plugin.
type hook struct {
	Hook
	index    int
	timeout  time.Duration
	interval time.Duration
	source   string // Lua source, read at load
	chunk    string // Lua chunk name
}

// job is one event for one plugin: for every hook of its event, or for one
// hook (a timer's).
type job struct {
	ev   Event
	only *hook
}

// New returns a manager without plugins. Load them with Load.
func New(o Options) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{o: o, ctx: ctx, cancel: cancel, closedDone: make(chan struct{}), now: time.Now, after: time.After, perMinute: DefaultHookRunsPerMinute}
}

// Run blocks until ctx ends, then closes the manager. Plugins deliver and
// timers fire from Load on; Run only ties the manager's life to ctx.
func (m *Manager) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-m.ctx.Done():
	}
	m.Close()
}

// Close stops every plugin, cancels running hooks and waits until nothing
// of the manager runs any more. No hook runs after Close; later calls of
// Load, Dispatch and Trigger do nothing or fail with ErrClosed.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		<-m.closedDone
		return
	}
	m.closed = true
	defer close(m.closedDone)
	var running []*plugin
	for _, e := range m.entries {
		if e.p != nil {
			running = append(running, e.p)
		}
	}
	m.mu.Unlock()
	m.stopTasks(func(*task) bool { return true }, TaskShutdown)
	m.tasksWG.Wait()
	m.stopArtifacts("", false)
	m.artifactsWG.Wait()
	m.cancel()
	for _, p := range running {
		p.cancel()
	}
	for _, p := range running {
		p.wg.Wait()
	}
	m.manual.Wait()
	if m.o.Unloaded != nil {
		for _, p := range running {
			m.o.Unloaded(p.name, TaskShutdown)
		}
	}
}

// Load reconciles installed Root/<name> trees with settings. Unchanged
// runtimes survive. Config changes cancel old hooks/timers while preserving
// tasks and startup lifecycle; code changes stop the old runtime's tasks.
// Replaced queued events are discarded except undelivered startup, which is
// scheduled on the replacement. Invalid plugins stay off with an error in the
// result and Infos; disabled plugins are omitted from the error result.
func (m *Manager) Load(settings []api.PluginSettings) map[string]error {
	m.loadMu.Lock()
	defer m.loadMu.Unlock()
	errs := map[string]error{}
	list := append([]api.PluginSettings(nil), settings...)
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		for _, s := range list {
			errs[s.Name] = ErrClosed
		}
		return errs
	}
	previous := map[string]*entry{}
	for _, e := range m.entries {
		previous[e.settings.Name] = e
	}
	m.mu.Unlock()
	known := m.knownRuntimes()

	var entries []*entry
	seen := map[string]bool{}
	for _, s := range list {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		e := &entry{settings: s, state: StateReady}
		if old := previous[s.Name]; old != nil {
			m.mu.Lock()
			e.lastEvent, e.lastRun = old.lastEvent, old.lastRun
			if s.Enabled && old.settings.Enabled {
				e.started = old.started
			}
			m.mu.Unlock()
		} else if r, ok := known[s.Name]; ok {
			e.lastEvent = r.LastEvent
			if !r.LastRunAt.IsZero() {
				at := r.LastRunAt
				e.lastRun = &at
			}
		}
		switch {
		case !ValidName(s.Name):
			e.state, e.message = StateInvalid, "the plugin's name is not a valid folder name"
			errs[s.Name] = errors.New(e.message)
		case !s.Enabled:
			e.state = StateDisabled
			if man, err := ReadManifest(filepath.Join(m.o.Root, s.Name)); err == nil {
				e.manifest = man
			}
		default:
			p, man, err := m.open(s)
			e.manifest = man
			switch {
			case errors.Is(err, ErrNotInstalled):
				e.state, e.message = StateNotInstalled, err.Error()
				errs[s.Name] = err
			case err != nil:
				e.state, e.message = StateInvalid, cutRunes(err.Error(), maxErrorRunes)
				errs[s.Name] = err
			default:
				old := previous[s.Name]
				if old != nil && old.p != nil && old.p.codeID == p.codeID && old.p.configJSON == p.configJSON {
					p.cancel()
					m.mu.Lock()
					old.settings = s
					m.mu.Unlock()
					e = old
				} else {
					p.entry = e
					e.p = p
				}
			}
		}
		entries = append(entries, e)
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		for _, e := range entries {
			if e.p != nil {
				e.p.cancel()
			}
		}
		return errs
	}
	old := m.entries
	m.mu.Unlock()

	kept := map[*plugin]bool{}
	next := map[string]*entry{}
	for _, e := range entries {
		next[e.settings.Name] = e
		if e.p != nil {
			kept[e.p] = true
		}
	}
	for _, e := range old {
		if e.p == nil || kept[e.p] {
			continue
		}
		e.p.cancel()
		replacement := next[e.settings.Name]
		reason := ""
		switch {
		case replacement == nil:
			reason = TaskPluginRemoved
		case !replacement.settings.Enabled:
			reason = TaskPluginDisabled
		case replacement.p == nil || replacement.p.codeID != e.p.codeID:
			reason = TaskPluginReplaced
		}
		if reason != "" {
			m.stopArtifacts(e.settings.Name, false)
			m.stopTasks(func(t *task) bool { return t.plugin == e.settings.Name }, reason)
		}
		e.p.wg.Wait()
		if reason != "" && m.o.Unloaded != nil {
			e.p.forwardMu.Lock()
			m.o.Unloaded(e.settings.Name, reason)
			e.p.forwardMu.Unlock()
		}
		if replacement != nil && replacement.p != nil {
			m.mu.Lock()
			replacement.started = e.started
			m.mu.Unlock()
		}
	}
	// Forget removed namespaces, including bounded terminal history.
	for name := range previous {
		if next[name] != nil {
			continue
		}
		m.stopTasks(func(t *task) bool { return t.plugin == name }, TaskPluginRemoved)
		m.stopArtifacts(name, true)
		m.tasksMu.Lock()
		for id, t := range m.tasks {
			if t.plugin == name {
				delete(m.tasks, id)
			}
		}
		m.tasksMu.Unlock()
	}
	m.mu.Lock()
	m.entries = entries
	m.mu.Unlock()
	m.syncRuntimes(entries)
	for _, e := range entries {
		if e.p != nil && previous[e.settings.Name] != e {
			m.start(e.p)
		}
	}
	m.queueStartup()
	for _, e := range entries {
		m.changed(e)
	}
	return errs
}

// knownRuntimes are the runtime records of the database, read once.
func (m *Manager) knownRuntimes() map[string]db.PluginRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.known != nil || m.o.DB == nil {
		return m.known
	}
	m.known = map[string]db.PluginRuntime{}
	rows, err := db.PluginRuntimes(m.o.DB)
	if err != nil {
		m.logf("plugins: read the runtime records: %v", err)
		return m.known
	}
	for _, r := range rows {
		m.known[r.Name] = r
	}
	return m.known
}

// open reads and checks plugin s from Root/<name>.
func (m *Manager) open(s api.PluginSettings) (*plugin, Manifest, error) {
	dir := filepath.Join(m.o.Root, s.Name)
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, Manifest{}, ErrNotInstalled
	}
	if err != nil || !info.IsDir() {
		return nil, Manifest{}, errors.New("the plugin's folder is not a folder")
	}
	man, err := ReadManifest(dir)
	if err != nil {
		return nil, Manifest{}, err
	}
	config := map[string]any{}
	for k, v := range man.DefaultConfig {
		config[k] = v
	}
	for k, v := range s.Config {
		config[k] = v
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, man, errors.New("the configuration cannot be written as JSON")
	}
	p := &plugin{
		name: s.Name, dir: dir, config: config, configJSON: string(configJSON),
		secrets: configSecrets(config), queue: make(chan job, QueueSize), startup: make(chan job, 1),
	}
	p.ctx, p.cancel = context.WithCancel(m.ctx)
	ok := false
	defer func() {
		if !ok {
			p.cancel()
		}
	}()
	for i, h := range man.Hooks {
		lh := &hook{Hook: h, index: i, timeout: DefaultTimeout}
		if h.Timeout != "" {
			lh.timeout, _ = time.ParseDuration(h.Timeout)
		}
		if h.Lua != "" {
			rel, _ := luaPath(h.Lua)
			src, err := homefile.Read(filepath.Join(dir, filepath.FromSlash(rel)), MaxLuaBytes)
			if err != nil {
				if text, ok := homefile.Refusal(err, MaxLuaBytes); ok {
					return nil, man, fmt.Errorf("hook %d: %s", i+1, text)
				}
				return nil, man, fmt.Errorf("hook %d: the Lua file cannot be read", i+1)
			}
			if bytes.HasPrefix(src, []byte("\x1b")) {
				return nil, man, fmt.Errorf("hook %d: precompiled Lua is not accepted", i+1)
			}
			lh.source, lh.chunk = string(src), s.Name+"/"+rel
		}
		if h.Event == EventTimer {
			lh.interval, err = timerInterval(h, config)
			if err != nil {
				return nil, man, fmt.Errorf("hook %d: %w", i+1, err)
			}
		}
		p.hooks = append(p.hooks, lh)
	}
	identity := sha256.New()
	identity.Write([]byte(man.contentID))
	marker, markerErr := homefile.Read(filepath.Join(dir, ".omo-install.json"), MaxManifestBytes)
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return nil, man, errors.New("the plugin install record cannot be read")
	}
	identity.Write(marker)
	identity.Write([]byte(s.Source + "\x00" + s.Subpath + "\x00" + s.Ref))
	for _, h := range p.hooks {
		identity.Write([]byte("\x00" + h.source))
	}
	p.codeID = hex.EncodeToString(identity.Sum(nil))
	ok = true
	return p, man, nil
}

// timerInterval is a timer hook's interval: its own, or the configuration's
// value of interval_config (a duration text or a number of seconds).
func timerInterval(h Hook, config map[string]any) (time.Duration, error) {
	if h.Interval != "" {
		return time.ParseDuration(h.Interval)
	}
	var d time.Duration
	switch v := config[h.IntervalConfig].(type) {
	case string:
		d, _ = time.ParseDuration(v)
	case float64:
		d = time.Duration(v * float64(time.Second))
	case int:
		d = time.Duration(v) * time.Second
	case int64:
		d = time.Duration(v) * time.Second
	case json.Number:
		if f, err := v.Float64(); err == nil {
			d = time.Duration(f * float64(time.Second))
		}
	}
	if d < MinInterval {
		return 0, fmt.Errorf("interval_config: the configuration's value of %s is not a duration of at least 1m", quoted(h.IntervalConfig))
	}
	return d, nil
}

// configSecrets are the string values of a configuration: they are removed
// from every log line and error text of the plugin.
func configSecrets(config map[string]any) []string {
	var out []string
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if depth > luabox.MaxDepth || len(out) > 1000 {
			return
		}
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			for _, x := range v {
				walk(x, depth+1)
			}
		case []any:
			for _, x := range v {
				walk(x, depth+1)
			}
		}
	}
	walk(config, 0)
	return out
}

// redactor is a new redactor holding the plugin's configuration values.
func (p *plugin) redactor() *luabox.Redactor {
	r := luabox.NewRedactor()
	for _, s := range p.secrets {
		r.Add(s)
	}
	return r
}

// start starts the plugin's delivery goroutine and its timers, unless the
// manager is closed.
func (m *Manager) start(p *plugin) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	p.wg.Add(1)
	go m.deliver(p)
	for _, h := range p.hooks {
		if h.Event == EventTimer {
			p.wg.Add(1)
			go m.tick(p, h)
		}
	}
}

// Dispatch hands ev to every loaded plugin with a hook for it and returns
// at once. Each plugin gets its events in the order of Dispatch calls; when
// a plugin's queue is full the event is dropped for it and counted in its
// log. timer and manual events are not dispatched. ev.Data must not be
// changed after the call.
func (m *Manager) Dispatch(ev Event) {
	if ev.Name == EventTimer || ev.Name == EventManual || ev.Name == EventOmoStarted {
		return
	}
	if ev.At.IsZero() {
		ev.At = m.now()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	for _, e := range m.entries {
		if e.p == nil || !e.p.wants(ev.Name) {
			continue
		}
		select {
		case e.p.queue <- job{ev: ev}:
		default:
			e.p.dropped.Add(1)
		}
	}
}

func (p *plugin) wants(event string) bool {
	for _, h := range p.hooks {
		if h.Event == event {
			return true
		}
	}
	return false
}

// deliver runs the plugin's queued events, one at a time, in order.
func (m *Manager) deliver(p *plugin) {
	defer p.wg.Done()
	for {
		var j job
		// Startup has its own reserved slot, never competing for hook capacity.
		select {
		case j = <-p.startup:
		default:
			select {
			case <-p.ctx.Done():
				return
			case j = <-p.startup:
			case j = <-p.queue:
			}
		}
		if p.ctx.Err() != nil {
			return
		}
		if j.ev.Name == EventOmoStarted {
			m.mu.Lock()
			already := p.entry.started
			if !already {
				p.entry.started = true
				p.entry.startQueued = false
			}
			m.mu.Unlock()
			if already {
				continue
			}
		} else if j.only == nil && !m.allowRun(p, j.ev.Name) {
			continue
		}
		if n := p.dropped.Swap(0); n > 0 {
			m.logLine(p.name, fmt.Sprintf("%d events dropped: the plugin's queue of %d was full", n, QueueSize))
		}
		for _, h := range p.hooks {
			if p.ctx.Err() != nil {
				return
			}
			if (j.only == nil && h.Event == j.ev.Name) || j.only == h {
				_, _ = m.runHook(p.ctx, p, h, j.ev)
			}
		}
	}
}

// tick queues a timer event for h at every interval.
func (m *Manager) tick(p *plugin, h *hook) {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case at := <-m.after(h.interval):
			ev := Event{Name: EventTimer, At: at, Data: map[string]any{"at": at.UTC().Format(time.RFC3339)}}
			select {
			case p.queue <- job{ev: ev, only: h}:
			default:
				p.dropped.Add(1)
			}
		}
	}
}

// runHook runs h for ev under parent with h's time limit and records the
// run. Its error is the cleaned error text. A run cancelled because the
// plugin was stopped or reloaded records nothing.
func (m *Manager) runHook(parent context.Context, p *plugin, h *hook, ev Event) (any, error) {
	if parent.Err() != nil || p.ctx.Err() != nil {
		return nil, ErrClosed
	}
	m.begin(p, ev.Name)
	ctx, cancel := context.WithTimeout(parent, h.timeout)
	defer cancel()
	var (
		result any
		lines  []string
		text   string
	)
	if h.Lua != "" {
		result, lines, text = m.runLua(ctx, p, h, ev)
	} else {
		result, lines, text = m.runCommand(ctx, p, h, ev)
	}
	stopped := p.ctx.Err() != nil
	for _, l := range lines {
		m.logLine(p.name, l)
	}
	if text != "" && !stopped {
		m.logLine(p.name, "error in the "+ev.Name+" hook: "+text)
	}
	m.finish(p, ev.Name, text, stopped)
	if text != "" {
		return nil, errors.New(text)
	}
	return result, nil
}

// begin marks a run of p as started.
func (m *Manager) begin(p *plugin, event string) {
	at := m.now().UTC()
	m.mu.Lock()
	e := p.entry
	e.running++
	e.state = StateRunning
	e.lastEvent, e.lastRun = event, &at
	m.mu.Unlock()
	m.saveState(p.name, StateRunning, event, at)
	m.changed(e)
}

// finish marks a run of p as ended, with errText "" on success.
func (m *Manager) finish(p *plugin, event, errText string, stopped bool) {
	m.mu.Lock()
	e := p.entry
	e.running--
	switch {
	case e.running > 0:
		e.state = StateRunning
	case stopped:
		// A run cut short by a reload or Close says nothing about the
		// plugin: the last outcome stays.
		e.state = StateReady
		if e.message != "" {
			e.state = StateError
		}
	case errText != "":
		e.state, e.message = StateError, errText
	default:
		e.state, e.message = StateReady, ""
	}
	state := e.state
	var at time.Time
	if e.lastRun != nil {
		at = *e.lastRun
	}
	m.mu.Unlock()
	m.saveState(p.name, state, event, at)
	m.changed(e)
}

// runCommand runs a command hook: the manifest's argument list, the event
// JSON on standard input, the plugin environment.
func (m *Manager) runCommand(ctx context.Context, p *plugin, h *hook, ev Event) (any, []string, string) {
	red := p.redactor()
	input, err := json.Marshal(ev)
	if err != nil {
		return nil, nil, "the event cannot be written as JSON"
	}
	env, err := m.environment(p, ev.Name)
	if err != nil {
		return nil, nil, red.Clean(err.Error())
	}
	stdout := newHeadBuffer(maxOutputBytes)
	stderr := newTailBuffer(maxStderrBytes)
	cmd := exec.CommandContext(ctx, h.Command[0], h.Command[1:]...)
	cmd.Dir = p.dir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	text := runProgram(ctx, cmd)
	var lines []string
	for _, l := range splitLines(stderr.String()) {
		lines = append(lines, cutRunes(red.Clean(l), maxLogLineRunes))
	}
	if text != "" {
		return nil, lines, text
	}
	if stdout.overflow {
		return nil, lines, fmt.Sprintf("standard output exceeds %d bytes", maxOutputBytes)
	}
	return stdout.Bytes(), lines, ""
}

// runProgram runs cmd in its own process group, ending the whole group when
// it ends or ctx ends, and returns "" or a fixed error text.
func runProgram(ctx context.Context, cmd *exec.Cmd) string {
	cmd.WaitDelay = time.Second
	group, err := newProcessGroup(cmd)
	if err != nil {
		return "the program could not start"
	}
	defer group.close()
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return stopText(ctx)
		}
		return "the program could not start"
	}
	if err := group.started(cmd); err != nil {
		group.close()
		_ = cmd.Wait()
		return "the program could not start"
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return stopText(ctx)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Sprintf("the program exited with status %d", exit.ExitCode())
		}
		return "the program failed"
	}
	return ""
}

// stopText says why ctx ended.
func stopText(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "time limit reached"
	}
	return "run canceled"
}

// environment is the environment of a plugin's programs: omo's own
// (childenv.Inherited) without any OMO_ variable, then OMO_SOCKET, OMO_PLUGIN_NAME, OMO_PLUGIN_EVENT,
// OMO_PLUGIN_CONFIG and OMO_PLUGIN_DATA. Nothing of an event's data is in
// it.
func (m *Manager) environment(p *plugin, event string) ([]string, error) {
	data, err := m.dataDir(p.name)
	if err != nil {
		return nil, err
	}
	var env []string
	for _, kv := range childenv.Inherited() {
		name, _, _ := strings.Cut(kv, "=")
		if len(name) >= 4 && strings.EqualFold(name[:4], "OMO_") {
			continue
		}
		env = append(env, kv)
	}
	if m.o.Socket != nil {
		if s := m.o.Socket(); s != "" {
			env = append(env, "OMO_SOCKET="+s)
		}
	}
	return append(env,
		"OMO_PLUGIN_NAME="+p.name,
		"OMO_PLUGIN_EVENT="+event,
		"OMO_PLUGIN_CONFIG="+p.configJSON,
		"OMO_PLUGIN_DATA="+data,
	), nil
}

// dataDir is the plugin's data folder, Root/.data/<name>, made (mode 0700)
// when missing. A link in its place is refused.
func (m *Manager) dataDir(name string) (string, error) {
	base := filepath.Join(m.o.Root, ".data")
	dir := filepath.Join(base, name)
	for _, d := range []string{m.o.Root, base, dir} {
		if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", errors.New("the plugin's data folder cannot be made")
		}
		info, err := os.Lstat(d)
		if err != nil || !info.IsDir() {
			return "", errors.New("the plugin's data folder is not a folder")
		}
	}
	return dir, nil
}

// Infos describes every plugin of the last Load, by name.
func (m *Manager) Infos() []api.PluginInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]api.PluginInfo, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.infoLocked())
	}
	return out
}

func (e *entry) infoLocked() api.PluginInfo {
	info := api.PluginInfo{
		Name:         e.settings.Name,
		Version:      e.manifest.Version,
		Description:  e.manifest.Description,
		State:        e.state,
		Message:      e.message,
		LastEvent:    e.lastEvent,
		Hooks:        []api.PluginHook{},
		Buttons:      append([]api.PluginButton{}, e.manifest.Buttons...),
		Frontend:     e.manifest.Frontend,
		ConfigFields: append([]api.PluginConfigField{}, e.manifest.ConfigFields...),
		ContentID:    e.manifest.contentID,
	}
	if e.lastRun != nil {
		at := *e.lastRun
		info.LastRunAt = &at
	}
	for _, h := range e.manifest.Hooks {
		ph := api.PluginHook{Event: h.Event, Name: h.Name, Description: h.Description, Scope: h.Scope, Interval: h.Interval, Hidden: h.Hidden}
		if h.Event == EventTimer && h.Interval == "" && e.p != nil {
			for _, lh := range e.p.hooks {
				if lh.Event == EventTimer && lh.IntervalConfig == h.IntervalConfig {
					ph.Interval = lh.interval.String()
				}
			}
		}
		info.Hooks = append(info.Hooks, ph)
	}
	return info
}

// LogLines is the newest limit lines (0: 200, at most LogLines) of a
// plugin's log, oldest first.
func (m *Manager) LogLines(plugin string, limit int) []api.PluginLogLine {
	if limit <= 0 {
		limit = 200
	}
	limit = min(limit, LogLines)
	out := []api.PluginLogLine{}
	if m.o.DB == nil {
		return out
	}
	rows, err := db.PluginLogTail(m.o.DB, plugin, limit)
	if err != nil {
		m.logf("plugins: read a log: %v", err)
		return out
	}
	for _, r := range rows {
		out = append(out, api.PluginLogLine{At: r.CreatedAt, Message: r.Message})
	}
	return out
}

// changed tells Options.Changed about e.
func (m *Manager) changed(e *entry) {
	if m.o.Changed == nil {
		return
	}
	m.mu.Lock()
	info := e.infoLocked()
	m.mu.Unlock()
	m.o.Changed(info)
}

// syncRuntimes writes the runtime records of entries; records of plugins
// no longer in the settings go, with their logs.
func (m *Manager) syncRuntimes(entries []*entry) {
	if m.o.DB == nil {
		return
	}
	rows := make([]db.PluginRuntime, 0, len(entries))
	m.mu.Lock()
	for _, e := range entries {
		rows = append(rows, db.PluginRuntime{
			Name: e.settings.Name, Version: e.manifest.Version, Description: e.manifest.Description,
			State: e.state, HookCount: len(e.manifest.Hooks),
		})
	}
	m.mu.Unlock()
	if err := db.SyncPluginRuntimes(m.o.DB, rows); err != nil {
		m.logf("plugins: write the runtime records: %v", err)
	}
}

func (m *Manager) saveState(name, state, event string, at time.Time) {
	if m.o.DB == nil {
		return
	}
	if err := db.SetPluginRuntimeState(m.o.DB, name, state, event, at); err != nil {
		m.logf("plugins: write the state of %s: %v", name, err)
	}
}

// logLine adds one cleaned line to a plugin's log.
func (m *Manager) logLine(name, line string) {
	if m.o.DB == nil || strings.TrimSpace(line) == "" {
		return
	}
	line = strings.ReplaceAll(cutRunes(line, maxLogLineRunes), "\n", " ")
	if err := db.AppendPluginRuntimeLog(m.o.DB, name, line, m.now(), LogLines); err != nil {
		m.logf("plugins: write the log of %s: %v", name, err)
	}
}

func (m *Manager) logf(format string, args ...any) {
	if m.o.Log != nil {
		m.o.Log.Printf(format, args...)
	}
}
