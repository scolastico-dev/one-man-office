package supervisor

import (
	"fmt"
	"path/filepath"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

// ensurePMIntegrationWorktree creates the durable PM target needed before a
// child developer can be assigned. The target is based on the repository's
// current branch and never requires checking out that branch in the repo.
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
	repoPath, ok := s.Config().RepoPath(repoKey)
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
	branch := fmt.Sprintf("%spm-%d", s.Config().Branches.Prefix, pmJobID)
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
