package supervisor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

const maxAgentInputBytes = 64 * 1024

var namedAgentKeys = map[string]string{
	"enter":     "\r",
	"return":    "\r",
	"tab":       "\t",
	"space":     " ",
	"escape":    "\x1b",
	"esc":       "\x1b",
	"backspace": "\x7f",
	"delete":    "\x1b[3~",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"home":      "\x1b[H",
	"end":       "\x1b[F",
	"pageup":    "\x1b[5~",
	"page-up":   "\x1b[5~",
	"pgup":      "\x1b[5~",
	"pagedown":  "\x1b[6~",
	"page-down": "\x1b[6~",
	"pgdown":    "\x1b[6~",
}

func agentInputBytes(text string, keys []string) (string, error) {
	var input strings.Builder
	input.WriteString(text)
	for _, raw := range keys {
		key := strings.ToLower(strings.TrimSpace(raw))
		if encoded, ok := namedAgentKeys[key]; ok {
			input.WriteString(encoded)
			continue
		}
		control := strings.TrimPrefix(key, "ctrl+")
		if control == key {
			control = strings.TrimPrefix(key, "ctrl-")
		}
		if len(control) == 1 && control[0] >= 'a' && control[0] <= 'z' && control != key {
			input.WriteByte(control[0] - 'a' + 1)
			continue
		}
		return "", fmt.Errorf("unknown key %q", raw)
	}
	if input.Len() == 0 {
		return "", fmt.Errorf("text or at least one --key is required")
	}
	if input.Len() > maxAgentInputBytes {
		return "", fmt.Errorf("agent input must not exceed %d bytes", maxAgentInputBytes)
	}
	return input.String(), nil
}

func (s *Supervisor) registerInputVerbs(srv *sockd.Server) {
	srv.Handle("agent.input", func(agentID string, args json.RawMessage) (any, error) {
		caller := agentID
		if agentID != "user" {
			a, err := db.GetAgent(s.DB, agentID)
			if err != nil {
				return nil, err
			}
			if a.Role != "ceo" && a.Role != "firefighter" {
				return nil, fmt.Errorf("only the user, CEO, or firefighter may send terminal input to agents")
			}
			caller = a.Name
		}
		var a proto.AgentInputArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		input, err := agentInputBytes(a.Text, a.Keys)
		if err != nil {
			return nil, err
		}
		target, err := db.GetAgent(s.DB, a.Name)
		if err != nil || target.State == "done" || target.State == "dead" {
			return nil, fmt.Errorf("no active agent named %q", a.Name)
		}
		sess, ok := s.Session(target.Name)
		if !ok {
			return nil, fmt.Errorf("no active agent named %q", a.Name)
		}
		if err := sess.SendText(input); err != nil {
			return nil, fmt.Errorf("send input to %s: %w", target.Name, err)
		}
		db.AppendEvent(s.DB, "agent_input_sent", caller, target.JobID,
			fmt.Sprintf("target=%s bytes=%d keys=%d", target.Name, len(input), len(a.Keys)))
		return nil, nil
	})
}
