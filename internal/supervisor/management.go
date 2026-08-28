package supervisor

import (
	"fmt"
	"time"

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
		if a.JobID != 0 {
			j, err := s.Jobs.Get(a.JobID)
			if err != nil {
				return err
			}
			if queue.ValidTransition(j.State, queue.StateCancelled) {
				if err := s.Jobs.Transition(j.ID, queue.StateCancelled); err != nil {
					return err
				}
				db.AppendEvent(s.DB, "job_cancelled", actor, j.ID, "assigned agent killed")
			}
		}
		db.AppendEvent(s.DB, "agent_killed", name, a.JobID, "by "+actor)
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
