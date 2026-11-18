package engine

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// agentReviewed creates a standalone task in Planning that only a reviewer
// agent reviews, whatever the review default is.
func (h *harness) agentReviewed(title string) int64 {
	h.t.Helper()
	return h.create(api.CreateTicket{Type: api.TypeTask, Title: title, Review: api.ReviewAgent})
}

// approveReview lets the live reviewer of n approve.
func (h *harness) approveReview(n int64) {
	h.t.Helper()
	rev := h.roundReviewer(n, h.ticket(n).ReviewRound)
	if _, err := h.b.Done(h.ctx, h.actor(rev.ID), api.DoneRequest{Verdict: board.VerdictApprove}); err != nil {
		h.t.Fatal(err)
	}
}

func TestReviewedCommitIsMerged(t *testing.T) {
	h := gitHarness(t)
	n := h.agentReviewed("Add b")
	reviewed := h.startTask(n, h.project, "b.txt", "b\n")
	h.handIn(n)
	h.step()
	h.approveReview(n)
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s (attention %q)", c, h.ticket(n).Attention)
	}
	if parents := strings.Fields(gitRun(t, h.repo, "log", "-1", "--format=%P", "main")); len(parents) != 2 || parents[1] != reviewed {
		t.Fatalf("merge parents %v, want the reviewed commit %s", parents, reviewed)
	}
}

func TestCommitAfterVerdictIsNotMerged(t *testing.T) {
	h := gitHarness(t)
	n := h.agentReviewed("Add b")
	reviewed := h.startTask(n, h.project, "b.txt", "b\n")
	h.handIn(n)
	h.step()
	h.approveReview(n)
	// The parked developer, woken by a mention, commits after the verdict.
	late := commit(t, h.worktree(h.project, n), "late.txt", "not reviewed\n")
	mainBefore := gitRun(t, h.repo, "rev-parse", "main")
	h.steps(2)

	tk := h.ticket(n)
	if tk.Column != api.ColumnReview || tk.FailedStep != api.FailedMerge {
		t.Fatalf("ticket is %s, failed %q", tk.Column, tk.FailedStep)
	}
	h.checkClaimEnded(n)
	if got := gitRun(t, h.repo, "rev-parse", "main"); got != mainBefore {
		t.Fatal("main moved although the branch has unreviewed commits")
	}
	body := h.lastComment(n).Body
	for _, want := range []string{"New commits were added", short(late), short(reviewed),
		"- Retry: omo merges the reviewed commit", "- Reject, with a comment (only the user can)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment lacks %q:\n%s", want, body)
		}
	}

	// Back at the reviewed commit, a retry merges it.
	gitRun(t, h.worktree(h.project, n), "reset", "-q", "--hard", reviewed)
	if _, err := h.b.Retry(h.ctx, board.User, n); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s after the retry", c)
	}
	gitRun(t, h.repo, "cat-file", "-e", "main:b.txt")
	if err := exec.Command("git", "-C", h.repo, "cat-file", "-e", "main:late.txt").Run(); err == nil {
		t.Fatal("the unreviewed file reached main")
	}
}

// movedAfterVerdict hands n in, lets the reviewer approve and commits on
// its branch after the verdict; it returns the reviewed and the late
// commit once the merge failed because of that.
func (h *harness) movedAfterVerdict(n int64) (reviewed, late string) {
	h.t.Helper()
	reviewed = h.startTask(n, h.project, "b.txt", "b\n")
	h.handIn(n)
	h.step()
	h.approveReview(n)
	late = commit(h.t, h.worktree(h.project, n), "late.txt", "not reviewed\n")
	h.steps(2)
	if f := h.ticket(n).FailedStep; f != api.FailedMerge {
		h.t.Fatalf("failed step %q", f)
	}
	return reviewed, late
}

