package supervisor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

type branchNameResult struct {
	agent string
	name  string
	err   error
}

func (s *Supervisor) branchNameForJob(j *queue.Job) (string, error) {
	cfg := s.Config()
	if cfg.Branches.Naming == "generated" {
		return fmt.Sprintf("%s%d", cfg.Branches.Prefix, j.ID), nil
	}
	var profileKey string
	var err error
	if pending, ok := s.deferredJobSpawn("branch_namer", j.ID); ok {
		// Leave quota waits and saved retry progress to spawnAttempt.
		profileKey = pending.profile
	} else {
		profileKey, err = s.roleProfile("smokealarm", j.Retries)
	}
	if err != nil {
		return "", err
	}
	result := make(chan branchNameResult, 1)
	s.mu.Lock()
	s.branchNameWaiters[j.ID] = result
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.branchNameWaiters, j.ID)
		s.mu.Unlock()
	}()
	brief := fmt.Sprintf("Title: %s\nRole: %s\nRepository: %s\n\n%s", j.Title, j.Role, j.Repo, j.Goal)
	_, err = s.spawnAttempt("branch_namer", profileKey, j.ID, s.OfficeDir, brief, 0, true, false, false)
	if err != nil {
		return "", err
	}
	timeout := s.readyTimeout() * time.Duration(s.maxSpawnRetries()+1)
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case got := <-result:
		return s.finishBranchNameResult(j.ID, got)
	case <-timer.C:
		if !s.cancelBranchNameWaiter(j.ID, result) {
			return s.finishBranchNameResult(j.ID, <-result)
		}
		s.stopBranchNamers(j.ID)
		fallback := fmt.Sprintf("%s%d", cfg.Branches.Prefix, j.ID)
		if err := db.AppendEvent(s.DB, "branch_name_fallback", "", j.ID,
			fmt.Sprintf("%s: branch naming agent timed out after %s", fallback, timeout)); err != nil {
			return "", fmt.Errorf("record branch name fallback: %w", err)
		}
		return fallback, nil
	}
}

func (s *Supervisor) finishBranchNameResult(jobID int64, got branchNameResult) (string, error) {
	if got.err != nil {
		s.stopBranchNamers(jobID)
		return "", got.err
	}
	_ = db.SetAgentState(s.DB, got.agent, "done")
	_ = s.KillAgent(got.agent, false)
	return got.name, nil
}

// deliverBranchNameResult atomically claims an active naming request for its
// result. Deleting the waiter while holding the same lock used by the timeout
// path ensures that only delivery or timeout can win.
func (s *Supervisor) deliverBranchNameResult(jobID int64, result branchNameResult) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiter := s.branchNameWaiters[jobID]
	if waiter == nil {
		return false
	}
	select {
	case waiter <- result:
		delete(s.branchNameWaiters, jobID)
		return true
	default:
		return false
	}
}

func (s *Supervisor) cancelBranchNameWaiter(jobID int64, waiter chan branchNameResult) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.branchNameWaiters[jobID] != waiter {
		return false
	}
	delete(s.branchNameWaiters, jobID)
	return true
}

func (s *Supervisor) registerBranchNameVerb(srv *sockd.Server) {
	srv.Handle("branch.name", func(agentID string, raw json.RawMessage) (any, error) {
		agent, err := db.GetAgent(s.DB, agentID)
		if err != nil {
			return nil, err
		}
		if agent.Role != "branch_namer" {
			return nil, fmt.Errorf("only a branch naming agent may return a branch name")
		}
		var args proto.BranchNameArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		args.Name = strings.TrimSpace(args.Name)
		if !validAIBranchName(args.Name) {
			return nil, fmt.Errorf("branch name must be <conventional-type>/<short-kebab-description>")
		}
		if !s.deliverBranchNameResult(agent.JobID, branchNameResult{agent: agentID, name: args.Name}) {
			return nil, fmt.Errorf("branch naming request is no longer active")
		}
		_ = db.AppendEvent(s.DB, "branch_name_generated", agentID, agent.JobID, args.Name)
		return nil, nil
	})
}

func (s *Supervisor) stopBranchNamers(jobID int64) {
	agents, _ := db.LivingByJobRole(s.DB, jobID, "branch_namer")
	for _, agent := range agents {
		_ = db.SetAgentState(s.DB, agent.Name, "dead")
		_ = s.KillAgent(agent.Name, false)
	}
}

func validAIBranchName(name string) bool {
	if len(name) < 3 || len(name) > 63 || strings.ToLower(name) != name || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return false
	}
	typeName, description, ok := strings.Cut(name, "/")
	if !ok || description == "" || strings.HasPrefix(description, "-") {
		return false
	}
	allowed := map[string]bool{"feat": true, "fix": true, "docs": true, "refactor": true, "test": true, "chore": true, "perf": true, "build": true, "ci": true, "revert": true, "style": true}
	if !allowed[typeName] {
		return false
	}
	for _, r := range description {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func (s *Supervisor) failBranchNaming(jobID int64, err error) {
	s.deliverBranchNameResult(jobID, branchNameResult{err: err})
}
