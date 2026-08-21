package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

const maxDiffBytes = 64 * 1024

// spawnReviewer starts a clean-context reviewer in the job's worktree with
// only the goal and the branch diff.
func (s *Supervisor) spawnReviewer(j *queue.Job) error {
	repoPath, ok := s.Config().Repos[j.Repo]
	if !ok {
		return fmt.Errorf("job %d: unknown repo %q", j.ID, j.Repo)
	}
	diff, err := s.Git.Diff(repoPath, j.Branch)
	if err != nil {
		diff = "(diff unavailable: " + err.Error() + ")"
	}
	if len(diff) > maxDiffBytes {
		diff = diff[:maxDiffBytes] + "\n… [diff truncated — inspect the worktree for the rest]"
	}
	goal := s.Msgs.ReviewGoal(messages.ReviewData{
		JobID: j.ID, Title: j.Title, Goal: j.Goal, Branch: j.Branch, Diff: diff,
	})
	name, err := s.spawnRole("reviewer", j.ID, j.Worktree, goal, j.Retries)
	if err != nil {
		return err
	}
	db.AppendEvent(s.DB, "review_started", name, j.ID, "")
	return nil
}

// developerDone: working|rework → review, then a fresh reviewer.
func (s *Supervisor) developerDone(a *db.Agent, result string) error {
	if !s.spawnAllowed("reviewer") {
		return ErrSpawningHalted
	}
	j, err := s.Jobs.Get(a.JobID)
	if err != nil {
		return err
	}
	if j.State != queue.StateWorking && j.State != queue.StateRework {
		return fmt.Errorf("job %d is %s — done is only valid while working/rework", j.ID, j.State)
	}
	if j.State == queue.StateRework {
		// A normal re-review deliberately gets fresh context. The rejected
		// reviewer stays alive for questions until this exact point.
		if old := s.reviewerForJob(j.ID); old != "" {
			_ = s.KillAgent(old, true)
		}
	}
	if err := s.Jobs.Transition(j.ID, queue.StateReview); err != nil {
		return err
	}
	if err := s.Jobs.SetResult(j.ID, result); err != nil {
		return err
	}
	// The developer session stays alive: its prompt tells it to `omo wait`
	// for possible rework. It is terminated on merge.
	return s.spawnReviewer(j)
}

func (s *Supervisor) registerReviewVerbs(srv *sockd.Server) {
	srv.Handle("job.verdict", func(agentID string, args json.RawMessage) (any, error) {
		var a proto.VerdictArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		caller, err := db.GetAgent(s.DB, agentID)
		if err != nil {
			return nil, err
		}
		if caller.Role != "reviewer" || caller.JobID != a.JobID {
			return nil, fmt.Errorf("only the assigned reviewer may deliver a verdict for job %d", a.JobID)
		}
		j, err := s.Jobs.Get(a.JobID)
		if err != nil {
			return nil, err
		}
		if j.State != queue.StateReview {
			return nil, fmt.Errorf("job %d is %s, not review", j.ID, j.State)
		}
		switch a.Verdict {
		case "merge":
			return nil, s.mergeVerdict(caller, j, a.Notes)
		case "reject":
			if a.Notes == "" {
				return nil, fmt.Errorf("reject requires --notes with concrete findings")
			}
			return nil, s.rejectVerdict(caller, j, a.Notes)
		default:
			return nil, fmt.Errorf("verdict must be merge or reject, got %q", a.Verdict)
		}
	})
	srv.Handle("job.review_override", func(agentID string, args json.RawMessage) (any, error) {
		var a proto.ReviewOverrideArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		return nil, s.overrideReview(agentID, a)
	})
}

func (s *Supervisor) mergeVerdict(reviewer *db.Agent, j *queue.Job, notes string) error {
	if err := s.Jobs.Transition(j.ID, queue.StateMerging); err != nil {
		return err
	}
	repoPath := s.Config().Repos[j.Repo]
	if err := s.Git.MergeBranch(repoPath, j.Branch); err != nil {
		s.Jobs.Transition(j.ID, queue.StateReview)
		if errors.Is(err, gitops.ErrMergeConflict) {
			return fmt.Errorf("merge conflict for job %d: in your worktree, merge the target branch into %s, resolve, commit, then retry the verdict. Details: %v", j.ID, j.Branch, err)
		}
		return err
	}
	if err := s.Jobs.Transition(j.ID, queue.StateDone); err != nil {
		return err
	}
	s.Jobs.SetResult(j.ID, notes)
	s.Jobs.ResetReviewState(j.ID)
	// The PM may be parked waiting for this exact state change. Make the
	// completion durable as mail so DeliverNudge wakes an active wait and a
	// wait started just after delivery still observes the unread message.
	if pm := s.pmForJob(j.ID); pm != "" {
		body := fmt.Sprintf("Job #%d (%s) was approved and merged.", j.ID, j.Title)
		if notes != "" {
			body += "\n\nReviewer notes: " + notes
		}
		if _, err := s.Mail.Send(reviewer.Name, pm, fmt.Sprintf("job merged: #%d %s", j.ID, j.Title), body, bus.PrioHigh); err != nil {
			db.AppendEvent(s.DB, "notification_error", pm, j.ID, err.Error())
		}
	}
	// Terminate the developer and clean up.
	if j.Assignee != "" {
		s.WakeAgent(j.Assignee) // release a parked wait before killing
		s.KillAgent(j.Assignee, true)
	}
	if err := s.Git.RemoveWorktree(repoPath, j.Worktree, j.Branch); err != nil {
		db.AppendEvent(s.DB, "cleanup_error", "", j.ID, err.Error())
	}
	// Publish completion only after worktree cleanup has stopped touching the
	// repository. Integration tests and other observers can use this durable
	// event as the boundary for safely removing or inspecting an office.
	db.AppendEvent(s.DB, "job_merged", j.Assignee, j.ID, j.Branch)
	s.kickDispatch()
	return nil
}