func TestRetryAfterMovedBranchMergesReviewedCommitAndKeepsTheRest(t *testing.T) {
	h := gitHarness(t)
	n := h.agentReviewed("Add b")
	reviewed, late := h.movedAfterVerdict(n)
	branch := h.ticket(n).Projects[0].Branch
	if _, err := h.b.Retry(h.ctx, board.User, n); err != nil {
		t.Fatal(err)
	}
	h.steps(3)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s after the retry (attention %q)", c, h.ticket(n).Attention)
	}
	if parents := strings.Fields(gitRun(t, h.repo, "log", "-1", "--format=%P", "main")); len(parents) != 2 || parents[1] != reviewed {
		t.Fatalf("merge parents %v, want the reviewed commit %s", parents, reviewed)
	}
	if err := exec.Command("git", "-C", h.repo, "cat-file", "-e", "main:late.txt").Run(); err == nil {
		t.Fatal("the unreviewed file reached main")
	}
	if got := gitRun(t, h.repo, "rev-parse", "refs/heads/"+branch); got != late {
		t.Fatalf("branch at %s, want it kept at the late commit %s", got, late)
	}
	if body := h.lastComment(n).Body; !strings.Contains(body, short(late)) || !strings.Contains(body, "change late.txt") {
		t.Fatalf("comment:\n%s", body)
	}
}

func TestRetryAfterBranchMovedFurtherFailsAgain(t *testing.T) {
	h := gitHarness(t)
	n := h.agentReviewed("Add b")
	_, late := h.movedAfterVerdict(n)
	later := commit(t, h.worktree(h.project, n), "later.txt", "later\n")
	mainBefore := gitRun(t, h.repo, "rev-parse", "main")
	if _, err := h.b.Retry(h.ctx, board.User, n); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	tk := h.ticket(n)
	if tk.Column != api.ColumnReview || tk.FailedStep != api.FailedMerge || gitRun(t, h.repo, "rev-parse", "main") != mainBefore {
		t.Fatalf("ticket is %s, failed %q; a retry merged although the branch moved again", tk.Column, tk.FailedStep)
	}
	body := h.lastComment(n).Body
	if !strings.Contains(body, short(later)) || !strings.Contains(body, short(late)) {
		t.Fatalf("comment:\n%s", body)
	}
}

func TestRejectAfterMovedBranchSendsTicketBackToWork(t *testing.T) {
	h := gitHarness(t)
	n := h.agentReviewed("Add b")
	h.movedAfterVerdict(n)
	if _, err := h.b.Reject(h.ctx, board.User, n, api.RejectRequest{Comment: "Hand the new commits in for review too."}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnInProgress {
		t.Fatalf("ticket is %s after the rejection", c)
	}
	if _, ok := h.liveOK(api.RoleDeveloper, n); !ok {
		t.Fatal("no developer works on the rejected ticket")
	}
	// The record of the moved branch is gone with the review it belonged
	// to: the next merge reports anything new again.
	if seen, err := h.e.pins.moved(n, h.ticket(n).ReviewRound, h.project); err != nil || seen != "" {
		t.Fatalf("moved record %q (%v) kept after the rejection", seen, err)
	}
}

func TestMultiProjectStoryPinsEachProject(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	a, b := h.ids[0], h.ids[1]
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{a, b}, Review: api.ReviewSkipped})
	h.startStory(s)
	ta := h.storyTask(s, a, "In a")
	h.finishTask(ta, a, "new.txt", "a\n")
	h.steps(2)
	tb := h.storyTask(s, b, "In b")
	h.finishTask(tb, b, "b.txt", "b\n")
	h.steps(2)
	headA := gitRun(t, h.repos[a], "rev-parse", h.ticket(s).Projects[0].Branch)
	headB := gitRun(t, h.repos[b], "rev-parse", h.ticket(s).Projects[1].Branch)
	mainA, mainB := gitRun(t, h.repos[a], "rev-parse", "main"), gitRun(t, h.repos[b], "rev-parse", "main")
	h.handIn(s)
	pins, err := h.e.pins.reviewed(s, h.ticket(s).ReviewRound)
	if err != nil || pins[a] != headA || pins[b] != headB {
		t.Fatalf("pins %v (%v), want %s and %s", pins, err, headA, headB)
	}
	var late string
	h.e.pause = func(point string) {
		if point == "merge-claimed" && late == "" {
			late = commit(t, h.worktree(b, s), "late.txt", "late\n")
		}
	}
	h.step()
	h.e.pause = nil
	tk := h.ticket(s)
	if tk.FailedStep != api.FailedMerge || !strings.Contains(h.lastComment(s).Body, short(late)) {
		t.Fatalf("failed %q, comment:\n%s", tk.FailedStep, h.lastComment(s).Body)
	}
	if gitRun(t, h.repos[a], "rev-parse", "main") != mainA || gitRun(t, h.repos[b], "rev-parse", "main") != mainB {
		t.Fatal("a project was merged although another one moved after the hand-in")
	}
}

