package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	officedb "github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
)

// ManualTriggerResult is the durable identity and JSON-compatible value
// returned by one successful targeted manual hook.
type ManualTriggerResult struct {
	RequestID int64
	Value     any
}

// ManualPermissionError reports a manual hook denied to an authenticated
// caller by the action's allowed roles.
type ManualPermissionError struct {
	Caller string
	Role   string
	Plugin string
	Action string
}

func (e *ManualPermissionError) Error() string {
	return fmt.Sprintf("agent %q with role %q may not trigger plugin %q action %q", e.Caller, e.Role, e.Plugin, e.Action)
}

func validateManualResult(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("manual hook returned a non-JSON value: %w", err)
	}
	if len(raw) > maxManualResultBytes {
		return nil, fmt.Errorf("manual hook result exceeds %d KiB", maxManualResultBytes/1024)
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("manual hook returned invalid JSON: %w", err)
	}
	return normalized, nil
}

func validateLegacyManualResult(value any) (string, error) {
	result, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("manual result must be a string")
	}
	if !utf8.ValidString(result) {
		return "", fmt.Errorf("manual result must be valid UTF-8")
	}
	if len(result) > 4*1024 {
		return "", fmt.Errorf("manual result exceeds %d bytes", 4*1024)
	}
	return result, nil
}

// ManualActions lists loaded actions in plugin/manifest order. An empty plugin
// includes all enabled plugins. Returned values are copies of runtime metadata.
func (m *Manager) ManualActions(plugin string) []proto.PluginAction {
	actions := []proto.PluginAction{}
	if m == nil {
		return actions
	}
	for _, hook := range m.hooks {
		if hook.hook.Event == EventManual && (plugin == "" || hook.plugin == plugin) {
			roles := hook.hook.Roles
			if roles == nil {
				roles = []string{"user"}
			}
			actions = append(actions, proto.PluginAction{Plugin: hook.plugin, Name: hook.hook.Name, Description: hook.hook.Description, ManualArgs: hook.hook.ManualArgs, Roles: append([]string{}, roles...)})
		}
	}
	return actions
}

// TriggerManual synchronously runs only the selected named manual hook.
// The caller must be authorized by the supervisor. Requests are
// audited before execution; interruptions are not replayed on office restart.
func (m *Manager) TriggerManual(name, action, caller string, args []string) error {
	_, err := m.TriggerManualContextWithRoleResult(context.Background(), name, action, caller, "user", args)
	return err
}

// TriggerManualResult is the result-bearing form of TriggerManual.
func (m *Manager) TriggerManualResult(name, action, caller string, args []string) (ManualTriggerResult, error) {
	return m.TriggerManualContextWithRoleResult(context.Background(), name, action, caller, "user", args)
}

// TriggerManualContext is TriggerManual with caller cancellation in addition
// to manager shutdown and the hook's configured timeout.
func (m *Manager) TriggerManualContext(ctx context.Context, name, action, caller string, args []string) error {
	_, err := m.TriggerManualContextWithRoleResult(ctx, name, action, caller, "user", args)
	return err
}

// TriggerManualContextResult is the result-bearing form of TriggerManualContext.
func (m *Manager) TriggerManualContextResult(ctx context.Context, name, action, caller string, args []string) (ManualTriggerResult, error) {
	return m.TriggerManualContextWithRoleResult(ctx, name, action, caller, "user", args)
}

// TriggerManualContextWithRole is TriggerManualContext with the caller's
// authenticated role included in the event delivered to the hook.
func (m *Manager) TriggerManualContextWithRole(ctx context.Context, name, action, caller, callerRole string, args []string) error {
	_, err := m.TriggerManualContextWithRoleResult(ctx, name, action, caller, callerRole, args)
	return err
}

// TriggerManualContextWithRoleAndData includes trusted supervisor state in a
// manual event. User callers and agents without jobs pass no extra data.
func (m *Manager) TriggerManualContextWithRoleAndData(ctx context.Context, name, action, caller, callerRole string, args []string, contextData map[string]any) error {
	_, err := m.TriggerManualContextWithRoleAndDataResult(ctx, name, action, caller, callerRole, args, contextData)
	return err
}

// TriggerManualContextWithRoleAndDataResult is the result-bearing form that
// also carries trusted supervisor state into the manual event.
func (m *Manager) TriggerManualContextWithRoleAndDataResult(ctx context.Context, name, action, caller, callerRole string, args []string, contextData map[string]any) (ManualTriggerResult, error) {
	execution, err := m.prepareManualTrigger(name, action, caller, callerRole, args, contextData)
	if err != nil {
		return ManualTriggerResult{}, err
	}
	return m.runManualTrigger(ctx, execution)
}

// TriggerManualContextWithRoleAsync authorizes and records a manual trigger,
// then runs the selected hook in the manager's background lifecycle. The
// returned request ID is durable before this method returns; hook completion
// or failure is recorded asynchronously.
func (m *Manager) TriggerManualContextWithRoleAsync(ctx context.Context, name, action, caller, callerRole string, args []string) (int64, error) {
	return m.TriggerManualContextWithRoleAndDataAsync(ctx, name, action, caller, callerRole, args, nil)
}

// TriggerManualContextWithRoleAndDataAsync is the asynchronous form that
// carries trusted supervisor state into the manual event.
func (m *Manager) TriggerManualContextWithRoleAndDataAsync(ctx context.Context, name, action, caller, callerRole string, args []string, contextData map[string]any) (int64, error) {
	execution, err := m.prepareManualTrigger(name, action, caller, callerRole, args, contextData)
	if err != nil {
		return 0, err
	}
	go func() {
		_, _ = m.runManualTrigger(ctx, execution)
	}()
	return execution.requestID, nil
}

