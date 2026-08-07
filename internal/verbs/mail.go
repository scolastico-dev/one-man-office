// Package verbs holds socket verb handlers that need only the database —
// no supervisor state. Supervisor-coupled verbs live in internal/supervisor.
package verbs

import (
	"encoding/json"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func RegisterMail(srv *sockd.Server, mail *bus.Store) {
	srv.Handle("send", func(agentID string, args json.RawMessage) (any, error) {
		var a proto.SendArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		if a.Priority == "" {
			a.Priority = string(bus.PrioNormal)
		}
		if a.Subject == "" {
			return nil, fmt.Errorf("subject required")
		}
		ids, err := mail.Send(agentID, a.To, a.Subject, a.Body, bus.Priority(a.Priority))
		if err != nil {
			return nil, err
		}
		return map[string]any{"delivered": len(ids)}, nil
	})
	srv.Handle("inbox", func(agentID string, _ json.RawMessage) (any, error) {
		msgs, err := mail.Inbox(agentID)
		if err != nil {
			return nil, err
		}
		if msgs == nil {
			msgs = []bus.Message{}
		}
		return msgs, nil
	})
	srv.Handle("read", func(agentID string, args json.RawMessage) (any, error) {
		var a proto.ReadArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		return mail.Read(agentID, a.ID)
	})
}