func TestUnmergedCommitsOfCompletedBranchAreNamed(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	h.finishTask(n, h.project, "b.txt", "b\n")
	dir := h.worktree(h.project, n)
	var after string
	h.e.pause = func(point string) {
		if point == "before-remove" && after == "" {
			after = commit(t, dir, "after.txt", "after the merge\n")
		}
	}
	h.steps(2)
	h.e.pause = nil
	branch := h.ticket(n).Projects[0].Branch
	if branch == "" || !strings.Contains(strings.Join(h.branches(h.repo), " "), branch) {
		t.Fatalf("branch %q was not kept", branch)
	}
	body := h.lastComment(n).Body
	if !strings.Contains(body, short(after)) || !strings.Contains(body, "does not contain these commits") {
		t.Fatalf("comment:\n%s", body)
	}
	h.steps(2)
	if c := h.commentsWith(n, "does not contain these commits"); c != 1 {
		t.Fatalf("%d comments about the kept branch", c)
	}
}

func TestHandInWithoutCheckIsPinnedByThePass(t *testing.T) {
	h := gitHarness(t)
	n := h.skippedTask("Add b")
	reviewed := h.startTask(n, h.project, "b.txt", "b\n")
	dev := h.live(api.RoleDeveloper, n)
	if _, err := h.b.Done(h.ctx, h.actor(dev.ID), api.DoneRequest{}); err != nil {
		t.Fatal(err)
	}
	h.steps(2)
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("ticket is %s (attention %q)", c, h.ticket(n).Attention)
	}
	if parents := strings.Fields(gitRun(t, h.repo, "log", "-1", "--format=%P", "main")); len(parents) != 2 || parents[1] != reviewed {
		t.Fatalf("merge parents %v, want %s", parents, reviewed)
	}
}

func TestSquashRetryDoesNotReapplyWhatTheUserReverted(t *testing.T) {
	h := gitHarness(t, withProjects(2))
	a, b := h.ids[0], h.ids[1]
	squash := api.MergeModeSquash
	for _, id := range h.ids {
		if _, err := h.st.UpdateProject(id, settings.ProjectUpdate{MergeStory: &squash}); err != nil {
			t.Fatal(err)
		}
	}
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Two", Projects: []string{a, b}, Review: api.ReviewSkipped})
	h.startStory(s)
	ta := h.storyTask(s, a, "In a")
	h.finishTask(ta, a, "new.txt", "a\n")
	h.steps(2)
	tb := h.storyTask(s, b, "In b")
	h.finishTask(tb, b, "a.txt", "b's a\n")
	h.steps(2)
	write(t, h.repos[b], "a.txt", "user's uncommitted a\n")
	h.handIn(s)
	h.step()
	if f := h.ticket(s).FailedStep; f != api.FailedMerge {
		t.Fatalf("failed step %q, want merge (project b is blocked)", f)
	}
	gitRun(t, h.repos[a], "cat-file", "-e", "main:new.txt")
	// The user reverts the story's change in project a.
	gitRun(t, h.repos[a], "rm", "-q", "new.txt")
	gitRun(t, h.repos[a], "commit", "-q", "-m", "revert new.txt")
	mainA := gitRun(t, h.repos[a], "rev-parse", "main")
	gitRun(t, h.repos[b], "checkout", "--", "a.txt")
	if _, err := h.b.Retry(h.ctx, board.User, s); err != nil {
		t.Fatal(err)
	}
	h.steps(2)

	if got := gitRun(t, h.repos[a], "rev-parse", "main"); got != mainA {
		t.Fatal("the retry merged the story into project a again")
	}
	tk := h.ticket(s)
	body := h.lastComment(s).Body
	if tk.FailedStep != api.FailedMerge || !strings.Contains(body, "already received story") || !strings.Contains(body, "changed since") {
		t.Fatalf("failed %q, comment:\n%s", tk.FailedStep, body)
	}
}

