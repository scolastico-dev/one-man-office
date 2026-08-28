package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/modelusage"
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
	srv.Handle("wait", func(agentID string, args json.RawMessage) (any, error) {
		var a proto.WaitArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, err
			}
		}
		if a.TimeoutMillis < 0 {
			return nil, fmt.Errorf("wait timeout must not be negative")
		}
		return s.waitVerb(agentID, time.Duration(a.TimeoutMillis)*time.Millisecond)
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
	s.registerConfigVerbs(srv)
	s.registerLogVerbs(srv)
	s.registerInputVerbs(srv)
	s.registerShutdownVerbs(srv)
}

func (s *Supervisor) registerConfigVerbs(srv *sockd.Server) {
	srv.Handle("office.reload", func(agentID string, _ json.RawMessage) (any, error) {
		caller := agentID
		if agentID != "user" {
			a, err := db.GetAgent(s.DB, agentID)
			if err != nil {
				return nil, err
			}
			if a.Role != "ceo" && a.Role != "firefighter" {
				return nil, fmt.Errorf("only the user, CEO, or firefighter may reload the office config")
			}
			caller = a.Name
		}
		cfg, err := config.Load(filepath.Join(s.OfficeDir, ".omo", "omo.yaml"))
		if err != nil {
			return nil, fmt.Errorf("reload config: %w", err)
		}
		if s.Usage != nil {
			timeout := time.Duration(cfg.Startup.CheckTimeout)
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			err = modelusage.Preflight(ctx, cfg, s.Usage)
			cancel()
			if err != nil {
				return nil, fmt.Errorf("reload config usage preflight: %w", err)
			}
		}
		s.replaceConfig(cfg)
		db.AppendEvent(s.DB, "office_config_reloaded", caller, 0,
			fmt.Sprintf("%d models, %d repositories", len(cfg.Models), len(cfg.Repos)))
		s.kickDispatch()
		return proto.ConfigReloadResponse{Models: len(cfg.Models), Repos: len(cfg.Repos)}, nil
	})
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
		if a.Role == "product_manager" {
			switch {
			case j.ForceDeveloperModel != "":
				goal += fmt.Sprintf("\n\nDEVELOPER MODEL POLICY: The CEO requires every developer job under this PM to use %q. omo enforces this even if --model is omitted.", j.ForceDeveloperModel)
			case len(j.DeveloperModels) > 0:
				goal += fmt.Sprintf("\n\nDEVELOPER MODEL POLICY: The CEO allows developer jobs under this PM to use only: %s. Choose with --model; omitting it is valid only when the configured developer default is in this set.", strings.Join(j.DeveloperModels, ", "))
			default:
				goal += "\n\nDEVELOPER MODEL POLICY: You may choose any selectable profile per developer job with --model; omitting it uses the configured developer default."
			}
		}
		if j.State == queue.StateAssigned {
			if err := s.Jobs.Transition(j.ID, queue.StateWorking); err != nil {
				return proto.ReadyResponse{}, err
			}
		}
	}
	prompt, err := s.renderRolePrompt(a.Name, a.Role, goal, a.JobID, a.WorkDir)
	if err != nil {
		return proto.ReadyResponse{}, err
	}
	if saved, err := db.GetShutdownContext(s.DB, a.Role, a.JobID); err != nil {
		return proto.ReadyResponse{}, err
	} else if saved != nil {
		goal += "\n\nSAFE-SHUTDOWN HANDOFF FROM THE PREVIOUS OFFICE RUN:\n" + saved.Context
		prompt, err = s.renderRolePrompt(a.Name, a.Role, goal, a.JobID, a.WorkDir)
		if err != nil {
			return proto.ReadyResponse{}, err
		}
		if err := db.DeleteShutdownContext(s.DB, saved.ID); err != nil {
			return proto.ReadyResponse{}, err
		}
		db.AppendEvent(s.DB, "shutdown_context_restored", a.Name, a.JobID, "from "+saved.Agent)
	}
	return proto.ReadyResponse{Prompt: prompt, JobID: a.JobID}, nil
}

func (s *Supervisor) renderRolePrompt(name, role, goal string, jobID int64, workDir string) (string, error) {
	// Only the roles that decide where work goes need the repo list; a
	// developer or reviewer already sits in its worktree.
	var context string
	if role == "ceo" || role == "product_manager" {
		context = s.RepoContext()
	}
	return prompts.Render(s.OfficeDir, role, prompts.Data{
		Name: name, Role: role, Goal: goal, Context: context, JobID: jobID,
		Paths: s.PromptPaths(workDir), StorageRetentionDays: s.Config().Cleanup.StorageActiveDays,
	})
}

// PreviewPrompt renders the same common and role templates used by ready,
// without creating an agent or changing durable office state.
func (s *Supervisor) PreviewPrompt(role, goal string) (string, error) {
	valid := false
	for _, candidate := range config.AllRoles {
		if candidate == role {
			valid = true
			break
		}
	}
	if !valid {
		return "", fmt.Errorf("unknown role %q", role)
	}
	return s.renderRolePrompt(role+"-preview", role, goal, 0, "")
}

// waitVerb parks the agent until mail arrives or the supervisor wakes it.
// The CEO and smoke alarm are refused because neither role may park.
func (s *Supervisor) waitVerb(agentID string, timeout time.Duration) (proto.WaitResponse, error) {
	a, err := db.GetAgent(s.DB, agentID)
	if err != nil {
		return proto.WaitResponse{}, err
	}
	switch a.Role {
	case "ceo":
		return proto.WaitResponse{}, fmt.Errorf(
			"the CEO must not wait: this terminal belongs to the user, and waiting blocks their input. " +
				"Stay at your prompt — ask the user what they want next, or report what the office is doing. " +
				"Mail still reaches you here")
	case "smokealarm":
		return proto.WaitResponse{}, fmt.Errorf(
			"a smoke alarm must not wait; finish checking and reporting, then use omo done")
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
	reason := "mail or supervisor release"
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-ch:
		case <-timer.C:
			reason = "timeout"
		}
	} else {
		<-ch
	}
	if err := db.SetAgentState(s.DB, agentID, "working"); err != nil {
		return proto.WaitResponse{}, err
	}
	if reason == "timeout" {
		db.AppendEvent(s.DB, "agent_wait_timeout", agentID, 0, "")
	} else {
		db.AppendEvent(s.DB, "agent_woken", agentID, 0, "")
	}
	return proto.WaitResponse{Reason: reason}, nil
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
	case "freelancer":
		if a.JobID != 0 {
			j, err := s.Jobs.Get(a.JobID)
			if err != nil {
				return err
			}
			if j.State == queue.StateWorking {
				if err := s.Jobs.Transition(j.ID, queue.StateMerging); err != nil {
					return err
				}
				if err := s.Jobs.Transition(j.ID, queue.StateDone); err != nil {
					return err
				}
			}
			s.Jobs.SetResult(a.JobID, result)
		}
		// Keep the session and its context alive for CEO follow-up questions.
		// The role prompt parks it with omo wait after this acknowledgement.
		s.kickDispatch()
		return nil
	default:
		if a.JobID != 0 {
			j, err := s.Jobs.Get(a.JobID)
			if err != nil {
				return err
			}
			if j.State == queue.StateWorking {
				// PM jobs go straight to done — no review stage.
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
