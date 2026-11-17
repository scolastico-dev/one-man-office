package board

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

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
		return api.Errorf(api.CodeInvalidTransition, "#%d is not %s (in %s, review stage %q)", t.ID, stage, columnWords(t.Col), t.ReviewStage)
	}
	return nil
}

// BeginWork records that the engine started work on a ticket in Ready: it
// moves to In Progress. A task inside a story starts only while its story
// is in progress, and no ticket starts while a dependency still blocks it.
func (b *Board) BeginWork(ctx context.Context, n int64) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if t.Col != api.ColumnReady {
			return api.Errorf(api.CodeInvalidTransition, "#%d is in %s; work begins only on a ticket in Ready For Dev", n, columnWords(t.Col))
		}
		if t.inStory() {
			story, err := loadTicket(tx, t.StoryID)
			if err != nil {
				return err
			}
			if story.Col != api.ColumnInProgress {
				return api.Errorf(api.CodeInvalidTransition,
					"#%d belongs to story #%d, which is in %s; its tasks start only while the story is In Progress", n, story.ID, columnWords(story.Col))
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

// CompleteExport records a successful export of a ticket with one project:
// the project gets the pull request's url and the ticket is done as
// exported. A ticket with several projects is exported by one export job per
// project, each reporting its own pull request through Done.
func (b *Board) CompleteExport(ctx context.Context, n int64, url string) error {
	url, err := checkExportURL(url)
	if err != nil {
		return err
	}
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkExporting(t); err != nil {
			return err
		}
		ps, err := loadProjects(tx, n)
		if err != nil {
			return err
		}
		if len(ps) != 1 {
			return api.Errorf(api.CodeValidation,
				"#%d has %d projects; each project's export job reports its own pull request", n, len(ps))
		}
		if _, err := tx.Exec(`UPDATE ticket_projects SET export_url = ? WHERE ticket_id = ?`, url, n); err != nil {
			return err
		}
		return b.completeExport(tx, System, t)
	})
}

// checkExporting requires t to be a story or standalone task in Review at
// stage exporting.
func checkExporting(t ticketRow) error {
	if t.inStory() {
		return api.Errorf(api.CodeInvalidTransition, "#%d is a task inside a story and is never exported", t.ID)
	}
	return checkStage(t, api.ReviewStageExporting)
}

// maxExportURL is the longest pull request address in characters.
const maxExportURL = 2000

// checkExportURL trims a pull request address and requires an absolute http
// or https url with a host, of at most maxExportURL characters, without
// spaces or any character a title may not contain (isTitleControl): the
// address is printed as a line of its own.
func checkExportURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", api.Errorf(api.CodeValidation, "an export needs the url of its pull request")
	}
	if utf8.RuneCountInString(raw) > maxExportURL {
		return "", api.Errorf(api.CodeValidation, "the url of a pull request has at most %d characters", maxExportURL)
	}
	if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || isTitleControl(r) }) {
		return "", api.Errorf(api.CodeValidation, "the url of a pull request contains no spaces or control characters")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return "", api.Errorf(api.CodeValidation, "the url of a pull request is an absolute http:// or https:// url with a host, got %q", raw)
	}
	return raw, nil
}

// FinishExport ends, for the engine, the export of a ticket whose projects
// have their pull request, except those the engine left out for having no
// commits of their own: the ticket is done and exported. When no project
// has a pull request (every project was left out), there was nothing to
// export: the ticket is done as completed, its branches being contained in
// their base branches already.
func (b *Board) FinishExport(ctx context.Context, n int64) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkExporting(t); err != nil {
			return err
		}
		var exported int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ticket_projects WHERE ticket_id = ? AND export_url <> ''`, n).Scan(&exported); err != nil {
			return err
		}
		if exported > 0 {
			return b.completeExport(tx, System, t)
		}
		if err := b.setColumn(tx, n, api.ColumnDone, t.Rank, api.ResolutionCompleted); err != nil {
			return err
		}
		if err := b.finish(tx, System, []move{{id: n, from: api.ColumnReview, to: api.ColumnDone, action: ActionMerged}}); err != nil {
			return err
		}
		return b.systemComment(tx, n, fmt.Sprintf("Nothing to export: no project of #%d has commits of its own. It is done.", n))
	})
}

// completeExport closes t, whose projects have a pull request (or were
// left out of the export), as done and exported. The ticket's export_url
// lists the pull requests in project order, one per line.
func (b *Board) completeExport(tx *sql.Tx, a Actor, t ticketRow) error {
	var urls []string
	if err := each(tx, `SELECT export_url FROM ticket_projects WHERE ticket_id = ? AND export_url <> '' ORDER BY position`, t.ID,
		func(rows *sql.Rows) error {
			var u string
			if err := rows.Scan(&u); err != nil {
				return err
			}
			urls = append(urls, u)
			return nil
		}); err != nil {
		return err
	}
	if err := b.setColumn(tx, t.ID, api.ColumnDone, t.Rank, api.ResolutionExported); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tickets SET export_url = ? WHERE id = ?`, strings.Join(urls, "\n"), t.ID); err != nil {
		return err
	}
	return b.finish(tx, a, []move{{id: t.ID, from: api.ColumnReview, to: api.ColumnDone, action: ActionExported}})
}

