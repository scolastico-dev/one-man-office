package supervisor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/session"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

const maxAgentInputBytes = 64 * 1024

type queuedAgentInput struct {
	text   string
	keys   string
	caller string
	jobID  int64
	detail string
	done   chan error
}

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
	encodedKeys, err := agentKeyBytes(keys)
	if err != nil {
		return "", err
	}
	input.WriteString(encodedKeys)
	if input.Len() == 0 {
		return "", fmt.Errorf("text or at least one --key is required")
	}
	if input.Len() > maxAgentInputBytes {
		return "", fmt.Errorf("agent input must not exceed %d bytes", maxAgentInputBytes)
	}
	return input.String(), nil
}

func agentKeyBytes(keys []string) (string, error) {
	var input strings.Builder
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
	return input.String(), nil
}

func (s *Supervisor) registerInputVerbs(srv *sockd.Server) {
	srv.Handle("agent.input", func(agentID string, args json.RawMessage) (any, error) {
		caller := agentID
		if agentID != "user" && agentID != bus.SystemSender {
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
		keys, err := agentKeyBytes(a.Keys)
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
		detail := fmt.Sprintf("target=%s bytes=%d keys=%d", target.Name, len(input), len(a.Keys))
		if err := db.AppendEvent(s.DB, "agent_input_requested", caller, target.JobID, detail); err != nil {
			return nil, fmt.Errorf("record input request: %w", err)
		}
		request := &queuedAgentInput{
			text: a.Text, keys: keys, caller: caller, jobID: target.JobID,
			detail: detail, done: make(chan error, 1),
		}
		if err := s.queueAgentInput(target.Name, sess, request); err != nil {
			return nil, err
		}
		if err := <-request.done; err != nil {
			return nil, err
		}
		return nil, nil
	})
}

func (s *Supervisor) queueAgentInput(agent string, sess *session.Session, input *queuedAgentInput) error {
	s.mu.Lock()
	current := s.sessions[agent]
	if current == nil || current != sess {
		s.mu.Unlock()
		return fmt.Errorf("no active agent named %q", agent)
	}
	s.pendingAgentInput[agent] = append(s.pendingAgentInput[agent], input)
	s.mu.Unlock()
	go s.flushAgentInput(agent)
	return nil
}

func (s *Supervisor) flushAgentInput(agent string) {
	for {
		cfg := s.Config()
		s.mu.Lock()
		queue := s.pendingAgentInput[agent]
		if len(queue) == 0 {
			delete(s.agentInputFlushing, agent)
			s.mu.Unlock()
			return
		}
		if s.agentInputFlushing[agent] {
			s.mu.Unlock()
			return
		}
		if delay := s.inputDebounceDelayLocked(agent, time.Duration(cfg.Notifications.InputDebounce)); delay > 0 {
			if s.agentInputTimers[agent] == nil {
				var timer *time.Timer
				timer = time.AfterFunc(delay, func() {
					s.mu.Lock()
					claimed := s.claimAgentInputTimerLocked(agent, timer)
					s.mu.Unlock()
					if claimed {
						s.flushAgentInput(agent)
					}
				})
				s.agentInputTimers[agent] = timer
			}
			s.mu.Unlock()
			return
		}
		if timer := s.agentInputTimers[agent]; timer != nil {
			timer.Stop()
			delete(s.agentInputTimers, agent)
		}
		input := queue[0]
		sess := s.sessions[agent]
		s.agentInputFlushing[agent] = true
		s.mu.Unlock()

		var err error
		sent := false
		if sess == nil {
			err = fmt.Errorf("no active agent named %q", agent)
		} else {
			active := true
			sent, err = s.sendAgentInput(sess, input.text, input.keys, func() bool {
				cfg := s.Config()
				s.mu.Lock()
				defer s.mu.Unlock()
				active = s.sessions[agent] == sess
				return active && s.inputDebounceDelayLocked(agent, time.Duration(cfg.Notifications.InputDebounce)) == 0
			})
			if err != nil {
				err = fmt.Errorf("send input to %s: %w", agent, err)
			} else if !active {
				err = fmt.Errorf("no active agent named %q", agent)
			}
		}
		if err == nil && !sent {
			s.mu.Lock()
			queue = s.pendingAgentInput[agent]
			if len(queue) == 0 || queue[0] != input {
				s.mu.Unlock()
				input.done <- fmt.Errorf("send input to %s: session ended", agent)
				return
			}
			s.agentInputFlushing[agent] = false
			s.mu.Unlock()
			continue
		}
		if err == nil {
			if auditErr := db.AppendEvent(s.DB, "agent_input_sent", input.caller, input.jobID, input.detail); auditErr != nil {
				err = fmt.Errorf("record input delivery: %w", auditErr)
			}
		}

		s.mu.Lock()
		queue = s.pendingAgentInput[agent]
		if len(queue) > 0 && queue[0] == input {
			queue = queue[1:]
			if len(queue) == 0 {
				delete(s.pendingAgentInput, agent)
			} else {
				s.pendingAgentInput[agent] = queue
			}
		}
		if len(s.pendingAgentInput[agent]) == 0 {
			delete(s.agentInputFlushing, agent)
		} else {
			s.agentInputFlushing[agent] = false
		}
		s.mu.Unlock()
		input.done <- err
	}
}

// claimAgentInputTimerLocked removes timer only when it is still the current
// timer for agent. The caller must hold s.mu.
func (s *Supervisor) claimAgentInputTimerLocked(agent string, timer *time.Timer) bool {
	if s.agentInputTimers[agent] != timer {
		return false
	}
	delete(s.agentInputTimers, agent)
	return true
}
