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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	officedb "github.com/scolastico-dev/one-man-office/internal/db"
)

const Dir = ".omo/plugins"

const DefaultLogLines = 500

const (
	EventCron           = "cron"
	EventAgentStart     = "agent_start"
	EventAgentLogLine   = "agent_log_line"
	EventJobCreate      = "job_create"
	EventPromptRender   = "prompt_render"
	EventManual         = "manual"
	EventCompanyStartup = "company_startup"
	EventCompanyLoad    = "company_load"
)

type Event struct {
	Name    string         `json:"event"`
	Data    map[string]any `json:"data"`
	Mutable bool           `json:"mutable,omitempty"`
}

type Manifest struct {
	Name          string        `json:"name"`
	Version       string        `json:"version,omitempty"`
	Description   string        `json:"description,omitempty"`
	Requires      []Dependency  `json:"requires,omitempty"`
	Hooks         []Hook        `json:"hooks"`
	DefaultConfig DefaultConfig `json:"default_config,omitempty"`
}

// Dependency identifies a plugin that must be loaded alongside the declaring
// plugin and carries enough information for an interactive startup to install it.
type Dependency struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Subpath string `json:"subpath,omitempty"`
}

// MissingDependency groups every loaded plugin that requires one absent plugin.
type MissingDependency struct {
	Dependency
	RequiredBy []string
}

// MissingDependenciesError lets the CLI offer installation before retrying
// office startup while non-interactive callers still receive an actionable error.
type MissingDependenciesError struct {
	Dependencies []MissingDependency
}

func (e *MissingDependenciesError) Error() string {
	parts := make([]string, 0, len(e.Dependencies))
	for _, dependency := range e.Dependencies {
		parts = append(parts, fmt.Sprintf("%s (required by %s)", dependency.Name, strings.Join(dependency.RequiredBy, ", ")))
	}
	return "missing required plugins: " + strings.Join(parts, "; ")
}

// DefaultConfig is an optional JSON object of plugin-owned configuration defaults.
// Null is rejected at the object root; nested nulls are valid default values.
type DefaultConfig map[string]any

func (c *DefaultConfig) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return fmt.Errorf("default_config must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode((*map[string]any)(c))
}

// ReadManifest uses the same strict schema during installation and runtime load.
func ReadManifest(dir string) (Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, fmt.Errorf("trailing manifest data")
	}
	if manifest.Name != "" {
		if err := validatePluginName(manifest.Name); err != nil {
			return Manifest{}, fmt.Errorf("name: %w", err)
		}
	}
	for i, dependency := range manifest.Requires {
		normalized, err := normalizeDependency(dependency)
		if err != nil {
			return Manifest{}, fmt.Errorf("dependency %d: %w", i, err)
		}
		manifest.Requires[i] = normalized
	}
	return manifest, nil
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
	Javascript     string   `json:"javascript,omitempty"`
	Files          []string `json:"files,omitempty"`
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
	Snapshot    func() map[string]any
	runtimeMu   sync.Mutex
	running     map[string]int
	logLines    int
	lifecycleMu sync.RWMutex
	closed      bool
	snapshotDir string
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
	return LoadSourcesContextWithOptions(context.Background(), officeDir, db, options, sources...)
}

// LoadSourcesContext bounds waiting for an update owned by another process.
func LoadSourcesContext(ctx context.Context, officeDir string, db *sql.DB, sources ...Source) (*Manager, error) {
	return LoadSourcesContextWithOptions(ctx, officeDir, db, Options{LogLines: DefaultLogLines}, sources...)
}

