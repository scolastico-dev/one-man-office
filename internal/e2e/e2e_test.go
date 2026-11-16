package e2e

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// TestStandaloneTaskEndToEnd takes a standalone task from Ready through its
// developer, an agent review and the merge into main, in the folder the user
// has main checked out in, and checks what is left.
func TestStandaloneTaskEndToEnd(t *testing.T) {
	w := newWorld(t, config{})
	n := w.task("Add a file", api.ReviewAgent, api.WhenApprovedMerge)

	tk := w.waitColumn(n, api.ColumnDone, "")
	if tk.Resolution != api.ResolutionCompleted || tk.ReviewRound != 1 {
		t.Fatalf("resolution %q round %d", tk.Resolution, tk.ReviewRound)
	}
	roles := map[api.Role]int{}
	for _, s := range w.sessions(n) {
		roles[s.Role]++
		if s.Model != fakeModel {
			t.Errorf("session %s has model %q", s.Name, s.Model)
		}
	}
	if roles[api.RoleDeveloper] != 1 || roles[api.RoleReviewer] != 1 {
		t.Fatalf("sessions by role %v", roles)
	}
	// main has the work as a merge commit, and the user's checkout of main
	// was fast-forwarded to it.
	merge := w.commitWith("main", "Merge: Add a file")
	if w.git("rev-parse", "main") != merge || len(strings.Fields(w.git("rev-list", "--parents", "-n1", merge))) != 3 {
		t.Fatalf("main is not the merge commit with two parents:\n%s", w.git("log", "--graph", "--oneline", "main"))
	}
	if body, ok := w.fileOn("main", "fake-"+itoa(n)+".txt"); !ok || !strings.HasPrefix(body, "done by ") {
		t.Fatalf("fake-%d.txt on main: %q %v", n, body, ok)
	}
	if _, err := os.Stat(filepath.Join(w.repo, "fake-"+itoa(n)+".txt")); err != nil {
		t.Fatalf("the user's checkout was not advanced: %v", err)
	}
	w.checkoutClean()
	w.settled()
	w.noCrashes(n)
}

// TestStoryFlowEndToEnd approves a story: its product manager writes two
// tasks, the second depending on the first, and releases them; each task is
// developed, reviewed and merged into the story's integration branch, the
// story is reviewed and merged into main.
func TestStoryFlowEndToEnd(t *testing.T) {
	w := newWorld(t, config{maxRunning: 2})
	story := w.create(api.CreateTicket{Type: api.TypeStory, Title: "A story", Review: api.ReviewAgent}).Number
	w.approve(story)

	st := w.waitColumn(story, api.ColumnDone, "")
	if st.Resolution != api.ResolutionCompleted {
		t.Fatalf("story resolution %q", st.Resolution)
	}
	list, err := w.user.Tickets(context.Background(), api.TicketFilter{Story: &story, Type: api.TypeTask, Column: api.ColumnDone})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tickets) != 2 {
		t.Fatalf("%d done tasks, want 2", len(list.Tickets))
	}
	t1, t2 := list.Tickets[0].Number, list.Tickets[1].Number
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	if deps := w.ticket(t2).DependsOn; len(deps) != 1 || deps[0] != t1 {
		t.Fatalf("#%d depends on %v, want #%d", t2, deps, t1)
	}
	for _, n := range []int{t1, t2} {
		if _, ok := w.fileOn("main", "fake-"+itoa(n)+".txt"); !ok {
			t.Errorf("main lacks fake-%d.txt", n)
		}
	}
	// One merge commit per task and one for the story, which is main.
	storyMerge := w.commitWith("main", "Merge: A story")
	m1 := w.commitWith("main", "Merge: task 1")
	m2 := w.commitWith("main", "Merge: task 2")
	if w.git("rev-parse", "main") != storyMerge {
		t.Fatalf("main is not the story's merge:\n%s", w.git("log", "--graph", "--oneline", "main"))
	}
	if !w.isAncestor(m1, m2) {
		t.Fatal("the merge of task 1 is not before the merge of task 2")
	}
	// The second task started after the first was merged: its own work
	// already contains the first task's file.
	work2 := w.git("rev-parse", m2+"^2")
	if _, ok := w.fileOn(work2, "fake-"+itoa(t1)+".txt"); !ok {
		t.Fatalf("task 2 was cut before task 1 was merged")
	}
	pms := 0
	for _, s := range w.sessions(story) {
		if s.Role == api.RolePM {
			pms++
		}
	}
	if pms != 1 {
		t.Fatalf("%d product manager sessions, want 1", pms)
	}
	w.checkoutClean()
	w.settled()
	w.noCrashes(story, t1, t2)
}