// ExportFailed records a failed export: the ticket stays in Review and waits
// for the user, flagged with message (cut to one line, see attentionLine),
// which is also posted in full as a comment for its supervisor. The user then approves again to retry the export, approves
// with merge to merge it locally instead, or rejects it. Projects that
// already have a pull request keep it, so a retry exports only the others.
func (b *Board) ExportFailed(ctx context.Context, n int64, message string) error {
	message = strings.TrimSpace(message)
	if attentionLine(message) == "" {
		return api.Errorf(api.CodeValidation, "a failed export needs the error message")
	}
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		return b.exportFailed(tx, System, t, message)
	})
}

// exportFailed is ExportFailed in tx, recorded as done by a.
func (b *Board) exportFailed(tx *sql.Tx, a Actor, t ticketRow, message string) error {
	if err := checkExporting(t); err != nil {
		return err
	}
	if err := b.setStage(tx, t.ID, api.ReviewStageHuman, attentionLine(message)); err != nil {
		return err
	}
	if err := b.finish(tx, a, []move{{id: t.ID, from: api.ColumnReview, to: api.ColumnReview, action: ActionExportFailed}}); err != nil {
		return err
	}
	return b.systemComment(tx, t.ID, "Export failed: "+message, t.Supervisor)
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

// SetProjectExport records, for the engine, that the export of project of
// ticket n failed (state api.ExportFailedState, with reason cut to one line)
// or has nothing to export (api.ExportLeftOut). It is shown on the ticket's
// project until the ticket is exported again (a failure) or leaves the
// export; it applies only while the ticket is exporting. Nothing changes, and no event is sent, when the project already
// has this state.
func (b *Board) SetProjectExport(ctx context.Context, n int64, project string, state api.ExportState, reason string) error {
	if state != api.ExportFailedState && state != api.ExportLeftOut {
		return api.Errorf(api.CodeValidation, "the engine records only failed and left-out exports, not %q", state)
	}
	if state == api.ExportFailedState {
		if reason = attentionLine(reason); reason == "" {
			return api.Errorf(api.CodeValidation, "a failed export needs the error message")
		}
	} else {
		reason = ""
	}
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if err := checkExporting(t); err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE ticket_projects SET export_state = ?, export_error = ? WHERE ticket_id = ? AND project_id = ?
			AND (export_state <> ? OR export_error <> ?)`, state, reason, n, project, state, reason)
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

// maxAttention is the longest attention flag in characters.
const maxAttention = 200

// attentionLine turns a reason into the stored attention flag: one line of
// at most maxAttention characters. Reasons come from git, forges and agent
// transcripts and may span lines or carry terminal controls, while the flag
// is shown on the card and next to omo's own lines. White space, line
// breaks included, becomes single spaces; every other character a title
// may not contain is dropped; a longer reason is cut with "…". The full
// reason goes into the comment that is posted with the flag.
func attentionLine(reason string) string {
	line := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r) || r == '\u2028' || r == '\u2029':
			return ' '
		case isTitleControl(r):
			return -1
		}
		return r
	}, reason)), " ")
	if rs := []rune(line); len(rs) > maxAttention {
		line = strings.TrimSpace(string(rs[:maxAttention-1])) + "…"
	}
	return line
}

// SetAttention flags a ticket as needing attention with reason, or clears
// the flag when reason is empty. A new reason is also posted in full as a
// comment for the ticket's supervisor; the flag holds it cut to one line
// (see attentionLine). Done tickets cannot be flagged.
func (b *Board) SetAttention(ctx context.Context, n int64, reason string) error {
	reason = strings.TrimSpace(reason)
	line := attentionLine(reason)
	if reason != "" && line == "" {
		return api.Errorf(api.CodeValidation, "an attention reason needs readable text")
	}
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if t.Attention == line {
			return nil
		}
		if reason != "" && t.Col == api.ColumnDone {
			return api.Errorf(api.CodeInvalidTransition, "#%d is done and needs no attention", n)
		}
		if _, err := tx.Exec(`UPDATE tickets SET attention = ?, updated_at = ? WHERE id = ?`, line, b.stamp(), n); err != nil {
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