// LoadSourcesContextWithOptions bounds waiting for an update owned by another
// process and applies runtime options to every selected plugin.
func LoadSourcesContextWithOptions(ctx context.Context, officeDir string, db *sql.DB, options Options, sources ...Source) (*Manager, error) {
	if options.LogLines < 1 {
		return nil, fmt.Errorf("plugin log line limit must be positive")
	}
	entries, snapshot, err := prepareDirectories(ctx, sources)
	if err != nil {
		return nil, err
	}
	loaded := false
	defer func() {
		if !loaded && snapshot != "" {
			_ = os.RemoveAll(snapshot)
		}
	}()
	m := &Manager{OfficeDir: officeDir, DB: db, async: make(chan Event, 256), running: map[string]int{}, logLines: options.LogLines, snapshotDir: snapshot}
	runtimes := make(map[string]officedb.PluginRuntime)
	presentNames := make(map[string]bool)
	requirements := make(map[string]*MissingDependency)
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
		manifest, err := ReadManifest(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("plugin %s: %w", entry.name, err)
		}
		if manifest.Name == "" {
			manifest.Name = entry.name
		}
		if err := validatePluginName(manifest.Name); err != nil {
			return nil, fmt.Errorf("plugin %s: %w", entry.name, err)
		}
		if seenNames[manifest.Name] {
			return nil, fmt.Errorf("plugin name %q is used by more than one directory", manifest.Name)
		}
		seenNames[manifest.Name] = true
		presentNames[entry.name] = true
		presentNames[manifest.Name] = true
		for _, dependency := range manifest.Requires {
			declared := requirements[dependency.Name]
			if declared == nil {
				copy := MissingDependency{Dependency: dependency}
				declared = &copy
				requirements[dependency.Name] = declared
			} else if declared.Source != dependency.Source || declared.Subpath != dependency.Subpath {
				return nil, fmt.Errorf("plugin dependency %q has conflicting installation sources", dependency.Name)
			}
			if len(declared.RequiredBy) == 0 || declared.RequiredBy[len(declared.RequiredBy)-1] != manifest.Name {
				declared.RequiredBy = append(declared.RequiredBy, manifest.Name)
			}
		}
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
	missingNames := make([]string, 0, len(requirements))
	for name := range requirements {
		if !presentNames[name] {
			missingNames = append(missingNames, name)
		}
	}
	if len(missingNames) > 0 {
		sort.Strings(missingNames)
		missing := make([]MissingDependency, 0, len(missingNames))
		for _, name := range missingNames {
			dependency := *requirements[name]
			sort.Strings(dependency.RequiredBy)
			missing = append(missing, dependency)
		}
		return nil, &MissingDependenciesError{Dependencies: missing}
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
	loaded = true
	return m, nil
}

var manualActionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func normalizeDependency(dependency Dependency) (Dependency, error) {
	if err := validatePluginName(dependency.Name); err != nil {
		return Dependency{}, err
	}
	source := strings.TrimSpace(dependency.Source)
	if source == "" {
		return Dependency{}, fmt.Errorf("source is required")
	}
	if strings.ContainsAny(source, "\x00\r\n\x1b") {
		return Dependency{}, fmt.Errorf("source contains control characters")
	}
	if strings.HasPrefix(source, "builtin:") {
		builtin := strings.TrimPrefix(source, "builtin:")
		if builtin != dependency.Name || !manualActionName.MatchString(builtin) {
			return Dependency{}, fmt.Errorf("bundled source must match dependency name")
		}
	} else if strings.Contains(source, "://") {
		parsed, err := url.Parse(source)
		if err != nil || parsed.Scheme == "" {
			return Dependency{}, fmt.Errorf("invalid repository URL %q", source)
		}
		switch parsed.Scheme {
		case "https", "http", "ssh", "git", "file":
		default:
			return Dependency{}, fmt.Errorf("unsupported repository URL scheme %q", parsed.Scheme)
		}
		if parsed.Scheme != "file" && parsed.Host == "" {
			return Dependency{}, fmt.Errorf("repository URL needs a host")
		}
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
		if !strings.HasSuffix(parsed.Path, ".git") {
			parsed.Path += ".git"
		}
		source = parsed.String()
	} else if strings.Contains(source, "@") && strings.Contains(source, ":") {
		source = strings.TrimSuffix(source, "/")
		if !strings.HasSuffix(source, ".git") {
			source += ".git"
		}
	} else {
		return Dependency{}, fmt.Errorf("source must be an http(s), ssh, git, file, bundled, or scp-style repository URL")
	}
	if dependency.Subpath != "" {
		clean := filepath.Clean(dependency.Subpath)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
			return Dependency{}, fmt.Errorf("subpath must stay inside the repository")
		}
		if clean == "." {
			dependency.Subpath = ""
		} else {
			dependency.Subpath = filepath.ToSlash(clean)
		}
	}
	dependency.Source = source
	return dependency, nil
}

func validatePluginName(name string) error {
	if !manualActionName.MatchString(name) || name == "." || name == ".." || name == ".repos" || filepath.Base(name) != name {
		return fmt.Errorf("name %q must be one plugin path segment using letters, digits, dots, dashes, or underscores", name)
	}
	return nil
}

