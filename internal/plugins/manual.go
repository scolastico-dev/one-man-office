package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

// TriggerManualContextWithRoleResult is the result-bearing manual-trigger
// path. Authorization is shared here so every caller—HTTP, socket, and TUI—
// uses the same role boundary before admission, execution, and durable audit.
func (m *Manager) TriggerManualContextWithRoleResult(ctx context.Context, name, action, caller, callerRole string, args []string) (ManualTriggerResult, error) {
	var triggerResult ManualTriggerResult
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
		return triggerResult, fmt.Errorf("plugin %q has no enabled manual action %q", name, action)
	}
	if len(args) > 0 && !selected.hook.ManualArgs {
		return triggerResult, fmt.Errorf("plugin %q action %q does not accept manual arguments", name, action)
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
		return triggerResult, &ManualPermissionError{Caller: caller, Role: callerRole, Plugin: name, Action: action}
	}
	m.manualMu.Lock()
	if m.manualClosing {
		m.manualMu.Unlock()
		return triggerResult, fmt.Errorf("plugins are shutting down")
	}
	if m.manualActive[name] {
		m.manualMu.Unlock()
		return triggerResult, fmt.Errorf("plugin %q manual hooks are already running", name)
	}
	m.manualActive[name] = true
	m.manualWG.Add(1)
	m.manualMu.Unlock()
	defer func() {
		m.manualMu.Lock()
		delete(m.manualActive, name)
		m.manualMu.Unlock()
		m.manualWG.Done()
	}()
	if m.DB == nil {
		return triggerResult, fmt.Errorf("manual trigger requires durable storage")
	}
	detail, _ := json.Marshal(map[string]any{"plugin": name, "action": action, "argument_count": len(args)})
	result, err := m.DB.Exec(`INSERT INTO events(kind, agent, detail) VALUES('plugin_manual_requested', ?, ?)`, caller, string(detail))
	if err != nil {
		return triggerResult, fmt.Errorf("record manual trigger: %w", err)
	}
	requestID, err := result.LastInsertId()
	if err != nil {
		return triggerResult, fmt.Errorf("identify manual trigger: %w", err)
	}
	triggerResult.RequestID = requestID
	detail, _ = json.Marshal(map[string]any{"plugin": name, "action": action, "request_id": requestID, "argument_count": len(args)})
	if _, err := m.DB.Exec(`UPDATE events SET detail=? WHERE id=? AND kind='plugin_manual_requested'`, string(detail), requestID); err != nil {
		return triggerResult, fmt.Errorf("identify manual trigger: %w", err)
	}
	event := timestampEvent(Event{Name: EventManual, Data: map[string]any{
		"plugin": name, "action": action, "caller": caller, "caller_role": callerRole, "args": append([]string{}, args...), "request_id": requestID, "home_path": m.OfficeDir,
	}, Mutable: true})
	var errs []error
	runCtx, cancel := context.WithCancel(ctx)
	stopShutdownCancel := context.AfterFunc(m.manualCtx, cancel)
	defer func() {
		stopShutdownCancel()
		cancel()
	}()
	_, value, hookErr := m.runHookResult(runCtx, *selected, event, nil)
	if hookErr != nil {
		errs = append(errs, fmt.Errorf("%s/%s: %w", name, action, hookErr))
		m.logError(name, hookErr)
	} else {
		triggerResult.Value = value
	}
	kind := "plugin_manual_completed"
	if len(errs) > 0 {
		kind = "plugin_manual_failed"
	}
	detail, _ = json.Marshal(map[string]any{"plugin": name, "action": action, "request_id": requestID, "argument_count": len(args)})
	if err := officedb.AppendEvent(m.DB, kind, caller, 0, string(detail)); err != nil {
		errs = append(errs, fmt.Errorf("record manual trigger outcome: %w", err))
	}
	return triggerResult, errors.Join(errs...)
}
