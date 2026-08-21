package supervisor

import (
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

// CancelJob applies the durable queue transition and retires any assigned
// agent without allowing the death handler to requeue the cancelled work.
func (s *Supervisor) CancelJob(id int64, actor string) error {
	j, err := s.Jobs.Get(id)
	if err != nil {
		return err
	}
	if err := s.Jobs.Transition(id, queue.StateCancelled); err != nil {
		return err
	}
	db.AppendEvent(s.DB, "job_cancelled", actor, id, "management action")
	if j.Assignee != "" {
		s.WakeAgent(j.Assignee)
		return s.KillAgent(j.Assignee, true)
	}
	return nil
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
