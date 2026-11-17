package engine

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// reviewers lists the projects of the live reviewers of n.
func (h *harness) reviewers(n int64) []string {
	var out []string
	for _, s := range h.snap().Sessions {
		if s.Role == api.RoleReviewer && s.Ticket == n {
			out = append(out, s.Project)
		}
	}
	sort.Strings(out)
	return out
}

// reviewerRounds maps the project of every live reviewer of n to its
// review round.
func (h *harness) reviewerRounds(n int64) map[string]int {
	out := map[string]int{}
	for _, s := range h.snap().Sessions {
		if s.Role == api.RoleReviewer && s.Ticket == n {
			out[s.Project] = s.ReviewRound
		}
	}
	return out
}

// verdict gives the verdict of the live reviewer of project of n in its
// current review round.
func (h *harness) verdict(n int64, project, verdict, comment string) {
	h.t.Helper()
	for _, s := range h.snap().Sessions {
		if s.Role == api.RoleReviewer && s.Ticket == n && s.Project == project && s.ReviewRound == h.ticket(n).ReviewRound {
			if _, err := h.b.Done(h.ctx, h.actor(s.ID), api.DoneRequest{Verdict: verdict, Comment: comment}); err != nil {
				h.t.Fatal(err)
			}
			return
		}
	}
	h.t.Fatalf("no reviewer of %s on #%d", project, n)
}

// A later review round reviews only what changed: a project approved
// before whose commit is the same keeps its approval, a project whose
// branch has no commits of its own is approved without a reviewer (noted
// once), and a rejected project is always reviewed again.
func TestReviewRoundReviewsOnlyWhatChanged(t *testing.T) {
	h := gitHarness(t, withProjects(4))
	a, b, c, d := h.ids[0], h.ids[1], h.ids[2], h.ids[3]
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Four", Projects: []string{a, b, c, d}, Review: api.ReviewAgent})
	h.startStory(s)
	for _, p := range []string{a, b, c} {
		task := h.storyTask(s, p, "In "+p)
		h.finishTask(task, p, "new.txt", p+"\n")
		h.steps(2)
	}
	h.handIn(s)
	h.steps(2)
	if got := h.reviewers(s); strings.Join(got, ",") != strings.Join([]string{a, b, c}, ",") {
		t.Fatalf("round 1 reviewers %v, want %s, %s and %s", got, a, b, c)
	}
	notes := h.commentsWith(s, "no commits of its own")
	if notes != 1 || !strings.Contains(h.lastComment(s).Body, "- "+d+": no commits of its own") {
		t.Fatalf("%d notes; last comment:\n%s", notes, h.lastComment(s).Body)
	}

	// a approves, b rejects, c approves: the round ends with b's rejection.
	h.verdict(s, a, board.VerdictApprove, "")
	h.verdict(s, b, board.VerdictReject, "b is broken")
	h.verdict(s, c, board.VerdictApprove, "")
	h.step()
	if tk := h.ticket(s); tk.Column != api.ColumnInProgress {
		t.Fatalf("after round 1: %s", tk.Column)
	}
	// The pm fixes b and changes c; a and d stay as they were.
	commit(t, h.worktree(b, s), "fix.txt", "fixed\n")
	commit(t, h.worktree(c, s), "more.txt", "more\n")
	h.handIn(s)
	h.steps(2)
	// b and c get reviewers of round 2, whose start ended those of round 1;
	// a's approval carried over, so its reviewer of round 1 stays for
	// questions about it.
	if got := h.reviewerRounds(s); !reflect.DeepEqual(got, map[string]int{a: 1, b: 2, c: 2}) {
		t.Fatalf("reviewers by project and round %v, want %s of round 1, %s and %s of round 2", got, a, b, c)
	}
	note := h.lastComment(s).Body
	if !strings.Contains(note, "Review round 2") || !strings.Contains(note, "- "+a+": approved in round 1, unchanged") ||
		strings.Contains(note, d) {
		t.Fatalf("round 2 note:\n%s", note)
	}
	if n := h.commentsWith(s, "no commits of its own"); n != 1 {
		t.Fatalf("the note on %s was posted %d times", d, n)
	}
	h.verdict(s, b, board.VerdictApprove, "")
	h.verdict(s, c, board.VerdictApprove, "")
	h.steps(3)
	if tk := h.ticket(s); tk.Column != api.ColumnDone {
		t.Fatalf("after round 2: %s %q (attention %q)", tk.Column, tk.ReviewStage, tk.Attention)
	}
}

func TestApprovalsHoldForTheirCommitAndRejectionsNeverCarry(t *testing.T) {
	tk := board.EngineTicket{ID: 1, Type: api.TypeStory, Column: api.ColumnReview, ReviewStage: api.ReviewStageAgent, ReviewRound: 2,
		Projects: []board.EngineProject{{ID: "a"}, {ID: "b"}, {ID: "c"}}, Verdicts: map[string]string{"a": "approve", "b": "reject"}}
	pins := map[string]string{"a": "c1", "b": "c2", "c": "c3"}
	old := map[string]approval{"a": {Commit: "c1", Round: 1}, "b": {Commit: "c2", Round: 1}}
	got, changed := recordApprovals(tk, pins, old)
	if !changed || got["a"] != (approval{Commit: "c1", Round: 1}) {
		t.Fatalf("approvals %+v (changed %v): an approval of the same commit keeps its round", got, changed)
	}
	if _, ok := got["b"]; ok {
		t.Fatalf("a rejection kept the approval: %+v", got)
	}
	if old["b"].Commit != "c2" {
		t.Fatal("recordApprovals changed its input")
	}
	// Round 3: b was rejected, so it is reviewed again although its commit
	// is the same; a carries over; c is new.
	tk.ReviewRound, tk.Verdicts = 3, map[string]string{}
	cs := carryOver(tk, pins, got, func(string, string) bool { return false })
	if len(cs) != 1 || cs[0].project != "a" || cs[0].from.Round != 1 {
		t.Fatalf("carried %+v", cs)
	}
	if text := carriedText(3, cs); text != "Review round 3 needs no reviewer for these projects:\n- a: approved in round 1, unchanged" {
		t.Fatalf("text %q", text)
	}
	// A changed commit needs a reviewer.
	pins["a"] = "c9"
	if cs := carryOver(tk, pins, got, nil); len(cs) != 0 {
		t.Fatalf("carried %+v", cs)
	}
}