// TestRejectionRoundEndToEnd has the reviewer reject the first round: the
// parked developer is woken, works again and hands in, a fresh reviewer of
// round 2 approves, and the task is merged.
func TestRejectionRoundEndToEnd(t *testing.T) {
	w := newWorld(t, config{scripts: map[string]string{
		"reviewer": "ready\nverdicts|reject,approve|round verdict\nwait|10m\nhang",
	}})
	n := w.task("Reject once", api.ReviewAgent, api.WhenApprovedMerge)

	tk := w.waitColumn(n, api.ColumnDone, "")
	if tk.ReviewRound != 2 || tk.Resolution != api.ResolutionCompleted {
		t.Fatalf("round %d resolution %q", tk.ReviewRound, tk.Resolution)
	}
	var devs, rounds []int
	for _, s := range w.sessions(n) {
		switch s.Role {
		case api.RoleDeveloper:
			devs = append(devs, 1)
		case api.RoleReviewer:
			rounds = append(rounds, s.ReviewRound)
		}
	}
	// One developer worked both rounds; each round had its own reviewer.
	slices.Sort(rounds)
	if len(devs) != 1 || len(rounds) != 2 || rounds[0] != 1 || rounds[1] != 2 {
		t.Fatalf("developers %d, reviewer rounds %v", len(devs), rounds)
	}
	if !w.hasComment(n, "round verdict") {
		t.Fatalf("no rejection comment: %q", w.comments(n))
	}
	work := w.git("rev-parse", "main^2")
	if got := w.subjects(work); len(got) < 3 || got[0] != got[1] {
		t.Fatalf("the developer did not commit again in round 2: %q", got)
	}
	w.checkoutClean()
	w.settled()
	w.noCrashes(n)
}

// TestExportFlowEndToEnd exports a task instead of merging it: omo pushes
// the commit handed in to the project's origin as the task's branch and says
// so, the export job (in a folder omo made, not the worktree) reports its
// pull request, main stays as it was and the branch stays.
func TestExportFlowEndToEnd(t *testing.T) {
	w := newWorld(t, config{origin: true})
	before := w.git("rev-parse", "main")
	n := w.task("Export me", api.ReviewSkipped, api.WhenApprovedExport)

	tk := w.waitColumn(n, api.ColumnDone, "")
	if tk.Resolution != api.ResolutionExported || tk.ExportURL != "https://example.invalid/you/app/pull/"+itoa(n) {
		t.Fatalf("resolution %q url %q", tk.Resolution, tk.ExportURL)
	}
	exports := 0
	for _, s := range w.sessions(n) {
		if s.Purpose == api.PurposeExport {
			exports++
			if want := filepath.Join(w.layout.Run, "export", itoa(n)+"-"+w.project); s.Workdir != want {
				t.Errorf("the export job worked in %s, want %s", s.Workdir, want)
			}
		}
	}
	if exports != 1 {
		t.Fatalf("%d export sessions, want 1", exports)
	}
	if got := w.git("rev-parse", "main"); got != before {
		t.Fatal("main moved for an exported task")
	}
	branch := tk.Projects[0].Branch
	if _, ok := w.fileOn(branch, "fake-"+itoa(n)+".txt"); !ok {
		t.Fatalf("the exported branch %s lacks the work", branch)
	}
	head := w.git("rev-parse", branch)
	if got := w.remoteHead(branch); got != head {
		t.Fatalf("origin has %s at %q, want %s", branch, got, head)
	}
	if !w.hasComment(n, "Pushed `"+head[:12]+"` to `origin` (`"+originAddress+"`) as `"+branch+"`.") {
		t.Fatalf("no push comment:\n%s", strings.Join(w.comments(n), "\n---\n"))
	}
	w.checkoutClean()
	w.settled(branch)
	if left := w.exportFolders(); len(left) != 0 {
		t.Fatalf("export folders left: %v", left)
	}
	w.noCrashes(n)
}

