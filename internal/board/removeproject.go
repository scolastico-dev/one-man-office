package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Removing a project from a ticket that already has a branch there is the
// engine's work (the worktree and the branch are omo's), framed by the
// board: ClaimProjectRemoval checks the rules and claims the ticket,
// RemoveProject records the outcome and ends the claim (ReleaseClaim when
// the engine gave up). A ticket without a branch in the project needs none
// of this; PATCH projects changes it.
//
// The rules:
//   - who: the user, and the CEO on tickets it created or supervises;
//   - when: in Planning, in Ready For Dev, or in Review while it waits for
//     the user (stage human, also after a failed export). No agent works on
//     the ticket then and omo does nothing with it on its own. A ticket in
//     progress is stopped first (moved back to Ready For Dev);
//   - the ticket keeps at least one project (a ticket with a branch does
//     not turn into a task without a project);
//   - a story keeps a project while a task of it that is not done uses it:
//     that task is cancelled, or finished, first. Done tasks keep the
//     project as their history;
//   - no agent of the ticket may be working (starting or running) in that
//     project or its worktree. Agents there that only wait (a reviewer
//     after its verdict, a parked product manager) are ended by the engine.
//
// What goes with the project: its row of the ticket (branch, worktree,
// export state and pull request address), the review verdicts given for it,
// and, if the attention flag names the project's failed export, that part
// of the flag.

// removeRule names who may remove a project from a ticket.
const removeRule = "the user removes a project from every ticket, the CEO from tickets it created or supervises"

// mayRemoveProject reports whether a may remove a project from t.
func mayRemoveProject(a Actor, t ticketRow) bool {
	switch a.Role {
	case api.RoleUser:
		return true
	case api.RoleCEO:
		return t.CreatedByRole == api.RoleCEO || t.Supervisor == api.RoleCEO
	}
	return false
}

// removableNow refuses a removal while t is anywhere but Planning, Ready For
// Dev or Review at stage human.
func removableNow(t ticketRow) error {
	switch {
	case t.Col == api.ColumnDone:
		return api.Errorf(api.CodeInvalidTransition, "#%d is done; done tickets are read-only", t.ID)
	case t.Col == api.ColumnPlanning, t.Col == api.ColumnReady:
		return nil
	case t.Col == api.ColumnReview && t.ReviewStage == api.ReviewStageHuman:
		return nil
	case t.Col == api.ColumnInProgress:
		return api.Errorf(api.CodeInvalidTransition,
			"#%d is in progress; a project is removed while the ticket is in Planning, Ready For Dev or waits for the user in Review. Move it back to Ready For Dev first", t.ID)
	}
	return api.Errorf(api.CodeInvalidTransition,
		"#%d is in Review at stage %s; a project is removed while the ticket is in Planning, Ready For Dev or waits for the user in Review", t.ID, t.ReviewStage)
}

// ProjectRemoval is what the engine needs to remove a project from a
// ticket it claimed with ClaimProjectRemoval.
type ProjectRemoval struct {
	Ticket  EngineTicket
	Project EngineProject
	// Parked are the live sessions of the ticket in that project or its
	// worktree; each only waits (state waiting) and must end before the
	// worktree goes.
	Parked []EngineSession
}

// ClaimProjectRemoval checks that a may remove project from ticket n now
// (see the rules above) and claims n for the engine, which removes the
// worktree and branch and then calls RemoveProject, or ReleaseClaim when it
// gives up.
func (b *Board) ClaimProjectRemoval(ctx context.Context, a Actor, n int64, project string) (ProjectRemoval, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return ProjectRemoval{}, err
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return ProjectRemoval{}, api.Errorf(api.CodeValidation, "name the project to remove")
	}
	var out ProjectRemoval
	err := b.write(ctx, func(tx *sql.Tx) error {
		if err := checkRead(tx, a, n); err != nil {
			return err
		}
		t, err := loadTicket(tx, n)
		if err != nil {
			return err
		}
		if !mayRemoveProject(a, t) {
			return api.Errorf(api.CodeForbidden, "a %s cannot remove a project from #%d: %s", a.Role, n, removeRule)
		}
		if err := removableNow(t); err != nil {
			return err
		}
		ps, err := loadProjects(tx, n)
		if err != nil {
			return err
		}
		if !containsProject(ps, project) {
			return api.Errorf(api.CodeValidation, "project %q is not a project of #%d", project, n)
		}
		if len(ps) == 1 {
			return api.Errorf(api.CodeValidation,
				"%q is the only project of #%d, and a ticket keeps at least one project here: "+
					"a ticket with a branch does not become one without a project; cancel the ticket instead "+
					"(a task without a project is created as one, or set so while it has no branch)", project, n)
		}
		if t.isStory() {
			if err := checkNoOpenTask(tx, t, project); err != nil {
				return err
			}
		}
		if err := checkNotBusy(tx, t); err != nil {
			return err
		}
		snap, err := b.engineSnapshot(tx)
		if err != nil {
			return err
		}
		for _, et := range snap.Tickets {
			if et.ID == n {
				out.Ticket = et
			}
		}
		for _, p := range out.Ticket.Projects {
			if p.ID == project {
				out.Project = p
			}
		}
		for _, s := range snap.Sessions {
			if s.Ticket != n || (s.Project != project && (out.Project.Worktree == "" || s.Workdir != out.Project.Worktree)) {
				continue
			}
			if s.State != api.SessionWaiting {
				return api.Errorf(api.CodeConflict,
					"%s (%s) is working in project %q of #%d; remove the project once it waits or has ended", s.Name, s.Role, project, n)
			}
			out.Parked = append(out.Parked, s)
		}
		if _, err := tx.Exec(`UPDATE tickets SET busy = 1 WHERE id = ?`, n); err != nil {
			return err
		}
		return b.claimChanged(tx, t, "busy")
	})
	return out, err
}

