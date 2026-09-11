package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

// PluginPermissionError reports a manual plugin action denied to an
// authenticated caller by the action's allowed roles.
type PluginPermissionError struct {
	Caller string
	Role   string
	Plugin string
	Action string
}

func (e *PluginPermissionError) Error() string {
	return fmt.Sprintf("agent %q with role %q may not trigger plugin %q action %q", e.Caller, e.Role, e.Plugin, e.Action)
}

func (s *Supervisor) registerPluginVerbs(srv *sockd.Server) {
	srv.Handle("plugin.trigger", func(caller string, raw json.RawMessage) (any, error) {
		var args proto.PluginTriggerArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		result, err := s.TriggerPluginResult(caller, args.Name, args.Action, args.Args)
		if err != nil {
			return nil, err
		}
		return proto.PluginTriggerResponse{Result: result}, nil
	})
	srv.Handle("plugin.actions", func(caller string, raw json.RawMessage) (any, error) {
		if caller != "user" {
			return nil, fmt.Errorf("only the user may list manual plugin actions")
		}
		var args proto.AgentNameArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		return s.Plugins.ManualActions(args.Name), nil
	})
}

// TriggerPlugin is the shared authorization boundary for socket and TUI runs.
func (s *Supervisor) TriggerPlugin(caller, name, action string, args []string) error {
	_, err := s.TriggerPluginResult(caller, name, action, args)
	return err
}

func (s *Supervisor) TriggerPluginResult(caller, name, action string, args []string) (string, error) {
	if s.Plugins == nil {
		return "", fmt.Errorf("no plugins are loaded")
	}
	role := "user"
	contextData := map[string]any(nil)
	if caller != "user" {
		agent, err := db.GetAgent(s.DB, caller)
		if err != nil {
			return "", fmt.Errorf("unknown authenticated plugin caller %q: %w", caller, err)
		}
		role = agent.Role
		if agent.JobID != 0 {
			contextData, err = s.jobPluginContext(agent)
			if err != nil {
				return "", err
			}
		}
	}
	for _, candidate := range s.Plugins.ManualActions(name) {
		if candidate.Name != action {
			continue
		}
		allowed := false
		for _, allowedRole := range candidate.Roles {
			if allowedRole == role {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", &PluginPermissionError{Caller: caller, Role: role, Plugin: name, Action: action}
		}
		break
	}
	return s.Plugins.TriggerManualContextWithRoleAndDataResult(context.Background(), name, action, caller, role, args, contextData)
}

// jobPluginContext is the supervisor boundary for job metadata. It derives
// the base branch from trusted repository state when the integration job has
// not already supplied one; callers never get metadata invented from their
// plugin arguments or prompt text.
func (s *Supervisor) jobPluginContext(agent *db.Agent) (map[string]any, error) {
	job, err := s.Jobs.Get(agent.JobID)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"job_id":      job.ID,
		"repo":        job.Repo,
		"branch":      job.Branch,
		"base_branch": "",
		"worktree":    job.Worktree,
	}
	if job.Role == "product_manager" && len(job.IntegrationBranches) > 0 {
		repos := make([]string, 0, len(job.IntegrationBranches))
		for repo := range job.IntegrationBranches {
			repos = append(repos, repo)
		}
		sort.Strings(repos)
		entries := make([]map[string]any, 0, len(repos))
		for _, repo := range repos {
			integration := job.IntegrationBranches[repo]
			entries = append(entries, map[string]any{
				"repo":        repo,
				"branch":      integration.Branch,
				"base_branch": integration.Base,
				"worktree":    integration.Worktree,
			})
		}
		data["integration_branches"] = entries
		// Preserve the single-repository shape for existing PM hooks while
		// exposing the deterministic full list for multi-repository hooks.
		if len(entries) == 1 {
			entry := entries[0]
			data["repo"] = entry["repo"]
			data["branch"] = entry["branch"]
			data["base_branch"] = entry["base_branch"]
			data["worktree"] = entry["worktree"]
		}
		return data, nil
	}
	if job.Repo == "" {
		return data, nil
	}
	if job.ParentJob != 0 {
		parent, err := s.Jobs.Get(job.ParentJob)
		if err != nil {
			return nil, fmt.Errorf("job %d: load parent PM job %d: %w", job.ID, job.ParentJob, err)
		}
		if integration, ok := parent.IntegrationBranches[job.Repo]; ok {
			if integration.Branch == "" {
				return nil, fmt.Errorf("job %d: parent PM job %d has no integration branch for repository %q", job.ID, job.ParentJob, job.Repo)
			}
			data["base_branch"] = integration.Branch
		} else {
			return nil, fmt.Errorf("job %d: parent PM job %d has no integration branch for repository %q", job.ID, job.ParentJob, job.Repo)
		}
		return data, nil
	}
	if integration, ok := job.IntegrationBranches[job.Repo]; ok && integration.Base != "" {
		data["base_branch"] = integration.Base
		return data, nil
	}
	repoPath, ok := s.Config().RepoPath(job.Repo)
	if !ok {
		return nil, fmt.Errorf("job %d: unknown repo %q", job.ID, job.Repo)
	}
	base, err := s.Git.CurrentBranch(repoPath)
	if err != nil {
		return nil, fmt.Errorf("job %d: determine base branch: %w", job.ID, err)
	}
	data["base_branch"] = base
	return data, nil
}
