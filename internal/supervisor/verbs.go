package supervisor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

var GraceBeforeReap = 2 * time.Second

// Register wires the supervisor-coupled verbs. Extended by later tasks.
func (s *Supervisor) Register(srv *sockd.Server) {
	srv.Handle("ready", func(agentID string, _ json.RawMessage) (any, error) {
		return s.ready(agentID)
	})
	srv.Handle("wait", func(agentID string, _ json.RawMessage) (any, error) {
		return s.waitVerb(agentID)
	})
	srv.Handle("done", func(agentID string, args json.RawMessage) (any, error) {
		var a proto.DoneArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
		}
		return nil, s.done(agentID, a.Result)
	})
	srv.Handle("step", func(agentID string, args json.RawMessage) (any, error) {
		var a proto.StepArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		a.Description = strings.TrimSpace(a.Description)
		if a.Description == "" {
			return nil, fmt.Errorf("step description is required")
		}
		if utf8.RuneCountInString(a.Description) > 500 {
			return nil, fmt.Errorf("step description must be at most 500 characters")
		}
		if err := db.SetAgentStep(s.DB, agentID, a.Description); err != nil {
			return nil, err
		}
		agent, err := db.GetAgent(s.DB, agentID)
		if err != nil {
			return nil, err
		}
		db.AppendEvent(s.DB, "agent_step", agentID, agent.JobID, a.Description)
		return nil, nil
	})
	srv.Handle("agent.list", func(_ string, _ json.RawMessage) (any, error) {
		agents, err := db.LivingAgents(s.DB)
		if err != nil {
			return nil, err
		}
		out := make([]proto.AgentStatus, 0, len(agents))
		for _, a := range agents {
			updated := ""
			if a.StepUpdatedAt.Valid {
				updated = a.StepUpdatedAt.String
			}
			out = append(out, proto.AgentStatus{Name: a.Name, Role: a.Role, State: a.State, JobID: a.JobID, Step: a.Step, StepUpdatedAt: updated})
		}
		return out, nil
	})
	s.registerJobVerbs(srv)
	s.registerReviewVerbs(srv)
	s.registerIncidentCreate(srv)
	s.registerFireVerbs(srv)
}

// ready flips the agent to working, moves its job to working and returns
// the fully rendered role prompt. State is committed before returning —
// the ack IS the durable handshake.
func (s *Supervisor) ready(agentID string) (proto.ReadyResponse, error) {
	a, err := db.GetAgent(s.DB, agentID)
	if err != nil {
		return proto.ReadyResponse{}, err
	}
	if err := db.SetAgentState(s.DB, agentID, "working"); err != nil {
		return proto.ReadyResponse{}, err
	}
	db.AppendEvent(s.DB, "agent_ready", agentID, a.JobID, "")
	goal := a.Goal
	if a.JobID != 0 {
		j, err := s.Jobs.Get(a.JobID)
		if err != nil {
			return proto.ReadyResponse{}, err
		}
		goal = j.Goal
		if j.Note != "" {
			goal += "\n\nNOTE: " + j.Note
		}
		if j.State == queue.StateAssigned {
			if err := s.Jobs.Transition(j.ID, queue.StateWorking); err != nil {
				return proto.ReadyResponse{}, err
			}
		}
	}
	// Only the roles that decide where work goes need the repo list; a
	// developer or reviewer already sits in its worktree.
	var context string
	if a.Role == "ceo" || a.Role == "product_manager" {
		context = s.RepoContext()
	}
	prompt, err := prompts.Render(s.OfficeDir, a.Role, prompts.Data{
		Name: a.Name, Role: a.Role, Goal: goal, Context: context, JobID: a.JobID,
	})
	if err != nil {
		return proto.ReadyResponse{}, err
	}
	return proto.ReadyResponse{Prompt: prompt, JobID: a.JobID}, nil
}