// TestExportPushesReviewedCommitEndToEnd: the developer, parked after its
// hand-in, commits again before the user approves the export. omo pushes the
// commit that was reviewed, not the branch's newer head, and its comment
// names that commit.
func TestExportPushesReviewedCommitEndToEnd(t *testing.T) {
	w := newWorld(t, config{origin: true})
	n := w.task("Reviewed export", api.ReviewHuman, api.WhenApprovedExport)
	tk := w.waitColumn(n, api.ColumnReview, api.ReviewStageHuman)
	wt := tk.Projects[0].Worktree
	reviewed := gitIn(t, wt, "rev-parse", "HEAD")
	extra := commitFile(t, wt, "late.txt", "not reviewed\n", "late change")
	w.do("POST", "/api/tickets/"+itoa(n)+"/approve", api.ApproveRequest{Export: true}, nil)

	tk = w.waitColumn(n, api.ColumnDone, "")
	branch := tk.Projects[0].Branch
	if got := w.remoteHead(branch); got != reviewed {
		t.Fatalf("origin has %q, want the reviewed %s (not %s)", got, reviewed, extra)
	}
	if !w.hasComment(n, "Pushed `"+reviewed[:12]+"`") {
		t.Fatalf("the comment does not name the reviewed commit:\n%s", strings.Join(w.comments(n), "\n---\n"))
	}
	if tk.ExportURL != "https://example.invalid/you/app/pull/"+itoa(n) {
		t.Fatalf("url %q", tk.ExportURL)
	}
	w.settled(branch)
	w.noCrashes(n)
}

// TestExportPushRefusedEndToEnd: origin already has the task's branch at a
// commit the reviewed one does not descend from. The push is refused (never
// forced), the task waits in Review for the user with the reason, no export
// job starts; once the remote branch is gone, approving with export again
// exports it.
func TestExportPushRefusedEndToEnd(t *testing.T) {
	w := newWorld(t, config{origin: true})
	n := w.task("Refused export", api.ReviewHuman, api.WhenApprovedExport)
	tk := w.waitColumn(n, api.ColumnReview, api.ReviewStageHuman)
	branch := tk.Projects[0].Branch
	other := w.git("commit-tree", "-m", "someone else", "main^{tree}")
	w.git("push", "-q", w.origin, other+":refs/heads/"+branch)

	w.do("POST", "/api/tickets/"+itoa(n)+"/approve", api.ApproveRequest{Export: true}, nil)
	w.waitFor("the refused push", func() bool { return w.hasComment(n, "Export failed: Export of "+w.project+" failed") })
	tk = w.ticket(n)
	if tk.Column != api.ColumnReview || tk.ReviewStage != api.ReviewStageHuman || tk.Attention == "" {
		t.Fatalf("ticket %s/%s attention %q", tk.Column, tk.ReviewStage, tk.Attention)
	}
	if !w.hasComment(n, "refused the push") {
		t.Fatalf("the comment does not say the push was refused:\n%s", strings.Join(w.comments(n), "\n---\n"))
	}
	if got := w.remoteHead(branch); got != other {
		t.Fatalf("origin's branch changed to %q", got)
	}
	for _, s := range w.sessions(n) {
		if s.Purpose == api.PurposeExport {
			t.Fatal("an export job started after the refused push")
		}
	}

	gitIn(t, w.origin, "update-ref", "-d", "refs/heads/"+branch)
	w.do("POST", "/api/tickets/"+itoa(n)+"/approve", api.ApproveRequest{Export: true}, nil)
	tk = w.waitColumn(n, api.ColumnDone, "")
	if got, want := w.remoteHead(branch), w.git("rev-parse", branch); got != want {
		t.Fatalf("after the retry origin has %q, want %s", got, want)
	}
	w.settled(branch)
	w.noCrashes(n)
}

// conflictDeveloper is a developer that merges main into its branch before
// it works, resolving every conflict in favour of its own side, as an agent
// told about a merge conflict would.
const conflictDeveloper = `ready
git|merge|--no-edit|-X|ours|main
write|fake-$TICKET.txt|done by $NAME
commit|feat: fake work for #$TICKET
done|fake work
wait-loop|10m`

