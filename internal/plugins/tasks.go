package plugins

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/luabox"
)

const (
	MaxRunningTasks     = 32 // per plugin
	MaxTaskHistory      = 64 // terminal records per plugin
	TaskOutputLines     = 100
	TaskOutputLineRunes = 1024
	taskRawLineBytes    = 64 << 10
	TaskStopped         = "stopped"
	TaskTicketDone      = "ticket_done"
	TaskPluginDisabled  = "plugin_disabled"
	TaskPluginRemoved   = "plugin_removed"
	TaskPluginReplaced  = "plugin_replaced"
	TaskShutdown        = "shutdown"
	TaskExited          = "exited"
)

var (
	ErrTaskRunning = errors.New("a task with this name is already running")
	ErrUnknownTask = errors.New("no such task belongs to this plugin")
	ErrTaskTicket  = errors.New("task ticket must exist and be open")
	ErrTaskLimit   = errors.New("the plugin already has 32 running or finishing tasks")
	envKey         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// TaskStart is the exact argument of omo.task_start. Command is an executable,
// never a shell expression. Cwd defaults to the plugin directory; a relative
// cwd is resolved there. Ticket zero is global. OMO_* overrides are forbidden.
type TaskStart struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Ticket  int               `json:"ticket,omitempty"`
}

type task struct {
	sequence     uint64
	exitSequence uint64
	record       api.PluginTask // guarded by tasksMu
	plugin       string
	cancel       context.CancelFunc
	done         chan struct{} // closed after reaping, output flush and exit callback
	output       *taskOutput
}

func validateTask(req TaskStart) error {
	if !ValidName(req.Name) {
		return errors.New("task name must be 1 to 64 letters, digits, '.', '_' or '-', starting alphanumeric")
	}
	if strings.TrimSpace(req.Command) == "" || len(req.Command) > maxArgBytes || containsNUL(req.Command) {
		return errors.New("task command must name a program of at most 4096 bytes without NUL")
	}
	if len(req.Args) > maxArgs || len(req.Cwd) > maxArgBytes || containsNUL(req.Cwd) || req.Ticket < 0 {
		return errors.New("invalid task arguments, cwd or ticket")
	}
	for _, arg := range req.Args {
		if len(arg) > maxArgBytes || containsNUL(arg) {
			return errors.New("task argument exceeds 4096 bytes or contains NUL")
		}
	}
	if len(req.Env) > 64 {
		return errors.New("task environment exceeds 64 overrides")
	}
	seen := map[string]bool{}
	for k, v := range req.Env {
		upper := strings.ToUpper(k)
		if !envKey.MatchString(k) || len(k) > 128 || strings.HasPrefix(upper, "OMO_") || seen[upper] || len(v) > 32<<10 || containsNUL(v) {
			return errors.New("task environment has an invalid, duplicate or reserved key, or invalid value")
		}
		seen[upper] = true
	}
	return nil
}