func TestCleanupKnowsTheSquashCommit(t *testing.T) {
	h := gitHarness(t)
	squash := api.MergeModeSquash
	if _, err := h.st.UpdateProject(h.project, settings.ProjectUpdate{MergeStory: &squash}); err != nil {
		t.Fatal(err)
	}
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Squashed", Review: api.ReviewSkipped})
	h.startStory(s)
	task := h.storyTask(s, h.project, "One")
	h.finishTask(task, h.project, "one.txt", "1\n")
	h.steps(2)
	h.handIn(s)
	var objects string
	h.e.pause = func(point string) {
		if point == "before-remove" && objects == "" {
			// Right after the squash, the user reverts part of it; from
			// then on signing would fail.
			gitRun(t, h.repo, "rm", "-q", "one.txt")
			gitRun(t, h.repo, "commit", "-q", "-m", "revert one.txt")
			gitRun(t, h.repo, "config", "commit.gpgsign", "true")
			gitRun(t, h.repo, "config", "gpg.program", "false")
			objects = gitRun(t, h.repo, "count-objects", "-v")
		}
	}
	h.steps(2)
	h.e.pause = nil
	if c := h.ticket(s).Column; c != api.ColumnDone {
		t.Fatalf("story is %s", c)
	}
	if len(h.branches(h.repo)) != 0 {
		t.Fatalf("the squashed story's branch was kept: %v", h.branches(h.repo))
	}
	if got := gitRun(t, h.repo, "count-objects", "-v"); got != objects {
		t.Fatalf("the cleanup wrote objects:\n%s\n%s", objects, got)
	}
}

func TestMovedBranchListsCappedCleanSubjects(t *testing.T) {
	h := gitHarness(t)
	n := h.agentReviewed("Add b")
	h.startTask(n, h.project, "b.txt", "b\n")
	h.handIn(n)
	h.step()
	h.approveReview(n)
	dir := h.worktree(h.project, n)
	hostile := "evil \x1b[31mred‮ @ceo `x` " + strings.Repeat("y", 300)
	write(t, dir, "h.txt", "h\n")
	gitRun(t, dir, "add", "h.txt")
	gitRun(t, dir, "commit", "-q", "-m", hostile)
	var late []string
	for i := range 11 {
		late = append(late, commit(t, dir, fmt.Sprintf("late%d.txt", i), "late\n"))
	}
	h.steps(2)
	if f := h.ticket(n).FailedStep; f != api.FailedMerge {
		t.Fatalf("failed step %q", f)
	}
	body := h.lastComment(n).Body
	// Newest first, ten of the twelve.
	if !strings.Contains(body, "- `"+short(late[10])+"` `change late10.txt`") ||
		!strings.Contains(body, "- `"+short(late[1])+"` `change late1.txt`") ||
		strings.Contains(body, short(late[0])) {
		t.Fatalf("comment:\n%s", body)
	}
	if !strings.Contains(body, "- and 2 more") {
		t.Fatalf("comment lacks the rest count:\n%s", body)
	}
	if strings.ContainsAny(body, "\x1b‮") {
		t.Fatalf("control characters reached the comment:\n%q", body)
	}
}