func (s *Supervisor) rejectVerdict(reviewer *db.Agent, j *queue.Job, notes string) error {
	if err := s.Jobs.Transition(j.ID, queue.StateRework); err != nil {
		return err
	}
	s.Jobs.SetNote(j.ID, notes)
	db.AppendEvent(s.DB, "job_rejected", j.Assignee, j.ID, notes)
	n, _ := s.Jobs.IncrementReviewRejections(j.ID)
	pm := s.pmForJob(j.ID)
	if pm != "" {
		// This is enforced by the supervisor, not left to prompt compliance.
		_, _ = s.Mail.Send(reviewer.Name, pm, fmt.Sprintf("FYI/noop: review findings for job #%d", j.ID), notes, bus.PrioNormal)
	}
	if j.Assignee != "" {
		_, _ = s.Mail.Send(reviewer.Name, j.Assignee, fmt.Sprintf("review rejected for job #%d", j.ID),
			notes+"\n\nIf these findings seem out of scope or overly picky, contact your PM; the PM can override the rejection.", bus.PrioHigh)
		// Mail.Send's Notify hook wakes or debounce-nudges the developer.
	}
	if n >= s.Config().Reviews.EscalateAfter {
		detail := s.Msgs.ReviewEscalated(j.ID, n)
		if pm != "" {
			_, _ = s.Mail.Send(reviewer.Name, pm, fmt.Sprintf("review escalation: job #%d", j.ID), detail, bus.PrioHigh)
		} else if ceo, ok := s.Mail.Dir.CEO(); ok {
			_, _ = s.Mail.Send(reviewer.Name, ceo, fmt.Sprintf("review escalation: job #%d", j.ID), detail, bus.PrioHigh)
		}
		db.AppendEvent(s.DB, "review_escalated", reviewer.Name, j.ID, fmt.Sprintf("consecutive=%d", n))
	}
	return nil
}

func (s *Supervisor) reviewerForJob(jobID int64) string {
	agents, err := db.LivingByJobRole(s.DB, jobID, "reviewer")
	if err != nil || len(agents) == 0 {
		return ""
	}
	return agents[0].Name
}

func (s *Supervisor) pmForJob(jobID int64) string {
	var pm string
	_ = s.DB.QueryRow(`SELECT pj.assignee FROM jobs j JOIN jobs pj ON pj.id = j.parent_job WHERE j.id = ?`, jobID).Scan(&pm)
	return pm
}

func (s *Supervisor) overrideReview(agentID string, a proto.ReviewOverrideArgs) error {
	caller, err := db.GetAgent(s.DB, agentID)
	if err != nil {
		return err
	}
	if caller.Role != "product_manager" || caller.JobID == 0 {
		return fmt.Errorf("only the owning product manager may override a review")
	}
	j, err := s.Jobs.Get(a.JobID)
	if err != nil {
		return err
	}
	if j.ParentJob != caller.JobID {
		return fmt.Errorf("job %d does not belong to this product manager", j.ID)
	}
	if j.State != queue.StateRework {
		return fmt.Errorf("job %d is %s, not awaiting rework", j.ID, j.State)
	}
	if a.Notes == "" {
		return fmt.Errorf("override requires notes with the PM decision")
	}
	reviewer := s.reviewerForJob(j.ID)
	if reviewer == "" {
		return fmt.Errorf("job %d has no retained reviewer to override", j.ID)
	}
	if err := s.Jobs.SetReviewOverride(j.ID, true); err != nil {
		return err
	}
	if err := s.Jobs.SetNote(j.ID, "PM override: "+a.Notes); err != nil {
		return err
	}
	if err := s.Jobs.Transition(j.ID, queue.StateReview); err != nil {
		return err
	}
	detail := s.Msgs.ReviewOverride(j.ID, a.Notes)
	if _, err := s.Mail.Send(agentID, reviewer, fmt.Sprintf("PM override: merge job #%d", j.ID), detail, bus.PrioUrgent); err != nil {
		return err
	}
	if j.Assignee != "" {
		_, _ = s.Mail.Send(agentID, j.Assignee, fmt.Sprintf("PM accepted your review concern for job #%d", j.ID),
			"The rejection was overridden. Return to `omo wait`; the retained reviewer has been directed to merge.", bus.PrioHigh)
	}
	db.AppendEvent(s.DB, "review_overridden", reviewer, j.ID, a.Notes)
	return nil
}

// handleReviewerDeath respawns a fresh reviewer for a job stuck in review.
func (s *Supervisor) handleReviewerDeath(a *db.Agent) {
	if a.JobID == 0 {
		return
	}
	j, err := s.Jobs.Get(a.JobID)
	if err != nil || j.State != queue.StateReview {
		return
	}
	n, err := s.Jobs.IncrementRetries(j.ID)
	if err != nil || n > s.maxJobRetries() {
		s.Jobs.Transition(j.ID, queue.StateFailed)
		s.Mail.Send(bus.SystemSender, "user", "review failed",
			s.Msgs.ReviewFailed(j.ID, n), bus.PrioUrgent)
		return
	}
	s.spawnReviewer(j)
}

// resumePendingReviews recovers review jobs whose reviewer could not be
// spawned while the CEO or firefighter had work-agent spawning halted.
func (s *Supervisor) resumePendingReviews() {
	jobs, err := s.Jobs.List(queue.StateReview)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if s.reviewerForJob(j.ID) == "" {
			_ = s.spawnReviewer(j)
		}
	}
}
