package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/engine"
)

// POST /api/tickets/{n}/remove-workdir removes the working folder of a
// done task without a project for the user; agents are refused.
func TestRemoveWorkdirRoute(t *testing.T) {
	s, _ := engineServer(t, &testRunner{}, fakeProfile(), false)
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "poc"})
	if err != nil {
		t.Fatal(err)
	}
	n := tk.Number
	path := "/api/tickets/" + strconv.Itoa(n) + "/remove-workdir"
	dir := engine.WorkdirDir(s.opts.Layout.Workdirs, int64(n))
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.board.SetWorkdir(ctx, int64(n), dir); err != nil {
		t.Fatal(err)
	}

	// Not done yet.
	rec := socketDo(t, s, http.MethodPost, path, struct{}{}, "")
	if rec.Code != http.StatusConflict || errorCode(t, rec.Body) != api.CodeInvalidTransition {
		t.Fatalf("before done: status %d", rec.Code)
	}
	if _, err := s.board.Cancel(ctx, board.User, int64(n), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	ceo, err := s.board.CreateSession(ctx, board.NewSession{Role: api.RoleCEO, Name: "ceo-ada"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := socketDo(t, s, http.MethodPost, path, struct{}{}, ceo.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("CEO: status %d", rec.Code)
	}
	rec = socketDo(t, s, http.MethodPost, path, struct{}{}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("user: status %d: %s", rec.Code, rec.Body)
	}
	if got := decodeBody[api.Ticket](t, rec.Body); got.Workdir != "" {
		t.Fatalf("workdir %q after the removal", got.Workdir)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("the folder is still there: %v", err)
	}
}
