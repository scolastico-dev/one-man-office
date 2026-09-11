package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

var SafeShutdownTimeout = 2 * time.Minute

const safeShutdownInstruction = `SAFE SHUTDOWN REQUESTED BY THE USER.

Do not start another action. If your current assigned work is genuinely within a few minutes of complete, finish the minimum remaining work and use the normal omo completion command for your role. Otherwise halt the current action immediately and save a concise handoff with the important state, completed work, remaining work, blockers, and exact next step:

  omo context save "<concise handoff>"

If normal completion is rejected because it would need to spawn dependent work during shutdown, save the handoff instead. The CEO cannot complete and must always save a handoff. Do not write a full context dump, transcript, or speculative narrative. After completing or saving the handoff, remain idle; the office will stop automatically.`

func (s *Supervisor) registerShutdownVerbs(srv *sockd.Server) {
	srv.Handle("context.save", func(agentID string, args json.RawMessage) (any, error) {
		var request proto.ContextSaveArgs
		if err := json.Unmarshal(args, &request); err != nil {
			return nil, err
		}
		request.Summary = strings.TrimSpace(request.Summary)
		if request.Summary == "" {
			return nil, fmt.Errorf("a concise handoff summary is required")
		}
		if utf8.RuneCountInString(request.Summary) > 8000 {
			return nil, fmt.Errorf("handoff summary must be at most 8000 characters; save important state, not a context dump")
		}
		a, err := db.GetAgent(s.DB, agentID)
		if err != nil {
			return nil, err
		}
		if err := db.SaveShutdownContext(s.DB, db.ShutdownContext{
			Role: a.Role, JobID: a.JobID, Agent: a.Name, Context: request.Summary,
		}); err != nil {
			return nil, err
		}
		db.AppendEvent(s.DB, "shutdown_context_saved", a.Name, a.JobID, fmt.Sprintf("%d characters", utf8.RuneCountInString(request.Summary)))
		return nil, nil
	})

	srv.Handle("office.safe-shutdown", func(agentID string, args json.RawMessage) (any, error) {
		var request proto.SafeShutdownArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &request); err != nil {
				return nil, err
			}
		}
		actor := agentID
		if agentID != "user" {
			a, err := db.GetAgent(s.DB, agentID)
			if err != nil {
				return nil, err
			}
			if a.Role != "ceo" && a.Role != "firefighter" {
				return nil, fmt.Errorf("only the user, CEO, or firefighter may safely shut down omo")
			}
			actor = a.Name
		}
		return nil, s.beginSafeShutdown(actor, request.Reason)
	})
}

// BeginSafeShutdown halts new work, broadcasts and injects the handoff
// instruction, then stops after every targeted agent finishes/checkpoints or
// the bounded deadline expires.
func (s *Supervisor) BeginSafeShutdown(actor string) error {
	return s.beginSafeShutdown(actor, "")
}

// EmitShutdown publishes the shutdown lifecycle event once. Safe shutdown
// supplies the first recorded safe reason; ordinary close leaves it blank.
func (s *Supervisor) EmitShutdown(safe bool) {
	if s.Plugins == nil {
		return
	}
	reason := ""
	s.mu.Lock()
	if s.shutdownInProgress {
		safe = true
		reason = s.exitReason
	} else if safe {
		reason = s.exitReason
	}
	s.mu.Unlock()
	s.shutdownLifecycleMu.Lock()
	if !s.shutdownLifecycleStarted {
		s.shutdownLifecycleStarted = true
		s.shutdownLifecycleSafe = safe
		s.shutdownLifecycleReason = reason
	}
	if s.shutdownLifecycleEmitted {
		s.shutdownLifecycleMu.Unlock()
		return
	}
	s.shutdownLifecycleEmitted = true
	safe = s.shutdownLifecycleSafe
	reason = s.shutdownLifecycleReason
	s.shutdownLifecycleMu.Unlock()
	_, _ = s.Plugins.EmitLifecycle(context.Background(), plugins.Event{
		Name: plugins.EventShutdown,
		Data: map[string]any{"office_path": s.OfficeDir, "reason": reason, "safe": safe},
	})
}