func TestCleanSubjectAndCodeSpan(t *testing.T) {
	long := strings.Repeat("é", 250)
	for _, c := range []struct{ in, want string }{
		{"fix \x1b[2Jit⁦ now ", "fix [2Jit now"},
		{"  plain  ", "plain"},
		{"hid\u200bden\ufeff \U000E0041tag", "hidden tag"},
		{"\x00\x07", ""},
		{long, strings.Repeat("é", maxSubject-1) + "…"},
	} {
		if got := cleanSubject(c.in); got != c.want {
			t.Errorf("cleanSubject(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range []struct{ in, want string }{
		{"plain @ceo", "`plain @ceo`"},
		{"a `b` c", "``a `b` c``"},
		{"`x``", "``` `x`` ```"},
	} {
		got := codeSpan(c.in)
		if got != c.want {
			t.Errorf("codeSpan(%q) = %q, want %q", c.in, got, c.want)
		}
		if m := board.ParseMentions("x " + got); len(m) != 0 {
			t.Errorf("%q mentions %v", got, m)
		}
	}
	for in, want := range map[string]string{
		"src/a.go":             "src/a.go",
		"src/a\u202egod.exe":   `"src/a\u202egod.exe"`,
		"src/zero\u200bwidth":  `"src/zero\u200bwidth"`,
		"src/tag\U000E0041.go": `"src/tag\U000e0041.go"`,
	} {
		if got := showPath(in); got != want {
			t.Errorf("showPath(%q) = %q, want %q", in, got, want)
		}
	}
	// A path of this platform is written as it is: on Windows its
	// separator is the backslash, which must not turn every path of a
	// comment into a quoted string with doubled backslashes.
	if p := filepath.Join("src", "pkg", "a.go"); showPath(p) != p {
		t.Errorf("showPath(%q) = %q, want it unchanged", p, showPath(p))
	}
	if q := quotedIn('\\'); strings.Contains(q, `\`) || !strings.Contains(q, "`") || !strings.Contains(q, `"`) {
		t.Errorf("with the backslash as separator showPath quotes %q", q)
	}
	// Where the backslash separates nothing, it is quoted like a quote.
	if q := quotedIn('/'); !strings.Contains(q, `\`) || !strings.Contains(q, "`") || !strings.Contains(q, `"`) {
		t.Errorf("with the slash as separator showPath quotes %q", q)
	}
	if filepath.Separator == '/' {
		if got, want := showPath(`src\a.go`), `"src\\a.go"`; got != want {
			t.Errorf("showPath of a name with a backslash = %q, want %q", got, want)
		}
	}
}

// What the store hands out and takes in is a copy: the engine reads and
// walks the commits of a round while another goroutine removes a project
// from the same record.
func TestPinStoreKeepsItsOwnCopyOfTheReviewedCommits(t *testing.T) {
	s := &pinStore{path: filepath.Join(t.TempDir(), pinsFile)}
	in := map[string]string{"a": "1", "b": "2"}
	if err := s.setReviewed(7, 1, in); err != nil {
		t.Fatal(err)
	}
	in["c"] = "3"
	got, err := s.reviewed(7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("a later change of the map handed in reached the store: %v", got)
	}
	got["a"] = "changed"
	if err := s.forgetProject(7, "b"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["b"] != "2" {
		t.Fatalf("a project removed from the store changed a map read before: %v", got)
	}
	if again, _ := s.reviewed(7, 1); len(again) != 1 || again["a"] != "1" {
		t.Fatalf("a change of a map read before reached the store: %v", again)
	}
}
