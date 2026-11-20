package engine

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

func TestExportFollowUpRebasesAndRecordsReviewedUpdate(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	for _, review := range []api.Review{api.ReviewHuman, api.ReviewSkipped} {
		t.Run(string(review), func(t *testing.T) {
			h := gitHarness(t)
			commit(t, h.repo, "b.txt", "base\n")
			n, job, dir, branch := h.exportedTask("Managed rebase")
			if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
				t.Fatal(err)
			}
			h.steps(2)
			previous := h.ticket(n).Projects[0].ExportCommit
			if _, err := h.b.Update(h.ctx, board.User, n, api.UpdateTicket{Review: api.Some(review), WhenApproved: api.Some(api.WhenApprovedMerge)}); err != nil {
				t.Fatal(err)
			}
			if _, err := h.b.RequestFollowUp(h.ctx, board.User, n, api.FollowUpRequest{Role: "developer", Comment: "Rebase onto origin/main"}); err != nil {
				t.Fatal(err)
			}
			h.steps(2)
			remote := h.origin(h.project)
			landed := filepath.Join(t.TempDir(), "landed")
			gitRun(t, h.repo, "clone", "-q", "-b", "main", remote, landed)
			target := commit(t, landed, "b.txt", "upstream edit\n")
			gitRun(t, landed, "push", "-q", "origin", "main")
			gitRun(t, dir, "fetch", "-q", "origin", "main")
			cmd := exec.Command("git", "rebase", "FETCH_HEAD")
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "CONFLICT") {
				t.Fatalf("rebase did not conflict: %v\n%s", err, out)
			}
			write(t, dir, "b.txt", "resolved feature\n")
			gitRun(t, dir, "add", "b.txt")
			gitRun(t, dir, "commit", "-q", "--no-edit")
			gitRun(t, dir, "rebase", "--continue")
			reviewed := gitRun(t, dir, "rev-parse", "HEAD")
			h.handIn(n)
			h.steps(2)
			if review == api.ReviewHuman {
				got := h.ticket(n)
				if got.ReviewStage != api.ReviewStageHuman || got.EffectiveWhenApproved != api.WhenApprovedExport || got.Projects[0].ReviewCommit != reviewed || got.Projects[0].LastExportCommit != previous {
					t.Fatalf("re-export review = %+v", got.TicketSummary)
				}
				if got := remoteHead(t, remote, branch); got != previous {
					t.Fatal("pushed before human approval")
				}
				commit(t, dir, "unreviewed.txt", "not part of the hand-in\n")
				// A follow-up updates the existing PR's recorded destination.
				gitRun(t, h.repo, "remote", "remove", "origin")
				h.exportNow(n)
			}
			if got := remoteHead(t, remote, branch); got != reviewed {
				t.Fatalf("re-export pushed %s, want reviewed %s", got, reviewed)
			}
			next, ok := h.exportSession(n)
			if !ok {
				t.Fatal("no re-export job")
			}
			if _, err := h.b.Done(h.ctx, h.actor(next.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
				t.Fatal(err)
			}
			h.steps(2)
			if p := h.ticket(n).Projects[0]; p.ExportCommit != reviewed || p.ExportTargetCommit != target {
				t.Fatalf("re-export watch = %+v", p)
			}
			h.e.checkExportsMerged(h.ctx)
			if h.ticket(n).Column != api.ColumnReview {
				t.Fatal("updated pull request completed before merge")
			}
			gitRun(t, landed, "fetch", "-q", "origin", "refs/heads/"+branch)
			gitRun(t, landed, "merge", "-q", "--ff-only", "FETCH_HEAD")
			commit(t, landed, "later.txt", "later upstream change\n")
			gitRun(t, landed, "push", "-q", "origin", "main", ":refs/heads/"+branch)
			h.e.checkExportsMerged(h.ctx)
			if got := h.ticket(n); got.Column != api.ColumnDone || !got.ExportMerged {
				t.Fatalf("managed fast-forward not detected: %+v", got.TicketSummary)
			}
		})
	}
}

func TestExportFollowUpReviewerRunsAndReturnsToHuman(t *testing.T) {
	h := gitHarness(t)
	n, job, dir, _ := h.exportedTask("Follow-up verification")
	if _, err := h.b.Done(h.ctx, h.actor(job.ID), api.DoneRequest{URL: "https://example.invalid/pr/1"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if _, err := h.b.Update(h.ctx, board.User, n, api.UpdateTicket{Review: api.Some(api.ReviewHuman)}); err != nil {
		t.Fatal(err)
	}
	tip := commit(t, dir, "followup.txt", "verify this change\n")
	if err := h.e.pins.setApprovals(n, map[string]approval{h.project: {Commit: tip, Round: h.ticket(n).ReviewRound}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.b.RequestFollowUp(h.ctx, board.User, n, api.FollowUpRequest{Role: "reviewer", Comment: "Verify the new change"}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	reviewer, ok := h.liveOK(api.RoleReviewer, n)
	if !ok {
		t.Fatal("explicit reviewer task was carried over without a reviewer")
	}
	if prompt, err := h.e.rolePrompt(h.ctx, h.actor(reviewer.ID)); err != nil || !strings.Contains(prompt, "follow-up verification task") {
		t.Fatalf("reviewer did not receive its follow-up scope: %v\n%s", err, prompt)
	}
	h.approveReview(n)
	h.steps(2)
	if got := h.ticket(n); got.ReviewStage != api.ReviewStageHuman || got.Projects[0].ReviewCommit != tip || got.EffectiveWhenApproved != api.WhenApprovedExport {
		t.Fatalf("follow-up reviewer bypassed human approval: %+v", got.TicketSummary)
	}
}

func TestReviewCommitRecoversPinnedHandIn(t *testing.T) {
	h := gitHarness(t)
	n, _, dir, _ := h.exportedTask("Recover the reviewed commit")
	reviewed := h.ticket(n).Projects[0].ReviewCommit
	if reviewed == "" {
		t.Fatal("missing hand-in")
	}
	// Simulate a restart after the private pin was saved but before its
	// database update, or an upgrade of a ticket already in review.
	h.inject(`UPDATE ticket_projects SET review_commit=''`)
	commit(t, dir, "unreviewed.txt", "added after hand-in\n")
	h.steps(1)
	if got := h.ticket(n).Projects[0].ReviewCommit; got != reviewed {
		t.Fatalf("recovered review commit = %q, want original hand-in %q", got, reviewed)
	}
}