// prepareShutdownLifecycle publishes safe-shutdown state before the
// supervisor mutex is released, so an ordinary close fallback cannot win the
// once-only lifecycle emission race.
func (s *Supervisor) prepareShutdownLifecycleLocked(safe bool, reason string) {
	s.shutdownLifecycleMu.Lock()
	defer s.shutdownLifecycleMu.Unlock()
	if s.shutdownLifecycleStarted || s.shutdownLifecycleEmitted {
		return
	}
	s.shutdownLifecycleStarted = true
	s.shutdownLifecycleSafe = safe
	s.shutdownLifecycleReason = reason
}

func (s *Supervisor) beginSafeShutdown(actor, reason string) error {
	reason = strings.TrimSpace(reason)
	s.mu.Lock()
	if s.shutdownInProgress {
		s.mu.Unlock()
		return nil
	}
	s.shutdownInProgress = true
	s.firefighterPaused = true
	s.ceoSpawnHalted = true
	if reason != "" {
		s.setExitReasonLocked(reason)
	}
	s.prepareShutdownLifecycleLocked(true, s.exitReason)
	s.mu.Unlock()
	s.EmitShutdown(true)
	agents, _ := db.LivingAgents(s.DB)
	db.AppendEvent(s.DB, "safe_shutdown_started", actor, 0, fmt.Sprintf("%d agents", len(agents)))
	_, _ = s.Mail.Send(bus.SystemSender, "", "safe shutdown requested", safeShutdownInstruction, bus.PrioUrgent)
	for _, agent := range agents {
		name := agent.Name
		if sess, ok := s.Session(name); ok {
			go func() {
				if err := sess.SendPrompt(safeShutdownInstruction); err != nil {
					db.AppendEvent(s.DB, "safe_shutdown_injection_error", name, 0, err.Error())
					return
				}
				db.AppendEvent(s.DB, "safe_shutdown_injected", name, 0, "")
			}()
		}
	}
	go s.awaitSafeShutdown(agents)
	return nil
}

func (s *Supervisor) awaitSafeShutdown(targets []db.Agent) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	expires := time.Now().Add(SafeShutdownTimeout)
	complete := func() bool {
		for _, target := range targets {
			a, err := db.GetAgent(s.DB, target.Name)
			if err != nil || a.State == "done" || a.State == "dead" || s.safeShutdownRoleComplete(target) {
				continue
			}
			saved, contextErr := db.HasShutdownContextForAgent(s.DB, target.Name)
			if contextErr != nil || !saved {
				return false
			}
		}
		return true
	}
	for {
		if complete() {
			db.AppendEvent(s.DB, "safe_shutdown_ready", "", 0, "all agents completed or checkpointed")
			break
		}
		if time.Now().After(expires) {
			db.AppendEvent(s.DB, "safe_shutdown_timeout", "", 0, SafeShutdownTimeout.String())
			break
		}
		<-ticker.C
	}
	// Let the initiating socket handler acknowledge before the owning office
	// tears down its listener and database.
	time.Sleep(25 * time.Millisecond)
	s.requestEmergencyStop()
}

func (s *Supervisor) safeShutdownRoleComplete(target db.Agent) bool {
	if target.JobID == 0 {
		return false
	}
	j, err := s.Jobs.Get(target.JobID)
	if err != nil {
		return false
	}
	switch target.Role {
	case "developer":
		return j.State == queue.StateReview || j.State == queue.StateMerging || j.State == queue.StateDone
	case "reviewer":
		return j.State == queue.StateMerging || j.State == queue.StateDone
	case "freelancer":
		return j.State == queue.StateDone
	}
	return false
}
