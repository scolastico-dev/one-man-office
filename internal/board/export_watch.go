package board

import (
	"context"
	"database/sql"
	"regexp"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/secreturl"
)

// ExportCheckAttentionPrefix identifies attention owned by remote merge checks.
// A successful check clears only flags with this prefix, including after restart.
const ExportCheckAttentionPrefix = "Merge check failed: "

var exportRemoteURI = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// ExportRemoteDisplay redacts credentials from URI-form Git remotes while
// preserving SCP-style and filesystem addresses, which are not URL syntax.
// It is the representation persisted with an export watch and shown in the API.
func ExportRemoteDisplay(remote string) string {
	if exportRemoteURI.MatchString(remote) {
		return secreturl.Display(remote)
	}
	return remote
}

// RecordExportWatch records the branch and commit that the engine pushed and
// the target's remote tip at that time. It is allowed only while exporting.
// Identical calls change nothing and emit no event. Remote is stored only in
// its redacted display form; the engine retains the actual transport address.
func (b *Board) RecordExportWatch(ctx context.Context, n int64, project, remote, branch, base, commit, target string) error {
	if strings.TrimSpace(remote) == "" || strings.TrimSpace(branch) == "" || strings.TrimSpace(base) == "" || !commitID(commit) || !commitID(target) {
		return api.Errorf(api.CodeValidation, "an export watch needs a remote, branch, base and full exported and target commit ids")
	}
	remote = ExportRemoteDisplay(remote)
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkExporting(t); err != nil {
			return err
		}
		var oldRemote, oldBranch, oldBase, oldCommit, oldTarget string
		err := tx.QueryRow(`SELECT export_remote,export_branch,export_base,export_commit,export_target_commit FROM ticket_projects WHERE ticket_id=? AND project_id=?`, n, project).Scan(&oldRemote, &oldBranch, &oldBase, &oldCommit, &oldTarget)
		if err == sql.ErrNoRows {
			return api.Errorf(api.CodeValidation, "project %q is not a project of #%d", project, n)
		}
		if err != nil {
			return err
		}
		if oldRemote == remote && oldBranch == branch && oldBase == base && oldCommit == commit && oldTarget == target {
			return nil
		}
		if _, err := tx.Exec(`UPDATE ticket_projects SET export_remote=?,export_branch=?,export_base=?,export_commit=?,export_target_commit=? WHERE ticket_id=? AND project_id=?`, remote, branch, base, commit, target, n, project); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tickets SET updated_at=? WHERE id=?`, b.stamp(), n); err != nil {
			return err
		}
		return b.emitUpdated(tx, System, n, "projects")
	})
}

// CompleteExportMerge closes an export after the engine confirmed every
// watched project was merged. The round and exact current watch commits are
// checked in the same transaction as the Done transition, so a result from a
// rejected or replaced export cannot close newer work. Projects left out or
// merged locally have no pull request to confirm. Missing watches for exported
// projects prevent automatic completion.
func (b *Board) CompleteExportMerge(ctx context.Context, n int64, round int, commits map[string]string) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkStage(t, api.ReviewStageAwaitingMerge); err != nil {
			return err
		}
		if t.ReviewRound != round {
			return api.Errorf(api.CodeConflict, "the export review round of #%d changed", n)
		}
		count := 0
		if err := each(tx, `SELECT project_id,export_commit FROM ticket_projects WHERE ticket_id=? AND export_url<>'' ORDER BY position`, n, func(rows *sql.Rows) error {
			var project, commit string
			if err := rows.Scan(&project, &commit); err != nil {
				return err
			}
			if commit == "" || commits[project] != commit {
				return api.Errorf(api.CodeConflict, "the export watch of project %s of #%d changed or is missing", project, n)
			}
			count++
			return nil
		}); err != nil {
			return err
		}
		if count == 0 || count != len(commits) {
			return api.Errorf(api.CodeConflict, "the watched projects of #%d changed", n)
		}
		return b.markMerged(tx, System, t)
	})
}

// SetExportCheckAttention records or clears a remote merge check's attention
// only while the same review round still awaits its exported merge. A delayed
// check cannot flag rejected work or clear attention belonging to a newer round.
// Reasons include ExportCheckAttentionPrefix; clearing preserves other flags.
func (b *Board) SetExportCheckAttention(ctx context.Context, n int64, round int, reason string) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkStage(t, api.ReviewStageAwaitingMerge); err != nil {
			return err
		}
		if t.ReviewRound != round {
			return api.Errorf(api.CodeConflict, "the export review round of #%d changed", n)
		}
		if strings.TrimSpace(reason) == "" && t.AttentionOwner != AttentionMergeCheck {
			return nil
		}
		return b.setAttentionOwned(tx, t, AttentionMergeCheck, reason)
	})
}
