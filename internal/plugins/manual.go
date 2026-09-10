package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	officedb "github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
)

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
	return m.TriggerManualContextWithRole(context.Background(), name, action, caller, "user", args)
}

// TriggerManualContext is TriggerManual with caller cancellation in addition
// to manager shutdown and the hook's configured timeout.
func (m *Manager) TriggerManualContext(ctx context.Context, name, action, caller string, args []string) error {
	return m.TriggerManualContextWithRole(ctx, name, action, caller, "user", args)
}

// TriggerManualContextWithRole is TriggerManualContext with the caller's
// authenticated role included in the event delivered to the hook.
func (m *Manager) TriggerManualContextWithRole(ctx context.Context, name, action, caller, callerRole string, args []string) error {
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
		return fmt.Errorf("plugin %q has no enabled manual action %q", name, action)
	}
	if len(args) > 0 && !selected.hook.ManualArgs {
		return fmt.Errorf("plugin %q action %q does not accept manual arguments", name, action)
	}
	m.manualMu.Lock()
	if m.manualClosing {
		m.manualMu.Unlock()
		return fmt.Errorf("plugins are shutting down")
	}
	if m.manualActive[name] {
		m.manualMu.Unlock()
		return fmt.Errorf("plugin %q manual hooks are already running", name)
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
		return fmt.Errorf("manual trigger requires durable storage")
	}
	detail, _ := json.Marshal(map[string]any{"plugin": name, "action": action, "argument_count": len(args)})
	result, err := m.DB.Exec(`INSERT INTO events(kind, agent, detail) VALUES('plugin_manual_requested', ?, ?)`, caller, string(detail))
	if err != nil {
		return fmt.Errorf("record manual trigger: %w", err)
	}
	requestID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("identify manual trigger: %w", err)
	}
	event := timestampEvent(Event{Name: EventManual, Data: map[string]any{
		"plugin": name, "action": action, "caller": caller, "caller_role": callerRole, "args": append([]string{}, args...), "request_id": requestID,
	}})
	var errs []error
	runCtx, cancel := context.WithCancel(ctx)
	stopShutdownCancel := context.AfterFunc(m.manualCtx, cancel)
	defer func() {
		stopShutdownCancel()
		cancel()
	}()
	if _, err := m.runHook(runCtx, *selected, event); err != nil {
		errs = append(errs, fmt.Errorf("%s/%s: %w", name, action, err))
		m.logError(name, err)
	}
	kind := "plugin_manual_completed"
	if len(errs) > 0 {
		kind = "plugin_manual_failed"
	}
	detail, _ = json.Marshal(map[string]any{"plugin": name, "action": action, "request_id": requestID, "argument_count": len(args)})
	if err := officedb.AppendEvent(m.DB, kind, caller, 0, string(detail)); err != nil {
		errs = append(errs, fmt.Errorf("record manual trigger outcome: %w", err))
	}
	return errors.Join(errs...)
}
