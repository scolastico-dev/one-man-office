// Package plugins implements office-local event plugins backed by Lua or
// external commands.
package plugins

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	officedb "github.com/scolastico-dev/one-man-office/internal/db"
)

const Dir = ".omo/plugins"

const DefaultLogLines = 500

const (
	EventCron         = "cron"
	EventAgentStart   = "agent_start"
	EventAgentLogLine = "agent_log_line"
	EventJobCreate    = "job_create"
	EventManual       = "manual"
)

type Event struct {
	Name    string         `json:"event"`
	Data    map[string]any `json:"data"`
	Mutable bool           `json:"mutable,omitempty"`
}

type Manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Hooks       []Hook `json:"hooks"`
}

type Hook struct {
	Name           string   `json:"name,omitempty"`
	Description    string   `json:"description,omitempty"`
	ManualArgs     bool     `json:"manual_args,omitempty"`
	Event          string   `json:"event"`
	Interval       string   `json:"interval,omitempty"`
	IntervalConfig string   `json:"interval_config,omitempty"`
	Timeout        string   `json:"timeout,omitempty"`
	Lua            string   `json:"lua,omitempty"`
	Command        []string `json:"command,omitempty"`
}

type loadedHook struct {
	plugin     string
	dir        string
	hook       Hook
	config     map[string]any
	configJSON string
	interval   time.Duration
	timeout    time.Duration
}

// Settings is the office-owned configuration for one installed plugin.
// Config is intentionally schema-free: each plugin owns its object shape.
type Settings struct {
	Enabled bool
	Config  map[string]any
}

type Options struct {
	LogLines int
}

type Manager struct {
	OfficeDir     string
	DB            *sql.DB
	hooks         []loadedHook
	manualMu      sync.Mutex
	manualActive  map[string]bool
	manualClosing bool
	manualCtx     context.Context
	manualCancel  context.CancelFunc
	manualWG      sync.WaitGroup
	async         chan Event
	// Snapshot enriches cron events with safe supervisor-owned state.
	Snapshot  func() map[string]any
	runtimeMu sync.Mutex
	running   map[string]int
	logLines  int
}

func Load(officeDir string, db *sql.DB) (*Manager, error) {
	return LoadConfiguredWithOptions(officeDir, db, nil, Options{LogLines: DefaultLogLines})
}

// LoadConfigured loads local plugins while honoring enabled flags for plugins
// managed through omo.yaml. Unmanaged local directories remain enabled.
func LoadConfigured(officeDir string, db *sql.DB, configured map[string]Settings) (*Manager, error) {
	return LoadConfiguredWithOptions(officeDir, db, configured, Options{LogLines: DefaultLogLines})
}

func LoadConfiguredWithOptions(officeDir string, db *sql.DB, configured map[string]Settings, options Options) (*Manager, error) {
	return LoadSourcesWithOptions(officeDir, db, options, Source{Root: filepath.Join(officeDir, Dir), Configured: configured})
}

// LoadSources loads the effective plugins from ordered installation scopes.
func LoadSources(officeDir string, db *sql.DB, sources ...Source) (*Manager, error) {
	return LoadSourcesWithOptions(officeDir, db, Options{LogLines: DefaultLogLines}, sources...)
}

