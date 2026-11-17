package engine

import (
	"context"
	"os"
	"path/filepath"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// RemoveWorkdir removes, for the user, the working folder of ticket n, a
// done task without a project, with everything in it, and records that it
// is gone (board.CheckWorkdirRemoval says who and when). Only the folder
// omo makes for the ticket is removed; a folder a user terminal is open in
// is kept, and the removal is refused with conflict until the terminal is
// closed, and so is a removal while an agent of the ticket is still
// ending. A folder that is already gone is only forgotten.
func (e *Engine) RemoveWorkdir(ctx context.Context, a board.Actor, n int64) (api.Ticket, error) {
	dir, err := e.b.CheckWorkdirRemoval(ctx, a, n)
	if err != nil {
		return api.Ticket{}, err
	}
	if filepath.Clean(dir) != WorkdirDir(e.o.Layout.Workdirs, n) {
		return api.Ticket{}, api.Errorf(api.CodeConflict,
			"the working folder recorded for #%d (%s) is not the one omo makes for it; omo removes nothing there", n, showPath(dir))
	}
	_, snap, _, err := e.read(ctx, n)
	if err != nil {
		return api.Ticket{}, err
	}
	for _, s := range snap.Sessions {
		if s.Ticket == n {
			return api.Ticket{}, api.Errorf(api.CodeConflict, "an agent of #%d is still ending; try again in a moment", n)
		}
	}
	var rerr error
	if !e.removeUnheld(dir, func() { rerr = os.RemoveAll(dir) }) {
		return api.Ticket{}, api.Errorf(api.CodeConflict, "a terminal is open in the working folder %s; close it, then remove the folder again", showPath(dir))
	}
	if rerr != nil {
		return api.Ticket{}, api.Errorf(api.CodeConflict, "omo could not remove the working folder %s completely: %v", showPath(dir), rerr)
	}
	e.forgetTrust(dir)
	ctx = context.WithoutCancel(ctx)
	if err := e.b.ForgetWorkdir(ctx, n, dir); err != nil {
		return api.Ticket{}, err
	}
	return e.b.Get(ctx, a, n)
}
