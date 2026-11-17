//go:build !windows

package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/engine"
)

// A task without a project opens its shells in its working folder: once
// work started, and still after it is done (the folder stays for the
// user), never in a folder omo did not make for it.
func TestOpenTerminalInWorkingFolder(t *testing.T) {
	s, b := shellServer(t)
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "poc"})
	if err != nil {
		t.Fatal(err)
	}
	n := tk.Number
	req := api.OpenTerminalRequest{Ticket: &n}
	notStarted := fmt.Sprintf("No working folder: work on #%d has not started, or the folder was removed.", n)
	refusedShell(t, b, s, req, http.StatusConflict, api.CodeConflict, notStarted)

	dir := engine.WorkdirDir(s.opts.Layout.Workdirs, int64(n))
	if _, err := s.board.Move(ctx, board.User, int64(n), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		t.Fatal(err)
	}
	if err := s.board.SetWorkdir(ctx, int64(n), dir); err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, req, http.StatusConflict, api.CodeConflict, notStarted) // not made yet
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sh := openShell(t, b, s, req)
	if sh.Dir != realPath(t, dir) || sh.Ticket == nil || *sh.Ticket != n || sh.Project != "" || sh.Title != fmt.Sprintf("#%d working folder", n) {
		t.Fatalf("opened %+v", sh)
	}
	refusedShell(t, b, s, api.OpenTerminalRequest{Ticket: &n, Project: "app"}, http.StatusUnprocessableEntity, api.CodeValidation, "")

	// Done: the folder is still there to look at.
	if _, err := s.board.Cancel(ctx, board.User, int64(n), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	openShell(t, b, s, req)

	// A link in its place, or a folder recorded elsewhere, is not opened.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, req, http.StatusConflict, api.CodeConflict, fmt.Sprintf("the working folder recorded for #%d is not one omo made", n))
	if err := s.board.SetWorkdir(ctx, int64(n), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, req, http.StatusConflict, api.CodeConflict, fmt.Sprintf("the working folder recorded for #%d is not one omo made", n))
}