func validateHook(plugin, dir string, hook Hook, pluginConfig map[string]any, configJSON string) (loadedHook, error) {
	allowed := map[string]bool{EventCron: true, "chron": true, EventAgentStart: true, EventAgentLogLine: true, EventJobCreate: true, EventPromptRender: true, EventManual: true, EventCompanyStartup: true, EventCompanyLoad: true}
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
	if hook.Event == EventCompanyLoad {
		if hook.Lua != "" || len(hook.Command) != 0 {
			return loadedHook{}, fmt.Errorf("company_load is declarative and cannot use lua or command")
		}
		if hook.Javascript == "" {
			return loadedHook{}, fmt.Errorf("company_load requires javascript")
		}
	} else {
		if hook.Javascript != "" || len(hook.Files) != 0 {
			return loadedHook{}, fmt.Errorf("javascript and files are only valid for company_load")
		}
		if (hook.Lua == "") == (len(hook.Command) == 0) {
			return loadedHook{}, fmt.Errorf("exactly one of lua or command is required")
		}
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
	if hook.Event == EventCompanyLoad {
		paths := append(append([]string{}, hook.Files...), hook.Javascript)
		seen := map[string]bool{}
		for _, path := range paths {
			clean := filepath.Clean(path)
			if path == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
				return loadedHook{}, fmt.Errorf("web file path must stay inside the plugin")
			}
			clean = filepath.ToSlash(clean)
			if seen[clean] {
				continue
			}
			seen[clean] = true
			info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(clean)))
			if err != nil {
				return loadedHook{}, err
			}
			if !info.Mode().IsRegular() {
				return loadedHook{}, fmt.Errorf("web file must be a regular file: %s", path)
			}
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
	var cron sync.WaitGroup
	defer cron.Wait()
	for _, hook := range m.hooks {
		if hook.hook.Event == EventCron {
			cron.Go(func() { m.runCron(ctx, hook) })
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
	if event.Name == EventCompanyLoad {
		return event, fmt.Errorf("company_load is a browser event")
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

// RenderPrompt runs the mutable prompt_render hooks with only the prompt
// boundary data exposed to plugins. Hook failures retain the last valid text.
func (m *Manager) RenderPrompt(ctx context.Context, role, agent string, jobID int64, text string) (string, error) {
	event, err := m.Emit(ctx, Event{
		Name: EventPromptRender, Mutable: true,
		Data: map[string]any{"role": role, "agent": agent, "job_id": jobID, "text": text},
	})
	if value, ok := event.Data["text"].(string); ok {
		return value, err
	}
	return text, err
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
	m.lifecycleMu.RLock()
	defer m.lifecycleMu.RUnlock()
	if m.closed {
		return event, fmt.Errorf("plugin manager is closed")
	}
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
	if err == nil && event.Name == EventPromptRender {
		updated, err = validatePromptRender(event, updated)
	}
	m.setHookFinished(hook.plugin, event.Name, err)
	return updated, err
}

const maxPromptRenderAppendBytes = 2 * 1024

func validatePromptRender(input, output Event) (Event, error) {
	inputText, ok := input.Data["text"].(string)
	if !ok {
		return input, fmt.Errorf("prompt_render input text must be a string")
	}
	outputText, ok := output.Data["text"].(string)
	if !ok {
		return input, fmt.Errorf("prompt_render hook must return a string text")
	}
	if !utf8.ValidString(outputText) {
		return input, fmt.Errorf("prompt_render hook returned invalid UTF-8 text")
	}
	if growth := len(outputText) - len(inputText); growth > maxPromptRenderAppendBytes {
		return input, fmt.Errorf("prompt_render hook appended %d bytes; maximum is %d", growth, maxPromptRenderAppendBytes)
	}
	for _, key := range []string{"role", "agent", "job_id"} {
		output.Data[key] = input.Data[key]
	}
	return output, nil
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
	company := ""
	if eventName == EventCompanyStartup {
		company = "1"
	}
	return append(os.Environ(),
		"OMO_AGENT_ID=",
		"OMO_SOCKET=",
		"OMO_PLUGIN_NAME="+hook.plugin,
		"OMO_PLUGIN_EVENT="+eventName,
		"OMO_PLUGIN_CONFIG="+hook.configJSON,
		"OMO_OFFICE_DIR="+m.OfficeDir,
		"OMO_COMPANY="+company,
	)
}
