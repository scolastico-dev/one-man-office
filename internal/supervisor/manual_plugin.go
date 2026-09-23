package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

// PluginPermissionError is kept as the supervisor-facing name for the shared
// plugin-manager authorization error.
type PluginPermissionError = plugins.ManualPermissionError

func (s *Supervisor) registerPluginVerbs(srv *sockd.Server) {
	srv.Handle("plugin.trigger", func(caller string, raw json.RawMessage) (any, error) {
		var args proto.PluginTriggerArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		if args.Async {
			requestID, err := s.TriggerPluginAsync(caller, args.Name, args.Action, args.Args)
			if err != nil {
				return nil, err
			}
			return proto.PluginTriggerResponse{RequestID: requestID}, nil
		}
		return s.TriggerPluginResult(caller, args.Name, args.Action, args.Args)
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

// TriggerPluginAsync shares caller authorization and plugin admission with the
// synchronous trigger path, but returns after the request audit is durable.
func (s *Supervisor) TriggerPluginAsync(caller, name, action string, args []string) (int64, error) {
	if s.Plugins == nil {
		return 0, fmt.Errorf("no plugins are loaded")
	}
	role, contextData, err := s.pluginCallerContext(caller, args)
	if err != nil {
		return 0, err
	}
	return s.Plugins.TriggerManualContextWithRoleAndDataAsyncResult(context.Background(), name, action, caller, role, args, contextData, func(result plugins.ManualTriggerResult, hookErr error) {
		if hookErr != nil {
			return
		}
		if err := s.recordManualPullRequests(contextData, result, name, action); err != nil {
			_ = db.AppendEvent(s.DB, "plugin_manual_record_failed", caller, 0, err.Error())
		}
	})
}

// TriggerPluginResult is the shared authorization boundary for socket and
// TUI/browser-forwarded runs. Callers that do not expose hook values may use
// TriggerPlugin, which intentionally discards the result.
func (s *Supervisor) TriggerPluginResult(caller, name, action string, args []string) (proto.PluginTriggerResponse, error) {
	var response proto.PluginTriggerResponse
	if s.Plugins == nil {
		return response, fmt.Errorf("no plugins are loaded")
	}
	role, contextData, err := s.pluginCallerContext(caller, args)
	if err != nil {
		return response, err
	}
	result, err := s.Plugins.TriggerManualContextWithRoleAndDataResult(context.Background(), name, action, caller, role, args, contextData)
	response.RequestID = result.RequestID
	response.Result = result.Value
	if err == nil {
		err = s.recordManualPullRequests(contextData, result, name, action)
	}
	return response, err
}

func (s *Supervisor) pluginCallerContext(caller string, args []string) (string, map[string]any, error) {
	if caller == "user" {
		if len(args) == 0 {
			return "user", nil, nil
		}
		repo, ok := explicitPluginRepo(args)
		if !ok {
			return "user", nil, nil
		}
		jobs, err := s.Jobs.List(queue.StateQueued, queue.StateAssigned, queue.StateWorking, queue.StateReview, queue.StateMerging, queue.StateRework)
		if err != nil {
			return "", nil, fmt.Errorf("find plugin job context: %w", err)
		}
		var match *queue.Job
		for _, job := range jobs {
			if job.Role == "product_manager" {
				if integration, exists := job.IntegrationBranches[repo]; !exists || s.Config().EffectiveMergeTarget(repo) != config.MergeTargetAsIs || integration.Branch == "" {
					continue
				}
			} else if job.Repo != repo || s.Config().EffectiveMergeTarget(repo) != config.MergeTargetAsIs {
				continue
			}
			if match != nil {
				return "user", nil, nil
			}
			match = job
		}
		if match == nil {
			return "user", nil, nil
		}
		contextData, err := s.jobPluginContext(&db.Agent{JobID: match.ID})
		if err != nil {
			return "", nil, err
		}
		return "user", contextData, nil
	}
	agent, err := db.GetAgent(s.DB, caller)
	if err != nil {
		return "", nil, fmt.Errorf("unknown authenticated plugin caller %q: %w", caller, err)
	}
	contextData := map[string]any(nil)
	if agent.JobID != 0 {
		contextData, err = s.jobPluginContext(agent)
		if err != nil {
			return "", nil, err
		}
	}
	return agent.Role, contextData, nil
}

func explicitPluginRepo(args []string) (string, bool) {
	var repo string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "repo=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(arg, "repo="))
		if value == "" || repo != "" {
			return "", false
		}
		repo = value
	}
	return repo, repo != ""
}

type manualPullRequest struct {
	Repo  string
	URL   string
	State string
}

func (s *Supervisor) recordManualPullRequests(contextData map[string]any, result plugins.ManualTriggerResult, plugin, action string) error {
	authorized := trustedManualRepos(contextData)
	if len(authorized) == 0 {
		return nil
	}
	value := result.Value
	if result.InternalValue != nil {
		value = result.InternalValue
	}
	for _, pullRequest := range normalizeManualPullRequests(value) {
		jobID, ok := authorized[pullRequest.Repo]
		if !ok {
			continue
		}
		if err := db.UpsertJobPullRequest(s.DB, db.JobPullRequest{
			JobID: jobID, Repo: pullRequest.Repo, URL: pullRequest.URL, State: pullRequest.State, Plugin: plugin, Action: action,
		}); err != nil {
			return fmt.Errorf("record pull request for job %d repository %q: %w", jobID, pullRequest.Repo, err)
		}
	}
	return nil
}

func trustedManualRepos(contextData map[string]any) map[string]int64 {
	authorized := map[string]int64{}
	if contextData == nil {
		return authorized
	}
	jobID, ok := manualJobID(contextData["job_id"])
	if !ok || jobID == 0 {
		return authorized
	}
	if entries, exists := contextData["integration_branches"]; exists {
		if list, ok := entries.([]map[string]any); ok {
			for _, entry := range list {
				if repo, ok := entry["repo"].(string); ok && strings.TrimSpace(repo) != "" {
					authorized[strings.TrimSpace(repo)] = jobID
				}
			}
		}
		return authorized
	}
	if repo, ok := contextData["repo"].(string); ok && strings.TrimSpace(repo) != "" {
		authorized[strings.TrimSpace(repo)] = jobID
	}
	return authorized
}

func manualJobID(value any) (int64, bool) {
	switch value := value.(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case float64:
		return int64(value), value == float64(int64(value))
	default:
		return 0, false
	}
}

func normalizeManualPullRequests(value any) []manualPullRequest {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil
	}
	var objects []any
	switch normalized := normalized.(type) {
	case map[string]any:
		objects = []any{normalized}
	case []any:
		objects = normalized
	default:
		return nil
	}
	var records []manualPullRequest
	for _, object := range objects {
		fields, ok := object.(map[string]any)
		if !ok {
			continue
		}
		repo, repoOK := fields["repo"].(string)
		url, urlOK := fields["url"].(string)
		if !repoOK || !urlOK {
			continue
		}
		repo = strings.TrimSpace(repo)
		url = strings.TrimSpace(url)
		if repo == "" || !isHTTPURL(url) {
			continue
		}
		state, _ := fields["state"].(string)
		records = append(records, manualPullRequest{Repo: repo, URL: url, State: state})
	}
	return records
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
	if job.Role == "product_manager" {
		repos := make([]string, 0, len(job.IntegrationBranches))
		for repo := range job.IntegrationBranches {
			if s.Config().EffectiveMergeTarget(repo) != config.MergeTargetAsIs {
				continue
			}
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
		// exposing the deterministic as-is list for multi-repository hooks.
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
