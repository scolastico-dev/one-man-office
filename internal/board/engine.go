package board

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// The methods in this file are called by the engine after it acted (started
// an agent, merged, exported). They only record the outcome; they run as the
// system actor.

// engineTransition loads ticket n and runs fn in one transaction.
func (b *Board) engineTransition(ctx context.Context, n int64, fn func(tx *sql.Tx, t ticketRow) error) error {
	return b.write(ctx, func(tx *sql.Tx) error {
		t, err := loadTicket(tx, n)
		if err != nil {
			return err
		}
		return fn(tx, t)
	})
}

// checkStage requires t to be in Review at stage.
func checkStage(t ticketRow, stage api.ReviewStage) error {
	if t.Col != api.ColumnReview || t.ReviewStage != stage {
		return api.Errorf(api.CodeInvalidTransition, "#%d is not %s (column %s, review stage %q)", t.ID, stage, t.Col, t.ReviewStage)
	}
	return nil
}

// BeginWork records that the engine started work on a ticket in Ready: it
// moves to In Progress. A task inside a story starts only while its story
// is in progress, and no ticket starts while a dependency still blocks it.
func (b *Board) BeginWork(ctx context.Context, n int64) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if t.Col != api.ColumnReady {
			return api.Errorf(api.CodeInvalidTransition, "#%d is in %s; work begins only on a ticket in ready", n, t.Col)
		}
		if t.inStory() {
			story, err := loadTicket(tx, t.StoryID)
			if err != nil {
				return err
			}
			if story.Col != api.ColumnInProgress {
				return api.Errorf(api.CodeInvalidTransition,
					"#%d belongs to story #%d, which is in %s; its tasks start only while the story is in progress", n, story.ID, story.Col)
			}
		}
		s, err := b.summary(tx, n)
		if err != nil {
			return err
		}
		if len(s.WaitingFor) > 0 {
			w := s.WaitingFor[0]
			return api.Errorf(api.CodeInvalidTransition, "#%d is waiting for #%d (%s)", n, w.Ticket, w.Reason)
		}
		return b.relocate(tx, System, t, api.ColumnInProgress, nil, ActionBeginWork)
	})
}

// CompleteMerge records a successful merge of an approved ticket: it is done
// and completed.
func (b *Board) CompleteMerge(ctx context.Context, n int64) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkStage(t, api.ReviewStageMerging); err != nil {
			return err
		}
		if err := b.setColumn(tx, n, api.ColumnDone, t.Rank, api.ResolutionCompleted); err != nil {
			return err
		}
		return b.finish(tx, System, []move{{id: n, from: api.ColumnReview, to: api.ColumnDone, action: ActionMerged}})
	})
}

// MergeConflict records a merge that failed with a conflict: the ticket goes
// back to In Progress and comment tells its developer (task) or product
// manager (story) what conflicted.
func (b *Board) MergeConflict(ctx context.Context, n int64, comment string) error {
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return api.Errorf(api.CodeValidation, "a merge conflict needs a comment naming what conflicted")
	}
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkStage(t, api.ReviewStageMerging); err != nil {
			return err
		}
		rank, err := endRankExcept(tx, api.ColumnInProgress, n)
		if err != nil {
			return err
		}
		if err := b.setColumn(tx, n, api.ColumnInProgress, rank, ""); err != nil {
			return err
		}
		if err := b.finish(tx, System, []move{{id: n, from: api.ColumnReview, to: api.ColumnInProgress, action: ActionConflict}}); err != nil {
			return err
		}
		return b.systemComment(tx, n, comment, workingRole(t))
	})
}

// CompleteExport records a successful export: the ticket is done as
// exported with the pull request's url.
func (b *Board) CompleteExport(ctx context.Context, n int64, url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return api.Errorf(api.CodeValidation, "an export needs the url of its pull request")
	}
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkStage(t, api.ReviewStageExporting); err != nil {
			return err
		}
		if t.inStory() {
			return api.Errorf(api.CodeInvalidTransition, "#%d is a task inside a story and is never exported", n)
		}
		if err := b.setColumn(tx, n, api.ColumnDone, t.Rank, api.ResolutionExported); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tickets SET export_url = ? WHERE id = ?`, url, n); err != nil {
			return err
		}
		return b.finish(tx, System, []move{{id: n, from: api.ColumnReview, to: api.ColumnDone, action: ActionExported}})
	})
}

// ExportFailed records a failed export: the ticket stays in Review and waits
// for the user, flagged with message, which is also posted as a comment for
// its supervisor. The user then approves again (to export or merge) or
// rejects.
func (b *Board) ExportFailed(ctx context.Context, n int64, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return api.Errorf(api.CodeValidation, "a failed export needs the error message")
	}
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkStage(t, api.ReviewStageExporting); err != nil {
			return err
		}
		if err := b.setStage(tx, n, api.ReviewStageHuman, message); err != nil {
			return err
		}
		if err := b.finish(tx, System, []move{{id: n, from: api.ColumnReview, to: api.ColumnReview, action: ActionExportFailed}}); err != nil {
			return err
		}
		return b.systemComment(tx, n, "Export failed: "+message, t.Supervisor)
	})
}

// SetBranch records the branch and worktree the engine created for one
// project of a ticket; empty values record that they were removed. While a
// branch is recorded, the ticket's projects, story and base branches are
// locked.
func (b *Board) SetBranch(ctx context.Context, n int64, project, branch, worktree string) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		res, err := tx.Exec(`UPDATE ticket_projects SET branch = ?, worktree = ? WHERE ticket_id = ? AND project_id = ?
			AND (branch <> ? OR worktree <> ?)`, branch, worktree, n, project, branch, worktree)
		if err != nil {
			return err
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			ps, err := loadProjects(tx, n)
			if err != nil {
				return err
			}
			if !containsProject(ps, project) {
				return api.Errorf(api.CodeValidation, "project %q is not a project of #%d", project, n)
			}
			return nil
		}
		if _, err := tx.Exec(`UPDATE tickets SET updated_at = ? WHERE id = ?`, b.stamp(), n); err != nil {
			return err
		}
		return b.emitUpdated(tx, System, n, "projects")
	})
}

// SetAttention flags a ticket as needing attention with reason, or clears
// the flag when reason is empty. A new reason is also posted as a comment
// for the ticket's supervisor. Done tickets cannot be flagged.
func (b *Board) SetAttention(ctx context.Context, n int64, reason string) error {
	reason = strings.TrimSpace(reason)
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if t.Attention == reason {
			return nil
		}
		if reason != "" && t.Col == api.ColumnDone {
			return api.Errorf(api.CodeInvalidTransition, "#%d is done and needs no attention", n)
		}
		if _, err := tx.Exec(`UPDATE tickets SET attention = ?, updated_at = ? WHERE id = ?`, reason, b.stamp(), n); err != nil {
			return err
		}
		if err := b.emitUpdated(tx, System, n, "attention"); err != nil {
			return err
		}
		if reason == "" {
			return nil
		}
		return b.systemComment(tx, n, fmt.Sprintf("Needs attention: %s", reason), t.Supervisor)
	})
}