// TriggerManualContextWithRoleResult is the result-bearing manual-trigger
// path. Authorization is shared here so every caller—HTTP, socket, and TUI—
// uses the same role boundary before admission, execution, and durable audit.
func (m *Manager) TriggerManualContextWithRoleResult(ctx context.Context, name, action, caller, callerRole string, args []string) (ManualTriggerResult, error) {
	return m.TriggerManualContextWithRoleAndDataResult(ctx, name, action, caller, callerRole, args, nil)
}

type manualExecution struct {
	name      string
	action    string
	caller    string
	args      []string
	requestID int64
	hook      loadedHook
	event     Event
	release   func()
}

func (m *Manager) prepareManualTrigger(name, action, caller, callerRole string, args []string, contextData map[string]any) (manualExecution, error) {
	var execution manualExecution
	var selected *loadedHook
	if m != nil {
		for i := range m.hooks {
			hook := &m.hooks[i]
			if hook.plugin == name && hook.hook.Event == EventManual && hook.hook.Name == action {
				selected = hook
				break
			}
		}
	}
	if selected == nil {
		return execution, fmt.Errorf("plugin %q has no enabled manual action %q", name, action)
	}
	if len(args) > 0 && !selected.hook.ManualArgs {
		return execution, fmt.Errorf("plugin %q action %q does not accept manual arguments", name, action)
	}
	roles := selected.hook.Roles
	if roles == nil {
		roles = []string{"user"}
	}
	allowed := false
	for _, role := range roles {
		if role == callerRole {
			allowed = true
			break
		}
	}
	if !allowed {
		return execution, &ManualPermissionError{Caller: caller, Role: callerRole, Plugin: name, Action: action}
	}
	m.manualMu.Lock()
	if m.manualClosing {
		m.manualMu.Unlock()
		return execution, fmt.Errorf("plugins are shutting down")
	}
	if m.manualActive[name] {
		m.manualMu.Unlock()
		return execution, fmt.Errorf("plugin %q manual hooks are already running", name)
	}
	m.manualActive[name] = true
	m.manualWG.Add(1)
	m.manualMu.Unlock()
	release := func() { m.releaseManual(name) }
	if m.DB == nil {
		release()
		return execution, fmt.Errorf("manual trigger requires durable storage")
	}
	detail, _ := json.Marshal(map[string]any{"plugin": name, "action": action, "argument_count": len(args)})
	dbResult, err := m.DB.Exec(`INSERT INTO events(kind, agent, detail) VALUES('plugin_manual_requested', ?, ?)`, caller, string(detail))
	if err != nil {
		release()
		return execution, fmt.Errorf("record manual trigger: %w", err)
	}
	requestID, err := dbResult.LastInsertId()
	if err != nil {
		release()
		return execution, fmt.Errorf("identify manual trigger: %w", err)
	}
	detail, _ = json.Marshal(map[string]any{"plugin": name, "action": action, "request_id": requestID, "argument_count": len(args)})
	if _, err := m.DB.Exec(`UPDATE events SET detail=? WHERE id=? AND kind='plugin_manual_requested'`, string(detail), requestID); err != nil {
		release()
		return execution, fmt.Errorf("identify manual trigger: %w", err)
	}
	eventData := map[string]any{
		"plugin": name, "action": action, "caller": caller, "caller_role": callerRole, "args": append([]string{}, args...), "request_id": requestID, "home_path": m.OfficeDir,
	}
	for key, value := range contextData {
		eventData[key] = value
	}
	event := timestampEvent(Event{Name: EventManual, Data: eventData, Mutable: true})
	return manualExecution{name: name, action: action, caller: caller, args: append([]string{}, args...), requestID: requestID, hook: *selected, event: event, release: release}, nil
}

func (m *Manager) runManualTrigger(ctx context.Context, execution manualExecution) (ManualTriggerResult, error) {
	defer execution.release()
	triggerResult := ManualTriggerResult{RequestID: execution.requestID}
	var errs []error
	runCtx, cancel := context.WithCancel(ctx)
	stopShutdownCancel := context.AfterFunc(m.manualCtx, cancel)
	defer func() {
		stopShutdownCancel()
		cancel()
	}()
	updated, value, hookErr := m.runHookResult(runCtx, execution.hook, execution.event, nil)
	if hookErr != nil {
		errs = append(errs, fmt.Errorf("%s/%s: %w", execution.name, execution.action, hookErr))
		m.logError(execution.name, hookErr)
	} else {
		// PR #145 hooks returned their string through mutable event data, while
		// newer hooks return JSON values directly. Keep both forms in one result.
		if value == nil {
			legacyValue := updated.Data["result"]
			if legacyValue != nil {
				value, hookErr = validateLegacyManualResult(legacyValue)
				if hookErr != nil {
					errs = append(errs, fmt.Errorf("%s/%s: %w", execution.name, execution.action, hookErr))
					m.logError(execution.name, hookErr)
				}
			}
		}
		if hookErr == nil {
			triggerResult.Value = value
		}
	}
	kind := "plugin_manual_completed"
	if len(errs) > 0 {
		kind = "plugin_manual_failed"
	}
	detail, _ := json.Marshal(map[string]any{"plugin": execution.name, "action": execution.action, "request_id": execution.requestID, "argument_count": len(execution.args)})
	if err := officedb.AppendEvent(m.DB, kind, execution.caller, 0, string(detail)); err != nil {
		errs = append(errs, fmt.Errorf("record manual trigger outcome: %w", err))
	}
	return triggerResult, errors.Join(errs...)
}

func (m *Manager) releaseManual(name string) {
	m.manualMu.Lock()
	delete(m.manualActive, name)
	m.manualMu.Unlock()
	m.manualWG.Done()
}
