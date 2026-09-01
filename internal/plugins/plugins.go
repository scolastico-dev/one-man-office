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
	"sort"
	"strings"
	"time"
)

const Dir = ".omo/plugins"

const (
	EventCron         = "cron"
	EventAgentStart   = "agent_start"
	EventAgentLogLine = "agent_log_line"
	EventJobCreate    = "job_create"
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
	Event    string   `json:"event"`
	Interval string   `json:"interval,omitempty"`
	Timeout  string   `json:"timeout,omitempty"`
	Lua      string   `json:"lua,omitempty"`
	Command  []string `json:"command,omitempty"`
}

type loadedHook struct {
	plugin   string
	dir      string
	hook     Hook
	interval time.Duration
	timeout  time.Duration
}

type Manager struct {
	OfficeDir string
	DB        *sql.DB
	hooks     []loadedHook
	async     chan Event
	// Snapshot enriches cron events with safe supervisor-owned state.
	Snapshot func() map[string]any
}

func Load(officeDir string, db *sql.DB) (*Manager, error) {
	return LoadConfigured(officeDir, db, nil)
}

// LoadConfigured loads local plugins while honoring enabled flags for plugins
// managed through omo.yaml. Unmanaged local directories remain enabled.
func LoadConfigured(officeDir string, db *sql.DB, configured map[string]bool) (*Manager, error) {
	root := filepath.Join(officeDir, Dir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	m := &Manager{OfficeDir: officeDir, DB: db, async: make(chan Event, 256)}
	seenNames := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if enabled, managed := configured[entry.Name()]; managed && !enabled {
			continue
		}
		dir := filepath.Join(root, entry.Name())
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
			return nil, fmt.Errorf("plugin %s: %w", entry.Name(), err)
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			return nil, fmt.Errorf("plugin %s: trailing manifest data", entry.Name())
		}
		if manifest.Name == "" {
			manifest.Name = entry.Name()
		}
		if seenNames[manifest.Name] {
			return nil, fmt.Errorf("plugin name %q is used by more than one directory", manifest.Name)
		}
		seenNames[manifest.Name] = true
		for i, hook := range manifest.Hooks {
			loaded, err := validateHook(manifest.Name, dir, hook)
			if err != nil {
				return nil, fmt.Errorf("plugin %s hook %d: %w", manifest.Name, i, err)
			}
			m.hooks = append(m.hooks, loaded)
		}
	}
	return m, nil
}

func validateHook(plugin, dir string, hook Hook) (loadedHook, error) {
	allowed := map[string]bool{EventCron: true, "chron": true, EventAgentStart: true, EventAgentLogLine: true, EventJobCreate: true}
	if !allowed[hook.Event] {
		return loadedHook{}, fmt.Errorf("unsupported event %q", hook.Event)
	}
	if hook.Event == "chron" {
		hook.Event = EventCron
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
		var err error
		interval, err = time.ParseDuration(hook.Interval)
		if err != nil || interval <= 0 {
			return loadedHook{}, fmt.Errorf("cron interval must be a positive duration")
		}
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
	return loadedHook{plugin: plugin, dir: dir, hook: hook, interval: interval, timeout: timeout}, nil
}

// Run consumes asynchronous lifecycle/log events and starts cron hooks.
func (m *Manager) Run(ctx context.Context) {
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
	if hook.hook.Lua != "" {
		return m.runLua(ctx, hook, event)
	}
	return m.runCommand(ctx, hook, event)
}

func (m *Manager) logError(plugin string, err error) {
	if m.DB != nil {
		_, _ = m.DB.Exec(`INSERT INTO events(kind, detail) VALUES('plugin_error', ?)`, plugin+": "+err.Error())
	}
}

func (m *Manager) runCommand(ctx context.Context, hook loadedHook, event Event) (Event, error) {
	cmd := exec.CommandContext(ctx, hook.hook.Command[0], hook.hook.Command[1:]...)
	cmd.Dir = hook.dir
	cmd.Env = append(os.Environ(), "OMO_PLUGIN_NAME="+hook.plugin, "OMO_PLUGIN_EVENT="+event.Name)
	input, _ := json.Marshal(event)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return event, fmt.Errorf("command: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if event.Mutable && strings.TrimSpace(stdout.String()) != "" {
		var data map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
			return event, fmt.Errorf("decode mutable command output: %w", err)
		}
		event.Data = data
	}
	return event, nil
}
