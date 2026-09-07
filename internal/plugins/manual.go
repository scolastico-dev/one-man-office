package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	officedb "github.com/scolastico-dev/one-man-office/internal/db"
)

// ManualCapability describes the loaded manifest, excluding disabled plugins.
func (m *Manager) ManualCapability(name string) (subscribed, acceptsArgs bool) {
	if m == nil {
		return false, false
	}
	acceptsArgs, subscribed = m.manual[name]
	return subscribed, acceptsArgs
}

// TriggerManual synchronously runs only the selected plugin's manual hooks in
// manifest order. The caller must be authorized by the supervisor. Requests are
// audited before execution; interruptions are not replayed on office restart.
func (m *Manager) TriggerManual(ctx context.Context, name, caller string, args []string) error {
	subscribed, acceptsArgs := m.ManualCapability(name)
	if !subscribed {
		return fmt.Errorf("plugin %q has no enabled manual hook", name)
	}
	if len(args) > 0 && !acceptsArgs {
		return fmt.Errorf("plugin %q does not accept manual arguments", name)
	}
	if m.DB == nil {
		return fmt.Errorf("manual trigger requires durable storage")
	}
	detail, _ := json.Marshal(map[string]any{"plugin": name, "argument_count": len(args)})
	result, err := m.DB.Exec(`INSERT INTO events(kind, agent, detail) VALUES('plugin_manual_requested', ?, ?)`, caller, string(detail))
	if err != nil {
		return fmt.Errorf("record manual trigger: %w", err)
	}
	requestID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("identify manual trigger: %w", err)
	}
	event := timestampEvent(Event{Name: EventManual, Data: map[string]any{
		"plugin": name, "caller": caller, "args": append([]string{}, args...), "request_id": requestID,
	}})
	var errs []error
	for _, hook := range m.hooks {
		if hook.plugin != name || hook.hook.Event != EventManual {
			continue
		}
		if _, err := m.runHook(ctx, hook, event); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			m.logError(name, err)
		}
	}
	kind := "plugin_manual_completed"
	if len(errs) > 0 {
		kind = "plugin_manual_failed"
	}
	detail, _ = json.Marshal(map[string]any{"plugin": name, "request_id": requestID})
	if err := officedb.AppendEvent(m.DB, kind, caller, 0, string(detail)); err != nil {
		errs = append(errs, fmt.Errorf("record manual trigger outcome: %w", err))
	}
	return errors.Join(errs...)
}