// LoadSourcesWithOptions loads the effective plugins from ordered installation
// scopes with runtime options shared by every selected plugin.
func LoadSourcesWithOptions(officeDir string, db *sql.DB, options Options, sources ...Source) (*Manager, error) {
	if options.LogLines < 1 {
		return nil, fmt.Errorf("plugin log line limit must be positive")
	}
	entries, err := selectDirectories(sources)
	if err != nil {
		return nil, err
	}
	m := &Manager{OfficeDir: officeDir, DB: db, async: make(chan Event, 256), running: map[string]int{}, logLines: options.LogLines}
	runtimes := make(map[string]officedb.PluginRuntime)
	for _, entry := range entries {
		if !entry.managed {
			continue
		}
		state := "missing"
		if !entry.settings.Enabled {
			state = "disabled"
		}
		runtimes[entry.name] = officedb.PluginRuntime{Name: entry.name, State: state}
	}
	seenNames := map[string]bool{}
	for _, entry := range entries {
		if entry.dir == "" {
			continue
		}
		settings, managed := entry.settings, entry.managed
		if managed && !settings.Enabled {
			continue
		}
		pluginConfig := settings.Config
		if pluginConfig == nil {
			pluginConfig = map[string]any{}
		}
		configJSON, err := json.Marshal(pluginConfig)
		if err != nil {
			return nil, fmt.Errorf("plugin %s config: %w", entry.name, err)
		}
		dir := entry.dir
		raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var manifest Manifest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&manifest); err != nil {
			return nil, fmt.Errorf("plugin %s: %w", entry.name, err)
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			return nil, fmt.Errorf("plugin %s: trailing manifest data", entry.name)
		}
		if manifest.Name == "" {
			manifest.Name = entry.name
		}
		if seenNames[manifest.Name] {
			return nil, fmt.Errorf("plugin name %q is used by more than one directory", manifest.Name)
		}
		seenNames[manifest.Name] = true
		if manifest.Name != entry.name {
			delete(runtimes, entry.name)
		}
		runtimes[manifest.Name] = officedb.PluginRuntime{
			Name: manifest.Name, Version: manifest.Version, Description: manifest.Description,
			State: "ready", HookCount: len(manifest.Hooks),
		}
		manualNames := map[string]bool{}
		for i, hook := range manifest.Hooks {
			loaded, err := validateHook(manifest.Name, dir, hook, pluginConfig, string(configJSON))
			if err != nil {
				return nil, fmt.Errorf("plugin %s hook %d: %w", manifest.Name, i, err)
			}
			m.hooks = append(m.hooks, loaded)
			if hook.Event == EventManual {
				if manualNames[hook.Name] {
					return nil, fmt.Errorf("plugin %s: duplicate manual action name %q", manifest.Name, hook.Name)
				}
				manualNames[hook.Name] = true
			}
		}
	}
	runtimeRows := make([]officedb.PluginRuntime, 0, len(runtimes))
	for _, runtime := range runtimes {
		runtimeRows = append(runtimeRows, runtime)
	}
	sort.Slice(runtimeRows, func(i, j int) bool { return runtimeRows[i].Name < runtimeRows[j].Name })
	if err := officedb.SyncPluginRuntimes(db, runtimeRows); err != nil {
		return nil, fmt.Errorf("sync plugin runtime state: %w", err)
	}
	m.manualCtx, m.manualCancel = context.WithCancel(context.Background())
	m.manualActive = make(map[string]bool)
	if err := officedb.TrimPluginLogs(db, options.LogLines); err != nil {
		return nil, fmt.Errorf("trim plugin log history: %w", err)
	}
	return m, nil
}

var manualActionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateHook(plugin, dir string, hook Hook, pluginConfig map[string]any, configJSON string) (loadedHook, error) {
	allowed := map[string]bool{EventCron: true, "chron": true, EventAgentStart: true, EventAgentLogLine: true, EventJobCreate: true, EventManual: true}
	if !allowed[hook.Event] {
		return loadedHook{}, fmt.Errorf("unsupported event %q", hook.Event)
	}
	if hook.Event == "chron" {
		hook.Event = EventCron
	}
	if hook.Event == EventManual {
		if !manualActionName.MatchString(hook.Name) {
			return loadedHook{}, fmt.Errorf("manual action name must start with a letter or digit and contain only letters, digits, '.', '_' or '-'")
		}
		if strings.TrimSpace(hook.Description) == "" {
			return loadedHook{}, fmt.Errorf("manual action description is required")
		}
	} else if hook.ManualArgs {
		return loadedHook{}, fmt.Errorf("manual_args is only valid for manual hooks")
	}
	if (hook.Lua == "") == (len(hook.Command) == 0) {
		return loadedHook{}, fmt.Errorf("exactly one of lua or command is required")
	}
	if hook.Lua != "" {
		clean := filepath.Clean(hook.Lua)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
			return loadedHook{}, fmt.Errorf("lua path must stay inside the plugin")
		}
		if _, err := os.Stat(filepath.Join(dir, hook.Lua)); err != nil {
			return loadedHook{}, err
		}
	}
	var interval time.Duration
	if hook.Event == EventCron {
		configuredInterval := any(hook.Interval)
		if hook.IntervalConfig != "" {
			if value, ok := pluginConfig[hook.IntervalConfig]; ok {
				configuredInterval = value
			}
		}
		var err error
		interval, err = configDuration(configuredInterval)
		if err != nil {
			return loadedHook{}, fmt.Errorf("cron interval must be a positive duration: %w", err)
		}
		if interval <= 0 {
			return loadedHook{}, fmt.Errorf("cron interval must be a positive duration")
		}
	} else if hook.IntervalConfig != "" {
		return loadedHook{}, fmt.Errorf("interval_config is only valid for cron hooks")
	}
	if len(hook.Command) > 0 && strings.TrimSpace(hook.Command[0]) == "" {
		return loadedHook{}, fmt.Errorf("command executable must not be empty")
	}
	timeout := 30 * time.Second
	if hook.Timeout != "" {
		var err error
		timeout, err = time.ParseDuration(hook.Timeout)
		if err != nil || timeout <= 0 {
			return loadedHook{}, fmt.Errorf("timeout must be a positive duration")
		}
	}
	return loadedHook{plugin: plugin, dir: dir, hook: hook, config: pluginConfig, configJSON: configJSON, interval: interval, timeout: timeout}, nil
}

func configDuration(value any) (time.Duration, error) {
	switch value := value.(type) {
	case string:
		return time.ParseDuration(value)
	case int:
		return time.Duration(value * int(time.Second)), nil
	case int64:
		return time.Duration(value) * time.Second, nil
	case float64:
		return time.Duration(value * float64(time.Second)), nil
	default:
		return 0, fmt.Errorf("got %T", value)
	}
}

// Run consumes asynchronous lifecycle/log events and starts cron hooks.
func (m *Manager) Run(ctx context.Context) {
	defer m.Close()
	for _, hook := range m.hooks {
		if hook.hook.Event == EventCron {
			go m.runCron(ctx, hook)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-m.async:
			_, _ = m.Emit(ctx, event)
		}
	}
}

func (m *Manager) runCron(ctx context.Context, hook loadedHook) {
	ticker := time.NewTicker(hook.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-ticker.C:
			data := map[string]any{"at": at.UTC().Format(time.RFC3339Nano), "at_unix": at.Unix()}
			if m.Snapshot != nil {
				for key, value := range m.Snapshot() {
					data[key] = value
				}
			}
			if _, err := m.runHook(ctx, hook, Event{Name: EventCron, Data: data}); err != nil {
				m.logError(hook.plugin, err)
			}
		}
	}
}

func (m *Manager) EmitAsync(event Event) {
	event = timestampEvent(event)
	select {
	case m.async <- event:
	default:
		if m.DB != nil {
			_, _ = m.DB.Exec(`INSERT INTO events(kind, detail) VALUES('plugin_event_dropped', ?)`, event.Name)
		}
	}
}

// Emit runs matching hooks in stable order. Mutable event data flows from one
// hook to the next; hook failures are logged and do not take the office down.
func (m *Manager) Emit(ctx context.Context, event Event) (Event, error) {
	if event.Name == EventManual {
		return event, fmt.Errorf("manual events require a targeted plugin trigger")
	}
	event = timestampEvent(event)
	var errs []error
	for _, hook := range m.hooks {
		if hook.hook.Event != event.Name {
			continue
		}
		updated, err := m.runHook(ctx, hook, event)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", hook.plugin, err))
			m.logError(hook.plugin, err)
			continue
		}
		if event.Mutable {
			event = updated
		}
	}
	return event, errors.Join(errs...)
}

