package supervisor

import (
	"errors"
	"fmt"
	"sort"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

// finishTopLevelJob applies the configured repository policy after a
// no-review role has completed. The state remains merging until policy/Git
// actions finish; finalization then establishes the done/cleanup/event order.
func (s *Supervisor) finishTopLevelJob(j *queue.Job, result string) error {
	if err := s.Jobs.Transition(j.ID, queue.StateMerging); err != nil {
		return err
	}
	return s.completeMergingJob(j, result)
}

// completeMergingJob is the single completion path for reviewed and
// no-review jobs. Policy actions happen before done; cleanup and the merged
// event happen after done.
func (s *Supervisor) completeMergingJob(j *queue.Job, notes string) error {
	if err := s.applyMergeTarget(j); err != nil {
		return err
	}
	if err := s.Jobs.SetResult(j.ID, notes); err != nil {
		return err
	}
	if err := s.Jobs.Transition(j.ID, queue.StateDone); err != nil {
		return err
	}
	s.Jobs.ResetReviewState(j.ID)

	// A reviewed developer is no longer needed once the job is durably done.
	// PM completion marks its agent done here; the verb schedules reaping after
	// this function returns.
	if j.Role == "product_manager" && j.Assignee != "" {
		if err := db.SetAgentState(s.DB, j.Assignee, "done"); err != nil {
			return err
		}
	} else if j.Assignee != "" && j.Role != "freelancer" {
		s.WakeAgent(j.Assignee)
		if err := s.KillAgent(j.Assignee, true); err != nil {
			return err
		}
	}

	if err := s.cleanupMergeTarget(j); err != nil {
		return err
	}
	if err := s.sendAsIsMails(j); err != nil {
		return err
	}
	if err := db.AppendEvent(s.DB, "job_merged", j.Assignee, j.ID, j.Branch); err != nil {
		return err
	}
	s.kickDispatch()
	return nil
}

func (s *Supervisor) applyMergeTarget(j *queue.Job) error {
	if j.Role == "product_manager" {
		return s.applyPMMergeTarget(j)
	}
	if j.Repo == "" || j.Branch == "" {
		return nil
	}
	if j.ParentJob != 0 {
		repoPath, ok := s.Config().RepoPath(j.Repo)
		if !ok {
			return s.policyFailure(j, fmt.Errorf("job %d: unknown repo %q", j.ID, j.Repo))
		}
		target, err := s.mergeTargetForChild(j, repoPath)
		if err != nil {
			return s.policyFailure(j, err)
		}
		if err := s.Git.MergeBranchInto(repoPath, target, j.Branch); err != nil {
			return s.policyFailure(j, fmt.Errorf("merge conflict for repository %q: resolve the conflict, commit the result, then retry the verdict: %w", j.Repo, err))
		}
		return nil
	}
	target := s.Config().EffectiveMergeTarget(j.Repo)
	branch := queue.IntegrationBranch{Branch: j.Branch, Worktree: j.Worktree}
	return s.applyRepositoryPolicy(j, j.Repo, branch, target)
}

// mergeTargetForChild returns the PM integration worktree when Task 1 has
// populated the parent's durable IntegrationBranches map. The checkout
// fallback keeps older offices and pre-integration test fixtures compatible;
// it is never selected once the parent integration target is known.
func (s *Supervisor) mergeTargetForChild(j *queue.Job, repoPath string) (string, error) {
	parent, err := s.Jobs.Get(j.ParentJob)
	if err != nil {
		return "", fmt.Errorf("job %d: load parent PM job %d: %w", j.ID, j.ParentJob, err)
	}
	if parent.Role != "product_manager" {
		return "", fmt.Errorf("job %d parent %d is not a product_manager job", j.ID, j.ParentJob)
	}
	if integration, ok := parent.IntegrationBranches[j.Repo]; ok && integration.Worktree != "" {
		return integration.Worktree, nil
	}
	return repoPath, nil
}

func (s *Supervisor) applyPMMergeTarget(j *queue.Job) error {
	keys := sortedIntegrationRepos(j.IntegrationBranches)
	// Complete every checkout mutation before touching any integration
	// worktree. A conflict therefore leaves the complete PM integration set
	// available for retry.
	for _, repo := range keys {
		branch := j.IntegrationBranches[repo]
		target := s.Config().EffectiveMergeTarget(repo)
		if target == config.MergeTargetAsIs {
			repoPath, ok := s.Config().RepoPath(repo)
			if !ok {
				return s.policyFailure(j, fmt.Errorf("job %d: unknown repo %q", j.ID, repo))
			}
			if branch.Base == "" {
				base, err := s.Git.CurrentBranch(repoPath)
				if err != nil {
					return s.policyFailure(j, fmt.Errorf("repository %q: determine pull request base: %w", repo, err))
				}
				branch.Base = base
				j.IntegrationBranches[repo] = branch
			}
			continue
		}
		repoPath, ok := s.Config().RepoPath(repo)
		if !ok {
			return s.policyFailure(j, fmt.Errorf("job %d: unknown repo %q", j.ID, repo))
		}
		if err := s.Git.MergeBranch(repoPath, branch.Branch); err != nil {
			return s.policyFailure(j, fmt.Errorf("merge conflict for repository %q: %w", repo, err))
		}
	}
	return nil
}

func (s *Supervisor) applyRepositoryPolicy(j *queue.Job, repo string, branch queue.IntegrationBranch, target string) error {
	repoPath, ok := s.Config().RepoPath(repo)
	if !ok {
		return s.policyFailure(j, fmt.Errorf("job %d: unknown repo %q", j.ID, repo))
	}
	if target == config.MergeTargetAsIs {
		if branch.Base == "" {
			base, err := s.Git.CurrentBranch(repoPath)
			if err != nil {
				return s.policyFailure(j, fmt.Errorf("repository %q: determine pull request base: %w", repo, err))
			}
			branch.Base = base
		}
		// Keep the resolved base available to finalization without adding a
		// second persistence format for top-level jobs.
		j.IntegrationBranches = map[string]queue.IntegrationBranch{repo: branch}
		return nil
	}
	if err := s.Git.MergeBranch(repoPath, branch.Branch); err != nil {
		if errors.Is(err, gitops.ErrMergeConflict) {
			return s.policyFailure(j, fmt.Errorf("merge conflict for repository %q: resolve the conflict, commit the result, then retry the verdict: %w", repo, err))
		}
		return s.policyFailure(j, fmt.Errorf("merge repository %q: %w", repo, err))
	}
	return nil
}

func (s *Supervisor) cleanupMergeTarget(j *queue.Job) error {
	if j.Role == "product_manager" {
		for _, repo := range sortedIntegrationRepos(j.IntegrationBranches) {
			branch := j.IntegrationBranches[repo]
			if err := s.cleanupRepositoryBranch(repo, branch, s.Config().EffectiveMergeTarget(repo)); err != nil {
				return err
			}
		}
		return nil
	}
	if j.Repo == "" || j.Branch == "" {
		return nil
	}
	branch := j.IntegrationBranches[j.Repo]
	if branch.Branch == "" {
		branch = queue.IntegrationBranch{Branch: j.Branch, Worktree: j.Worktree}
	}
	if j.ParentJob != 0 {
		return s.cleanupRepositoryBranch(j.Repo, branch, config.MergeTargetAutoMerge)
	}
	return s.cleanupRepositoryBranch(j.Repo, branch, s.Config().EffectiveMergeTarget(j.Repo))
}

func (s *Supervisor) cleanupRepositoryBranch(repo string, branch queue.IntegrationBranch, target string) error {
	if branch.Worktree == "" {
		return nil
	}
	repoPath, ok := s.Config().RepoPath(repo)
	if !ok {
		return fmt.Errorf("repository %q: unknown repository", repo)
	}
	var err error
	if target == config.MergeTargetAsIs {
		err = s.Git.RemoveWorktreeKeepBranch(repoPath, branch.Worktree, branch.Branch)
	} else {
		err = s.Git.RemoveWorktree(repoPath, branch.Worktree, branch.Branch)
	}
	if err != nil {
		return fmt.Errorf("repository %q: remove integration worktree: %w", repo, err)
	}
	return nil
}

func (s *Supervisor) sendAsIsMails(j *queue.Job) error {
	for _, repo := range sortedIntegrationRepos(j.IntegrationBranches) {
		if s.Config().EffectiveMergeTarget(repo) != config.MergeTargetAsIs {
			continue
		}
		if err := s.sendPullRequestMail(j, repo, j.IntegrationBranches[repo]); err != nil {
			return err
		}
	}
	return nil
}

func sortedIntegrationRepos(branches map[string]queue.IntegrationBranch) []string {
	keys := make([]string, 0, len(branches))
	for repo := range branches {
		keys = append(keys, repo)
	}
	sort.Strings(keys)
	return keys
}

func (s *Supervisor) policyFailure(j *queue.Job, err error) error {
	_ = s.Jobs.SetNote(j.ID, err.Error())
	state := queue.StateWorking
	// A reviewed top-level developer remains in review so its reviewer can
	// resolve the conflict and retry the same verdict. PM and no-review roles
	// retain their recovery-to-working behavior.
	if j.Role == "developer" {
		state = queue.StateReview
	}
	_ = s.Jobs.Transition(j.ID, state)
	return err
}

func (s *Supervisor) sendPullRequestMail(j *queue.Job, repo string, branch queue.IntegrationBranch) error {
	ceo, ok := s.Mail.Dir.CEO()
	if !ok {
		return errors.New("cannot notify CEO about pull request: no living CEO")
	}
	body := fmt.Sprintf("Job #%d (%s) left repository %s on branch %s (base %s).\nOpen a pull request from %s into %s.",
		j.ID, j.Title, repo, branch.Branch, branch.Base, branch.Branch, branch.Base)
	for _, recipient := range []string{"user", ceo} {
		if _, err := s.Mail.Send(bus.SystemSender, recipient, fmt.Sprintf("pull request required: job #%d", j.ID), body, bus.PrioHigh); err != nil {
			return fmt.Errorf("notify %s: %w", recipient, err)
		}
	}
	return nil
}
