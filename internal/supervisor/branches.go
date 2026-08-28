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

func branchNamingPrompt(brief, prefix string) string {
	return fmt.Sprintf(`You are a one-shot branch naming agent. Do not implement the task.

Choose a concise lowercase branch suffix that reflects the task and follows
Conventional Commits intent. It must use this form:

  <type>-<short-kebab-description>

Allowed types: feat, fix, docs, refactor, test, chore, perf, build, ci, revert,
or style. Return it by running exactly one command:

  omo branch-name <suffix>

The configured prefix %q is added automatically. Do not include that prefix,
spaces, slashes, quotes, commentary, or any other command.

TASK BRIEF:
%s`, prefix, brief)
}

func (s *Supervisor) branchNameForJob(j *queue.Job) (string, error) {
	cfg := s.Config()
	if cfg.Branches.Naming == "generated" {
		return fmt.Sprintf("%s%d", cfg.Branches.Prefix, j.ID), nil
	}
	profileKey := j.Model
	var err error
	if profileKey == "" {
		profileKey, err = s.roleProfile(j.Role, j.Retries)
	} else {
		profileKey, _, err = cfg.ProfileForJob(j.Role, profileKey)
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
	_, err = s.spawnAttempt("branch_namer", profileKey, j.ID, s.OfficeDir, brief, 0, false, j.ForceModel)
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
		if got.err != nil {
			s.stopBranchNamers(j.ID)
			return "", got.err
		}
		_ = db.SetAgentState(s.DB, got.agent, "done")
		_ = s.KillAgent(got.agent, false)
		return cfg.Branches.Prefix + got.name, nil
	case <-timer.C:
		s.stopBranchNamers(j.ID)
		return "", fmt.Errorf("branch naming agent timed out after %s", timeout)
	}
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
		if !validBranchSuffix(args.Name) {
			return nil, fmt.Errorf("branch suffix must be <conventional-type>-<short-kebab-description>")
		}
		s.mu.Lock()
		waiter := s.branchNameWaiters[agent.JobID]
		s.mu.Unlock()
		if waiter == nil {
			return nil, fmt.Errorf("branch naming request is no longer active")
		}
		select {
		case waiter <- branchNameResult{agent: agentID, name: args.Name}:
			_ = db.AppendEvent(s.DB, "branch_name_generated", agentID, agent.JobID, args.Name)
			return nil, nil
		default:
			return nil, fmt.Errorf("a branch name was already returned")
		}
	})
}

func (s *Supervisor) stopBranchNamers(jobID int64) {
	agents, _ := db.LivingByJobRole(s.DB, jobID, "branch_namer")
	for _, agent := range agents {
		_ = db.SetAgentState(s.DB, agent.Name, "dead")
		_ = s.KillAgent(agent.Name, false)
	}
}

func validBranchSuffix(name string) bool {
	if len(name) < 3 || len(name) > 63 || strings.ToLower(name) != name || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return false
	}
	typeName, description, ok := strings.Cut(name, "-")
	if !ok || description == "" {
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
	s.mu.Lock()
	waiter := s.branchNameWaiters[jobID]
	s.mu.Unlock()
	if waiter == nil {
		return
	}
	select {
	case waiter <- branchNameResult{err: err}:
	default:
	}
}