// TestMergeConflictEndToEnd changes main in a conflicting way while a task
// waits for the user's review: the merge fails with a conflict, the task
// goes back to its developer, who merges main, hands in again, and the
// second approval merges.
func TestMergeConflictEndToEnd(t *testing.T) {
	w := newWorld(t, config{scripts: map[string]string{"developer": conflictDeveloper}})
	n := w.task("Conflicting", api.ReviewHuman, api.WhenApprovedMerge)
	w.waitColumn(n, api.ColumnReview, api.ReviewStageHuman)
	file := "fake-" + itoa(n) + ".txt"
	theirs := commitFile(t, w.repo, file, "the user's version\n", "user: own version")

	w.do("POST", "/api/tickets/"+itoa(n)+"/approve", api.ApproveRequest{}, nil)
	w.waitFor("the conflict to reach the developer", func() bool {
		tk := w.ticket(n)
		return tk.ReviewRound == 2 || tk.Column == api.ColumnInProgress
	})
	if !w.hasComment(n, file) {
		t.Fatalf("no comment names the conflicting file: %q", w.comments(n))
	}
	if w.git("rev-parse", "main") != theirs {
		t.Fatal("main moved although the merge conflicted")
	}
	tk := w.waitColumn(n, api.ColumnReview, api.ReviewStageHuman)
	if tk.ReviewRound != 2 {
		t.Fatalf("round %d after the conflict", tk.ReviewRound)
	}
	w.do("POST", "/api/tickets/"+itoa(n)+"/approve", api.ApproveRequest{}, nil)
	w.waitColumn(n, api.ColumnDone, "")
	if !w.isAncestor(theirs, "main") {
		t.Fatal("the user's commit is not in main")
	}
	if body, _ := w.fileOn("main", file); !strings.HasPrefix(body, "done by ") {
		t.Fatalf("%s on main: %q", file, body)
	}
	w.checkoutClean()
	w.settled()
	w.noCrashes(n)
}

