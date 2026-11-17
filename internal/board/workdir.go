package board

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// A task without a project works in a working folder omo makes for it in
// its home (not a git worktree). The engine records the folder when work
// starts (SetWorkdir); from then on the ticket's projects and story are
// locked, as a branch locks them. The folder outlives the ticket's Done: it
// may hold the result (a proof of concept, notes), so it stays, and its
// path is on the ticket, until the user removes it (CheckWorkdirRemoval,
// then the engine removes the folder and records it with SetWorkdir "").

// SetWorkdir records, for the engine, the working folder dir of ticket n, a
// task without a project; "" records that the folder was removed. Nothing
// changes, and no event is sent, when the ticket already records dir.
func (b *Board) SetWorkdir(ctx context.Context, n int64, dir string) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if t.Workdir == dir {
			return nil
		}
		if dir != "" {
			none, err := hasNoProject(tx, t)
			if err != nil {
				return err
			}
			if !none {
				return api.Errorf(api.CodeValidation, "#%d has a project; only a task without one gets a working folder", n)
			}
		}
		if _, err := tx.Exec(`UPDATE tickets SET workdir = ?, updated_at = ? WHERE id = ?`, dir, b.stamp(), n); err != nil {
			return err
		}
		return b.emitUpdated(tx, System, n, "workdir")
	})
}

// CheckWorkdirRemoval checks that a may have the working folder of ticket n
// removed now and returns the folder: only the user, only once the ticket
// is done (until then its agents work there), and only while a folder is
// recorded.
func (b *Board) CheckWorkdirRemoval(ctx context.Context, a Actor, n int64) (string, error) {
	if a.Role != api.RoleUser {
		return "", api.Errorf(api.CodeForbidden, "only the user removes the working folder of a ticket")
	}
	var dir string
	err := b.read(ctx, func(tx *sql.Tx) error {
		t, err := loadTicket(tx, n)
		if err != nil {
			return err
		}
		switch {
		case t.Workdir == "":
			return api.Errorf(api.CodeInvalidTransition, "#%d has no working folder", n)
		case t.Col != api.ColumnDone:
			return api.Errorf(api.CodeInvalidTransition,
				"#%d is in %s; its working folder is removed once the ticket is done (cancel it first to stop the work)", n, columnWords(t.Col))
		}
		dir = t.Workdir
		return nil
	})
	return dir, err
}

// ForgetWorkdir records, for the engine, that the user had the working
// folder dir of ticket n removed, with a comment saying so. A ticket that
// records another folder by now is left alone.
func (b *Board) ForgetWorkdir(ctx context.Context, n int64, dir string) error {
	return b.engineTransition(ctx, n, func(tx *sql.Tx, t ticketRow) error {
		if t.Workdir != dir || dir == "" {
			return nil
		}
		if _, err := tx.Exec(`UPDATE tickets SET workdir = '', updated_at = ? WHERE id = ?`, b.stamp(), n); err != nil {
			return err
		}
		if err := b.emitUpdated(tx, System, n, "workdir"); err != nil {
			return err
		}
		return b.systemComment(tx, n, fmt.Sprintf("The working folder `%s` of #%d was removed.", dir, n))
	})
}