// checkNoOpenTask refuses while a task of story t that is not done uses
// project.
func checkNoOpenTask(tx *sql.Tx, t ticketRow, project string) error {
	var task int64
	err := tx.QueryRow(`SELECT t.id FROM tickets t JOIN ticket_projects tp ON tp.ticket_id = t.id
		WHERE t.story_id = ? AND t.col <> 'done' AND tp.project_id = ? ORDER BY t.id LIMIT 1`,
		t.ID, project).Scan(&task)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return api.Errorf(api.CodeValidation,
		"task #%d of story #%d uses project %q and is not done; cancel or finish it first", task, t.ID, project)
}

// RemoveProject records that project was removed from ticket n, which the
// engine claimed with ClaimProjectRemoval and whose worktree and branch it
// dealt with as note says: the project's row, its review verdicts and its
// part of the attention flag go, the claim ends, and a comment by omo says
// who removed what (note is its second part). It emits ticket.updated as
// done by a.
func (b *Board) RemoveProject(ctx context.Context, a Actor, n int64, project, note string) (api.Ticket, error) {
	var out api.Ticket
	err := b.write(ctx, func(tx *sql.Tx) error {
		t, err := loadTicket(tx, n)
		if err != nil {
			return err
		}
		if !t.Busy {
			return api.Errorf(api.CodeConflict, "#%d is not claimed for a project removal", n)
		}
		res, err := tx.Exec(`DELETE FROM ticket_projects WHERE ticket_id = ? AND project_id = ?`, n, project)
		if err != nil {
			return err
		}
		if gone, err := res.RowsAffected(); err != nil {
			return err
		} else if gone == 0 {
			return api.Errorf(api.CodeValidation, "project %q is not a project of #%d", project, n)
		}
		if _, err := tx.Exec(`DELETE FROM review_verdicts WHERE ticket_id = ? AND project_id = ?`, n, project); err != nil {
			return err
		}
		fields := []string{"projects", "busy"}
		attention, err := remainingExportFailures(tx, t, project)
		if err != nil {
			return err
		}
		if attention != t.Attention {
			fields = append(fields, "attention")
		}
		if _, err := tx.Exec(`UPDATE tickets SET busy = 0, attention = ?, updated_at = ? WHERE id = ?`,
			attention, b.stamp(), n); err != nil {
			return err
		}
		if err := b.emitUpdated(tx, a, n, fields...); err != nil {
			return err
		}
		if err := b.touch(tx, System, t.StoryID); err != nil {
			return err
		}
		body := fmt.Sprintf("%s removed project %s from #%d.", actorWords(a), codeWords(project), n)
		if note != "" {
			body += " " + note
		}
		var mentions []api.Role
		if t.Supervisor != a.Role {
			mentions = append(mentions, t.Supervisor)
		}
		if t.isStory() {
			mentions = append(mentions, api.RolePM)
		}
		if err := b.systemComment(tx, n, body, mentions...); err != nil {
			return err
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return forActor(a, out, err)
}

// remainingExportFailures is the attention flag of t once project is gone:
// after a failed export (stage human, no failed step) the flag lists the
// failed projects, and the removed one leaves that list; any other flag
// stays as it is.
func remainingExportFailures(tx *sql.Tx, t ticketRow, project string) (string, error) {
	if t.Col != api.ColumnReview || t.ReviewStage != api.ReviewStageHuman || t.FailedStep != api.FailedNone ||
		!strings.Contains(t.Attention, "Export of "+project+" failed") {
		return t.Attention, nil
	}
	// The removed project's row is gone already: the rows left are the
	// other projects.
	var parts []string
	failure := func(id, reason string) string { return fmt.Sprintf("Export of %s failed: %s", id, reason) }
	if err := each(tx, `SELECT project_id, export_state, export_error FROM ticket_projects WHERE ticket_id = ? ORDER BY position`, t.ID,
		func(rows *sql.Rows) error {
			var id, reason string
			var state api.ExportState
			if err := rows.Scan(&id, &state, &reason); err != nil {
				return err
			}
			if state == api.ExportFailedState {
				parts = append(parts, failure(id, reason))
			}
			return nil
		}); err != nil {
		return "", err
	}
	return attentionLine(strings.Join(parts, "; ")), nil
}

// actorWords names a in a comment: "The user", "The CEO (ceo-ada)".
func actorWords(a Actor) string {
	switch a.Role {
	case api.RoleUser:
		return "The user"
	case api.RoleCEO:
		if a.Name != "" {
			return "The CEO (" + a.Name + ")"
		}
		return "The CEO"
	}
	return "omo"
}

// codeWords is s as a code span.
func codeWords(s string) string { return "`" + s + "`" }