// TestBlockedMergeRetryEndToEnd has an untracked file in the user's
// checkout in the way of the fast-forward: the merge stops with failed step
// merge and touches nothing; once the user moved the file away, Retry
// merges.
func TestBlockedMergeRetryEndToEnd(t *testing.T) {
	w := newWorld(t, config{})
	tk := w.create(api.CreateTicket{Type: api.TypeTask, Title: "Blocked", Review: api.ReviewAgent})
	n := tk.Number
	file := filepath.Join(w.repo, "fake-"+itoa(n)+".txt")
	if err := os.WriteFile(file, []byte("the user's own file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := w.git("rev-parse", "main")
	w.approve(n)

	tk = w.waitFailed(n, api.FailedMerge)
	if tk.Column != api.ColumnReview || !strings.Contains(strings.Join(w.comments(n), "\n"), "fake-"+itoa(n)+".txt") {
		t.Fatalf("column %s, comments %q", tk.Column, w.comments(n))
	}
	if w.git("rev-parse", "main") != before {
		t.Fatal("main moved although the fast-forward was blocked")
	}
	if raw, _ := os.ReadFile(file); string(raw) != "the user's own file\n" {
		t.Fatalf("the user's file changed: %q", raw)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	w.do("POST", "/api/tickets/"+itoa(n)+"/retry", struct{}{}, nil)
	w.waitColumn(n, api.ColumnDone, "")
	if _, ok := w.fileOn("main", "fake-"+itoa(n)+".txt"); !ok {
		t.Fatal("main lacks the work after the retry")
	}
	w.checkoutClean()
	w.settled()
	w.noCrashes(n)
}

// movedBranch takes a task to the user's review, then adds a commit to its
// branch that nobody reviewed and approves: the merge stops with failed
// step merge naming the new commit. It returns the ticket, the reviewed
// commit and the unreviewed one.
func movedBranch(t *testing.T, w *world) (n int, reviewed, extra string) {
	t.Helper()
	n = w.task("Moved after review", api.ReviewHuman, api.WhenApprovedMerge)
	tk := w.waitColumn(n, api.ColumnReview, api.ReviewStageHuman)
	wt := tk.Projects[0].Worktree
	reviewed = gitIn(t, wt, "rev-parse", "HEAD")
	extra = commitFile(t, wt, "extra.txt", "not reviewed\n", "unreviewed change")
	w.do("POST", "/api/tickets/"+itoa(n)+"/approve", api.ApproveRequest{}, nil)
	tk = w.waitFailed(n, api.FailedMerge)
	if tk.Column != api.ColumnReview {
		t.Fatalf("column %s", tk.Column)
	}
	all := strings.Join(w.comments(n), "\n")
	if !strings.Contains(all, extra[:7]) || !strings.Contains(all, "unreviewed change") {
		t.Fatalf("the failed step does not name the new commit:\n%s", all)
	}
	return n, reviewed, extra
}

// TestBranchMovedAfterReviewEndToEnd covers both ways out of a branch that
// moved after its review: Retry merges exactly the reviewed commit and keeps
// the branch with the rest; Reject sends the task back to its developer,
// and the next round reviews and merges everything.
func TestBranchMovedAfterReviewEndToEnd(t *testing.T) {
	t.Run("retry", func(t *testing.T) {
		w := newWorld(t, config{})
		n, reviewed, extra := movedBranch(t, w)
		w.do("POST", "/api/tickets/"+itoa(n)+"/retry", struct{}{}, nil)
		tk := w.waitColumn(n, api.ColumnDone, "")
		merge := w.commitWith("main", "Merge: Moved after review")
		if got := w.git("rev-parse", merge+"^2"); got != reviewed {
			t.Fatalf("merged %s, want the reviewed %s", got, reviewed)
		}
		if _, ok := w.fileOn("main", "extra.txt"); ok {
			t.Fatal("the unreviewed change reached main")
		}
		branch := tk.Projects[0].Branch
		if got := w.git("rev-parse", branch); got != extra {
			t.Fatalf("branch %s at %s, want it kept at %s", branch, got, extra)
		}
		w.checkoutClean()
		w.settled(branch)
		w.noCrashes(n)
	})
	t.Run("reject", func(t *testing.T) {
		w := newWorld(t, config{})
		n, _, extra := movedBranch(t, w)
		w.do("POST", "/api/tickets/"+itoa(n)+"/reject", api.RejectRequest{Comment: "include the extra change properly"}, nil)
		tk := w.ticket(n)
		if tk.Column != api.ColumnInProgress || tk.FailedStep != api.FailedNone {
			t.Fatalf("column %s failed %q after the rejection", tk.Column, tk.FailedStep)
		}
		tk = w.waitColumn(n, api.ColumnReview, api.ReviewStageHuman)
		if tk.ReviewRound != 2 {
			t.Fatalf("round %d", tk.ReviewRound)
		}
		w.do("POST", "/api/tickets/"+itoa(n)+"/approve", api.ApproveRequest{}, nil)
		w.waitColumn(n, api.ColumnDone, "")
		if !w.isAncestor(extra, "main") {
			t.Fatal("the change reviewed in round 2 is not in main")
		}
		w.checkoutClean()
		w.settled()
		w.noCrashes(n)
	})
}

// TestStopAndResumeEndToEnd stops omo while a developer works: it is asked
// for a handoff and writes it, every session ends "omo stopped". A new
// server on the same home starts a fresh developer on the same task with the
// same model, whose prompt tells it about the earlier work, and the
// uncommitted file of the first one is still in the worktree.
func TestStopAndResumeEndToEnd(t *testing.T) {
	prompts := t.TempDir()
	save := "save-prompt|" + filepath.Join(prompts, "prompt-$NAME.txt")
	w := newWorld(t, config{handoff: time.Minute, scripts: map[string]string{"developer": "ready\n" + save +
		"\nwrite|wip.txt|half done\nawait-line|omo is stopping|10m\ncomment|$TICKET|Handoff: wip.txt is half done\nwait|10m"}})
	n := w.task("Long work", api.ReviewAgent, api.WhenApprovedMerge)
	first := w.waitSession(n, api.RoleDeveloper, api.SessionRunning)
	wip := filepath.Join(w.ticketWorktree(n), "wip.txt")
	w.waitFor("the developer's uncommitted file", func() bool {
		_, err := os.Stat(wip)
		return err == nil
	})
	w.stop(time.Minute)

	// The next run's developer only reads its prompt: what it finds in the
	// worktree is what the first one left.
	w.setScripts(w.openSettings(), config{scripts: map[string]string{"developer": "ready\n" + save + "\nhang"}})
	w.start()
	if !w.hasComment(n, "Handoff: wip.txt is half done") {
		t.Fatalf("no handoff comment: %q", w.comments(n))
	}
	for _, s := range w.sessions(n) {
		if s.ID == first.ID && (s.State != api.SessionEnded || s.EndReason != "omo stopped") {
			t.Fatalf("first developer %s %q", s.State, s.EndReason)
		}
	}
	second := w.waitSession(n, api.RoleDeveloper, api.SessionRunning)
	if second.ID == first.ID || second.Model != first.Model {
		t.Fatalf("resumed with %s (%s), first was %s (%s)", second.Name, second.Model, first.Name, first.Model)
	}
	if tk := w.ticket(n); tk.Column != api.ColumnInProgress {
		t.Fatalf("column %s after the restart", tk.Column)
	}
	if raw, err := os.ReadFile(wip); err != nil || string(raw) != "half done\n" {
		t.Fatalf("the uncommitted file after the restart: %q %v", raw, err)
	}
	var prompt []byte
	w.waitFor("the resumed developer's prompt", func() bool {
		var err error
		prompt, err = os.ReadFile(filepath.Join(prompts, "prompt-"+second.Name+".txt"))
		return err == nil
	})
	if !strings.Contains(string(prompt), "run `git status`") {
		t.Fatalf("the resumed prompt has no restart note:\n%s", prompt)
	}
	w.noCrashes(n)
}

// ticketWorktree returns the worktree recorded on ticket n, once there is
// one.
func (w *world) ticketWorktree(n int) string {
	w.t.Helper()
	var dir string
	w.waitFor("a worktree of #"+itoa(n), func() bool {
		tk := w.ticket(n)
		if len(tk.Projects) > 0 {
			dir = tk.Projects[0].Worktree
		}
		return dir != ""
	})
	return dir
}

// TestSlotLimitGatesOnlyNewWork runs with a limit of one agent: the second
// task waits in Ready with start_blocked slot while the first one's
// developer works, but the first one's reviewer starts at once although the
// limit is reached. Once the first task is done the second one runs.
func TestSlotLimitGatesOnlyNewWork(t *testing.T) {
	w := newWorld(t, config{maxRunning: 1, scripts: map[string]string{
		// The developer hands in and keeps running, holding its slot.
		"developer": "ready\nwrite|fake-$TICKET.txt|work\ncommit|feat: work on #$TICKET\ndone|fake work\nhang",
		"reviewer":  "ready\nawait-line|mentions @reviewer|10m\nverdict|approve|fine\nwait|10m",
	}})
	a := w.task("First", api.ReviewAgent, api.WhenApprovedMerge)
	w.waitSession(a, api.RoleDeveloper, api.SessionRunning)
	b := w.task("Second", api.ReviewAgent, api.WhenApprovedMerge)

	w.waitFor("the second task to be held back for a slot", func() bool {
		return w.ticket(b).StartBlocked == api.StartBlockedSlot
	})
	w.waitSession(a, api.RoleReviewer, api.SessionRunning)
	// Both agents of the first task run although the limit is one; the
	// second task has not started.
	if dev := w.waitSession(a, api.RoleDeveloper, api.SessionRunning); dev.Ticket == nil {
		t.Fatal("no developer")
	}
	var sched api.SchedulerState
	w.do("GET", "/api/scheduler", nil, &sched)
	if sched.Limit != 1 || sched.Running != 2 {
		t.Fatalf("scheduler %+v, want 2 running with a limit of 1", sched)
	}
	if tk := w.ticket(b); tk.Column != api.ColumnReady || tk.StartBlocked != api.StartBlockedSlot || len(w.sessions(b)) != 0 {
		t.Fatalf("second task %s blocked %q with %d sessions", tk.Column, tk.StartBlocked, len(w.sessions(b)))
	}

	w.do("POST", "/api/tickets/"+itoa(a)+"/comments", api.CommentRequest{Body: "@reviewer go ahead"}, nil)
	w.waitColumn(a, api.ColumnDone, "")
	w.waitSession(b, api.RoleReviewer, api.SessionRunning)
	w.do("POST", "/api/tickets/"+itoa(b)+"/comments", api.CommentRequest{Body: "@reviewer go ahead"}, nil)
	w.waitColumn(b, api.ColumnDone, "")
	w.checkoutClean()
	w.settled()
	w.noCrashes(a, b)
}

// TestPromptDeliveredByTyping types the start prompt into the agent's
// terminal (no delay), as omo does for a CLI that takes it no other way.
// The fake agent's script runs omo ai ready only after a line containing
// "omo ai ready" was typed into its terminal (the command line carries no
// prompt), so a running session proves the typed delivery.
func TestPromptDeliveredByTyping(t *testing.T) {
	on := true
	zero := api.Duration(0)
	w := newWorld(t, config{delivery: &api.PromptDelivery{Inject: &on, Delay: &zero},
		scripts: map[string]string{"developer": "await-prompt|typed\nready\nhang"}})
	n := w.task("Typed prompt", api.ReviewAgent, api.WhenApprovedMerge)
	w.waitSession(n, api.RoleDeveloper, api.SessionRunning)
	w.noCrashes(n)
}

// TestCancelKillsRunningAgentProcess cancels a task whose developer works:
// the session ends, the process is gone and the clean worktree and the
// branch are removed.
func TestCancelKillsRunningAgentProcess(t *testing.T) {
	w := newWorld(t, config{scripts: map[string]string{"developer": "ready\nhang"}})
	n := w.task("Cancel me", api.ReviewAgent, api.WhenApprovedMerge)
	w.waitSession(n, api.RoleDeveloper, api.SessionRunning)
	agent := w.runner.all()[0]
	w.do("POST", "/api/tickets/"+itoa(n)+"/cancel", api.CancelRequest{DeleteBranch: true}, nil)
	select {
	case <-agent.Done():
	case <-time.After(time.Minute):
		t.Fatal("the agent process outlived the cancel")
	}
	if tk := w.ticket(n); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCancelled {
		t.Fatalf("column %s resolution %q", tk.Column, tk.Resolution)
	}
	w.settled()
}

// TestKeptAgentsAnswerUntilDoneEndToEnd: the reviewer rejects round 1 and
// stays; the same developer session is woken, works again and hands in;
// round 2's reviewer starts, which ends round 1's; it approves and stays
// through the user's review, where the user asks the reviewer and the
// developer by mentions and both answer in the thread; the user approves
// and the merge ends both.
func TestKeptAgentsAnswerUntilDoneEndToEnd(t *testing.T) {
	const devAnswer, revAnswer = "The developer answers: two lines were asked for.", "The reviewer answers: every test ran."
	w := newWorld(t, config{scripts: map[string]string{
		"developer": "ready\nwrite|fake-$TICKET.txt|done by $NAME\ncommit|feat: fake work for #$TICKET\ndone|fake work\nwait-loop|10m|" + devAnswer,
		"reviewer":  "ready\nverdicts|reject,approve|round verdict\nwait-loop|10m|" + revAnswer,
	}})
	n := w.task("Kept agents", api.ReviewAgentThenHuman, api.WhenApprovedMerge)
	tk := w.waitColumn(n, api.ColumnReview, api.ReviewStageHuman)
	if tk.ReviewRound != 2 {
		t.Fatalf("round %d at the user's review", tk.ReviewRound)
	}
	var dev, rev1, rev2 api.AgentSession
	for _, s := range w.sessions(n) {
		switch {
		case s.Role == api.RoleDeveloper:
			if dev.ID != "" {
				t.Fatal("a second developer started")
			}
			dev = s
		case s.Role == api.RoleReviewer && s.ReviewRound == 1:
			rev1 = s
		case s.Role == api.RoleReviewer && s.ReviewRound == 2:
			rev2 = s
		}
	}
	if dev.State == api.SessionEnded || rev2.State == api.SessionEnded {
		t.Fatalf("an agent ended before the user's review: developer %s %q, reviewer %s %q", dev.State, dev.EndReason, rev2.State, rev2.EndReason)
	}
	if rev1.State != api.SessionEnded || rev1.EndReason != "a new review round of #"+itoa(n)+" began" {
		t.Fatalf("round 1's reviewer is %s %q", rev1.State, rev1.EndReason)
	}

	w.do("POST", "/api/tickets/"+itoa(n)+"/comments", api.CommentRequest{Body: "@reviewer did you run the tests? @developer why two lines?"}, nil)
	w.waitFor("both answers in the thread", func() bool { return w.hasComment(n, devAnswer) && w.hasComment(n, revAnswer) })

	w.do("POST", "/api/tickets/"+itoa(n)+"/approve", api.ApproveRequest{}, nil)
	w.waitColumn(n, api.ColumnDone, "")
	w.settled()
	for _, s := range w.sessions(n) {
		if (s.ID == dev.ID || s.ID == rev2.ID) && s.EndReason != "#"+itoa(n)+" was merged" {
			t.Fatalf("%s ended with %q", s.Role, s.EndReason)
		}
	}
	w.checkoutClean()
	w.noCrashes(n)
}
