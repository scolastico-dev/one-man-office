//go:build !windows

package server

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// skippedDeveloper creates a standalone task whose review is skipped,
// approves it and waits until the engine runs its developer.
func skippedDeveloper(t *testing.T, s *Server, project string) (int, api.AgentSession) {
	t.Helper()
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{project}, Review: api.ReviewSkipped})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Move(ctx, board.User, int64(tk.Number), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		t.Fatal(err)
	}
	var dev api.AgentSession
	waitFor(t, "the developer", func() bool {
		var ok bool
		dev, ok = liveOf(t, s, api.RoleDeveloper, tk.Number)
		return ok && hasTerminal(s, dev.ID)
	})
	return tk.Number, dev
}

// commitAndHandIn commits a file in the developer's worktree and hands the
// work in; with review skipped, the engine merges it and the ticket is Done.
func commitAndHandIn(t *testing.T, s *Server, n int, dev api.AgentSession) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dev.Workdir, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dev.Workdir, "add", "work.txt")
	gitIn(t, dev.Workdir, "commit", "-q", "-m", "feat: work")
	wantStatus(t, socketDo(t, s, "POST", "/api/ai/done", api.DoneRequest{Comment: "finished"}, dev.ID).Code, http.StatusOK)
}

// waitDoneAndIdle waits until ticket n is Done and no agent works on it.
// The engine's deterministic hold tests cover cleanup attempts while held;
// these route tests cover real shell acquisition, release and final removal.
func waitDoneAndIdle(t *testing.T, s *Server, n int) {
	t.Helper()
	waitFor(t, "the ticket to be done without agents", func() bool {
		tk, err := s.board.Get(context.Background(), board.User, int64(n))
		if err != nil || tk.Column != api.ColumnDone {
			return false
		}
		_, live := liveOf(t, s, api.RoleDeveloper, n)
		return !live
	})
}

func worktreeOf(t *testing.T, s *Server, n int) api.TicketProject {
	t.Helper()
	tk, err := s.board.Get(context.Background(), board.User, int64(n))
	if err != nil {
		t.Fatal(err)
	}
	return tk.Projects[0]
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// A shell open in a ticket's worktree does not stop the ticket from being
// merged and Done, and keeps the folder until the last shell there ends;
// then the cleanup removes it.
func TestShellHoldsWorktreeUntilClosed(t *testing.T) {
	isolateShells(t)
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	b := newBrowser(t, s)
	n, dev := skippedDeveloper(t, s, p)
	dir := dev.Workdir
	first := openShell(t, b, s, api.OpenTerminalRequest{Ticket: &n})
	second := openShell(t, b, s, api.OpenTerminalRequest{Project: p, Ticket: &n})
	if first.Dir != realPath(t, dir) {
		t.Fatalf("shell in %s, worktree %s", first.Dir, dir)
	}

	commitAndHandIn(t, s, n, dev)
	waitDoneAndIdle(t, s, n)
	if !exists(dir) {
		t.Fatal("the cleanup removed a worktree with open shells")
	}
	// Opening another shell there is refused now; the open ones stay.
	refusedShell(t, b, s, api.OpenTerminalRequest{Ticket: &n}, http.StatusConflict, api.CodeConflict, "")

	// The first shell exits by itself: one shell still holds the folder.
	out := shellConn(t, b, s, first.ID)
	out.typeLine("exit")
	waitFor(t, "the first shell to exit", func() bool {
		for _, sh := range listShells(t, b, s) {
			if sh.ID == first.ID {
				return sh.State == "exited"
			}
		}
		return false
	})
	if !exists(dir) {
		t.Fatal("the worktree went while a second shell was still open there")
	}

	// The last one is closed: the folder goes, and the ticket records it.
	wantStatus(t, asUser(b, s, "DELETE", "/api/terminals/"+second.ID, nil).StatusCode, http.StatusNoContent)
	waitFor(t, "the worktree to be removed", func() bool { return !exists(dir) })
	waitFor(t, "the removal to be recorded", func() bool { return worktreeOf(t, s, n).Worktree == "" })
	if tp := worktreeOf(t, s, n); tp.Branch != "" {
		t.Fatalf("the merged branch was kept: %+v", tp)
	}
}

// Cancel with "delete the branch" and "discard uncommitted changes" forces
// the removal of a dirty worktree; an open shell there still keeps it.
func TestShellHoldsWorktreeAgainstDiscard(t *testing.T) {
	isolateShells(t)
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	b := newBrowser(t, s)
	n, dev := skippedDeveloper(t, s, p)
	dir := dev.Workdir
	stray := filepath.Join(dir, "uncommitted.txt")
	if err := os.WriteFile(stray, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh := openShell(t, b, s, api.OpenTerminalRequest{Ticket: &n})
	if _, err := s.board.Cancel(context.Background(), board.User, int64(n), api.CancelRequest{DeleteBranch: true, DiscardUncommitted: true}); err != nil {
		t.Fatal(err)
	}
	waitDoneAndIdle(t, s, n)
	if !exists(stray) {
		t.Fatal("a forced removal deleted a worktree with an open shell")
	}
	wantStatus(t, asUser(b, s, "DELETE", "/api/terminals/"+sh.ID, nil).StatusCode, http.StatusNoContent)
	waitFor(t, "the worktree to be discarded", func() bool { return !exists(dir) })
	waitFor(t, "the branch to be deleted", func() bool { tp := worktreeOf(t, s, n); return tp.Worktree == "" && tp.Branch == "" })
}

// Closing the server ends the shells, releases their holds and leaves no
// process; holds live in memory only, so the next server's cleanup removes
// the worktree they kept.
func TestServerCloseReleasesHolds(t *testing.T) {
	isolateShells(t)
	r := &testRunner{}
	s, p := engineServer(t, r, fakeProfile(), true)
	b := newBrowser(t, s)
	n, dev := skippedDeveloper(t, s, p)
	dir := dev.Workdir
	sh := openShell(t, b, s, api.OpenTerminalRequest{Ticket: &n})
	pid := shellConn(t, b, s, sh.ID).shellPID()
	commitAndHandIn(t, s, n, dev)
	waitDoneAndIdle(t, s, n)
	if !exists(dir) {
		t.Fatal("the worktree went while a shell was open there")
	}

	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	err := <-done
	if err != nil {
		t.Fatal(err)
	}
	waitGroupGone(t, pid)

	// The next server finds no hold and cleans up.
	next, err := newServer(Options{Layout: s.opts.Layout, Config: testConfig(), Version: "test", Runner: r}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { next.Close() })
	next.StartEngine()
	waitFor(t, "the next server to remove the worktree", func() bool { return !exists(dir) })
}
