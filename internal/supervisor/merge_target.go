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
// no-review role has completed. The state remains merging until every
// repository action and worktree cleanup has succeeded.
func (s *Supervisor) finishTopLevelJob(j *queue.Job, result string) error {
	if err := s.Jobs.Transition(j.ID, queue.StateMerging); err != nil {
		return err
	}
	if err := s.applyMergeTarget(j); err != nil {
		return err
	}
	if err := s.Jobs.SetResult(j.ID, result); err != nil {
		return err
	}
	if err := s.Jobs.Transition(j.ID, queue.StateDone); err != nil {
		return err
	}
	s.Jobs.ResetReviewState(j.ID)
	db.AppendEvent(s.DB, "job_merged", j.Assignee, j.ID, j.Branch)
	s.kickDispatch()
	return nil
}

// completeMergingJob applies a policy after a reviewer has moved a job into
// merging. Child developer jobs keep their unconditional integration merge;
// only top-level branched jobs use the configured policy.
func (s *Supervisor) completeMergingJob(j *queue.Job, notes string) error {
	if j.Role != "product_manager" && j.ParentJob != 0 {
		return s.mergeAutomergeChild(j, notes)
	}
	if err := s.applyMergeTarget(j); err != nil {
		return err
	}
	if err := s.Jobs.Transition(j.ID, queue.StateDone); err != nil {
		return err
	}
	if err := s.Jobs.SetResult(j.ID, notes); err != nil {
		return err
	}
	s.Jobs.ResetReviewState(j.ID)
	db.AppendEvent(s.DB, "job_merged", j.Assignee, j.ID, j.Branch)
	s.kickDispatch()
	return nil
}

func (s *Supervisor) mergeAutomergeChild(j *queue.Job, notes string) error {
	repoPath, ok := s.Config().RepoPath(j.Repo)
	if !ok {
		return fmt.Errorf("job %d: unknown repo %q", j.ID, j.Repo)
	}
	if err := s.Git.MergeBranch(repoPath, j.Branch); err != nil {
		_ = s.Jobs.Transition(j.ID, queue.StateReview)
		return fmt.Errorf("merge conflict for job %d: in your worktree, merge the target branch into %s, resolve, commit, then retry the verdict. Details: %v", j.ID, j.Branch, err)
	}
	if err := s.Git.RemoveWorktree(repoPath, j.Worktree, j.Branch); err != nil {
		return err
	}
	if err := s.Jobs.Transition(j.ID, queue.StateDone); err != nil {
		return err
	}
	if err := s.Jobs.SetResult(j.ID, notes); err != nil {
		return err
	}
	s.Jobs.ResetReviewState(j.ID)
	db.AppendEvent(s.DB, "job_merged", j.Assignee, j.ID, j.Branch)
	s.kickDispatch()
	return nil
}

func (s *Supervisor) applyMergeTarget(j *queue.Job) error {
	if j.Role == "product_manager" {
		return s.applyPMMergeTarget(j)
	}
	if j.ParentJob != 0 || j.Repo == "" || j.Branch == "" {
		return nil
	}
	target := s.Config().EffectiveMergeTarget(j.Repo)
	branch := queue.IntegrationBranch{Branch: j.Branch, Worktree: j.Worktree}
	return s.applyRepositoryPolicy(j, j.Repo, branch, target)
}

func (s *Supervisor) applyPMMergeTarget(j *queue.Job) error {
	keys := make([]string, 0, len(j.IntegrationBranches))
	for repo := range j.IntegrationBranches {
		keys = append(keys, repo)
	}
	sort.Strings(keys)
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
				branch.Base, _ = s.Git.CurrentBranch(repoPath)
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
	for _, repo := range keys {
		branch := j.IntegrationBranches[repo]
		if branch.Worktree == "" {
			continue
		}
		repoPath, _ := s.Config().RepoPath(repo)
		var err error
		if s.Config().EffectiveMergeTarget(repo) == config.MergeTargetAsIs {
			err = s.Git.RemoveWorktreeKeepBranch(repoPath, branch.Worktree, branch.Branch)
		} else {
			err = s.Git.RemoveWorktree(repoPath, branch.Worktree, branch.Branch)
		}
		if err != nil {
			return s.policyFailure(j, fmt.Errorf("repository %q: remove integration worktree: %w", repo, err))
		}
	}
	for _, repo := range keys {
		if s.Config().EffectiveMergeTarget(repo) == config.MergeTargetAsIs {
			if err := s.sendPullRequestMail(j, repo, j.IntegrationBranches[repo]); err != nil {
				return err
			}
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
			branch.Base, _ = s.Git.CurrentBranch(repoPath)
		}
		if branch.Worktree != "" {
			if err := s.Git.RemoveWorktreeKeepBranch(repoPath, branch.Worktree, branch.Branch); err != nil {
				return s.policyFailure(j, fmt.Errorf("repository %q: remove integration worktree: %w", repo, err))
			}
		}
		return s.sendPullRequestMail(j, repo, branch)
	}
	if err := s.Git.MergeBranch(repoPath, branch.Branch); err != nil {
		if errors.Is(err, gitops.ErrMergeConflict) {
			return s.policyFailure(j, fmt.Errorf("merge conflict for repository %q: %w", repo, err))
		}
		return s.policyFailure(j, fmt.Errorf("merge repository %q: %w", repo, err))
	}
	if branch.Worktree != "" {
		if err := s.Git.RemoveWorktree(repoPath, branch.Worktree, branch.Branch); err != nil {
			return s.policyFailure(j, fmt.Errorf("repository %q: remove integration worktree: %w", repo, err))
		}
	}
	return nil
}

func (s *Supervisor) policyFailure(j *queue.Job, err error) error {
	_ = s.Jobs.SetNote(j.ID, err.Error())
	_ = s.Jobs.Transition(j.ID, queue.StateWorking)
	return err
}

func (s *Supervisor) sendPullRequestMail(j *queue.Job, repo string, branch queue.IntegrationBranch) error {
	ceo, ok := s.Mail.Dir.CEO()
	if !ok {
		return s.policyFailure(j, errors.New("cannot notify CEO about pull request: no living CEO"))
	}
	body := fmt.Sprintf("Job #%d (%s) left repository %s on branch %s (base %s).\nOpen a pull request from %s into %s.",
		j.ID, j.Title, repo, branch.Branch, branch.Base, branch.Branch, branch.Base)
	for _, recipient := range []string{"user", ceo} {
		if _, err := s.Mail.Send(bus.SystemSender, recipient, fmt.Sprintf("pull request required: job #%d", j.ID), body, bus.PrioHigh); err != nil {
			return s.policyFailure(j, fmt.Errorf("notify %s: %w", recipient, err))
		}
	}
	return nil
}