// waitVerb parks the agent until mail arrives or the supervisor wakes it.
// The CEO is refused: its session is the user's terminal, and a blocked CLI
// cannot be typed to.
func (s *Supervisor) waitVerb(agentID string) (proto.WaitResponse, error) {
	if a, err := db.GetAgent(s.DB, agentID); err == nil && a.Role == "ceo" {
		return proto.WaitResponse{}, fmt.Errorf(
			"the CEO must not wait: this terminal belongs to the user, and waiting blocks their input. " +
				"Stay at your prompt — ask the user what they want next, or report what the office is doing. " +
				"Mail still reaches you here")
	}
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.waiters[agentID] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		// Do not remove a newer waiter if a malformed client managed to issue
		// two concurrent wait calls for the same agent.
		if s.waiters[agentID] == ch {
			delete(s.waiters, agentID)
		}
		s.mu.Unlock()
	}()
	if err := db.SetAgentState(s.DB, agentID, "waiting"); err != nil {
		return proto.WaitResponse{}, err
	}
	db.AppendEvent(s.DB, "agent_waiting", agentID, 0, "")
	// Register the waiter before checking the durable inbox. This ordering
	// closes both sides of the lost-wakeup race: mail delivered before the
	// registration is found here, while mail delivered after it signals ch.
	unread, err := s.Mail.UnreadCount(agentID)
	if err != nil {
		_ = db.SetAgentState(s.DB, agentID, "working")
		return proto.WaitResponse{}, err
	}
	if unread > 0 {
		select {
		case ch <- struct{}{}:
		default: // delivery raced with the inbox check and already woke us
		}
	}
	<-ch
	if err := db.SetAgentState(s.DB, agentID, "working"); err != nil {
		return proto.WaitResponse{}, err
	}
	db.AppendEvent(s.DB, "agent_woken", agentID, 0, "")
	return proto.WaitResponse{Reason: "mail or supervisor release"}, nil
}

// done handles goal completion per role. The developer branch (→ review)
// is added in Task 15.
func (s *Supervisor) done(agentID, result string) error {
	a, err := db.GetAgent(s.DB, agentID)
	if err != nil {
		return err
	}
	db.AppendEvent(s.DB, "agent_done", agentID, a.JobID, result)
	switch a.Role {
	case "ceo":
		return fmt.Errorf("the CEO never leaves — the office runs as long as you do")
	case "developer":
		return s.developerDone(a, result) // Task 15
	case "reviewer":
		j, err := s.Jobs.Get(a.JobID)
		if err != nil {
			return err
		}
		if j.State == queue.StateRework {
			// Retain rejected reviewers for developer questions and possible PM
			// override. Their prompt sends them into omo wait after this ack.
			return nil
		}
		if err := db.SetAgentState(s.DB, agentID, "done"); err != nil {
			return err
		}
		go s.reapLater(agentID)
		return nil
	default:
		if a.JobID != 0 {
			j, err := s.Jobs.Get(a.JobID)
			if err != nil {
				return err
			}
			if j.State == queue.StateWorking {
				// PM/freelancer jobs go straight to done — no review stage.
				if err := s.Jobs.Transition(j.ID, queue.StateMerging); err != nil {
					return err
				}
				if err := s.Jobs.Transition(j.ID, queue.StateDone); err != nil {
					return err
				}
			}
			s.Jobs.SetResult(a.JobID, result)
		}
		if err := db.SetAgentState(s.DB, agentID, "done"); err != nil {
			return err
		}
		go s.reapLater(agentID)
		s.kickDispatch()
		return nil
	}
}

// reapLater kills a done agent's session after a short grace period (the
// CLI gets to print the ack).
func (s *Supervisor) reapLater(agentID string) {
	time.Sleep(GraceBeforeReap)
	s.mu.Lock()
	sess, ok := s.sessions[agentID]
	s.mu.Unlock()
	if ok {
		sess.Kill()
	}
}
