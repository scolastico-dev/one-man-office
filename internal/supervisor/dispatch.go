package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

// DispatchLoop assigns queued jobs to freshly spawned agents, forever.
func (s *Supervisor) DispatchLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
		s.dispatchOnce()
	}
}

func (s *Supervisor) dispatchOnce() {
	s.mu.Lock()
	paused := s.paused
	s.mu.Unlock()
	if paused {
		return
	}
	jobs, err := s.Jobs.List(queue.StateQueued)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if !s.hasCapacity(j.Role) {
			continue
		}
		if err := s.assign(j); err != nil {
			db.AppendEvent(s.DB, "dispatch_error", "", j.ID, err.Error())
		}
	}
}

func (s *Supervisor) hasCapacity(role string) bool {
	var cap int
	switch role {
	case "developer":
		cap = s.Cfg.Limits.MaxDevelopers
	case "freelancer":
		cap = s.Cfg.Limits.MaxFreelancers
	default:
		return true
	}
	n, err := db.CountLivingByRole(s.DB, role)
	return err == nil && n < cap
}

func (s *Supervisor) assign(j *queue.Job) error {
	dir := s.OfficeDir
	if j.Role == "developer" {
		repoPath, ok := s.Cfg.Repos[j.Repo]
		if !ok {
			s.Jobs.Transition(j.ID, queue.StateFailed)
			return fmt.Errorf("job %d: unknown repo %q", j.ID, j.Repo)
		}
		branch := fmt.Sprintf("omo/job-%d", j.ID)
		wt := filepath.Join(s.OfficeDir, ".omo", "worktrees", fmt.Sprintf("%s-%d", j.Repo, j.ID))
		if _, err := os.Stat(wt); os.IsNotExist(err) {
			if err := s.Git.AddWorktree(repoPath, wt, branch); err != nil {
				return fmt.Errorf("job %d: worktree: %w", j.ID, err)
			}
		}
		if err := s.Jobs.SetWorktree(j.ID, wt, branch); err != nil {
			return err
		}
		dir = wt
	}
	profileKey, _, err := s.Cfg.ProfileForJob(j.Role, j.Model)
	if err != nil {
		s.Jobs.Transition(j.ID, queue.StateFailed)
		return fmt.Errorf("job %d: %w", j.ID, err)
	}
	if err := s.Jobs.Transition(j.ID, queue.StateAssigned); err != nil {
		return err
	}
	name, err := s.Spawn(j.Role, profileKey, j.ID, dir, j.Goal)
	if err != nil {
		s.Jobs.Transition(j.ID, queue.StateQueued)
		return err
	}
	return s.Jobs.SetAssignee(j.ID, name)
}

// registerJobVerbs is called from Register (verbs.go).
func (s *Supervisor) registerJobVerbs(srv *sockd.Server) {
	srv.Handle("job.create", func(agentID string, args json.RawMessage) (any, error) {
		var a proto.JobCreateArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		creator, err := db.GetAgent(s.DB, agentID)
		if err != nil {
			return nil, err
		}
		switch creator.Role {
		case "ceo":
			if a.Role != "product_manager" && a.Role != "developer" && a.Role != "freelancer" {
				return nil, fmt.Errorf("ceo may create product_manager, developer or freelancer jobs, not %q", a.Role)
			}
		case "product_manager":
			if a.Role != "developer" {
				return nil, fmt.Errorf("a PM may only create developer jobs")
			}
			a.Parent = creator.JobID // lineage is enforced, not trusted
		default:
			return nil, fmt.Errorf("role %q may not create jobs", creator.Role)
		}
		if a.Title == "" || a.Goal == "" {
			return nil, fmt.Errorf("title and goal are required")
		}
		if a.Role == "developer" {
			if _, ok := s.Cfg.Repos[a.Repo]; !ok {
				return nil, fmt.Errorf("developer jobs need a repo from omo.yaml, got %q", a.Repo)
			}
		}
		if _, _, err := s.Cfg.ProfileForJob(a.Role, a.Model); err != nil {
			return nil, err
		}
		j := &queue.Job{Title: a.Title, Goal: a.Goal, Role: a.Role, Model: a.Model, Repo: a.Repo, ParentJob: a.Parent}
		if err := s.Jobs.Create(j); err != nil {
			return nil, err
		}
		s.kickDispatch()
		return proto.JobCreateResponse{ID: j.ID}, nil
	})
	srv.Handle("job.list", func(_ string, _ json.RawMessage) (any, error) {
		jobs, err := s.Jobs.List()
		if err != nil {
			return nil, err
		}
		out := make([]queue.Job, 0, len(jobs))
		for _, j := range jobs {
			out = append(out, *j)
		}
		return out, nil
	})
	srv.Handle("job.show", func(_ string, args json.RawMessage) (any, error) {
		var a proto.JobIDArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		return s.Jobs.Get(a.ID)
	})
}
