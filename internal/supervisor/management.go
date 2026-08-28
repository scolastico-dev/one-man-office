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

// StopAgent terminates one living agent. Its normal death path preserves and
// requeues assigned work, which is also the supervisor's restart behavior.
func (s *Supervisor) StopAgent(name, actor, action string) error {
	a, err := db.GetAgent(s.DB, name)
	if err != nil {
		return fmt.Errorf("no agent %q", name)
	}
	if a.State == "done" || a.State == "dead" {
		return fmt.Errorf("agent %q is no longer active", name)
	}
	kind := "agent_killed"
	if action == "restart" {
		kind = "agent_restart_requested"
	}
	db.AppendEvent(s.DB, kind, name, a.JobID, "by "+actor)
	if err := s.KillAgent(name, false); err != nil {
		return err
	}
	s.WakeAgent(name)
	return nil
}
