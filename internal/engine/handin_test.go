package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestHandInRefusedWhileWorktreeDirty(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Work")
	h.startTask(n, h.project, ".gitignore", "build/\n")
	dir := h.worktree(h.project, n)
	dev := h.actor(h.live(api.RoleDeveloper, n).ID)
	write(t, dir, "new.txt", "new\n")
	write(t, dir, "a.txt", "edited\n")

	err := h.e.CheckHandIn(h.ctx, dev, api.DoneRequest{})
	var apiErr *api.Error
	if !asAPIError(err, &apiErr) || apiErr.Code != api.CodeConflict {
		t.Fatalf("err = %v, want conflict", err)
	}
	for _, want := range []string{"commit or discard these changes first", "a.txt", "new.txt", dir} {
		if !strings.Contains(apiErr.Message, want) {
			t.Fatalf("message %q lacks %q", apiErr.Message, want)
		}
	}

	gitRun(t, dir, "add", "a.txt", "new.txt")
	gitRun(t, dir, "commit", "-q", "-m", "work")
	write(t, dir, "build/out.bin", "ignored\n")
	if err := h.e.CheckHandIn(h.ctx, dev, api.DoneRequest{}); err != nil {
		t.Fatalf("ignored files refused the hand-in: %v", err)
	}
}

func TestHandInGuardPerRole(t *testing.T) {
	h := gitHarness(t)
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Story", Review: api.ReviewSkipped})
	h.startStory(s)
	pm := h.actor(h.live(api.RolePM, s).ID)
	integration := h.worktree(h.project, s)
	if err := h.e.CheckHandIn(h.ctx, pm, api.DoneRequest{}); err != nil {
		t.Fatalf("clean integration worktree refused: %v", err)
	}
	write(t, integration, "stray.txt", "?\n")
	err := h.e.CheckHandIn(h.ctx, pm, api.DoneRequest{})
	var apiErr *api.Error
	if !asAPIError(err, &apiErr) || apiErr.Code != api.CodeConflict ||
		!strings.Contains(apiErr.Message, "stray.txt") || !strings.Contains(apiErr.Message, "ask your supervisor") {
		t.Fatalf("pm hand-in: %v", err)
	}
	if err := os.Remove(filepath.Join(integration, "stray.txt")); err != nil {
		t.Fatal(err)
	}

	n, job, dir, _ := h.exportedTask("Export me")
	write(t, dir, "left.txt", "x\n")
	exp := h.actor(job.ID)
	err = h.e.CheckHandIn(h.ctx, exp, api.DoneRequest{URL: "https://example.invalid/pr/1"})
	if !asAPIError(err, &apiErr) || !strings.Contains(apiErr.Message, "left.txt") || !strings.Contains(apiErr.Message, "--failed") {
		t.Fatalf("export url with a dirty worktree: %v", err)
	}
	if err := h.e.CheckHandIn(h.ctx, exp, api.DoneRequest{Error: "worktree is dirty"}); err != nil {
		t.Fatalf("a reported export failure was refused: %v", err)
	}
	if c := h.ticket(n).Column; c != api.ColumnReview {
		t.Fatalf("the check changed the ticket: %s", c)
	}

	// Many files are listed up to a limit.
	m := h.skippedTask("Many")
	h.startTask(m, h.project, "x.txt", "x\n")
	mdir := h.worktree(h.project, m)
	for i := 0; i < maxListed+5; i++ {
		write(t, mdir, filepath.Join("gen", string(rune('a'+i))+".txt"), "x\n")
	}
	err = h.e.CheckHandIn(h.ctx, h.actor(h.live(api.RoleDeveloper, m).ID), api.DoneRequest{})
	if !asAPIError(err, &apiErr) || !strings.Contains(apiErr.Message, "and 5 more") {
		t.Fatalf("long list: %v", err)
	}
}

// asAPIError reports whether err is an *api.Error and stores it in target.
func asAPIError(err error, target **api.Error) bool {
	return errors.As(err, target)
}
