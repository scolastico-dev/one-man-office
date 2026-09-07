package supervisor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func (s *Supervisor) registerPluginVerbs(srv *sockd.Server) {
	srv.Handle("plugin.trigger", func(caller string, raw json.RawMessage) (any, error) {
		var args proto.PluginTriggerArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		return nil, s.TriggerPlugin(caller, args.Name, args.Args)
	})
}

// TriggerPlugin is the shared authorization boundary for socket and TUI runs.
func (s *Supervisor) TriggerPlugin(caller, name string, args []string) error {
	if caller != "user" {
		return fmt.Errorf("only the user may trigger manual plugins")
	}
	if s.Plugins == nil {
		return fmt.Errorf("no plugins are loaded")
	}
	return s.Plugins.TriggerManual(context.Background(), name, caller, args)
}
