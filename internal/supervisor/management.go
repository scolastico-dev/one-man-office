package supervisor

import (
	"errors"
	"fmt"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

// CancelJob applies the durable queue transition, retires every agent attached
// to the job, and removes its managed worktree.
func (s *Supervisor) CancelJob(id int64, actor string) error {
	if err := s.Jobs.Transition(id, queue.StateCancelled); err != nil {
		return err
	}
	db.AppendEvent(s.DB, "job_cancelled", actor, id, "management action")
	agents, err := db.LivingByJob(s.DB, id)
	if err != nil {
		return err
	}
	sessions := make([]<-chan struct{}, 0, len(agents))
	var stopErrors []error
	for _, agent := range agents {
		s.WakeAgent(agent.Name)
		if sess, ok := s.Session(agent.Name); ok {
			sessions = append(sessions, sess.Done())
		}
		if err := s.KillAgent(agent.Name, true); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("stop %s: %w", agent.Name, err))
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	waiting := true
	for _, done := range sessions {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			stopErrors = append(stopErrors, fmt.Errorf("timed out stopping agents for job %d", id))
			waiting = false
			break
		}
		timer := time.NewTimer(remaining)
		select {
		case <-done:
			timer.Stop()
		case <-timer.C:
			stopErrors = append(stopErrors, fmt.Errorf("timed out stopping agents for job %d", id))
			waiting = false
		}
		if !waiting {
			break
		}
	}
	if len(stopErrors) == 0 {
		if err := s.cleanupTerminalWorktree(id); err != nil {
			db.AppendEvent(s.DB, "cleanup_error", actor, id, err.Error())
			stopErrors = append(stopErrors, err)
		}
	}
	s.kickDispatch()
	return errors.Join(stopErrors...)
}

// RequeueJob retries failed or cancelled work and wakes dispatch.
func (s *Supervisor) RequeueJob(id int64, actor string) error {
	if err := s.Jobs.Retry(id); err != nil {
		return err
	}
	db.AppendEvent(s.DB, "job_requeued", actor, id, "management action")
	s.kickDispatch()
	return nil
}

// StopAgent applies explicit management semantics. A kill is final and
// cancels active work. A restart replaces the process in-place without
// changing the job state or consuming a job retry.
func (s *Supervisor) StopAgent(name, actor, action string) error {
	if action != "kill" && action != "restart" {
		return fmt.Errorf("unknown agent stop action %q", action)
	}
	a, err := db.GetAgent(s.DB, name)
	if err != nil {
		return fmt.Errorf("no agent %q", name)
	}
	if a.State == "done" || a.State == "dead" {
		return fmt.Errorf("agent %q is no longer active", name)
	}
	if action == "kill" {
		db.AppendEvent(s.DB, "agent_killed", name, a.JobID, "by "+actor)
		if a.JobID != 0 {
			j, err := s.Jobs.Get(a.JobID)
			if err != nil {
				return err
			}
			if queue.ValidTransition(j.State, queue.StateCancelled) {
				return s.CancelJob(j.ID, actor)
			}
		}
		s.WakeAgent(name)
		if err := s.KillAgent(name, true); err != nil {
			return err
		}
		s.kickDispatch()
		return nil
	}

	db.AppendEvent(s.DB, "agent_restart_requested", name, a.JobID, "by "+actor)
	s.WakeAgent(name)
	if err := s.KillAgent(name, true); err != nil {
		return err
	}
	// Avoid overlapping processes in one worktree. For firefighters this
	// also lets their exit handoff observe the replacement spawned below.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := s.Session(name); !ok {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("agent %q did not stop before restart", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if a.JobID != 0 {
		j, err := s.Jobs.Get(a.JobID)
		if err != nil {
			return err
		}
		if j.State == queue.StateCancelled || j.State == queue.StateDone || j.State == queue.StateFailed {
			db.AppendEvent(s.DB, "agent_restart_skipped", name, a.JobID, "job is "+string(j.State))
			return nil
		}
	}
	replacement, err := s.spawnAttempt(a.Role, a.Profile, a.JobID, a.WorkDir, a.Goal, 0, true, false, true)
	if err != nil {
		db.AppendEvent(s.DB, "agent_restart_failed", name, a.JobID, err.Error())
		return fmt.Errorf("restart agent %q: %w", name, err)
	}
	if a.JobID != 0 {
		if err := s.Jobs.SetAssignee(a.JobID, replacement); err != nil {
			_ = s.KillAgent(replacement, true)
			return err
		}
	}
	db.AppendEvent(s.DB, "agent_restarted", replacement, a.JobID, "replaced="+name+" by "+actor)
	return nil
}
