package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

// CleanupTerminalWorktrees reconciles physical worktrees left by terminal
// jobs. It is safe to call repeatedly and deliberately leaves active and
// failed jobs alone so their changes remain available for recovery.
func (s *Supervisor) CleanupTerminalWorktrees() error {
	jobs, err := s.Jobs.List(queue.StateDone, queue.StateCancelled)
	if err != nil {
		return err
	}
	var cleanupErrors []error
	for _, j := range jobs {
		if err := s.cleanupTerminalWorktree(j.ID); err != nil {
			db.AppendEvent(s.DB, "cleanup_error", "", j.ID, err.Error())
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (s *Supervisor) cleanupTerminalWorktree(jobID int64) error {
	j, err := s.Jobs.Get(jobID)
	if err != nil {
		return err
	}
	if j.Worktree == "" || (j.State != queue.StateDone && j.State != queue.StateCancelled) {
		return nil
	}
	living, err := db.LivingByJob(s.DB, jobID)
	if err != nil {
		return err
	}
	if len(living) != 0 {
		return nil
	}
	if err := s.validateManagedWorktree(j.Worktree); err != nil {
		return fmt.Errorf("job %d: %w", jobID, err)
	}
	if _, err := os.Stat(j.Worktree); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("job %d: inspect worktree: %w", jobID, err)
		}
		if j.State == queue.StateDone {
			return nil
		}
	}
	repoPath, ok := s.Config().Repos[j.Repo]
	if !ok {
		return fmt.Errorf("job %d: unknown repo %q", jobID, j.Repo)
	}
	if err := s.Git.RemoveWorktree(repoPath, j.Worktree, j.Branch); err != nil {
		return fmt.Errorf("job %d: remove worktree: %w", jobID, err)
	}
	if j.State == queue.StateCancelled {
		if err := s.Jobs.SetWorktree(j.ID, "", ""); err != nil {
			return err
		}
	}
	db.AppendEvent(s.DB, "worktree_cleaned", "", jobID, j.Branch)
	return nil
}

func (s *Supervisor) validateManagedWorktree(path string) error {
	root, err := filepath.Abs(filepath.Join(s.OfficeDir, ".omo", "worktrees"))
	if err != nil {
		return fmt.Errorf("resolve worktree root: %w", err)
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve worktree: %w", err)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove unmanaged worktree %q", path)
	}
	return nil
}
