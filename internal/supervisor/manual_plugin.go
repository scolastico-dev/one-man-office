package supervisor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

// PluginPermissionError reports a manual plugin action denied to an
// authenticated caller by the action's allowed roles.
type PluginPermissionError struct {
	Caller string
	Role   string
	Plugin string
	Action string
}

func (e *PluginPermissionError) Error() string {
	return fmt.Sprintf("agent %q with role %q may not trigger plugin %q action %q", e.Caller, e.Role, e.Plugin, e.Action)
}

func (s *Supervisor) registerPluginVerbs(srv *sockd.Server) {
	srv.Handle("plugin.trigger", func(caller string, raw json.RawMessage) (any, error) {
		var args proto.PluginTriggerArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		return s.TriggerPluginResult(caller, args.Name, args.Action, args.Args)
	})
	srv.Handle("plugin.actions", func(caller string, raw json.RawMessage) (any, error) {
		if caller != "user" {
			return nil, fmt.Errorf("only the user may list manual plugin actions")
		}
		var args proto.AgentNameArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		return s.Plugins.ManualActions(args.Name), nil
	})
}

// TriggerPlugin is the shared authorization boundary for socket and TUI runs.
func (s *Supervisor) TriggerPlugin(caller, name, action string, args []string) error {
	_, err := s.TriggerPluginResult(caller, name, action, args)
	return err
}

// TriggerPluginResult is the shared authorization boundary for socket and
// TUI/browser-forwarded runs. Callers that do not expose hook values may use
// TriggerPlugin, which intentionally discards the result.
func (s *Supervisor) TriggerPluginResult(caller, name, action string, args []string) (proto.PluginTriggerResponse, error) {
	var response proto.PluginTriggerResponse
	if s.Plugins == nil {
		return response, fmt.Errorf("no plugins are loaded")
	}
	role := "user"
	if caller != "user" {
		agent, err := db.GetAgent(s.DB, caller)
		if err != nil {
			return response, fmt.Errorf("unknown authenticated plugin caller %q: %w", caller, err)
		}
		role = agent.Role
	}
	for _, candidate := range s.Plugins.ManualActions(name) {
		if candidate.Name != action {
			continue
		}
		allowed := false
		for _, allowedRole := range candidate.Roles {
			if allowedRole == role {
				allowed = true
				break
			}
		}
		if !allowed {
			return response, &PluginPermissionError{Caller: caller, Role: role, Plugin: name, Action: action}
		}
		break
	}
	result, err := s.Plugins.TriggerManualContextWithRoleResult(context.Background(), name, action, caller, role, args)
	response.RequestID = result.RequestID
	response.Result = result.Value
	return response, err
}