// credentialEnvironmentKey excludes ordinary paths/locales/usernames from
// inherited-secret collection while covering common credential conventions.
func credentialEnvironmentKey(key string) bool {
	key = strings.ToUpper(key)
	for _, part := range []string{"TOKEN", "PASSWORD", "PASSWD", "SECRET", "API_KEY", "APIKEY", "PRIVATE_KEY", "CREDENTIAL", "ACCESS_KEY", "AUTHORIZATION"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return key == "PASS" || strings.HasSuffix(key, "_PASS") || strings.HasSuffix(key, "_PWD") || strings.HasSuffix(key, "_PAT") || strings.HasSuffix(key, "_AUTH") || strings.HasSuffix(key, "_DSN") || key == "DATABASE_URL" || key == "REDIS_URL"
}

// StartTask starts an enabled plugin's task independently of ctx's lifetime.
// ctx bounds creation only. Lua uses its bound plugin pointer instead.
func (m *Manager) StartTask(ctx context.Context, name string, req TaskStart) (api.PluginTask, error) {
	return m.startTask(ctx, name, nil, req, nil)
}

func (m *Manager) startTask(ctx context.Context, name string, bound *plugin, req TaskStart, red *luabox.Redactor) (result api.PluginTask, resultErr error) {
	defer func() {
		if resultErr == nil && m.o.TasksChanged != nil {
			m.o.TasksChanged(name)
		}
	}()
	if err := validateTask(req); err != nil {
		return api.PluginTask{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || ctx.Err() != nil {
		return api.PluginTask{}, ErrClosed
	}
	var p *plugin
	for _, e := range m.entries {
		if e.settings.Name == name {
			p = e.p
			break
		}
	}
	if p == nil || (bound != nil && p != bound) || p.ctx.Err() != nil {
		return api.PluginTask{}, ErrUnknownPlugin
	}
	if red == nil {
		red = p.redactor()
	} else {
		red = red.Clone()
	}
	// Include full serialized configuration as well as each configured string.
	red.Add(p.configJSON)
	for _, v := range req.Env {
		red.Add(v)
	}
	m.tasksMu.Lock()
	defer m.tasksMu.Unlock()
	running := 0
	for _, t := range m.tasks {
		if t.plugin != name {
			continue
		}
		select {
		case <-t.done:
		default:
			running++
		}
		if t.record.State == "running" && t.record.Name == req.Name {
			return api.PluginTask{}, ErrTaskRunning
		}
	}
	if running >= MaxRunningTasks {
		return api.PluginTask{}, ErrTaskLimit
	}
	live := func() bool {
		return req.Ticket == 0 || (m.o.TaskTicketLive != nil && m.o.TaskTicketLive(ctx, req.Ticket))
	}
	if !live() {
		return api.PluginTask{}, ErrTaskTicket
	}
	env, err := m.environment(p, "task")
	if err != nil {
		return api.PluginTask{}, errors.New("the task environment could not be prepared")
	}
	for k, v := range req.Env {
		for i := 0; i < len(env); {
			key, _, _ := strings.Cut(env[i], "=")
			if strings.EqualFold(key, k) {
				env = append(env[:i], env[i+1:]...)
			} else {
				i++
			}
		}
		env = append(env, k+"="+v)
	}
	// Inherited credentials and helper-collected HTTP secrets are never output.
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		if credentialEnvironmentKey(key) {
			red.Add(value)
		}
	}
	if red.Clean(req.Name) != req.Name {
		return api.PluginTask{}, errors.New("task name contains a configuration or environment value")
	}
	taskCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(taskCtx, req.Command, req.Args...)
	cmd.Dir = req.Cwd
	if cmd.Dir == "" {
		cmd.Dir = p.dir
	} else if !filepath.IsAbs(cmd.Dir) {
		cmd.Dir = filepath.Join(p.dir, cmd.Dir)
	}
	cmd.Env = env
	cmd.WaitDelay = time.Second
	out := &taskOutput{red: red, changed: func() {
		if m.o.TaskOutputChanged != nil {
			m.o.TaskOutputChanged(name)
		}
	}}
	cmd.Stdout = out
	cmd.Stderr = out
	group, err := newProcessGroup(cmd)
	if err != nil {
		cancel()
		return api.PluginTask{}, errors.New("the task program could not start")
	}
	if err = cmd.Start(); err != nil {
		cancel()
		group.close()
		return api.PluginTask{}, errors.New("the task program could not start")
	}
	if err = group.started(cmd); err != nil {
		cancel()
		group.close()
		_ = cmd.Wait()
		return api.PluginTask{}, errors.New("the task program could not start")
	}
	m.taskSequence++
	t := &task{sequence: m.taskSequence, plugin: name, cancel: cancel, done: make(chan struct{}), output: out, record: api.PluginTask{ID: rand.Text(), Name: req.Name, Ticket: req.Ticket, StartedAt: m.now().UTC().Format(time.RFC3339Nano), State: "running", Output: []string{}}}
	if m.tasks == nil {
		m.tasks = map[string]*task{}
	}
	m.tasks[t.record.ID] = t
	// A completion handled before registration must still prevent a live child.
	creationErr := error(nil)
	switch {
	case !live():
		creationErr = ErrTaskTicket
	case p.ctx.Err() != nil:
		creationErr = ErrUnknownPlugin
	case ctx.Err() != nil:
		creationErr = ctx.Err()
	}
	if creationErr != nil {
		cancel()
		group.close()
		_ = cmd.Wait()
		delete(m.tasks, t.record.ID)
		close(t.done)
		return api.PluginTask{}, creationErr
	}
	m.tasksWG.Add(1)
	go m.waitTask(t, cmd, group)
	return t.record, nil
}

func (m *Manager) waitTask(t *task, cmd *exec.Cmd, group *processGroup) {
	defer m.tasksWG.Done()
	err := cmd.Wait()
	group.close() // Includes surviving descendants after a natural parent exit.
	t.cancel()
	t.output.flush()
	m.tasksMu.Lock()
	m.exitSequence++
	t.exitSequence = m.exitSequence
	t.record.State = "exited"
	t.record.EndedAt = m.now().UTC().Format(time.RFC3339Nano)
	if t.record.Reason == "" {
		t.record.Reason = TaskExited
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) && t.record.Reason == TaskExited {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.record.Error = fmt.Sprintf("the task exited with status %d", exit.ExitCode())
		} else {
			t.record.Error = "the task program failed"
		}
	}
	record := m.taskRecord(t)
	m.tasksMu.Unlock()
	if m.o.TaskExited != nil {
		m.o.TaskExited(t.plugin, record)
	}
	if m.o.TasksChanged != nil {
		m.o.TasksChanged(t.plugin)
	}
	m.tasksMu.Lock()
	close(t.done)
	m.trimTasksLocked(t.plugin)
	m.tasksMu.Unlock()
}

func (m *Manager) taskRecord(t *task) api.PluginTask {
	r := t.record
	r.Output = t.output.lines()
	return r
}

func (m *Manager) trimTasksLocked(plugin string) {
	var ended []*task
	for _, t := range m.tasks {
		if t.plugin == plugin && t.record.State != "running" {
			select {
			case <-t.done:
				ended = append(ended, t)
			default:
			}
		}
	}
	sort.Slice(ended, func(i, j int) bool { return ended[i].exitSequence < ended[j].exitSequence })
	for len(ended) > MaxTaskHistory {
		delete(m.tasks, ended[0].record.ID)
		ended = ended[1:]
	}
}

// Tasks lists the retained tasks of this plugin, oldest first. It never exposes
// commands, environment, pid or another plugin's records. Empty is [] not null.
func (m *Manager) Tasks(plugin string) ([]api.PluginTask, error) {
	m.mu.Lock()
	known := false
	for _, e := range m.entries {
		if e.settings.Name == plugin {
			known = true
			break
		}
	}
	m.mu.Unlock()
	if !known {
		return nil, ErrUnknownPlugin
	}
	m.tasksMu.Lock()
	defer m.tasksMu.Unlock()
	var own []*task
	for _, t := range m.tasks {
		if t.plugin == plugin {
			own = append(own, t)
		}
	}
	sort.Slice(own, func(i, j int) bool { return own[i].sequence < own[j].sequence })
	result := make([]api.PluginTask, 0, len(own))
	for _, t := range own {
		result = append(result, m.taskRecord(t))
	}
	return result, nil
}

// StopTask stops the latest run under name and waits for reaping/notification.
// A retained exited task succeeds; an unknown/wrong-owner name is not_found.
func (m *Manager) StopTask(plugin, name string) error {
	m.tasksMu.Lock()
	var latest *task
	for _, t := range m.tasks {
		if t.plugin == plugin && t.record.Name == name && (latest == nil || t.sequence > latest.sequence) {
			latest = t
		}
	}
	m.tasksMu.Unlock()
	if latest == nil {
		return ErrUnknownTask
	}
	return m.StopTaskID(plugin, latest.record.ID, TaskStopped)
}

// StopTaskID acts on a single identity, never a reused name. Callbacks receive
// the first termination reason. No callback runs while a manager lock is held.
func (m *Manager) StopTaskID(plugin, id, reason string) error {
	m.tasksMu.Lock()
	t := m.tasks[id]
	if t == nil || t.plugin != plugin {
		m.tasksMu.Unlock()
		return ErrUnknownTask
	}
	if t.record.State == "running" {
		if t.record.Reason == "" {
			t.record.Reason = reason
		}
		t.cancel()
	}
	m.tasksMu.Unlock()
	<-t.done
	return nil
}

func (m *Manager) stopTasks(match func(*task) bool, reason string) {
	m.tasksMu.Lock()
	var tasks []*task
	for _, t := range m.tasks {
		if !match(t) {
			continue
		}
		if t.record.State == "running" {
			if t.record.Reason == "" {
				t.record.Reason = reason
			}
			t.cancel()
		}
		tasks = append(tasks, t)
	}
	m.tasksMu.Unlock()
	for _, t := range tasks {
		<-t.done
	}
}

// StopTicketTasks is called directly by the reliable event-log follower for
// every committed Done transition, before delivery to droppable hook queues.
func (m *Manager) StopTicketTasks(ticket int) {
	m.stopTasks(func(t *task) bool { return t.record.Ticket == ticket }, TaskTicketDone)
}

// taskOutput holds complete raw lines only until redaction. Oversize lines are
// discarded whole, so dropping a prefix cannot expose a suffix of a secret.
// No unbounded buffer is allocated, even for a single enormous Write.
type taskOutput struct {
	changed  func()
	mu       sync.Mutex
	red      *luabox.Redactor
	pending  []byte
	dropping bool
	tail     []string
}

func (o *taskOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	before := append([]string(nil), o.tail...)
	defer func() {
		changed := !slices.Equal(before, o.tail)
		o.mu.Unlock()
		if changed && o.changed != nil {
			o.changed()
		}
	}()
	for _, b := range p {
		if b == '\n' {
			o.finishLine()
			continue
		}
		if o.dropping {
			continue
		}
		if len(o.pending) >= taskRawLineBytes {
			o.pending = nil
			o.dropping = true
			continue
		}
		o.pending = append(o.pending, b)
	}
	return len(p), nil
}
func (o *taskOutput) finishLine() {
	line := "[output line exceeded 64 KiB]"
	if !o.dropping {
		line = cutRunes(o.red.Clean(strings.TrimSuffix(string(o.pending), "\r")), TaskOutputLineRunes)
	}
	o.tail = append(o.tail, line)
	if len(o.tail) > TaskOutputLines {
		copy(o.tail, o.tail[len(o.tail)-TaskOutputLines:])
		o.tail = o.tail[:TaskOutputLines]
	}
	o.pending = nil
	o.dropping = false
}
func (o *taskOutput) flush() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.pending) > 0 || o.dropping {
		o.finishLine()
	}
}
func (o *taskOutput) lines() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	lines := append([]string{}, o.tail...)
	// Expose output without a final newline only after the stream is closed;
	// a later Write might finish a secret, so publishing prefixes is unsafe.
	return lines
}