func timestampEvent(event Event) Event {
	if event.Data == nil {
		event.Data = map[string]any{}
	}
	if _, exists := event.Data["at_unix"]; !exists {
		now := time.Now()
		event.Data["at"] = now.UTC().Format(time.RFC3339Nano)
		event.Data["at_unix"] = now.Unix()
	}
	return event
}

func (m *Manager) runHook(ctx context.Context, hook loadedHook, event Event) (Event, error) {
	ctx, cancel := context.WithTimeout(ctx, hook.timeout)
	defer cancel()
	m.setHookRunning(hook.plugin, event.Name)
	var updated Event
	var err error
	if hook.hook.Lua != "" {
		updated, err = m.runLua(ctx, hook, event)
	} else {
		updated, err = m.runCommand(ctx, hook, event)
	}
	m.setHookFinished(hook.plugin, event.Name, err)
	return updated, err
}

func (m *Manager) logError(plugin string, err error) {
	if m.DB != nil {
		_, _ = m.DB.Exec(`INSERT INTO events(kind, detail) VALUES('plugin_error', ?)`, plugin+": "+err.Error())
	}
	m.log(plugin, "error: "+err.Error())
}

func (m *Manager) runCommand(ctx context.Context, hook loadedHook, event Event) (Event, error) {
	cmd := exec.CommandContext(ctx, hook.hook.Command[0], hook.hook.Command[1:]...)
	// Descendants may inherit output pipes after the command is canceled.
	// Bound that drain so shutdown can finish and persist the hook outcome.
	cmd.WaitDelay = time.Second
	cmd.Dir = hook.dir
	cmd.Env = m.pluginEnvironment(hook, event.Name)
	input, _ := json.Marshal(event)
	cmd.Stdin = bytes.NewReader(input)
	stderr := newTailBuffer(maxLogBytes)
	stdout, stdoutWriter := commandStdoutWriter(event.Mutable)
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if stdout != nil && stdout.Overflowed() {
		if output := strings.TrimSpace(stderr.String()); output != "" {
			m.log(hook.plugin, output)
		}
		return event, fmt.Errorf("mutable command stdout exceeds %d byte limit", maxCommandOutputBytes)
	}
	if runErr != nil {
		return event, fmt.Errorf("command: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	if output := strings.TrimSpace(stderr.String()); output != "" {
		m.log(hook.plugin, output)
	}
	if stdout != nil && len(bytes.TrimSpace(stdout.Bytes())) != 0 {
		var data map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
			return event, fmt.Errorf("decode mutable command output: %w", err)
		}
		event.Data = data
	}
	return event, nil
}

func (m *Manager) setHookRunning(plugin, event string) {
	if m.DB == nil {
		return
	}
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	m.running[plugin]++
	_ = officedb.SetPluginRuntimeState(m.DB, plugin, "running", event, time.Now())
}

func (m *Manager) setHookFinished(plugin, event string, hookErr error) {
	if m.DB == nil {
		return
	}
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	if m.running[plugin] > 0 {
		m.running[plugin]--
	}
	state := "ready"
	if hookErr != nil {
		state = "error"
	} else if m.running[plugin] > 0 {
		state = "running"
	}
	_ = officedb.SetPluginRuntimeState(m.DB, plugin, state, event, time.Now())
}

func (m *Manager) log(plugin, message string) {
	if m.DB == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	message = boundedRuneTail(message, maxLogRunes)
	_ = officedb.AppendPluginRuntimeLog(m.DB, plugin, message, time.Now(), m.logLines)
}

func (m *Manager) pluginEnvironment(hook loadedHook, eventName string) []string {
	return append(os.Environ(),
		"OMO_AGENT_ID=",
		"OMO_SOCKET=",
		"OMO_PLUGIN_NAME="+hook.plugin,
		"OMO_PLUGIN_EVENT="+eventName,
		"OMO_PLUGIN_CONFIG="+hook.configJSON,
		"OMO_OFFICE_DIR="+m.OfficeDir,
	)
}
