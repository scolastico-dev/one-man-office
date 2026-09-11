package supervisor

import (
	"fmt"
	"path/filepath"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

// ensurePMIntegrationWorktree lazily creates the PM's integration branch for
// one repository. The supervisor mutex closes the check/create race for
// repeated child creation, while Git serializes competing PMs on a repo.
func (s *Supervisor) ensurePMIntegrationWorktree(pmJobID int64, repoKey string) (queue.IntegrationBranch, error) {
	s.integrationMu.Lock()
	defer s.integrationMu.Unlock()

	pm, err := s.Jobs.Get(pmJobID)
	if err != nil {
		return queue.IntegrationBranch{}, fmt.Errorf("load PM job %d: %w", pmJobID, err)
	}
	if pm.Role != "product_manager" {
		return queue.IntegrationBranch{}, fmt.Errorf("job %d is %s, not a product_manager job", pmJobID, pm.Role)
	}
	if existing, ok := pm.IntegrationBranches[repoKey]; ok {
		return existing, nil
	}
	repoPath, ok := s.Config().Repos[repoKey]
	if !ok {
		return queue.IntegrationBranch{}, fmt.Errorf("PM job %d references unknown repo %q", pmJobID, repoKey)
	}
	base, err := s.Git.CurrentBranch(repoPath)
	if err != nil {
		return queue.IntegrationBranch{}, fmt.Errorf("PM job %d: determine base branch for repo %q: %w", pmJobID, repoKey, err)
	}
	if base == "" {
		return queue.IntegrationBranch{}, fmt.Errorf("PM job %d: repo %q is in detached HEAD state", pmJobID, repoKey)
	}
	branch, err := s.pmIntegrationBranchName(pm, repoKey)
	if err != nil {
		return queue.IntegrationBranch{}, err
	}
	worktree := filepath.Join(s.OfficeDir, ".omo", "worktrees", fmt.Sprintf("%s-pm-%d", repoKey, pmJobID))
	if err := s.Git.AddWorktreeFromBase(repoPath, worktree, branch, base); err != nil {
		return queue.IntegrationBranch{}, fmt.Errorf("PM job %d: create integration worktree: %w", pmJobID, err)
	}
	entry := queue.IntegrationBranch{Branch: branch, Base: base, Worktree: worktree}
	if err := s.Jobs.SetIntegrationBranch(pmJobID, repoKey, entry); err != nil {
		_ = s.Git.RemoveWorktree(repoPath, worktree, branch)
		return queue.IntegrationBranch{}, fmt.Errorf("PM job %d: persist integration worktree: %w", pmJobID, err)
	}
	db.AppendEvent(s.DB, "integration_worktree_created", "", pmJobID, repoKey+": "+branch)
	return entry, nil
}

func (s *Supervisor) pmIntegrationBranchName(pm *queue.Job, repoKey string) (string, error) {
	prefix := fmt.Sprintf("%spm-%d", s.Config().Branches.Prefix, pm.ID)
	if s.Config().Branches.Naming == "generated" {
		return prefix, nil
	}
	suffix, err := s.branchNameForJob(pm)
	if err != nil {
		return "", fmt.Errorf("PM job %d: integration branch name: %w", pm.ID, err)
	}
	// Keep the AI-selected, validated developer suffix intact after the
	// deterministic PM prefix. This also makes each PM branch distinguishable
	// even when two PMs receive the same AI description.
	return prefix + "-" + suffix, nil
}

func (s *Supervisor) integrationBranchForJob(j *queue.Job) (queue.IntegrationBranch, error) {
	if j.ParentJob == 0 {
		return queue.IntegrationBranch{}, nil
	}
	parent, err := s.Jobs.Get(j.ParentJob)
	if err != nil {
		return queue.IntegrationBranch{}, fmt.Errorf("load parent PM job %d: %w", j.ParentJob, err)
	}
	if parent.Role != "product_manager" {
		return queue.IntegrationBranch{}, fmt.Errorf("job %d parent %d is not a product_manager job", j.ID, j.ParentJob)
	}
	if entry, ok := parent.IntegrationBranches[j.Repo]; ok {
		return entry, nil
	}
	return s.ensurePMIntegrationWorktree(parent.ID, j.Repo)
}

func (s *Supervisor) mergeTargetForJob(j *queue.Job) (string, error) {
	entry, err := s.integrationBranchForJob(j)
	if err != nil {
		return "", err
	}
	if entry.Worktree != "" {
		return entry.Worktree, nil
	}
	return s.Config().Repos[j.Repo], nil
}

// RecoverIntegrationWorktrees validates and reconnects durable PM worktrees
// after an office restart. It only uses the durable branch name; it never
// creates a replacement branch from the repository checkout.
func (s *Supervisor) RecoverIntegrationWorktrees() error {
	jobs, err := s.Jobs.List()
	if err != nil {
		return err
	}
	for _, pm := range jobs {
		if len(pm.IntegrationBranches) == 0 {
			continue
		}
		for repoKey, entry := range pm.IntegrationBranches {
			if err := s.validateManagedWorktree(entry.Worktree); err != nil {
				return fmt.Errorf("recover PM job %d repo %q: %w", pm.ID, repoKey, err)
			}
			repoPath, ok := s.Config().Repos[repoKey]
			if !ok {
				return fmt.Errorf("recover PM job %d: unknown repo %q", pm.ID, repoKey)
			}
			if err := s.Git.EnsureWorktree(repoPath, entry.Worktree, entry.Branch); err != nil {
				return fmt.Errorf("recover PM job %d repo %q: %w", pm.ID, repoKey, err)
			}
		}
	}
	return nil
}
